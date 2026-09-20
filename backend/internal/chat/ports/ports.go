// Package ports declares the boundary interfaces of the chat bounded
// context.
//
// Driven (outbound) ports: the contracts the application layer requires
// from infrastructure. Implementations live under internal/chat/adapters —
// Postgres for the repositories, an OpenAI-compatible HTTP client for LLM.
//
// Each repository operates per-workspace; every query takes a workspace id
// and filters server-side. Soft-deleted rows are filtered out by default.
package ports

import (
	"context"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
)

// --- repositories ---------------------------------------------------------

type ListFilter struct {
	Limit  int
	Offset int
}

// ConversationFilter narrows a conversation listing. It embeds ListFilter
// rather than growing it because AgentID means nothing to providers or
// agents, and a field that half the implementations must ignore is a field
// somebody eventually forgets to ignore.
//
// AgentID nil lists every conversation in the workspace — exactly what the
// repository did before this filter existed. A non-nil value NARROWS that
// set and never widens it: the workspace predicate is applied either way,
// so an id arriving from a URL selects nothing it did not already own.
type ConversationFilter struct {
	ListFilter
	AgentID *uuid.UUID
}

type ProviderRepo interface {
	Create(ctx context.Context, p *domain.Provider) error
	Update(ctx context.Context, p *domain.Provider) error
	SoftDelete(ctx context.Context, workspaceID, id uuid.UUID) error
	FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Provider, error)
	List(ctx context.Context, workspaceID uuid.UUID, f ListFilter) ([]domain.Provider, error)
	// CountAgents backs the referential check on delete. Providers are
	// soft-deleted, which slips past the ON DELETE RESTRICT constraint, so
	// the application layer has to ask before removing one.
	CountAgents(ctx context.Context, workspaceID, id uuid.UUID) (int, error)
}

type AgentRepo interface {
	Create(ctx context.Context, a *domain.Agent) error
	Update(ctx context.Context, a *domain.Agent) error
	SoftDelete(ctx context.Context, workspaceID, id uuid.UUID) error
	FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Agent, error)
	List(ctx context.Context, workspaceID uuid.UUID, f ListFilter) ([]domain.Agent, error)
	CountConversations(ctx context.Context, workspaceID, id uuid.UUID) (int, error)
	// CountMemories backs the same referential check for agent memories.
	// Agents are soft-deleted, which slips past ON DELETE RESTRICT, so
	// removing one with memories still attached would leave rows nothing
	// can ever reach again.
	CountMemories(ctx context.Context, workspaceID, id uuid.UUID) (int, error)
	// CountSources is the same guard for reference material. One rule for
	// every dependency of an agent, rather than a different answer per
	// table.
	CountSources(ctx context.Context, workspaceID, id uuid.UUID) (int, error)
}

// MemoryRepo stores what an agent remembers between conversations.
//
// Two reads on purpose. ListByAgent is the management surface: every live
// memory, enabled or not, with the origin resolved for display.
// ListForContext is the turn's read: only what can actually be sent, in the
// order the budget consumes it, and nothing the wire does not need.
// Collapsing them into one would mean either the page loses provenance or
// every turn pays for a join it never reads.
type MemoryRepo interface {
	Create(ctx context.Context, m *domain.Memory) error
	Update(ctx context.Context, m *domain.Memory) error
	SoftDelete(ctx context.Context, workspaceID, id uuid.UUID) error
	FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Memory, error)
	// ListByAgent returns the agent's live memories in selection order
	// (pinned first, then most recently updated), with the source
	// conversation's title joined in when that conversation still exists.
	ListByAgent(ctx context.Context, workspaceID, agentID uuid.UUID, limit int) ([]domain.Memory, error)
	// CountByAgent is every live memory of one agent, ignoring the limit —
	// so a truncated page can say how much it is not showing.
	CountByAgent(ctx context.Context, workspaceID, agentID uuid.UUID) (int64, error)
	// ListForContext returns only the enabled memories, in the order the
	// budget consumes them. This is what one turn reads.
	ListForContext(ctx context.Context, workspaceID, agentID uuid.UUID) ([]domain.Memory, error)
}

