package domain

import (
	"encoding/json"
	"strings"

	"github.com/google/uuid"
)

// Memory candidates: what a model proposed, before anyone agreed to it.
//
// ── Why a candidate is not a memory ────────────────────────────────────
// It has no id, no row, and no lifecycle. Consolidation produces a list,
// the interface shows it, and the user decides. A candidate that nobody
// confirms leaves nothing behind — which is why there is no table for one,
// and why generating them can never be mistaken for remembering them.
//
// ── Why the parser lives in the domain ─────────────────────────────────
// Reading a model's answer is the one step of consolidation where the
// system is at the mercy of something it does not control. The rules for
// what may be believed are therefore a pure function over a string,
// testable without a database, a gateway, or a prompt — and reviewable as
// one page instead of as a branch inside an orchestration.

// MaxMemoryCandidates bounds one consolidation.
//
// Five. A conversation that genuinely yielded more than five durable facts
// is a conversation the user should be reviewing in smaller pieces, and a
// list longer than this stops being reviewed and starts being accepted
// wholesale — which is the exact failure mode confirmation exists to
// prevent. The ceiling is also what bounds the output tokens this operation
// can be billed for.
const MaxMemoryCandidates = 5

// MaxCandidateReason bounds the explanation.
//
// The reason is a sentence justifying a proposal, shown next to it. It is
// never stored: what gets saved is the memory, and a rationale living on
// forever beside a fact would be a second, unmaintained description of it.
const MaxCandidateReason = 240

// MemoryCandidate is one proposal.
type MemoryCandidate struct {
	// Content is what would be remembered, and is bounded by the same rule
	// a stored memory is: a candidate that could not be saved is not a
	// proposal, it is a dead end.
	Content string `json:"content"`
	// Reason says why this would be useful later. It explains the proposal
	// and is discarded when the proposal is accepted.
	Reason string `json:"reason"`
	// DuplicateOf names an existing memory with the same normalized text.
	// Set by the server after parsing, never by the model.
	DuplicateOf *uuid.UUID `json:"duplicate_of"`
}

// candidateWire is the shape asked of the model.
//
// Separate from MemoryCandidate because the two differ in the field that
// matters most: the model cannot express `duplicate_of`, and a struct that
// let it try would be a struct where a hallucinated id could reach a
// comparison against real rows.
type candidateWire struct {
	Content string `json:"content"`
	Reason  string `json:"reason"`
}

// ParseMemoryCandidates reads a model's answer as a list of proposals.
//
// ── The rule, and it is one sentence ───────────────────────────────────
// Nothing is invented, nothing is repaired, and an answer that cannot be
// read produces an error rather than a shorter list.
//
// That distinction is the whole design. "The model proposed nothing" and
// "the model said something we could not read" are different facts: the
// first is a legitimate outcome shown as an empty state, the second is a
// failure that cost tokens and has to be reported as one. A parser that
// returned an empty list for both would let an unreadable answer look like
// a considered judgement that there was nothing worth keeping.
//
// ── What it tolerates, and why ─────────────────────────────────────────
// Fences and prose around the array, because gateways and models add them
// and refusing would be refusing over formatting rather than over meaning.
// Extra keys inside an object, because a key we ignore cannot fabricate
// content. Neither of those can change what a proposal says.
//
// ── What it drops rather than fixes ────────────────────────────────────
// An element whose content is empty, or longer than a memory may be. Both
// are unsaveable, and shortening one would mean showing the user a proposal
// the model did not make. Dropped elements are counted so the caller can
// say so; they are never silently replaced.
func ParseMemoryCandidates(raw string) (candidates []MemoryCandidate, dropped int, err error) {
	array, ok := extractJSONArray(raw)
	if !ok {
		return nil, 0, Invalid("the answer did not contain a JSON array")
	}

	var wire []candidateWire
	if err := json.Unmarshal([]byte(array), &wire); err != nil {
		return nil, 0, Invalid("the answer was not a readable list of candidates")
	}

	out := make([]MemoryCandidate, 0, len(wire))
	for _, w := range wire {
		content := strings.TrimSpace(w.Content)
		if content == "" || len([]rune(content)) > MaxMemoryContent {
			dropped++
			continue
		}
		if len(out) == MaxMemoryCandidates {
			// Past the ceiling. Counted as dropped rather than ignored, so
			// the caller can report a cut instead of implying the model
			// stopped there on its own.
			dropped++
			continue
		}
		out = append(out, MemoryCandidate{
			Content: content,
			Reason:  truncateRunes(strings.TrimSpace(w.Reason), MaxCandidateReason),
		})
	}
	return out, dropped, nil
}

// extractJSONArray finds the array inside whatever the model wrapped it in.
//
// First bracket to last bracket. Deliberately crude: anything cleverer
// would be a partial JSON parser, and a partial parser is exactly the kind
// of component that eventually reads half a malformed document as a whole
// valid one. If what lies between them is not valid JSON, the caller
// reports an unreadable answer — which is the honest outcome.
func extractJSONArray(raw string) (string, bool) {
	start := strings.Index(raw, "[")
	end := strings.LastIndex(raw, "]")
	if start < 0 || end < start {
		return "", false
	}
	return raw[start : end+1], true
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// NormalizeMemoryText is the comparison key for exact deduplication.
//
// ── Exact, and nothing more ────────────────────────────────────────────
// Case-folded, whitespace-collapsed, trimmed. That is the whole algorithm,
// and its limits are stated rather than papered over: it catches the same
// sentence proposed twice, and it does not catch a paraphrase. Catching
// paraphrases needs embeddings, which is a capability this system does not
// have and will not grow inside a deduplication helper.
//
// Case folding uses strings.ToLower, which is Unicode-aware — the module's
// content is Portuguese, and "Prefere Go" and "prefere go" are the same
// fact in it.
func NormalizeMemoryText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
}
