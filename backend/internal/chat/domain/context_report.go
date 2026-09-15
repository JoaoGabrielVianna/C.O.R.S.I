package domain

// The account of what one turn actually carried.
//
// ── Why this lives in the domain and not in the builder ────────────────
// It used to live beside the Context Builder, because the builder was the
// only thing that produced it and nothing consumed it. That stopped being
// true the moment a turn had to be inspectable after the fact: a report is
// now stamped onto the assistant message and stored with it, which makes it
// part of what a turn *is* rather than a by-product of composing one.
//
// The builder still owns the policy — which blocks exist, what fits, what
// gets cut. This file owns only the vocabulary they are recorded in.
//
// ── Why it is a snapshot and not a query ───────────────────────────────
// Everything here is a count or a reason. There is deliberately no id, no
// pointer and no reference to a memory, a source or an agent setting, and
// that absence is the design: a report must remain true after the memories
// were edited, the sources rewritten, the instructions changed and the
// history limit lowered. A report that named its inputs would invite being
// "resolved" against today's data, which would turn a historical record
// into a fabrication.
//
// ── Why it carries no content ──────────────────────────────────────────
// The report says how much and why, never what. Storing the text again
// would double the cost of every turn to duplicate something already
// stored, and would put instructions, memories and source material into a
// second place that has to be secured, deleted and reasoned about. The
// content stays where it lives.

// BlockKind names one contributor to a turn's context.
//
// Every kind has a producer, and a kind exists for one reason: it is paid
// for. That is why the tool ones are here despite not travelling as
// messages. A tool declaration rides in the request's `tools` field
// rather than in a message, and a tool result rides as a `tool` message on
// the second provider call — but both are prompt tokens billed at input
// rates, and a report that omitted them would understate what a turn cost
// precisely on the turns that cost the most.
type BlockKind string

const (
	BlockInstructions   BlockKind = "instructions"
	BlockMemory         BlockKind = "memory"
	BlockSources        BlockKind = "sources"
	BlockHistory        BlockKind = "history"
	BlockCurrentMessage BlockKind = "current_message"
	// BlockTools is the declaration of the authorized tools, sent on every
	// provider call of a turn that has any. Its characters are the JSON of
	// the names, descriptions and schemas.
	BlockTools BlockKind = "tools"
	// BlockToolResults is what the tools returned, fed back to the model as
	// `tool` messages. Only a turn that actually ran a tool has one.
	BlockToolResults BlockKind = "tool_results"
	// BlockToolEvidence is what tools returned on EARLIER turns of the same
	// conversation, replayed so a follow-up question does not have to be
	// answered from the assistant's own prose about what it once saw.
	//
	// ── Why it is not BlockToolResults ─────────────────────────────────
	// They are billed the same way and they are not the same thing. Tool
	// results are this turn's live exchange, produced between provider calls
	// and answering calls the model just made. Tool evidence is history: it
	// carries no tool_call_id, it answers nothing, and it is read from the
	// audit trail rather than from an executor. A reader who could not tell
	// them apart could not answer "did this turn actually run anything?",
	// which is the first question anyone asks of a turn that used tools.
	BlockToolEvidence BlockKind = "tool_evidence"
	// BlockExecutionEvidence is what EARLIER turns of this conversation
	// actually DID: the write capabilities that ran, and how each one ended.
	//
	// ── Why it is not BlockToolEvidence ────────────────────────────────
	// They are opposites, and folding them together would be a lie about
	// provenance in both directions. Tool evidence is PAYLOAD that came from
	// outside this system — file contents, descriptions, somebody else's
	// prose — and its header says so, because it has to be read as data that
	// may not be trusted. This block contains no payload at all: a capability
	// name, an outcome, a count, all of them derived by this system from its
	// own execution log. Putting our execution facts under a header that
	// disclaims them as untrusted outside content would teach the model to
	// discount the one record that is authoritative.
	//
	// The lifetimes differ too. A Confidential capability contributes NOTHING
	// to tool evidence — its payload is not kept, so there is nothing to
	// replay — and it contributes here exactly like any other, because what
	// is carried was never its payload.
	//
	// ── What it exists to close ────────────────────────────────────────
	// The mirror of the defect WriteReceipt answers. A live agent executed
	// `artifact.create`, the receipt recorded EXECUTED, and on the next turn
	// the model said "na verdade eu não cheguei a criar — só respondi como se
	// tivesse". It was reasoning correctly from what it could see: the
	// payload was withheld, its own earlier sentence is not the record, and
	// nothing in the turn said the call had run. Told nothing, it asserted
	// the negative, and then wrote the same thing again.
	BlockExecutionEvidence BlockKind = "execution_evidence"
	// BlockContextReferences names the ENTITIES this turn is about: the
	// subjects the user attached, plus whatever the thread was opened from.
	//
	// ── Why it is its own block and not part of the instructions ───────
	// Because it is per-turn evidence, not policy. The instructions are the
	// same bytes on every turn of an agent's life; this changes with what
	// the user attached, and a reader asking "why did this turn cost more"
	// has to be able to see it as a line item. It is also the block a
	// reader checks to answer "did the model actually know which
	// opportunity we meant?", which is unanswerable if it is folded into
	// the prompt.
	//
	// It carries identity, not entities: no stage, no state, no snapshot.
	// See app/context_references.go.
	BlockContextReferences BlockKind = "context_references"
	// BlockResume is what a CONTINUING turn is told about the attempt it
	// continues: which writes already executed, which entities they touched,
	// what is still pending, and why the first attempt stopped.
	//
	// ── Why it is not BlockExecutionEvidence ───────────────────────────
	// Execution evidence is a passive record of what earlier turns did,
	// carried on every turn of a conversation that wrote anything. This is
	// an instruction about ONE attempt, present only on a resume, and it
	// tells the model what to DO — continue, do not repeat. Folding them
	// together would make every ordinary turn carry resume wording, and
	// would lose which attempt a continuation is bound to.
	BlockResume BlockKind = "resume"
	// BlockReferenceState is the PRESENT state of the subjects above, read
	// from their providers at the start of this turn.
	//
	// ── Why it is not part of BlockContextReferences ───────────────────
	// Because they answer opposite questions and have opposite lifetimes.
	// The reference block is identity, it is persisted, and it is the same
	// bytes on every turn of a thread. This block is state, it is read
	// fresh, it is never written back, and it is the one line item that
	// explains why turn nine of a conversation cost more than turn eight.
	//
	// Keeping them apart is also what makes the report answer the question
	// an operator actually asks after a wrong answer: not "did the model
	// know which opportunity we meant" — that is the other block — but "did
	// this turn have the current stage in front of it, or did it not?".
	//
	// Its exclusions are the two ways state is not obtained: the capability
	// was not granted (ReasonUnauthorized) or the read did not answer
	// (ReasonUnavailable). Both are still CHARACTERS in the block, because
	// the model is told in words that the state is unavailable, and that
	// sentence is paid for like every other.
	BlockReferenceState BlockKind = "reference_state"
)