// SourceRepo stores the reference material an agent can consult.
//
// The same two reads as MemoryRepo, and for the same reason — but with one
// difference that matters: ListByAgent does NOT return content. A source is
// up to twenty thousand characters, and a list is a list. The page shows
// titles and sizes; the editor asks for one record by id when it opens it.
type SourceRepo interface {
	Create(ctx context.Context, s *domain.Source) error
	Update(ctx context.Context, s *domain.Source) error
	SoftDelete(ctx context.Context, workspaceID, id uuid.UUID) error
	FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Source, error)
	// ListByAgent returns the agent's live sources in selection order, with
	// Content left empty and Characters filled in from the database. The
	// count comes from `length(content)`, so the size shown on the page is
	// the size the budget will spend, without shipping the text to compute
	// it.
	ListByAgent(ctx context.Context, workspaceID, agentID uuid.UUID, limit int) ([]domain.Source, error)
	CountByAgent(ctx context.Context, workspaceID, agentID uuid.UUID) (int64, error)
	// ListForContext returns the enabled sources, content included, in the
	// order the budget consumes them. This is what one turn reads.
	ListForContext(ctx context.Context, workspaceID, agentID uuid.UUID) ([]domain.Source, error)
}

type ConversationRepo interface {
	Create(ctx context.Context, c *domain.Conversation) error
	Rename(ctx context.Context, workspaceID, id uuid.UUID, title string) (*domain.Conversation, error)
	SoftDelete(ctx context.Context, workspaceID, id uuid.UUID) error
	FindByID(ctx context.Context, workspaceID, id uuid.UUID) (*domain.Conversation, error)
	List(ctx context.Context, workspaceID uuid.UUID, f ConversationFilter) ([]domain.Conversation, error)
	// Count is how many rows the same filter matches, ignoring Limit and
	// Offset. It exists so a paged list can say how much it is not showing:
	// a page that stops at 50 without a total is a silent cut.
	Count(ctx context.Context, workspaceID uuid.UUID, f ConversationFilter) (int64, error)
	// TouchActivity stamps last_message_at, moving the thread to the top of
	// the sidebar. Called once per completed turn.
	TouchActivity(ctx context.Context, workspaceID, id uuid.UUID) error
}

type MessageRepo interface {
	Create(ctx context.Context, m *domain.Message) error
	// ListRecent returns the trailing `limit` messages of a conversation in
	// chronological order — the tail, not the head, because that is what
	// gets replayed to the model.
	ListRecent(ctx context.Context, workspaceID, conversationID uuid.UUID, limit int) ([]domain.Message, error)
	FindBySeq(ctx context.Context, workspaceID, conversationID uuid.UUID, seq int64) (*domain.Message, error)
	// ListUpToSeq returns the trailing `limit` turns at or before a seq, in
	// chronological order. It is what lets an operation work on the thread
	// as it was when the user asked, rather than as it is when the query
	// happens to run.
	ListUpToSeq(ctx context.Context, workspaceID, conversationID uuid.UUID, upToSeq int64, limit int) ([]domain.Message, error)
	// DeleteFromSeq hard-deletes a message and everything after it, backing
	// regenerate and edit — both replace a turn rather than append to it.
	DeleteFromSeq(ctx context.Context, workspaceID, conversationID uuid.UUID, seq int64) (int64, error)
	// UsageByConversation sums what the assistant turns of one conversation
	// consumed and cost, grouped by model. The cost comes from the figure
	// each turn froze when it happened — never from the current rate card,
	// which would make a past statement move.
	UsageByConversation(ctx context.Context, workspaceID, conversationID uuid.UUID, f UsageFilter) ([]ModelUsage, error)
	// UsageByAgent does the same across every conversation an agent owns.
	UsageByAgent(ctx context.Context, workspaceID, agentID uuid.UUID, f UsageFilter) ([]ModelUsage, error)
	// UsageByWorkspace does the same across the whole workspace. This is the
	// level a daily total is asked at.
	UsageByWorkspace(ctx context.Context, workspaceID uuid.UUID, f UsageFilter) ([]ModelUsage, error)
}

// UsageFilter narrows an aggregation to a time window, half-open: From is
// inclusive, To is exclusive, so consecutive days neither overlap nor drop
// a turn on the boundary. A nil bound is open on that side, and a filter
// with neither bound is the lifetime total.
//
// The window is compared against each message's own created_at, which is
// the moment the turn happened — not the conversation's. Asking "how much
// did today cost" any other way attributes a turn to the day its thread
// started.
//
// No timezone lives here on purpose. "Today" is a question about where the
// person is, and the caller is closer to that answer than this package.
type UsageFilter struct {
	From *time.Time
	To   *time.Time
}

