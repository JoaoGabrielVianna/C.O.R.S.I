package app

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
)

// Execution continuity: what this conversation's earlier turns actually DID,
// carried into the turns that follow.
//
// ══════════════════════════════════════════════════════════════════════
//
//	RECEIPT PROVES EXECUTION; READ PROVES STATE
//
// ══════════════════════════════════════════════════════════════════════
//
// ── The failure it exists to answer ────────────────────────────────────
// A live agent called a write capability. It ran, it succeeded, and the
// receipt recorded EXECUTED. On the next turn the same agent said:
//
//	"reparei que na verdade não cheguei a criar a lista — só respondi
//	 como se tivesse criado. Desculpa."
//
// False, and the model was not being careless. It was reasoning correctly
// from everything it could see. The capability was Confidential, so its
// payload is not kept and evidence continuity skips it (see evidence.go,
// which counts a redacted record as one that carried nothing). Its own
// earlier sentence is not the record, and it is instructed as much. So the
// turn contained no trace whatsoever that the call had run — and asked
// whether it had, the model asserted the negative.
//
// The consequence is not cosmetic. Convinced it had not written, it wrote
// again: two identical rows, six seconds apart. Denial of a real write is a
// duplication bug wearing a sentence.
//
// ── Why the receipt and not a replayed result ──────────────────────────
// Because the payload is precisely what must not come back. `WriteReceipt`
// already carries the whole answer to "did anything run" with no arguments
// and no result in it — that is its stated contract — so the block that
// closes this gap is a rendering of a record the system already computes,
// not a second, weaker copy of the data redaction removed.
//
// That is also the exact boundary of the guarantee, and it is one sentence:
// the receipt is authority for WHETHER something ran, and authority for
// nothing else. It does not say what was written. It does not say what the
// record holds now. A model that needs either must read through a capability
// in the current turn, which is what the grounding policy tells it to do.
//
// ── What this is NOT ───────────────────────────────────────────────────
// It is not a claim detector: nothing here reads the model's sentence.
// It is not authorization: a revoked capability cannot run, and that is
// decided in send.go.
// It is not a new receipt. Every entry below is derived by the SAME function
// the API and the interface derive theirs from — domain.NewWriteReceipt —
// so the block and the receipt beside the message cannot disagree about what
// a turn did. Nothing here writes an audit row, and carrying evidence about
// an earlier turn's write does not make the carrying turn a writing turn.

/* ── what a turn is reported to have done ────────────────────────────── */

// ExecutionEntry is one capability's outcome on one earlier turn, with
// identical outcomes folded together.
//
// ── Why the capability name is carried and is safe ─────────────────────
// It is not payload. It is a static identifier from the registry, bounded
// by ToolName's own contract to a few lowercase dotted segments, and it is
// already on the wire in the tool declaration of every turn that can use
// it. It came from the model's own request, never from a result.
//
// It is also what makes the block worth its characters. "One write
// executed" leaves the model guessing which; `palace.artifact.create
// EXECUTED` answers the question that was actually asked.
//
// ── Why the error code and never the message ───────────────────────────
// The code is a closed vocabulary of six constants and cannot carry
// content. The MESSAGE can, and routinely does: a domain refusal is allowed
// to name a field, a limit or an id, and `error_message` is NOT covered by
// the redaction that empties `arguments` and `result`. So the code travels
// and the message does not.
//
// The code also earns its place. A turn stopped by the ceiling records
// NOT_EXECUTED with `tool_round_limit`, and a model that cannot see that
// blames itself: the live battery produced "eu não cheguei a marcá-lo — só
// respondi como se tivesse feito" for a call the RUNTIME had refused.
type ExecutionEntry struct {
	Capability domain.ToolName
	Status     domain.WriteExecutionStatus
	// ErrorCode is empty on EXECUTED and present on the rest, exactly as the
	// receipt carries it.
	ErrorCode domain.ToolErrorCode
	// Refs are the entities this capability touched, in the order it
	// touched them. Empty whenever the capability reported no identity,
	// which is most of them.
	//
	// ── Why a list and not one ref folded into the key ────────────────
	// Two `item.add` calls on the same list are one line saying two; two
	// `artifact.create` calls made two different artifacts and must show
	// both, or a resume would be told one exists when two do. So identical
	// outcomes still fold, and every ref they carried is kept.
	Refs  []domain.EffectRef
	Count int
}

// ExecutionTurn is one earlier assistant turn's write record.
//
// A turn with no writes produces no ExecutionTurn at all rather than an
// empty one. It would render as a heading introducing nothing, and paying
// for a line that says "this turn changed nothing" on every read-only turn
// of every conversation is a cost with no reader: the whole block is
// one-directional, and its header says so.
type ExecutionTurn struct {
	MessageID uuid.UUID
	Entries   []ExecutionEntry
}