// ExclusionReason says why something a turn could have carried did not
// make it onto the wire. Only reasons that actually occur are defined:
// a constant with no producer is a promise the code does not keep.
//
// This is a closed vocabulary and the backend is its only author. A reader
// that invents a category, or infers one from a count, is reporting
// something the system never said.
type ExclusionReason string

const (
	// ReasonEmptyTurn covers a stored turn with no text. An aborted stream
	// is persisted anyway, carrying its finish reason, so the thread keeps
	// a record of what happened — but some gateways reject an empty
	// assistant message outright, so it is dropped from the replay.
	ReasonEmptyTurn ExclusionReason = "empty_turn"

	// ReasonBudget covers an item that did not fit the block's character
	// ceiling *this time*. Turning something else off would have let it in.
	// Cutting is normal; cutting silently is not, which is why every
	// dropped item is counted here.
	ReasonBudget ExclusionReason = "budget"

	// ReasonTooLarge covers an item that could not have fitted whatever
	// else was turned off — it is bigger than the block's whole ceiling on
	// its own.
	//
	// Distinct from ReasonBudget on purpose, and the distinction is the
	// difference between "switch something off" and "shorten this". A
	// reader that saw only "budget" would keep turning other items off,
	// waiting for an event that can never happen.
	ReasonTooLarge ExclusionReason = "too_large"

	// ReasonUnavailable covers a block whose source could not be read at
	// all. Items is 0 not because nothing was dropped but because the
	// count is exactly what the failure destroyed: a turn that could not
	// read memory does not know how much memory it was missing.
	//
	// This exists so a degraded turn is a *reported* fact rather than an
	// invisible one. The turn still happens: losing an answer because a
	// side table was unreachable would be a worse trade than answering
	// with less context.
	ReasonUnavailable ExclusionReason = "unavailable"

	// ReasonNotSelected covers something the agent was authorized to use
	// and the user did not attach to this turn. Only the tools block can
	// carry it, and only on a turn that made an explicit selection.
	//
	// ── Why this is an exclusion and not a new field ───────────────────
	// Because it is the same fact every other exclusion records: a turn
	// could have carried something and did not, and the reader is told
	// rather than left to infer it from a count that looks small. It is
	// also what makes "how many was this agent authorized to use?"
	// answerable from the snapshot alone — exposed plus withheld — without
	// storing names and without resolving anything against the grants as
	// they stand today, which by then may be a different set.
	//
	// Distinct from ReasonBudget, and the distinction is the whole point:
	// budget means "there was not room", this means "you did not ask for
	// it". Nothing was too big and nothing failed; the turn was scoped.
	ReasonNotSelected ExclusionReason = "not_selected"

	// ReasonTruncated covers the tail of an item that WAS carried, cut so
	// that what remained would fit. Characters is what was dropped.
	//
	// ── Why this reason exists when the others forbid truncation ───────
	// Memory and Sources take whole items or none, because half a written
	// statement is a statement that now says something else. Replayed tool
	// evidence is not a statement, it is a data payload — the first half of
	// a file listing is the first half of a file listing — and the tools
	// that produce it already have a convention for saying so in the
	// payload. Dropping a 13.000-character file read whole, to protect a
	// rule written about sentences, would throw away the most informative
	// evidence a conversation had.
	//
	// It is reported rather than done quietly, which is the actual rule
	// being kept: the Inspector says how much was cut, and the model is told
	// inside the block that it is reading part of something.
	ReasonTruncated ExclusionReason = "truncated"

	// ReasonSuperseded covers an item a later, identical one replaced. Only
	// tool evidence uses it: the same capability called twice with the same
	// arguments observed one thing twice, and carrying both would spend the
	// block's budget re-asserting the older, staler copy.
	ReasonSuperseded ExclusionReason = "superseded"

	// ReasonFailed covers an item that is not carried because it records a
	// failure. Only tool evidence uses it: a call that errored produced no
	// observation, and replaying its message as though it were one is how
	// "the tool refused" becomes "the repository is empty" two turns later.
	ReasonFailed ExclusionReason = "failed"

	// ReasonUnauthorized covers something this agent was not granted the
	// capability to read. Only reference state uses it: a subject the user
	// attached whose present state needs a tool grant the agent does not
	// have.
	//
	// ── Why it is not ReasonUnavailable ────────────────────────────────
	// Because they call for different actions and describe different
	// systems. Unavailable means something broke or vanished and there is
	// nothing to do but wait; this means a person made a decision and can
	// unmake it on the agent's settings page. A reader who could not tell
	// them apart would go looking for an outage that is really a revoked
	// grant working exactly as designed.
	//
	// It is also the line that proves the invariant: a turn whose report
	// shows this carried NO state for that subject, which is what "a
	// reference grants no capability" means when it is written down.
	ReasonUnauthorized ExclusionReason = "unauthorized"
)