// ModelUsage is a tally for one model, as it comes back from an aggregation
// query. Counts are int64 because a busy agent sums past the range a single
// response comfortably fits.
//
// The three source counts sum to Messages. They are what keeps a total
// honest: a report that says "$0.40" without saying "and 12 turns whose
// cost I do not know" is not a statement, it is a guess wearing one.
type ModelUsage struct {
	Model            string
	PromptTokens     int64
	CompletionTokens int64
	Messages         int64
	// ProviderMessages counted exact usage; EstimatedMessages counted our
	// heuristic; UnknownMessages contributed zero tokens because nothing is
	// known about them.
	ProviderMessages  int64
	EstimatedMessages int64
	UnknownMessages   int64
	// Cost sums only the turns that froze one. UnpricedMessages is how many
	// did not, and therefore how much of the total is missing.
	Cost             float64
	UnpricedMessages int64
	// The unit rates, reported only when every priced turn in the group
	// used the same ones. Nil when the group spans a price change, or when
	// no turn in it carries a price at all.
	InputCostPerToken  *float64
	OutputCostPerToken *float64
	// What the input of these turns was made of, summed over the turns the
	// provider actually measured. CacheMeasuredMessages says how many that
	// was, and it is the denominator every cache figure has to be read
	// against: three sums over one measured turn out of two hundred is not
	// a cache hit rate, and a reader that divides by Messages would report
	// one anyway.
	CacheReadTokens       int64
	CacheCreationTokens   int64
	ReasoningTokens       int64
	CacheMeasuredMessages int64
}

// AgentToolRepo stores which tools an agent is authorized to use.
//
// The store holds names, not definitions: what a tool *is* lives in the
// code that implements it (see domain/tool.go). So this interface can only
// answer "was this name authorized", and the registry answers "does this
// name still resolve". Both questions have to be asked, and they are asked
// in that order — an authorization for a tool that no longer exists is a
// stale row, not a permission.
type AgentToolRepo interface {
	// Authorize grants one tool to one agent. Idempotent: granting twice is
	// the same state as granting once.
	Authorize(ctx context.Context, workspaceID, agentID uuid.UUID, name domain.ToolName) error
	// Revoke removes one grant. Revoking something that was never granted is
	// not an error — the requested state is the resulting state.
	Revoke(ctx context.Context, workspaceID, agentID uuid.UUID, name domain.ToolName) error
	// ListByAgent returns the names this agent may use, in a stable order.
	// This is the read on the turn's hot path, which is why it returns names
	// and touches no other table.
	ListByAgent(ctx context.Context, workspaceID, agentID uuid.UUID) ([]domain.ToolName, error)
	// CountByAgent is how many grants an agent holds.
	CountByAgent(ctx context.Context, workspaceID, agentID uuid.UUID) (int64, error)
	// RevokeAll drops every grant of one agent. Used when the agent itself
	// is removed: an authorization is configuration of an agent, and outlives
	// nothing.
	RevokeAll(ctx context.Context, workspaceID, agentID uuid.UUID) error
}

// ToolCallRepo is the audit trail of what tools actually ran.
//
// Write-once. There is no Update: a record of something that happened is
// not a document to be edited. The one mutation the design anticipates is
// redaction, which drops payloads and sets a flag, and it does not exist
// yet — see domain.ToolCallRecord.
type ToolCallRepo interface {
	// CreateMany writes a turn's calls in one statement, because they are
	// written together at the end of the turn that made them.
	CreateMany(ctx context.Context, records []domain.ToolCallRecord) error
	// ListByConversation returns the calls of one conversation, newest turn
	// last, bounded. It backs the transcript's expandable detail.
	ListByConversation(ctx context.Context, workspaceID, conversationID uuid.UUID, limit int) ([]domain.ToolCallRecord, error)
	// WriteReceiptsFor answers, for each assistant turn named, whether it
	// actually changed anything — independently of what the model wrote in
	// prose. Every id asked about gets a receipt, empty ones included:
	// "nothing was executed" is a statement, not a missing key.
	WriteReceiptsFor(ctx context.Context, workspaceID uuid.UUID, messageIDs []uuid.UUID) (map[uuid.UUID]domain.WriteReceipt, error)
	// ReadReceiptsFor is the read-side twin: which turns actually read a
	// system this product does not own.
	//
	// `available` is passed through rather than queried, because it is a
	// fact about the CURRENT configuration and not about the recorded past
	// — see domain.ReadReceipt.Available.
	ReadReceiptsFor(ctx context.Context, workspaceID uuid.UUID, messageIDs []uuid.UUID, available bool) (map[uuid.UUID]domain.ReadReceipt, error)
	// ListByMessages returns the calls filed under specific assistant turns,
	// in the order they happened.
	//
	// ── Why by message and not by conversation ─────────────────────────
	// Because this is what feeds a later turn's context, and what a turn may
	// carry is bounded by the history window it is already replaying. Asking
	// by conversation would return evidence from turns that scrolled out of
	// the window, so the model would be reasoning about an exchange it can no
	// longer see. Passing the exact ids makes the two windows the same window
	// by construction rather than by two limits agreeing.
	//
	// The conversation id is still required and still filtered on. The ids
	// come from that conversation's own history, so it cannot change the
	// result — which is why it is there: an isolation that only holds because
	// of how the caller happens to build its argument is one a refactor
	// breaks silently.
	ListByMessages(ctx context.Context, workspaceID, conversationID uuid.UUID, messageIDs []uuid.UUID) ([]domain.ToolCallRecord, error)
}