// ExecutionEvidence is what a turn will carry about what came before it, and
// an account of what it will not.
type ExecutionEvidence struct {
	Turns []ExecutionTurn
	// DroppedTurns is turns the budget refused. Counted rather than silently
	// absent, because the block is already allowed to be incomplete and the
	// report is the only place that can say by how much.
	DroppedTurns int
	DroppedChars int
}

// Empty reports whether this contributes nothing to the wire.
func (e ExecutionEvidence) Empty() bool { return len(e.Turns) == 0 }

/* ── selection ───────────────────────────────────────────────────────── */

// ExecutionBudgetChars is the ceiling on what the execution record may
// contribute to one turn.
//
// The smallest budget in the ladder — memory 4.000, evidence 8.000, sources
// 12.000 — and deliberately so, because this is the densest block in the
// system: an entry is a name, a word and a small integer, and 1.500
// characters carries on the order of thirty of them. A conversation would
// have to be an unbroken run of write-heavy turns to reach it.
//
// It exists anyway, and it is not decoration. The window is `history_limit`
// turns, which an operator may set to forty; without a ceiling a long
// conversation of busy turns would grow this block without any bound the
// person could see.
const ExecutionBudgetChars = 1500

// SelectExecutionEvidence builds the record of what earlier turns did.
//
// `order` is the assistant turns of the history window, oldest first — the
// history decides the scope, not the audit trail, so the block can never
// describe a turn the model cannot see. `records` is the same slice evidence
// continuity was handed.
//
// ── Why newest-first while the budget lasts ────────────────────────────
// Rule 3 of SelectEvidence, for a sharper reason. The false negative this
// closes is overwhelmingly about the turn that just happened: the user asks
// "you didn't do it?" about the thing they watched being done. If the budget
// ever binds, the entry that must survive is the last one.
//
// What comes back is chronological, because that is the order the
// conversation happened in and the order the history below it reads.
func SelectExecutionEvidence(
	order []uuid.UUID,
	records []domain.ToolCallRecord,
	budget int,
) ExecutionEvidence {
	var out ExecutionEvidence
	if len(order) == 0 || len(records) == 0 {
		return out
	}

	byMessage := make(map[uuid.UUID][]domain.ToolCallRecord, len(order))
	for _, rec := range records {
		byMessage[rec.MessageID] = append(byMessage[rec.MessageID], rec)
	}

	used := utf8.RuneCountInString(executionHeader)
	picked := make([]ExecutionTurn, 0, len(order))

	for i := len(order) - 1; i >= 0; i-- {
		id := order[i]
		turn := executionTurnFor(id, byMessage[id])
		if len(turn.Entries) == 0 {
			continue
		}
		cost := utf8.RuneCountInString(renderExecutionTurn(turn))
		if used+cost > budget {
			// Not a break, for the reason selectWithinBudget gives: one
			// oversized turn must not hide the smaller ones behind it.
			out.DroppedTurns++
			out.DroppedChars += cost
			continue
		}
		used += cost
		picked = append(picked, turn)
	}

	for i := len(picked) - 1; i >= 0; i-- {
		out.Turns = append(out.Turns, picked[i])
	}
	return out
}

// executionTurnFor derives one turn's record from its audit rows.
//
// ── Why it goes through domain.NewWriteReceipt ─────────────────────────
// Because that function IS the definition of what a turn is reported to
// have done, and it is what `GET /messages`, the stream's `done` frame and
// the interface all read. Re-deriving the same thing here from `Status` and
// `Effect` would be a second implementation of one rule, free to disagree
// with the first the day either changes — and the whole value of this block
// is that it says the same thing the receipt says.
//
// It is also what makes the read/write split correct for free: the receipt
// skips read-effect calls, so a read-only turn arrives here with no writes
// and produces no entry. A read can never appear in this block, and
// therefore can never be mistaken for one.
func executionTurnFor(id uuid.UUID, records []domain.ToolCallRecord) ExecutionTurn {
	turn := ExecutionTurn{MessageID: id}
	if len(records) == 0 {
		return turn
	}
	receipt := domain.NewWriteReceipt(id, records)

	// Folded by (capability, status, code) in first-appearance order, which
	// the audit's own `round, id` ordering makes deterministic. Five calls to
	// one capability are one line saying five, not five lines: the model
	// gains nothing from the repetition and the budget pays for all of it.
	index := make(map[string]int, len(receipt.Writes))
	for _, w := range receipt.Writes {
		key := string(w.Capability) + "\x00" + string(w.Status) + "\x00" + string(w.ErrorCode)
		if i, ok := index[key]; ok {
			turn.Entries[i].Count++
			turn.Entries[i].Refs = appendRef(turn.Entries[i].Refs, w.Ref)
			continue
		}
		index[key] = len(turn.Entries)
		turn.Entries = append(turn.Entries, ExecutionEntry{
			Capability: w.Capability,
			Status:     w.Status,
			ErrorCode:  w.ErrorCode,
			Refs:       appendRef(nil, w.Ref),
			Count:      1,
		})
	}
	return turn
}