// ContextExclusion is one reason's worth of what was left out of a block.
type ContextExclusion struct {
	Reason     ExclusionReason `json:"reason"`
	Items      int             `json:"items"`
	Characters int             `json:"characters"`
}

// ContextBlock is what one kind contributed to the turn.
//
// Characters is counted in runes, not bytes: it feeds a token estimate,
// and "ç" is one character the model sees and two bytes on disk.
type ContextBlock struct {
	Kind            BlockKind          `json:"kind"`
	Items           int                `json:"items"`
	Characters      int                `json:"characters"`
	EstimatedTokens int                `json:"estimated_tokens"`
	Exclusions      []ContextExclusion `json:"exclusions,omitempty"`
}

// ContextReport describes the context that was built.
//
// It carries only blocks that contributed or excluded something, in
// composition order. A kind that is absent contributed nothing, which is
// the honest way to render "memory: 0" without inventing a row.
//
// Note the asymmetry in the names: characters are counted exactly, tokens
// are estimated.
type ContextReport struct {
	Blocks               []ContextBlock `json:"blocks"`
	TotalCharacters      int            `json:"total_characters"`
	TotalEstimatedTokens int            `json:"total_estimated_tokens"`
	// Rounds is one entry per call this turn made to the provider, and it is
	// present only on a turn that made more than one.
	//
	// ── Why it exists ──────────────────────────────────────────────────
	// Until tools, "one user turn" and "one provider call" were the same
	// thing, so the block list above described the whole turn. With tools
	// they are not: the context grows between calls, and the tokens and the
	// money are spent once per call. Showing the first call's snapshot as
	// though it described all of them would be the Inspector's first lie.
	//
	// ── Why it is a summary and not a trace ────────────────────────────
	// Each entry is counts and names — what the round added, what it cost,
	// which tools it asked for and whether they worked. No message bodies,
	// no arguments, no results: those live in chat.tool_calls, which is the
	// record designed to be redactable. This stays small enough that a turn
	// with the maximum number of rounds is still a few hundred bytes.
	Rounds []ContextRound `json:"rounds,omitempty"`
	// CacheBreakpointAfter names the block this turn asked the provider to
	// cache up to, or is empty when it asked for nothing.
	//
	// ── Why the request is recorded and not only the outcome ───────────
	// Because a cache read of zero has two completely different causes and
	// the counts alone cannot tell them apart: the turn never asked, or it
	// asked and the prefix had changed. The first is a configuration fact,
	// the second is a prefix-stability bug worth hunting. Without this
	// field the Inspector could only show the zero.
	//
	// Empty on every turn recorded before caching existed, and on every turn
	// of a deployment that has it switched off — which is the same fact
	// stated the same way.
	CacheBreakpointAfter BlockKind `json:"cache_breakpoint_after,omitempty"`
}