// --- tools ----------------------------------------------------------------

// Tool is one executable capability.
//
// ── Why this is a port and not a package of functions ──────────────────
// Because of who implements it. A tool backed by GitHub is implemented in
// Integrations; a tool backed by Finance is implemented by Finance exposing
// a capability. Neither may be imported here — `Module → Module` and
// `Module → Integration → External` are the only legal directions, and the
// second one is a dependency Agents declares, never one it reaches into.
// An interface owned by Agents and satisfied elsewhere, wired at the
// composition root, is exactly how a module receives a capability without
// learning who provides it.
//
// Execute receives arguments already validated against Definition().Schema,
// so an implementation may read the map without re-checking types. It must
// respect ctx: the deadline is the executor's, and cancellation is the user
// pressing stop.
type Tool interface {
	Definition() domain.ToolDefinition
	Execute(ctx context.Context, args map[string]any) (domain.ToolOutput, error)
}

// EffectReporter is an OPTIONAL second contract: a capability that can say
// which entity a successful call touched.
//
// ── Why it is the capability that says, and not the runtime ────────────
// The same argument Confidential and External already make. Only the
// capability knows the shape of its own output and which field in it is an
// identity rather than content. Teaching the runtime to go fishing for
// something that "looks like an id" in a payload it is about to redact
// would be inference dressed as a contract, and it would be wrong the first
// time a tool returned two ids.
//
// So this is declared, per capability, and it is opt-in: the 47 tools that
// do not implement it report nothing, which is a safe answer and not a
// missing one.
//
// ── Why it takes the output instead of returning it from Execute ───────
// Because changing Execute's signature would touch every tool in the
// product to serve the handful that have an identity worth reporting. This
// is called immediately after a SUCCESSFUL execution, with that execution's
// own output, before anything is redacted.
//
// ── What it must not do ────────────────────────────────────────────────
// Return content. The ref is a type and a UUID; see domain.EffectRef, which
// refuses anything else. A capability that cannot express its identity that
// way returns false, and the resume falls back to reading — which is the
// behaviour we want anyway.
type EffectReporter interface {
	// EffectRefOf names the entity this output identifies, or false when
	// the call touched nothing nameable. Never called for a failed call.
	EffectRefOf(out domain.ToolOutput) (domain.EffectRef, bool)
}

// --- context references ---------------------------------------------------

// ResolvedReference is what a provider says about one of its entities.
//
// It carries recognition text and nothing else. In particular it does NOT
// carry the entity's state: a resolver that returned a stage, a balance or
// a due date would be a second read path competing with the tools, and the
// value it returned would be frozen into a record that the entity then
// moves away from. See domain/context_reference.go on freshness.
type ResolvedReference struct {
	// Label is how the entity reads to a person: "Acme · Backend Engineer".
	Label string
	// Subtitle is one optional line of extra recognition. A provider with
	// nothing worth adding leaves it empty rather than inventing filler.
	Subtitle string
}

// ContextReferenceResolver turns an entity id into something a person can
// recognise, for one workspace.
//
// ── Why this exists at all, given the tools ────────────────────────────
// Two jobs the tools cannot do:
//
//  1. AUTHORSHIP. The label that goes into the permanent record and into
//     the model's context must be written by the backend, never by the
//     client — the same rule TurnReference follows. Only the provider can
//     write it, because only the provider knows what the entity is called.
//  2. ADMISSION. An id from a client is untrusted. Resolving it against
//     the workspace BEFORE it is stored is what stops a fabricated id from
//     ever becoming an attachment. A reference that was never admitted
//     cannot leak anything later.
//
// ── Why it is not a way around authorization ───────────────────────────
// It answers "does this workspace have an entity by this id, and what is it
// called". It never answers "what is its state", which is the question the
// agent needs a GRANT to ask. So an agent with a reference and no tool
// grant knows the subject exists and is called "Acme · Backend Engineer" —
// which is what the USER already told it by attaching it — and cannot learn
// the stage, the salary or the notes. The two capabilities stay separate
// because they are separate methods on separate interfaces, resolved for
// different principals: this one for the user rendering a screen, the tool
// for the agent reading state.
//
// Implementations live in the owning bounded context and are wired at the
// composition root, exactly like Tool. Chat declares this and satisfies
// none of it.
type ContextReferenceResolver interface {
	// Types are the reference types this resolver answers for, e.g.
	// "job_radar.opportunity". Returned rather than configured so the
	// registry is built from what the providers actually implement.
	Types() []domain.ContextReferenceType
	// Resolve looks up one entity for one workspace.
	//
	// It returns found=false for an entity that does not exist, was
	// deleted, or belongs to another workspace — the three are one answer
	// on purpose, so a caller cannot use the difference to probe for rows
	// it may not see. An error is reserved for a genuine failure to ask.
	Resolve(ctx context.Context, workspaceID uuid.UUID, ref domain.ContextReference) (ResolvedReference, bool, error)
}