/* ── rendering ───────────────────────────────────────────────────────── */

// executionHeader says what the block is and, more importantly, what its
// silence does not mean.
//
// ── Why the scope limit is in the header and not in the policy ─────────
// Because it is a fact about THIS block on THIS turn, not a rule. The
// window is `history_limit` turns and the budget may drop more, so a
// conversation can easily contain executed writes the block does not list.
// A header that claimed to be the whole record would manufacture the
// opposite failure: the model would read an absence as proof, and deny a
// write for a new reason.
//
// So the block is explicitly one-directional, and it is stated here rather
// than left to be inferred. What is listed happened. What is not listed is
// simply not evidence either way.
//
// ── Why the behavioural rule is NOT here ───────────────────────────────
// "Never deny something recorded as EXECUTED" is a rule about honesty that
// holds whether or not this block exists, so it belongs in the grounding
// policy — which is inside the cached prefix and is therefore paid for
// approximately once per agent rather than on every turn that writes. This
// header is the part that changes, and it is kept to the length that
// requires. See groundingPolicy.
const executionHeader = `Execution record for this conversation: the capabilities that actually ran on earlier turns, from this system's own log. Each "### turn" below is one earlier assistant turn, in the same order as the conversation. It covers only the turns shown there, so something absent from it is not evidence that it did not happen.

A "ref=" names the thing an entry touched. It identifies that thing and says nothing about what it is called or contains: to state any of that, read it with a capability.`

// executionTurnMarker introduces one turn. It carries no number: a turn
// index would be a name for something the model has no other way to
// reference, and it would invite an answer that cites "turn 3" at a person
// who has never seen one. Position is the whole of the ordering, and the
// header says what the position means.
const executionTurnMarker = "\n\n### turn\n"

// appendRef keeps the identities a folded entry accumulated, without
// repeating one. Nil refs — the common case — add nothing.
func appendRef(refs []domain.EffectRef, r *domain.EffectRef) []domain.EffectRef {
	if r == nil || !r.Valid() {
		return refs
	}
	for _, have := range refs {
		if have == *r {
			return refs
		}
	}
	return append(refs, *r)
}

// renderExecutionTurn is one turn's text, and it is the single definition of
// that text.
//
// Both the budget and the rendered block go through this function rather
// than through a cost estimate written beside it. The other blocks keep a
// separate `xCost` held to the renderer by a test; here the two cannot drift
// at all, because there is only one of them.
func renderExecutionTurn(t ExecutionTurn) string {
	var b strings.Builder
	b.WriteString(executionTurnMarker)
	for _, e := range t.Entries {
		// `STATUS capability xN [code]`. The status words are the receipt's
		// own constants, not a second vocabulary invented for the prompt: the
		// model, the API and the interface all say EXECUTED for the same
		// fact.
		b.WriteString(string(e.Status))
		b.WriteByte(' ')
		b.WriteString(e.Capability.String())
		b.WriteString(" x")
		b.WriteString(strconv.Itoa(e.Count))
		if e.ErrorCode != "" {
			b.WriteByte(' ')
			b.WriteString(string(e.ErrorCode))
		}
		// `ref=artifact:<uuid>`. A type and a UUID, which is the whole of
		// what a later turn is allowed to know about an effect without
		// reading it. Never a title, never a field of the payload.
		for _, r := range e.Refs {
			b.WriteString(" ref=")
			b.WriteString(r.String())
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// RenderExecutionBlock is the exact text the model receives.
//
// One system message for the whole block, like memory, sources and
// evidence, and for the same two reasons: a message per turn would read as
// a dialogue the model took part in, and would pay the per-message overhead
// every gateway charges once per entry.
//
// Note what it is NOT rendered as, and the note matters more here than it
// does for evidence: not as an assistant message, not as a `tool` message,
// and not as anything carrying a tool_call_id. This is a statement BY the
// system ABOUT the past. Rendering it as the model's own words would make
// the record indistinguishable from the prose the record exists to check.
func RenderExecutionBlock(turns []ExecutionTurn) string {
	var b strings.Builder
	b.WriteString(executionHeader)
	for _, t := range turns {
		b.WriteString(renderExecutionTurn(t))
	}
	return b.String()
}
