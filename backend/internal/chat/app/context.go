package app

import (
	"math"
	"strings"
	"unicode/utf8"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

// Context composition for one turn.
//
// This file is the single answer to "what context is this turn sending to
// the model?". Before it existed the answer was a thirteen-line helper
// inside send.go, which was enough while the answer was
// `system_prompt + tail(history_limit)` and stops being enough the moment
// anything else can contribute — memory, sources, and eventually a tool
// declaration.
//
// ── What it is responsible for ─────────────────────────────────────────
// Composing the message list that goes on the wire, in a deterministic
// order, and reporting what went in and what was left out.
//
// ── What it is deliberately NOT responsible for ────────────────────────
// Calling the provider, persisting anything, reading a repository, pricing
// a turn, enforcing a budget, resolving a provider, or selecting tools. It
// takes values in and returns values out; it has no collaborators. That is
// what makes it testable without a database and what keeps the next batch
// from turning it into a second service layer.
//
// ── The window is not composed here ────────────────────────────────────
// How much history a turn replays is `agent.history_limit`, applied by the
// repository (`Messages.ListRecent`) because it is a query concern: the
// tail is fetched with `ORDER BY seq DESC LIMIT n`, never loaded whole and
// then sliced. The builder composes exactly the window it is handed and
// reports how many messages that turned out to be. Applying the limit here
// as well would mean two places deciding the same thing.

// ContextInput is everything the builder needs, and nothing else.
//
// Current is the user turn being answered. It is a *persisted* message on
// purpose: the builder cannot be called before the question is written,
// which is the invariant that keeps a provider failure from costing the
// user what they typed. It is also present as the last entry of History,
// because History is the window the repository returned after the write —
// Current is passed separately so the report can tell the question apart
// from the conversation behind it, not to add it to the wire twice.
type ContextInput struct {
	// Agent supplies the instructions. It is passed whole because the knobs
	// that sources will need are on the same aggregate.
	Agent *domain.Agent
	// Memories are the agent's enabled memories, already in selection
	// order (pinned first, then most recently updated) — the repository
	// sorts them, because that is a query concern, and the builder decides
	// only how many of them fit.
	Memories []domain.Memory
	// MemoryUnavailable says the memory read failed. It is a separate flag
	// rather than an empty slice because "this agent remembers nothing" and
	// "we could not find out what this agent remembers" are different facts
	// and the report must not conflate them.
	MemoryUnavailable bool
	// Sources are the agent's enabled reference material, already in
	// selection order. Same division of labour as Memories: the repository
	// sorts, the builder decides how much fits.
	Sources []domain.Source
	// SourcesUnavailable is the same distinction as MemoryUnavailable, for
	// the same reason.
	SourcesUnavailable bool
	// Tools are the capabilities this agent is authorized to use, already
	// resolved against the registry. Empty means the request carries no
	// `tools` field at all.
	//
	// There is no ToolsUnavailable twin. Memory and Sources degrade because
	// answering with less context is better than not answering; tools do
	// not, because a turn that silently loses its capabilities invents an
	// answer instead of using one. A failed read refuses the turn before the
	// builder is ever called — see Service.authorizedTools.
	Tools []domain.ToolDefinition
	// ContextReferences are the ENTITIES this turn is about: what the user
	// attached to it, plus what the thread was opened from.
	//
	// They are identity only — see app/context_references.go — so the
	// builder treats them as a small fixed cost rather than something to
	// select within a budget. There is no ContextReferencesUnavailable
	// twin: an attachment that could not be resolved is refused at attach
	// time and never reaches storage, so by the time a turn is built there
	// is nothing left to degrade.
	ContextReferences []domain.ContextReference
	// HydratedReferences is the PRESENT state of those subjects whose type
	// declares per-turn freshness, read before this turn was composed.
	//
	// One entry per subject the runtime tried to hydrate — including the ones
	// it was not authorized to read and the ones that could not be found,
	// because "we did not read this" is something the model has to be told
	// rather than left to infer from an absence. Subjects whose type declares
	// no freshness at all are simply not here.
	//
	// There is no HydratedReferencesUnavailable twin: a failure to read is
	// per subject, carried as that subject's status, which is more precise
	// than a flag over the whole block could be. See app/hydration.go.
	HydratedReferences []HydratedReference
	// ToolsWithheld is how many authorized tools this turn deliberately did
	// not declare, because the user scoped it with an explicit selection.
	//
	// A count and not a list: the report holds no names anywhere, and the
	// names of what WAS selected are already frozen on the user's turn,
	// which is the record designed to answer that question. Zero on every
	// turn without a selection, which is every turn the module has ever had
	// until now.
	ToolsWithheld int
	// Evidence is what tools returned on earlier turns of this same
	// conversation, already selected and bounded — see SelectEvidence. Empty
	// on every turn of every conversation that never ran a tool, which keeps
	// those turns byte-identical to what they were before this field existed.
	Evidence EvidenceSelection
	// EvidenceUnavailable says the audit-trail read failed. Same distinction
	// as MemoryUnavailable: "this conversation observed nothing" and "we
	// could not find out what it observed" are different facts.
	EvidenceUnavailable bool
	// History is the trailing window of the thread in reading order, with
	// Current as its last entry.
	History []domain.Message
	Current *domain.Message
	// PromptCache asks the provider to cache the stable head of this
	// request. False builds exactly the context this builder built before
	// caching existed, byte for byte.
	//
	// It is an input rather than a constant because a before/after
	// measurement needs both halves runnable from one binary, and because a
	// gateway regression should cost an environment variable rather than a
	// deploy. See app.WithPromptCache.
	PromptCache bool
}

// BuiltContext is what a turn sends, plus the account of how it got there.
type BuiltContext struct {
	Messages []ports.ChatMessage
	// Tools ride in the request's own `tools` field rather than in the
	// message list, which is why they are a separate member here. They are
	// still context: they are sent on every call of the turn and billed as
	// input, and the report counts them for exactly that reason.
	Tools  []domain.ToolDefinition
	Report domain.ContextReport
}

// BuildContext composes the context for one turn.
//
// Order is fixed and is the whole point of the block list: instructions
// first, then the tool declaration, then memory, then sources, then the
// conversation, ending on the question being answered. Every kind has a
// producer.
//
// Tools sit second because that is where they belong conceptually — right
// after "who you are" comes "what you can do" — and because their position
// in the report is the order a reader scans. On the wire they are not in
// the message list at all, so this ordering costs nothing to arrange.
func BuildContext(in ContextInput) BuiltContext {
	// Calibrated to the model this turn is actually being sent to, so the
	// stored report is an estimate of THIS request rather than of an average
	// one. A turn with no agent falls back to the pessimistic default.
	model := ""
	if in.Agent != nil {
		model = in.Agent.Model
	}
	a := newContextAssembler(len(in.History)+3, EstimatorFor(model))

	// The policy is gated on what this turn can actually reach, which is
	// already the scoped set: a turn narrowed by `@` to one capability is
	// still a turn that can verify something, and a turn with none is not.
	a.instructions(in.Agent, len(in.Tools) > 0)
	a.tools(in.Tools, in.ToolsWithheld)
	// Subjects come after capabilities and before memory: the model reads
	// "who you are", "what you can do", "what this is about", and only then
	// the recalled material. Putting them last would bury the id the model
	// needs under everything it does not.
	//
	// Their PRESENT state follows immediately, and the adjacency is the
	// point: identity and state are one thought split across two blocks for
	// accounting reasons, and a reader — human or model — should meet them
	// together rather than have memory and sources wedged between "which
	// opportunity" and "what it is doing now".
	a.contextReferences(in.ContextReferences, in.HydratedReferences)
	a.referenceState(in.HydratedReferences)
	a.memory(in.Memories, in.MemoryUnavailable)
	a.sources(in.Sources, in.SourcesUnavailable)
	a.evidence(in.Evidence, in.EvidenceUnavailable)
	a.conversation(in.History, in.Current)

	if in.PromptCache {
		a.markStablePrefix()
	}

	built := a.finish()
	built.Tools = in.Tools
	return built
}

// markStablePrefix places this turn's single cache breakpoint.
//
// ── Where it goes, and why there ───────────────────────────────────────
// On the LAST message of the instructions block — the grounding policy and
// the agent's own system prompt. Those are the only two things in a request
// that are the same bytes on every turn of an agent's life.
//
// Everything after them moves. Subjects change with what the user attached,
// reference state is re-read every turn by design, evidence is recomputed
// from the audit trail every turn, and history grows. A breakpoint below any
// of those would be an entry written once and never read.
//
// ── What that one marker actually buys ─────────────────────────────────
// Far more than the instructions. The provider renders `tools` BEFORE
// `system`, so the tool declaration sits inside the marked prefix without
// being marked itself — and the tool catalogue is the single largest line
// item in this system's bill, 51,6% of every input token measured by the
// audit.
//
// That is not an inference about Anthropic's documented render order. It was
// measured through THIS deployment's gateway with a 66-character system
// message and a full-size catalogue: 18.387 of 18.829 prompt tokens came
// back as a cache read. See TestX3ToolCatalogueIsInsideTheCachedPrefix.
//
// ── Why exactly one, and why not more ──────────────────────────────────
// A second breakpoint at the end of history would extend the cached prefix
// over the conversation as well, and the audit modelled that as the
// difference between a 44% and a 64% saving. It is deliberately NOT done
// here: the blocks between instructions and history — reference state and
// tool evidence — are rewritten every turn, so a breakpoint below them would
// only ever be written and never read for the agents that carry them. Fixing
// that means REORDERING blocks, which this sprint has ruled out of scope by
// decision. One marker, placed where nothing has to move for it to work.
//
// ── When nothing is marked ─────────────────────────────────────────────
// An agent with no capabilities and no system prompt produces no
// instructions message, so there is nothing stable to mark and no marker is
// placed. A prefix below the model's minimum cacheable length is simply not
// cached by the provider, silently and at no cost, so a short prompt needs
// no special case here.
func (a *contextAssembler) markStablePrefix() {
	i, ok := a.index[domain.BlockInstructions]
	if !ok || a.blocks[i].Items == 0 {
		return
	}
	// The instructions are appended first and consecutively, so they are the
	// leading messages of the list. The last of them is the boundary.
	last := a.blocks[i].Items - 1
	if last < 0 || last >= len(a.messages) {
		return
	}
	a.messages[last].CacheBreakpoint = true
	a.cacheBreakpointAfter = domain.BlockInstructions
}

/* ── ESTIMATE, and what it is not ────────────────────────────────────── */

// Everything in this section produces an ESTIMATE: a prediction made from
// character counts, before a call, by this process.
//
//	ESTIMATE                what we think the input will be. Local, free,
//	                        available before the request goes out, and
//	                        wrong by design — see the margin below.
//	ACTUAL PROVIDER USAGE   what the gateway says the call consumed. It
//	                        arrives with the answer, it is what the invoice
//	                        is built from, and it is the only authority.
//
// The two are kept side by side on every stored turn — `estimated_prompt_
// tokens` beside `prompt_tokens`, qualified by `usage_source` — precisely so
// they can disagree and the disagreement can be measured. Nothing here ever
// overwrites the provider's number, and no report substitutes one for the
// other.
//
// ── Why the estimator was recalibrated ─────────────────────────────────
// It divided characters by four, the usual English rule of thumb. Against
// 159 real turns that assumption was wrong by nearly a factor of two and
// wrong in ONE direction: the density actually observed, measured as
// provider prompt tokens over the report's own character count, was
//
//	claude-opus-4-7    n=33   p50 0,4220   p95 0,4979   max 0,5000
//	claude-haiku-4-5   n=24   p50 0,3366   p95 0,3933   max 0,4274
//	claude-sonnet-4-6  n=4    p50 0,3816   p95 0,3988   max 0,3997
//
// against an assumed 0,2500. Portuguese with accents and dense JSON Schema
// both tokenise far worse than English prose.
//
// ── The margin, and where it comes from ────────────────────────────────
// Each figure below is the MAXIMUM observed for that family, not the
// median. That choice is the conservative margin, and it is deliberate: an
// estimator tuned to the median is wrong half the time, and half of those
// times it is LOW — which is the one direction a number feeding a spending
// limit must never be wrong in. Being high costs a slightly pessimistic
// display; being low costs a budget that does not hold.
//
// It is still an estimate and is still named one. It is not, and must never
// become, a billing input.

// modelTokenDensity is tokens per character, calibrated per model family.
//
// Keyed by family rather than by exact id so a point release inherits the
// calibration of its family instead of silently falling back to the
// default. A model nobody has measured gets defaultTokenDensity, which is
// the highest figure in this table.
var modelTokenDensity = []struct {
	prefix  string
	density float64
}{
	{"claude-opus", 0.50},
	{"claude-sonnet", 0.40},
	{"claude-haiku", 0.43},
}

// defaultTokenDensity is what an unmeasured model gets: the densest family
// in the table.
//
// An unknown model is the case where being wrong is most likely, so it gets
// the most pessimistic answer rather than an average of the ones we happen
// to have measured.
const defaultTokenDensity = 0.50

// TokenEstimator converts characters to an estimated token count for one
// model.
//
// A value type with no dependencies, so the Context Builder stays a pure
// function of its inputs and the estimate can be checked in a unit test
// without a gateway.
type TokenEstimator struct{ density float64 }

// EstimatorFor returns the calibrated estimator for a model.
func EstimatorFor(model string) TokenEstimator {
	for _, m := range modelTokenDensity {
		if strings.HasPrefix(model, m.prefix) {
			return TokenEstimator{density: m.density}
		}
	}
	return TokenEstimator{density: defaultTokenDensity}
}

// Tokens estimates how many tokens `characters` will become.
//
// Rounded UP, for the same reason the table holds maxima: a fractional
// token that gets floored is a token the estimate does not have and the
// invoice does.
func (e TokenEstimator) Tokens(characters int) int {
	if characters <= 0 {
		return 0
	}
	density := e.density
	if density <= 0 {
		density = defaultTokenDensity
	}
	return int(math.Ceil(float64(characters) * density))
}

// EstimateTokens is the model-agnostic estimator, for the callers that do
// not have a model in hand.
//
// The Sources page is the honest example: it shows what a document will
// cost an agent whose model it is not holding at that moment. It uses the
// most pessimistic density in the table, which is the right answer for a
// number a person reads before deciding whether to enable something.
//
// Callers that DO know the model should use EstimatorFor. The Context
// Builder does, which is why a turn's report is calibrated to the model
// that turn was actually sent to.
//
// Never use either to bill anything. The authoritative numbers are the
// prompt and completion counts the provider reports when the turn ends, and
// those are what `chat.messages` stores.
func EstimateTokens(characters int) int {
	return TokenEstimator{density: defaultTokenDensity}.Tokens(characters)
}

// wireContentOf is what a stored message contributes to the request body.
//
// Content, and only Content. `reasoning` is stored beside it and is never
// replayed on a later turn: it is displayed differently, some of it is not
// even the model's to re-read, and sending it back would quietly change
// what the model sees while multiplying the bill on every subsequent turn.
//
// This is a named function rather than a field access inside a loop so the
// rule has somewhere to live and something to point at. A mutation that
// appends the reasoning here fails TestReasoningIsNeverReplayed and
// TestHistoryReplayAndReasoningExclusion.
func wireContentOf(m domain.Message) string { return m.Content }

/* ── assembler ───────────────────────────────────────────────────────── */

// contextAssembler accumulates messages and their accounting together, so
// a block cannot contribute to the wire without also contributing to the
// report. Keeping the two in one place is what makes the report trustworthy
// instead of a second, drifting description of the same thing.
type contextAssembler struct {
	messages []ports.ChatMessage
	blocks   []domain.ContextBlock
	index    map[domain.BlockKind]int
	// est converts this turn's character counts into estimated tokens,
	// calibrated to the model the turn is going to.
	est TokenEstimator
	// cacheBreakpointAfter is the block this turn asked the provider to
	// cache up to, or empty when it asked for nothing. Recorded in the
	// report because "did this turn even request caching, and where" is the
	// first question anyone asks of a turn whose cache read came back zero.
	cacheBreakpointAfter domain.BlockKind
}

func newContextAssembler(capacity int, est TokenEstimator) *contextAssembler {
	return &contextAssembler{
		messages: make([]ports.ChatMessage, 0, capacity),
		index:    make(map[domain.BlockKind]int, len(blockKinds)),
		est:      est,
	}
}

// blockKinds exists only to size the index map. The composition order is
// the order BuildContext calls the producers in, not this slice.
var blockKinds = []domain.BlockKind{
	domain.BlockInstructions, domain.BlockTools,
	domain.BlockContextReferences, domain.BlockReferenceState,
	domain.BlockMemory, domain.BlockSources,
	domain.BlockHistory, domain.BlockCurrentMessage, domain.BlockToolResults,
}

// block returns the accumulator for a kind, creating it on first use so
// that report order follows composition order.
func (a *contextAssembler) block(kind domain.BlockKind) *domain.ContextBlock {
	if i, ok := a.index[kind]; ok {
		return &a.blocks[i]
	}
	a.blocks = append(a.blocks, domain.ContextBlock{Kind: kind})
	a.index[kind] = len(a.blocks) - 1
	return &a.blocks[len(a.blocks)-1]
}

func (a *contextAssembler) append(kind domain.BlockKind, role, content string) {
	a.messages = append(a.messages, ports.ChatMessage{Role: role, Content: content})
	b := a.block(kind)
	b.Items++
	b.Characters += utf8.RuneCountInString(content)
}

func (a *contextAssembler) exclude(kind domain.BlockKind, reason domain.ExclusionReason, characters int) {
	a.excludeN(kind, reason, 1, characters)
}

// excludeN records several items at once.
//
// Every producer but one drops items one at a time, as it walks a list and
// finds each one does not fit. The tools block does not walk anything: the
// turn was scoped before the builder was called, so what it knows is a
// count. Looping `exclude` to reach it would be pretending to a granularity
// this producer does not have.
func (a *contextAssembler) excludeN(kind domain.BlockKind, reason domain.ExclusionReason, items, characters int) {
	b := a.block(kind)
	for i := range b.Exclusions {
		if b.Exclusions[i].Reason == reason {
			b.Exclusions[i].Items += items
			b.Exclusions[i].Characters += characters
			return
		}
	}
	b.Exclusions = append(b.Exclusions, domain.ContextExclusion{
		Reason: reason, Items: items, Characters: characters,
	})
}

// instructions puts what the turn is TOLD at the head of the request: the
// platform's grounding policy, then the agent's own system prompt.
//
// ── Why the policy comes first ─────────────────────────────────────────
// It is a rule about the relationship between a claim and its evidence, and
// the agent's prompt is a voice, a job and a set of preferences. Stating
// the rule first and the persona second lets an agent specialise on top of
// it rather than have it appended as an afterthought to whatever it was
// last told. Both are system messages; neither rewrites the other.
//
// ── Why the policy is conditional and the prompt is not ────────────────
// `hasCapabilities` is the whole gate. A turn with no tools cannot verify
// anything, so telling it to verify would be instructing it to do something
// impossible and charging for the sentence. Most agents in this system have
// no capabilities, and for them this function does exactly what it did
// before it learned the second argument.
//
// A blank prompt still contributes nothing at all — not an empty system
// message. That is the current contract and several gateways are happier
// for it. The policy is what now stands in the gap for an agent that never
// wrote one, which is the agent this whole thing was reported against.
func (a *contextAssembler) instructions(agent *domain.Agent, hasCapabilities bool) {
	if hasCapabilities {
		a.append(domain.BlockInstructions, "system", groundingPolicy)
	}
	if agent == nil {
		return
	}
	prompt := strings.TrimSpace(agent.SystemPrompt)
	if prompt == "" {
		return
	}
	a.append(domain.BlockInstructions, "system", prompt)
}

/* ── tools ───────────────────────────────────────────────────────────── */

// tools accounts for the capability declaration.
//
// ── Why it is counted but not appended ─────────────────────────────────
// The declaration does not travel as a message. It goes in the request's
// own `tools` field, which is a different part of the same body — and every
// character of it is a prompt token, charged at input rates, on every call
// of the turn. A report that itemised five kinds of message and omitted the
// schema block would understate the turns that cost the most, which is the
// one direction a cost report must never be wrong in.
//
// So this is the assembler's only producer that contributes to the report
// without contributing to the message list, and the exception is named
// rather than hidden.
//
// ── Why the count is an estimate ───────────────────────────────────────
// The exact bytes are decided by the LLM adapter, which owns the wire
// format and is deliberately not visible from here. What is counted is the
// same information in a canonical form: the name, the description, and the
// schema. It is within a few per cent of what goes out, and it is reported
// through EstimateTokens, which is already named an estimate everywhere it
// appears.
//
// ── Why `withheld` is recorded as an exclusion ─────────────────────────
// A turn the user scoped declared fewer tools than the agent may use, and
// the difference is not a failure, a cut or a shortage — it is a decision.
// Recording it as an exclusion is what lets a later reader answer "how many
// could it have used?" from the snapshot alone, instead of asking the
// grants as they stand today and getting an answer about a different
// moment. See domain.ReasonNotSelected.
func (a *contextAssembler) tools(defs []domain.ToolDefinition, withheld int) {
	if len(defs) == 0 && withheld == 0 {
		return
	}
	b := a.block(domain.BlockTools)
	b.Items = len(defs)
	for _, d := range defs {
		b.Characters += toolDeclarationChars(d)
	}
	if withheld > 0 {
		// Zero characters, and that is exact rather than a placeholder: a
		// tool that was not declared put nothing on the wire. The block's
		// own Characters is what the turn actually paid for.
		a.excludeN(domain.BlockTools, domain.ReasonNotSelected, withheld, 0)
	}
}

// toolDeclarationChars is what one tool costs to declare.
//
// The literal overhead of the JSON scaffolding is counted alongside the
// text, because the model is charged for the braces too. `toolWireOverhead`
// is the fixed part per tool and per property, measured against the shape
// the adapter emits.
func toolDeclarationChars(d domain.ToolDefinition) int {
	n := toolWireOverhead +
		utf8.RuneCountInString(d.Name.String()) +
		utf8.RuneCountInString(d.DeclaredDescription())
	for _, name := range d.Schema.PropertyNames() {
		p := d.Schema.Properties[name]
		n += toolPropertyOverhead +
			utf8.RuneCountInString(name) +
			utf8.RuneCountInString(string(p.Type)) +
			utf8.RuneCountInString(p.Description)
	}
	for _, req := range d.Schema.Required {
		n += utf8.RuneCountInString(req) + 3 // quotes and a comma
	}
	return n
}

// The two constants below are the JSON scaffolding a declaration carries
// beyond its own text: `{"type":"function","function":{"name":…}}` and the
// nested schema envelope for the tool, and `"x":{"type":…,"description":…}`
// for each property. They are approximate on purpose — see the note on
// toolDeclarationChars — and they exist so the estimate is not silently
// zero for a tool whose name and description are short.
const (
	toolWireOverhead     = 110
	toolPropertyOverhead = 30
)

/* ── context references ──────────────────────────────────────────────── */

// contextReferences puts the turn's subjects in front of the model.
//
// One system message for the whole list, for the reason `memory` gives: a
// message per subject would read to the model as a dialogue it took part
// in, and would pay the per-message overhead every gateway charges.
//
// There is no budget selection and no exclusion path. The list is capped at
// domain.MaxContextReferences and every field inside it is length-bounded,
// so the worst case is a few hundred characters — small enough that
// dropping one would cost the turn its subject to save nothing worth
// counting. What IS reported is the cost, because a block that reached the
// model without appearing in the account would be a silent charge.
func (a *contextAssembler) contextReferences(refs []domain.ContextReference, hydrated []HydratedReference) {
	if len(refs) == 0 {
		return
	}
	block := RenderContextReferencesBlock(refs, hydratedKeys(hydrated))
	if block == "" {
		return
	}
	a.append(domain.BlockContextReferences, "system", block)
	// Items is the number of subjects rather than the one message they were
	// rendered into: "3 items" is the fact a reader is checking, and "1
	// item" would be an honest count of the wrong thing.
	if b := a.block(domain.BlockContextReferences); b != nil {
		b.Items = len(refs)
	}
}

/* ── reference state ─────────────────────────────────────────────────── */

// referenceState puts the subjects' PRESENT state directly below their
// identity.
//
// ── Why it is charged and why it is charged in full ────────────────────
// Hydration runs on every turn of every conversation that has a live
// subject, so a cost that did not appear in the report would not be a
// rounding error — it would be a recurring charge invisible to the one
// screen built to explain what a turn cost. Every character of the rendered
// block is counted, including the "NOT AVAILABLE" sentences: the model reads
// those and the gateway bills for them, so pretending a refused subject is
// free would understate the turn.
//
// ── Items vs exclusions ────────────────────────────────────────────────
// Items counts the subjects that actually carried state. A subject the agent
// was not authorized to read, or one that could not be found, appears as an
// EXCLUSION with its own reason — which is what makes the stored report
// answer, months later, the only question worth asking about a wrong answer:
// did this turn have the present state in front of it, and if not, why not.
//
// There is no unavailable-for-the-whole-block path. Hydration fails per
// subject, so it is reported per subject; a flag over the block would lose
// which of three attached items was the one that could not be read.
func (a *contextAssembler) referenceState(hydrated []HydratedReference) {
	if len(hydrated) == 0 {
		return
	}
	block := RenderReferenceStateBlock(hydrated)
	if block == "" {
		return
	}
	a.append(domain.BlockReferenceState, "system", block)
	if b := a.block(domain.BlockReferenceState); b != nil {
		b.Items = hydratedItems(hydrated)
	}
	unauthorized, unavailable := hydrationExclusions(hydrated)
	if unauthorized > 0 {
		// Zero characters, and that is exact rather than a placeholder: what
		// was excluded is the STATE, which put nothing on the wire. The
		// sentence saying so did, and it is already in the block's own
		// Characters.
		a.excludeN(domain.BlockReferenceState, domain.ReasonUnauthorized, unauthorized, 0)
	}
	if unavailable > 0 {
		a.excludeN(domain.BlockReferenceState, domain.ReasonUnavailable, unavailable, 0)
	}
}

/* ── memory ──────────────────────────────────────────────────────────── */

// memory puts what the agent remembers between the instructions and the
// conversation.
//
// One system message, whatever the count. A memory per message would let
// the model read the list as a dialogue it took part in, and would multiply
// the per-message overhead every gateway charges.
func (a *contextAssembler) memory(memories []domain.Memory, unavailable bool) {
	if unavailable {
		// Reported even though nothing was dropped that we can count: an
		// invisible degradation is the failure mode this whole report
		// exists to prevent. See ReasonUnavailable.
		a.exclude(domain.BlockMemory, domain.ReasonUnavailable, 0)
		return
	}

	selected, dropped := SelectMemories(memories, MemoryBudgetChars)
	for _, m := range dropped {
		a.exclude(domain.BlockMemory, domain.ReasonBudget, utf8.RuneCountInString(m.Content))
	}
	if len(selected) == 0 {
		// No header on its own. An agent that remembers nothing sends
		// nothing, rather than a label introducing an empty list.
		return
	}
	a.append(domain.BlockMemory, "system", RenderMemoryBlock(selected))
}

// MemoryBudgetChars is the ceiling on what memory may contribute to one
// turn, counted in characters rather than tokens.
//
// Why characters: counting tokens needs a tokenizer per model family, and
// the gateway is model-agnostic by design. A wrong tokenizer is worse than
// an honest heuristic — see EstimateTokens.
//
// Why a ceiling at all: memory grows monotonically and the bill grows with
// it. With a ceiling the worst case is known. 4.000 characters is roughly
// 1.000 tokens, or forty to sixty short facts, which is not a tight limit
// for a personal system. When it does start cutting, the controls are
// `pinned` and `enabled` — things the user can see and reason about —
// rather than a relevance score they cannot.
const MemoryBudgetChars = 4000

// memoryHeader introduces the block. It costs characters like everything
// else and is counted against the budget, because it is paid for like
// everything else.
const memoryHeader = "O que você deve lembrar sobre este usuário e este trabalho:"

// memoryBullet is what each item costs beyond its own text.
const memoryBullet = "\n- "

// selectWithinBudget is the block budget rule, written once.
//
// Two properties, and both are load-bearing:
//
//   - **Whole items only.** An item enters complete or not at all. Half a
//     fact is a fact that now says something else; half a document is one
//     the model will answer about with confidence. Truncation is the one
//     outcome forbidden outright.
//   - **A skipped item does not block the ones behind it.** The loop
//     continues rather than breaking, so a single oversized item cannot
//     take the rest of the block down with it. The order is a priority,
//     not a queue.
//
// `overhead` is what the block's header costs before any item does; it is
// charged because it is paid for. `cost` is what one item adds.
//
// Memory and Sources share this because they are one rule with two shapes,
// not two rules. A second copy would drift on the first change, and the
// counters both pages show exist precisely to be trusted.
func selectWithinBudget[T any](items []T, budget, overhead int, cost func(T) int) (selected, dropped []T) {
	if len(items) == 0 {
		return nil, nil
	}
	used := overhead
	for _, item := range items {
		c := cost(item)
		if used+c > budget {
			dropped = append(dropped, item)
			continue
		}
		used += c
		selected = append(selected, item)
	}
	return selected, dropped
}

// SelectMemories decides which memories fit the budget, in the order given.
//
// Nothing is reordered here; the caller hands them over in priority order
// and this only decides where the line falls.
//
// Exported because the Memory page answers "which of these is the agent
// actually using?" with the same function, over the same input.
func SelectMemories(memories []domain.Memory, budget int) (selected, dropped []domain.Memory) {
	return selectWithinBudget(memories, budget,
		utf8.RuneCountInString(memoryHeader), memoryCost)
}

func memoryCost(m domain.Memory) int {
	return utf8.RuneCountInString(memoryBullet) + utf8.RuneCountInString(flattenLines(m.Content))
}

// RenderMemoryBlock is the exact text the model receives.
//
// No dates, no ids, no provenance. The model does not need to know when a
// fact was recorded or which thread it came from; the *interface* does, and
// that is where those live. Every character here is paid for on every turn.
//
// The rune count of what this returns equals the budget SelectMemories
// charged for the same slice. TestMemoryBudgetMatchesRenderedBlock holds
// the two together.
func RenderMemoryBlock(memories []domain.Memory) string {
	var b strings.Builder
	b.WriteString(memoryHeader)
	for _, m := range memories {
		b.WriteString(memoryBullet)
		b.WriteString(flattenLines(m.Content))
	}
	return b.String()
}

// flattenLines keeps one memory on one bullet. The replacement is
// one-rune-for-one-rune, which is what lets the budget and the rendered
// block agree on a count.
func flattenLines(s string) string {
	return lineFlattener.Replace(s)
}

// A CRLF is two separate replacements and becomes two spaces, which keeps
// the count identity intact.
var lineFlattener = strings.NewReplacer("\n", " ", "\r", " ")

/* ── sources ─────────────────────────────────────────────────────────── */

// sources puts the agent's reference material after what it remembers and
// before the conversation.
//
// One system message for the whole set, same reasoning as memory: a message
// per source would read to the model as a dialogue it took part in, and
// would pay the per-message overhead every gateway charges, once per
// document.
func (a *contextAssembler) sources(sources []domain.Source, unavailable bool) {
	if unavailable {
		a.exclude(domain.BlockSources, domain.ReasonUnavailable, 0)
		return
	}

	selected, dropped := SelectSources(sources, SourcesBudgetChars)
	for _, s := range dropped {
		// The two ways a source can be absent are recorded as two reasons,
		// because they call for two different actions: "budget" means
		// switching something else off would have let it in, "too_large"
		// means nothing would have. The Sources page already draws that
		// line; the report has to draw it too, or the Inspector explaining
		// a past turn is less honest than the list explaining the present.
		reason := domain.ReasonBudget
		if SourceExceedsBudget(s, SourcesBudgetChars) {
			reason = domain.ReasonTooLarge
		}
		// Charged at what the source would have cost the block, not at the
		// raw length of its text: the report is an account of the budget,
		// and the budget is what the item was measured against.
		a.exclude(domain.BlockSources, reason, sourceCost(s))
	}
	if len(selected) == 0 {
		return
	}
	a.append(domain.BlockSources, "system", RenderSourcesBlock(selected))
}

// SourcesBudgetChars is the ceiling on what reference material may
// contribute to one turn.
//
// Three times the memory budget, and the ratio is the point: memory is a
// handful of short facts, sources are documents. 12.000 characters is
// roughly 3.000 tokens.
//
// Note what this implies, and it is deliberate: a source may be stored at
// up to 20.000 characters, which is more than this block can ever carry.
// That case is not an error and is not truncated — it is reported. See
// SourceExceedsBudget.
const SourcesBudgetChars = 12000

// sourcesHeader introduces the block, and is charged against the budget.
const sourcesHeader = "Material de referência que você pode consultar:"

// sourceTitleMark is what one source costs beyond its own text: a blank
// line, a markdown heading for the title, and the newline before the body.
const sourceTitleMark = "\n\n## "

// SelectSources decides which sources fit the budget, in the order given.
//
// Same rule as memory, same implementation: whole documents only, and a
// document that does not fit does not hide the ones behind it.
func SelectSources(sources []domain.Source, budget int) (selected, dropped []domain.Source) {
	return selectWithinBudget(sources, budget,
		utf8.RuneCountInString(sourcesHeader), sourceCost)
}

// sourceCost is what one source adds to the block: the heading, the title,
// the newline that ends it, and the text.
func sourceCost(s domain.Source) int {
	return utf8.RuneCountInString(sourceTitleMark) +
		utf8.RuneCountInString(s.Title) + 1 +
		sourceContentChars(s)
}

// sourceContentChars is how many characters a source's text costs.
//
// The text when it is loaded, the stored count when it deliberately is not.
// The management list leaves twenty-thousand-character documents in the
// database and brings back `length(content)` instead, so measuring
// `len(Content)` there would score every source as empty and the page would
// cheerfully report that everything fits.
//
// The two are the same number by construction: the repository fills
// Characters from Content on every read that loads it, and `length()` is
// measured in characters on the read that does not.
func sourceContentChars(s domain.Source) int {
	if s.Content != "" {
		return utf8.RuneCountInString(s.Content)
	}
	return s.Characters
}

// SourceExceedsBudget says a source cannot fit the block on its own —
// there is no combination of other sources being turned off that would let
// it in.
//
// This is a different fact from "it did not make it this time", and the two
// must not be shown as one: a user who cannot tell them apart will keep
// turning other sources off, waiting for something that can never happen.
// The interface is required to say so on the source's own line.
func SourceExceedsBudget(s domain.Source, budget int) bool {
	return utf8.RuneCountInString(sourcesHeader)+sourceCost(s) > budget
}

// RenderSourcesBlock is the exact text the model receives.
//
// The title is included and the description is not. The title earns its
// characters by giving the model an anchor to cite ("according to the
// content strategy…"); the description is a note the user wrote to
// themselves about a text that is right there, and sending it would be
// paying twice for the same information.
//
// Content is NOT flattened, unlike memory: a source is a document, and its
// paragraphs are part of what it says.
//
// The rune count of what this returns equals the budget SelectSources
// charged for the same slice. TestSourcesBudgetMatchesRenderedBlock holds
// the two together.
func RenderSourcesBlock(sources []domain.Source) string {
	var b strings.Builder
	b.WriteString(sourcesHeader)
	for _, s := range sources {
		b.WriteString(sourceTitleMark)
		b.WriteString(s.Title)
		b.WriteString("\n")
		b.WriteString(s.Content)
	}
	return b.String()
}

/* ── tool evidence ───────────────────────────────────────────────────── */

// evidence puts what this conversation's tools already observed between the
// reference material and the conversation itself.
//
// ── Why here and not with the instructions ─────────────────────────────
// Because it is the one position that states the right thing about its
// authority. Above the conversation, so the model reads what was observed
// before it reads what was said about it; below memory and sources, because
// those are what the operator decided and this is only what a capability
// returned. Putting it in the instructions block would give text that came
// out of a README the standing of a platform rule, which is precisely the
// escalation evidenceHeader exists to prevent.
//
// ── Why it degrades instead of failing ─────────────────────────────────
// Same trade as memory: a turn that answers with less context is better
// than a turn that does not answer. Unlike tools, nothing here is an
// authorization — losing it costs continuity, never safety.
func (a *contextAssembler) evidence(sel EvidenceSelection, unavailable bool) {
	if unavailable {
		a.exclude(domain.BlockToolEvidence, domain.ReasonUnavailable, 0)
		return
	}
	if sel.FailedItems > 0 {
		a.excludeN(domain.BlockToolEvidence, domain.ReasonFailed, sel.FailedItems, 0)
	}
	if sel.SupersededItems > 0 {
		a.excludeN(domain.BlockToolEvidence, domain.ReasonSuperseded, sel.SupersededItems, 0)
	}
	if sel.DroppedItems > 0 {
		a.excludeN(domain.BlockToolEvidence, domain.ReasonBudget, sel.DroppedItems, sel.DroppedChars)
	}
	if sel.TruncatedChars > 0 {
		// Items zero on purpose: nothing was left out, a tail was. Counting
		// it as an excluded item would make the block report a number of
		// entries that never existed.
		a.excludeN(domain.BlockToolEvidence, domain.ReasonTruncated, 0, sel.TruncatedChars)
	}
	if len(sel.Items) == 0 {
		// No header on its own, same as memory: a conversation that observed
		// nothing sends nothing, rather than a label introducing an empty
		// list and a security notice about content that is not there.
		return
	}
	a.append(domain.BlockToolEvidence, "system", RenderEvidenceBlock(sel.Items))
}

// conversation replays the window, splitting the report between the thread
// behind the question and the question itself.
//
// The split is for the report only: both kinds append to the same message
// list, in the same order they arrived, so the wire is identical to what a
// single undivided loop would produce.
func (a *contextAssembler) conversation(history []domain.Message, current *domain.Message) {
	for _, m := range history {
		kind := domain.BlockHistory
		if current != nil && m.ID == current.ID {
			kind = domain.BlockCurrentMessage
		}
		content := wireContentOf(m)
		if content == "" {
			a.exclude(kind, domain.ReasonEmptyTurn, 0)
			continue
		}
		a.append(kind, string(m.Role), content)
	}
}

// addToBlock records content that reached the model after the builder had
// already run, and keeps the report's totals consistent with it.
//
// Exactly one caller, and it needs a reason to exist: the tool exchange. A
// turn's tool results are produced by the loop, in between provider calls,
// long after BuildContext returned — and they are context, sent and billed
// like everything else. The alternative was to run the builder again per
// round, which would recount the instructions, memory and sources every
// time and report a turn as carrying its own history several times over.
//
// It lives here, next to the assembler, so the two ways a block can be
// written are one page apart and cannot drift on how a total is derived.
func addToBlock(r *domain.ContextReport, est TokenEstimator, kind domain.BlockKind, items, characters int) {
	found := false
	for i := range r.Blocks {
		if r.Blocks[i].Kind != kind {
			continue
		}
		r.Blocks[i].Items += items
		r.Blocks[i].Characters += characters
		found = true
		break
	}
	if !found {
		r.Blocks = append(r.Blocks, domain.ContextBlock{Kind: kind, Items: items, Characters: characters})
	}
	// Recomputed from the blocks rather than incremented, for the same
	// reason finish() sums them: the numbers a reader adds up on screen have
	// to be the numbers reported.
	r.TotalCharacters, r.TotalEstimatedTokens = 0, 0
	for i := range r.Blocks {
		r.Blocks[i].EstimatedTokens = est.Tokens(r.Blocks[i].Characters)
		r.TotalCharacters += r.Blocks[i].Characters
		r.TotalEstimatedTokens += r.Blocks[i].EstimatedTokens
	}
}

func (a *contextAssembler) finish() BuiltContext {
	report := domain.ContextReport{
		Blocks:               a.blocks,
		CacheBreakpointAfter: a.cacheBreakpointAfter,
	}
	for i := range report.Blocks {
		b := &report.Blocks[i]
		b.EstimatedTokens = a.est.Tokens(b.Characters)
		report.TotalCharacters += b.Characters
		// Summed from the blocks rather than estimated from the total, so
		// the numbers a reader adds up on screen are the numbers reported.
		report.TotalEstimatedTokens += b.EstimatedTokens
	}
	return BuiltContext{Messages: a.messages, Report: report}
}