/* ── reference hydration ─────────────────────────────────────────────── */

// Hydration is the second half of the reference contract: a reference
// PERSISTS identity, hydration SUPPLIES the entity's present state, for one
// turn, and never writes it back.
//
//	persisted   type + id + label + stable recognition metadata
//	hydrated    stage = interview        (now, and only in this turn's context)
//
// ── The failure this exists to close ───────────────────────────────────
// A conversation holds a reference to a live entity. The model reads its
// state once, answers, and the entity then moves. On the next turn the
// model has its own earlier sentence in the history — "it is applied" — and
// no reason to doubt it, so it answers from that and is wrong. Nothing
// about that is a prompting problem: from the model's side its own recent
// reading is the best evidence it has, and telling it to distrust its
// evidence competes with telling it to use evidence at all.
//
// So the runtime stops asking the model to remember to refresh and puts the
// present state in front of it, on every turn that has the subject
// attached. See app/hydration.go.

// ReferenceFreshness says how a reference type's current state is obtained.
//
// It is declared BY THE PROVIDER, per type, because only the provider knows
// how big its entities are and how fast they move. An opportunity is four
// short fields that change weekly; a GitHub repository is not something to
// pull into every turn's prompt on the chance it was mentioned.
type ReferenceFreshness string

const (
	// FreshnessOnDemand: the runtime reads nothing on its own. The model
	// uses a tool when it needs the state, exactly as it did before
	// hydration existed.
	//
	// This is the DEFAULT for anything a provider does not declare, and the
	// default is the safe one: a type that says nothing costs nothing and
	// leaks nothing.
	FreshnessOnDemand ReferenceFreshness = "on_demand"
	// FreshnessPerTurn: the state is small and moves, so the runtime reads
	// it before every turn the subject is attached to.
	FreshnessPerTurn ReferenceFreshness = "per_turn"
)

// ReferenceHydrationPolicy is what a provider declares about one of its
// reference types.
type ReferenceHydrationPolicy struct {
	Freshness ReferenceFreshness
	// Capability is the tool whose GRANT authorizes reading this state.
	//
	// ── Why a tool name and not a boolean ──────────────────────────────
	// Because hydration must not become a way around authorization, and the
	// only way to guarantee that is to make it ask the same question the
	// agent's own tool call asks, against the same grant, in the same
	// vocabulary. `job_radar.opportunity.get` is a row the operator can see
	// on the agent's settings page and revoke with one click; the moment it
	// is revoked, hydration stops, on the next turn, for the same reason the
	// tool call stops.
	//
	// An empty name authorizes NOTHING. A provider cannot opt out of the
	// check by declining to name a capability, and a name that is not a tool
	// in this build authorizes nothing either — see
	// Service.hydrationAuthorized.
	Capability domain.ToolName
}

// ReferenceStateField is one named value of an entity's present state.
//
// Flat strings, deliberately. This is what the model reads, not a payload
// another system parses, and a nested structure would invite a provider to
// hand over the record instead of the handful of fields the reasoning
// needs.
type ReferenceStateField struct {
	Name  string
	Value string
}

// ReferenceState is one entity's present state, as the provider chooses to
// state it.
//
// ── Why the provider chooses the fields ────────────────────────────────
// Because "the state that matters" is a domain question. Job Radar knows
// that an opportunity's stage is the thing conversations turn on and that
// its description is not; Agents cannot know that for any provider and must
// not guess. What Agents owns is the ceiling: this is context, it is paid
// for on every turn, and it is not a database dump.
type ReferenceState struct {
	Fields []ReferenceStateField
}