// ContextRound is one provider call inside a turn.
//
// AddedCharacters is what this round put on the wire *beyond* the previous
// one: the tool results and the assistant's own tool-call request. Round 1
// reports zero, because everything it carried is already itemised in Blocks.
type ContextRound struct {
	Round                int         `json:"round"`
	AddedCharacters      int         `json:"added_characters"`
	AddedEstimatedTokens int         `json:"added_estimated_tokens"`
	PromptTokens         int         `json:"prompt_tokens"`
	CompletionTokens     int         `json:"completion_tokens"`
	UsageSource          UsageSource `json:"usage_source"`
	// Cost is what this one call cost, nil when it could not be priced.
	// Summing the rounds gives the turn's cost; that is how the turn's
	// frozen figure was produced in the first place.
	Cost *float64 `json:"cost"`
	// FinishReason is why this call stopped. `tool_calls` on every round but
	// the last.
	FinishReason string `json:"finish_reason,omitempty"`
	// Tools is what this round asked to run, in the order it asked.
	Tools []RoundToolCall `json:"tools,omitempty"`
	// Usage is everything else the provider reported about THIS call.
	//
	// Absent on a round the provider did not measure, and absent on a
	// gateway that reports only the two totals — which is the state every
	// round recorded before this field existed. It is per round rather than
	// per turn because that is the granularity the question is asked at: a
	// tool turn whose first call wrote the cache and whose second read it is
	// the shape this whole field exists to make visible.
	Usage *RoundUsage `json:"usage,omitempty"`
}

// RoundUsage is the provider's own account of one call, beyond the two
// counts ContextRound already carries.
//
// Every field is a pointer and nil means the provider did not report it.
// None of them is ever computed, defaulted or back-filled: this is a record
// of what a gateway said, and the only honest answer to a question it did
// not answer is silence.
//
// It carries no content and no identifiers, like the rest of the report.
type RoundUsage struct {
	// TotalTokens is the provider's own total, recorded rather than derived.
	// Whether it equals prompt + completion on a cached call is a fact about
	// the gateway, not an identity to assume.
	TotalTokens *int `json:"total_tokens,omitempty"`
	// CacheReadTokens and CacheCreationTokens are Anthropic's own counts,
	// forwarded by the gateway. Read at a fraction of the input rate,
	// written at a premium over it.
	CacheReadTokens     *int `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens *int `json:"cache_creation_tokens,omitempty"`
	// CachedTokens is the OpenAI-shaped restatement of the read count. Kept
	// beside CacheReadTokens rather than merged into it, because whether the
	// two agree on a given deployment is something to observe rather than
	// assume. See ports.Usage.
	CachedTokens *int `json:"cached_tokens,omitempty"`
	// ReasoningTokens is the share of the output that was thought. Part of
	// CompletionTokens, not additional to it.
	ReasoningTokens *int `json:"reasoning_tokens,omitempty"`
}

// RoundToolCall is the outcome of one tool call, at report granularity.
type RoundToolCall struct {
	Name   ToolName       `json:"name"`
	Status ToolCallStatus `json:"status"`
	// ErrorCode is empty on success. It is carried here so the transcript
	// can say "this one was refused" without reading the audit table.
	ErrorCode  ToolErrorCode `json:"error_code,omitempty"`
	DurationMS int           `json:"duration_ms"`
}

// Block returns the report for one kind, and whether it contributed.
func (r ContextReport) Block(kind BlockKind) (ContextBlock, bool) {
	for _, b := range r.Blocks {
		if b.Kind == kind {
			return b, true
		}
	}
	return ContextBlock{}, false
}

// Degraded lists the kinds whose source could not be read for this turn.
//
// It exists so every surface answers "was anything missing?" the same way,
// from the same field, instead of each one re-deriving it from the
// exclusion list and one of them eventually getting it wrong.
func (r ContextReport) Degraded() []BlockKind {
	var out []BlockKind
	for _, b := range r.Blocks {
		for _, ex := range b.Exclusions {
			if ex.Reason == ReasonUnavailable {
				out = append(out, b.Kind)
				break
			}
		}
	}
	return out
}