// ContextReferenceHydrator is the OPTIONAL second half of a resolver.
//
// A provider that does not implement it has every one of its types treated
// as FreshnessOnDemand, which is what every type was before this interface
// existed. Adding it to a resolver is additive and touches nothing else.
//
// ── Why it is on the resolver and not a separate registration ──────────
// Because "who can name this entity" and "who can read its present state"
// are the same authority over the same rows, and splitting them across two
// registrations would create a build in which one is wired and the other is
// not — a reference that resolves and never refreshes, or refreshes without
// ever having been admitted.
type ContextReferenceHydrator interface {
	// HydrationPolicy declares what happens for one type. ok=false means
	// this type is never hydrated by the runtime, which is the same as
	// declaring FreshnessOnDemand.
	HydrationPolicy(t domain.ContextReferenceType) (ReferenceHydrationPolicy, bool)
	// Hydrate reads one entity's present state for one workspace.
	//
	// The same three-way answer as Resolve: found=false covers deleted,
	// never existed and another workspace's, told apart from an error, which
	// is reserved for a genuine failure to ask.
	//
	// It is called ONLY after the capability named by HydrationPolicy has
	// been checked against the calling agent's grants. An implementation may
	// therefore read its own application layer directly — but it is reading
	// on behalf of an agent that was authorized to read exactly this, never
	// on behalf of one that was not.
	Hydrate(ctx context.Context, workspaceID uuid.UUID, ref domain.ContextReference) (ReferenceState, bool, error)
}

// ContextReferenceRegistry is the single source of which entity types can
// be referenced in this build.
//
// The sibling of ToolRegistry, and separate for the same reason the two
// reference concepts are separate: one answers "what may this agent do",
// the other "what can this conversation be about". Composed at start-up and
// immutable afterwards.
type ContextReferenceRegistry interface {
	// Resolve looks one reference up through whichever provider owns its
	// type, scoped to the workspace. A type with no resolver in this build
	// is a domain refusal, not a not-found.
	Resolve(ctx context.Context, workspaceID uuid.UUID, ref domain.ContextReference) (ResolvedReference, bool, error)
	// HydrationPolicy answers what the owning provider declared for a type.
	// ok=false for an unknown type, and for a known type whose provider does
	// not hydrate.
	HydrationPolicy(t domain.ContextReferenceType) (ReferenceHydrationPolicy, bool)
	// Hydrate reads one entity's present state through its provider.
	//
	// The registry does NOT check authorization: it holds no grants and no
	// notion of an agent. The caller checks, before calling, against the
	// capability HydrationPolicy named. Keeping the check out of here is
	// what stops there being two answers to "may this agent read this".
	Hydrate(ctx context.Context, workspaceID uuid.UUID, ref domain.ContextReference) (ReferenceState, bool, error)
}

// ToolRegistry is the single source of which tools exist.
//
// Composed in code at start-up and immutable afterwards. There is no
// Register on this interface on purpose: a registry that can be written to
// at runtime is one whose contents depend on which requests have been
// served, and the answer to "which tools exist" must not.
type ToolRegistry interface {
	// Lookup resolves a name to its executor. Not found means the name is
	// not a tool in this build — never that the caller may not use it.
	Lookup(name domain.ToolName) (Tool, bool)
	// Definitions lists every registered tool, in name order.
	Definitions() []domain.ToolDefinition
}

// --- LLM ------------------------------------------------------------------

// ChatMessage is one turn on the wire to the provider. Deliberately not
// domain.Message: the provider protocol has a `system` role that we never
// persist, and none of our bookkeeping fields belong in a request body.
//
// The two tool fields are mid-turn only. An assistant message carrying
// ToolCalls is the model asking; a message with Role "tool" and a
// ToolCallID is us answering that exact ask. Neither is ever persisted as a
// domain.Message, and neither is replayed on a later turn — see
// app/send.go for why the scaffolding does not outlive its turn.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// ToolCalls is what an assistant message asked to run.
	ToolCalls []domain.ToolCall `json:"tool_calls,omitempty"`
	// ToolCallID correlates a `tool` message with the call it answers.
	ToolCallID string `json:"tool_call_id,omitempty"`
	// CacheBreakpoint marks this message as the END of a prefix that is
	// stable enough to be worth caching provider-side.
	//
	// ── Why the flag lives on a message and not on the request ─────────
	// Because "where does the stable part stop" is a fact about the
	// COMPOSITION of a context, and only the Context Builder knows it. The
	// adapter knows how to write a breakpoint down; it has no way to know
	// which of eight system messages is the last one whose bytes are the
	// same on every turn of an agent's life.
	//
	// ── What it is not ─────────────────────────────────────────────────
	// It is not a request for a different completion. A breakpoint changes
	// how the provider BILLS a prefix, never what the model reads: the same
	// characters arrive in the same order either way. That is the whole
	// claim of the lossless sprint, and it is asserted byte for byte in
	// TestCacheBreakpointDoesNotChangeWhatTheModelReads.
	//
	// False on every message of a turn is the pre-caching request body,
	// unchanged.
	CacheBreakpoint bool `json:"-"`
}

// Credentials carry the resolved, decrypted access details for one call.
// They are passed per-request rather than held on the adapter so a single
// client instance serves every workspace and every provider.
type Credentials struct {
	BaseURL string
	APIKey  string
}

type CompletionRequest struct {
	Creds    Credentials
	Model    string
	Messages []ChatMessage
	// Temperature is the operator's explicit preference, or nil when they
	// expressed none — in which case the adapter must not put the field on
	// the wire at all. A zero here is a real request for 0, not an absence.
	// See domain.Agent.Temperature.
	Temperature *float32
	MaxTokens   int
	// User is forwarded as the OpenAI `user` field so the gateway can
	// attribute spend to the agent that made the call. Metadata rides along
	// as LiteLLM request metadata (conversation_id, agent_id) for its spend
	// logs. Neither changes the completion; both are pure attribution.
	User     string
	Metadata map[string]string
	// Tools are the capabilities this call may ask for. Empty means the
	// request body carries no `tools` field at all, byte-identical to what
	// every turn sent before tools existed — which is the backward
	// compatibility guarantee, and it is asserted by
	// TestAgentWithoutToolsSendsNoToolsField.
	//
	// Declaring a tool is not permission to run it. Authorization is checked
	// again, against the store, when a call comes back. The model is not an
	// authority.
	Tools []domain.ToolDefinition
}

// StreamEvent is one increment of a streamed completion. A single event
// carries at most one kind of payload: reasoning, answer text, a terminal
// reason, or usage numbers — providers send them in separate frames.
type StreamEvent struct {
	// Reasoning is a chunk of the model's chain of thought, on the channel
	// reasoning models emit separately from the answer. It always arrives
	// before the first Delta and is never sent back on a later turn.
	Reasoning    string
	Delta        string
	FinishReason string
	Usage        *Usage
	// ToolCalls are complete, assembled calls, and they arrive exactly once
	// per stream — on the event that also carries the terminal reason.
	//
	// Assembly is the adapter's job, not the caller's. On the wire a call is
	// fragmented across chunks (the id and name in one, the arguments a few
	// characters at a time in the ones after), which is a property of the
	// OpenAI streaming protocol and therefore something the application
	// layer must never have to know. See adapters/llm/stream.go.
	ToolCalls []domain.ToolCall
}

// Usage is what the provider said one call consumed.
//
// ── Two counts are required, the rest are optional, and the difference is
//
//	the whole design of this type ────────────────────────────────────────
//
// PromptTokens and CompletionTokens are plain ints because a usage frame
// that omitted them would not be a usage frame; the adapter only builds a
// Usage at all when the provider reported them.
//
// Everything below is a pointer, and nil means **ABSENT** — the provider did
// not say — never **ZERO**. Collapsing the two would be the same mistake
// UsageSource exists to prevent one level up: a turn whose gateway does not
// report cache counts would be indistinguishable from a turn that read
// nothing from cache, and every cache-effectiveness number computed from
// them would silently include calls that were never measured.
//
// ── Why the fields are what they are ───────────────────────────────────
// The captured frame in adapters/llm/litellm_real_test.go shows a real
// LiteLLM reporting the same facts twice, in two vocabularies: OpenAI's
// (`prompt_tokens_details.cached_tokens`,
// `completion_tokens_details.reasoning_tokens`) and Anthropic's, forwarded
// verbatim (`cache_read_input_tokens`, `cache_creation_input_tokens`).
//
// CachedTokens and CacheReadTokens are kept APART rather than merged, even
// though a gateway is expected to make them equal. They come from different
// fields written by different layers, and the only way to find out whether
// they agree on this deployment is to record both and look. A single merged
// field would have decided that question by assumption.
type Usage struct {
	PromptTokens     int
	CompletionTokens int

	// TotalTokens is the provider's own total. Recorded rather than derived:
	// on a call that used prompt caching, whether the total equals
	// prompt+completion is a fact about the gateway's accounting, not an
	// identity we get to assume. See TestRealLiteLLMTotalTokensIsTheSum,
	// which is careful to scope its claim to the captured scenario.
	TotalTokens *int

	// CachedTokens is OpenAI's `prompt_tokens_details.cached_tokens`.
	CachedTokens *int
	// CacheReadTokens is Anthropic's `cache_read_input_tokens`, forwarded by
	// the gateway. Billed at a fraction of the input rate.
	CacheReadTokens *int
	// CacheCreationTokens is Anthropic's `cache_creation_input_tokens`.
	// Billed at a premium over the input rate, once, to establish an entry.
	CacheCreationTokens *int

	// ReasoningTokens is `completion_tokens_details.reasoning_tokens`. It is
	// part of the OUTPUT bill, and a turn that thought for a page and
	// answered in a line did not produce a line.
	ReasoningTokens *int
}

// CacheObserved reports whether this call carries any cache measurement at
// all.
//
// It is the guard every cache-effectiveness figure has to pass first: false
// means the numbers are unknown, which is a different statement from "no
// cache was used" and must never be rendered as a zero hit rate.
func (u *Usage) CacheObserved() bool {
	if u == nil {
		return false
	}
	return u.CacheReadTokens != nil || u.CacheCreationTokens != nil || u.CachedTokens != nil
}

// Stream is an open completion. Recv blocks until the next event and
// returns io.EOF when the provider closes the stream cleanly. Callers must
// Close, whether or not they read to EOF.
type Stream interface {
	Recv() (StreamEvent, error)
	io.Closer
}

// Model is one entry from the provider's catalog, used to populate the
// model picker instead of making the user type an identifier from memory.
type Model struct {
	ID      string `json:"id"`
	OwnedBy string `json:"owned_by,omitempty"`
}

// Price is the per-token cost of a model, as the gateway reports it. Values
// are dollars per single token (e.g. 1e-6), matching LiteLLM's /model/info.
type Price struct {
	InputCostPerToken  float64
	OutputCostPerToken float64
	// The two rates a cached prompt is billed at, when the gateway publishes
	// them. Nil is ABSENT — this model has no cache pricing on the card —
	// and is never read as free.
	//
	// ── Why they are read and not derived ──────────────────────────────
	// The published convention is 1,25x input to write an entry and 0,10x to
	// read one, and this deployment's card agrees for claude-opus-4-7
	// (6,25e-06 and 5,0e-07 against an input of 5,0e-06). Deriving them from
	// those multipliers would put a hardcoded pricing assumption in a system
	// whose whole accounting design exists to avoid exactly that: the card
	// is what the gateway will bill, the multiplier is what a document says.
	//
	// ── Why a cached turn cannot be priced without them ────────────────
	// `prompt_tokens` INCLUDES the cached tokens — measured, not assumed:
	// an identical request with and without a breakpoint reported the same
	// 13.982 prompt tokens, and the cached one attributed 13.965 of them to
	// a cache read. So pricing every prompt token at the input rate charges
	// a cache read at ten times what it cost, and the books would report a
	// saving of exactly zero on a turn that saved 90% of its input.
	CacheCreationCostPerToken *float64
	CacheReadCostPerToken     *float64
}

// KeySpend is the authoritative spend of one virtual key, read straight
// from the gateway. Unlike the local token tallies this is the real amount
// billed. MaxBudget is nil when the key is uncapped.
type KeySpend struct {
	KeyAlias  string   `json:"key_alias"`
	Spend     float64  `json:"spend"`
	MaxBudget *float64 `json:"max_budget"`
	Models    []string `json:"models"`
}

type LLM interface {
	Stream(ctx context.Context, req CompletionRequest) (Stream, error)
	// Models lists what the endpoint exposes. Doubles as the connection
	// test: it is the cheapest authenticated round trip available.
	Models(ctx context.Context, creds Credentials) ([]Model, error)
	// ModelPrices returns per-token costs keyed by model name, for the
	// models this credential can see. Used to price local token tallies.
	ModelPrices(ctx context.Context, creds Credentials) (map[string]Price, error)
	// KeyInfo reads the gateway's own spend record for the key in creds —
	// the real billed amount, which a scoped key is allowed to read about
	// itself even though it cannot read the global spend logs.
	KeyInfo(ctx context.Context, creds Credentials) (KeySpend, error)
}

// TurnMetrics counts how turns END.
//
// ══════════════════════════════════════════════════════════════════════
//
//	SAFE METADATA ONLY — NEVER CONTENT
//
// ══════════════════════════════════════════════════════════════════════
//
// ── Why this port exists at all ────────────────────────────────────────
// R1 asked one question of the running system — "why was this turn
// interrupted?" — and could not answer it without reading source code and
// reconstructing the turn by hand from three tables. Fourteen turns had
// been killed by a router deadline over two weeks and nothing counted them.
//
// ── Why it takes a label and not a turn ────────────────────────────────
// Because a port that took the turn would eventually be handed the prompt.
// The only argument is a terminal reason from a CLOSED vocabulary — see
// domain.FinishReason.TerminalLabel — which bounds the label cardinality
// the metric can ever have and makes a content leak through this interface
// impossible to write rather than merely against the rules.
//
// Declared here, in the consuming module, and satisfied by
// platform/metrics. Same arrangement the workspace middleware uses, and for
// the same reason: the module names an interface, never a registry.
//
// Nil is a valid deployment: a binary with no metrics runs the same turns.
type TurnMetrics interface {
	TurnTerminal(reason string)
}
