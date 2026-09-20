package domain

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Tools: the vocabulary of what an agent is allowed to *do*.
//
// ── The three concepts this file keeps apart ───────────────────────────
// A **Tool** is a capability offered to an agent: a name, an input
// contract, and an authorization. A **Integration** is the protocol talk
// with an external system. A **Module Capability** is an operation owned by
// an internal domain. This file knows only about the first one, on purpose:
// the day `github.repository.read` exists, Agents must still be unable to
// name GitHub.
//
// ── What lives here and what does not ──────────────────────────────────
// Here: the name rule, the declaration, the input schema and its
// validation, the call and its result, and the error vocabulary. Not here:
// the registry (a composition concern, adapters/tools), the authorization
// store (a repository), the execution loop (app/send.go), and the wire
// encoding for the provider (adapters/llm). All four depend on this file;
// none of them is this file.
//
// ── Why the definition is code and not a table ─────────────────────────
// A tool's name, description and input contract are properties of the
// program that implements it. Storing them would create a second copy that
// drifts the first time a deploy changes a schema, and would let a row
// describe an executor that no longer exists. What IS stored is the
// authorization — see AgentTool — because that is a decision the user makes
// and the code cannot know.

/* ── the name ────────────────────────────────────────────────────────── */

// ToolName is the stable, machine-readable identity of a capability.
//
// The convention, and it is normative:
//
//	namespace.resource.action        github.repository.read
//	namespace.action                 system.echo
//
// Two to four dot-separated segments, each `[a-z][a-z0-9_]*`. Lowercase
// because a name that differs only in case is two names to a database and
// one to a human. The namespace is the *owner* of the capability, never the
// vendor behind it: a tool backed by the Finance module is `finance.…`, and
// a tool backed by an integration is named after the system it exposes.
//
// The name is what `chat.agent_tools` stores and what the audit trail
// records, so it must outlive refactors of whatever implements it. Renaming
// a tool is therefore a breaking change and revokes every authorization
// that referenced the old name.
type ToolName string

// maxToolNameLength bounds the canonical name. Sixty-four is the ceiling
// OpenAI-compatible gateways document for a function name, and the wire
// encoding of a dotted name is never shorter than the name itself — see
// the encoding note in adapters/llm.
const maxToolNameLength = 64

// ValidToolName reports whether s follows the convention above.
//
// A double underscore is rejected even though `[a-z0-9_]` would allow it.
// That is not cosmetic: the provider wire encoding maps `.` to `__`, and a
// segment containing `__` would make that mapping ambiguous in reverse.
func ValidToolName(s string) bool {
	if s == "" || len(s) > maxToolNameLength {
		return false
	}
	if strings.Contains(s, "__") {
		return false
	}
	segments := strings.Split(s, ".")
	if len(segments) < 2 || len(segments) > 4 {
		return false
	}
	for _, seg := range segments {
		if seg == "" || seg[0] < 'a' || seg[0] > 'z' {
			return false
		}
		for i := 0; i < len(seg); i++ {
			c := seg[i]
			ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_'
			if !ok {
				return false
			}
		}
	}
	return true
}

func (n ToolName) Valid() bool { return ValidToolName(string(n)) }

func (n ToolName) String() string { return string(n) }

// Namespace is the owner of the capability: the first segment.
//
// ── Why this is a method and not a `strings.Split` at each call site ───
// Because the namespace stopped being a naming convention and became an
// IDENTITY the moment `@` started selecting by integration. A user who
// attaches GitHub is attaching `github`, and that string travels on the
// wire, decides which capabilities a turn may declare, and is compared
// against the registry. Three copies of `name[:strings.Index(name,".")]`
// would be three chances to disagree about what a provider is.
//
// It is derived rather than stored for the same reason the convention is
// normative: the name IS the ownership statement — see the contract above —
// so a separate field would be a second answer to a question the first one
// already settles, free to drift the first time somebody sets one and not
// the other.
//
// A name that is not Valid() has no meaningful namespace; this returns
// whatever precedes the first dot, or the whole string when there is none,
// because callers that reach here have already validated and the ones that
// have not are better served by a stable answer than by a panic.
func (n ToolName) Namespace() string {
	s := string(n)
	if dot := strings.Index(s, "."); dot > 0 {
		return s[:dot]
	}
	return s
}

/* ── the declaration ─────────────────────────────────────────────────── */

// ToolEffect says whether running a tool changes anything.
//
// Two values, and only two, because only two are actionable today: the
// interface has to be able to say "this one only reads" before someone
// authorizes it. A finer risk scale would be a judgement nobody has asked
// for yet.
type ToolEffect string

const (
	// EffectRead: the tool observes and returns. Running it twice with the
	// same input changes nothing and is safe.
	EffectRead ToolEffect = "read"
	// EffectWrite: the tool changes state somewhere. Every write tool must
	// go through the owning module's public capability, never a table.
	EffectWrite ToolEffect = "write"
)

func (e ToolEffect) Valid() bool { return e == EffectRead || e == EffectWrite }

// ToolDefinition is everything the system needs to declare a tool to a
// model, show it to a user, and decide whether it may run.
//
// It carries no executor. The executor is a ports.Tool, and keeping the two
// apart is what lets the HTTP layer list definitions without being able to
// run anything.
type ToolDefinition struct {
	Name        ToolName   `json:"name"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Effect      ToolEffect `json:"effect"`
	// Internal marks a tool that exists to prove or exercise the machinery
	// rather than to be useful. It is shown, and shown as such: hiding it
	// would mean the only surface that can demonstrate authorization is a
	// test, and a capability the user cannot see is one they cannot revoke.
	Internal bool `json:"internal"`
	// Confidential marks a tool whose ARGUMENTS AND RESULT must not be kept
	// in the audit trail in the clear.
	//
	// ── Why the tool declares this rather than the audit deciding ──────
	// Because only the tool knows what it carries. The audit trail is a
	// generic mechanism that files whatever it is handed; teaching it which
	// namespaces are sensitive would put a list of module names inside
	// Agents, which is the one thing this module is arranged never to
	// contain. A capability that handles someone's money says so about
	// itself, and the recorder reads the flag.
	//
	// ── What is still recorded, and why that is enough ─────────────────
	// Everything except the payload: which capability ran, in which round,
	// for which conversation and turn, how long it took, whether it
	// succeeded, and the error code when it did not. That answers the
	// questions an audit trail exists for — what did this agent do, when,
	// and did it work — without keeping a copy of what somebody spent on
	// what.
	//
	// The cost is real and worth naming: a redacted result cannot feed
	// evidence continuity on later turns (see app/evidence.go, which skips
	// them). The agent re-reads through a capability instead, which for a
	// domain whose records change under the conversation is the behaviour
	// the hydration design already argues for.
	Confidential bool `json:"confidential"`
	// External marks a capability whose RESULT DESCRIBES A SYSTEM THIS
	// PRODUCT DOES NOT OWN.
	//
	// ── Why the tool declares this rather than Agents deciding ─────────
	// The same reason Confidential is declared and not inferred: only the
	// capability knows where its answer came from. Teaching Agents which
	// namespaces are external would put a list of vendor names inside the
	// one module that is arranged never to contain one.
	//
	// ── What it is for ────────────────────────────────────────────────
	// A claim about our own state is checkable against our own database
	// whenever somebody doubts it. A claim about somebody else's — a
	// follower count, a view count, what was published and when — is not,
	// and it looks exactly as authoritative whether it was read or
	// invented. A live agent reported 1.535 followers and 40.055 views in
	// a turn with no tool calls; the true figures were 163 and 224.
	//
	// So a turn's external reads are recorded and reported, and the
	// product can say "nothing was read here" instead of rendering prose
	// that reads like a measurement. See ReadReceipt.
	//
	// It is orthogonal to Effect. A write to an external system would be
	// both; nothing in this build is.
	External bool       `json:"external"`
	Schema   ToolSchema `json:"schema"`
}

// writeNotice is appended to what a write tool declares to the model.
//
// ── Why the effect has to be on the wire at all ────────────────────────
// The model is handed a name, a description and a schema. Nothing else. It
// has never been told which capabilities merely look and which ones change
// something, and for as long as every tool was a read that cost nothing.
//
// It stops costing nothing the moment the platform is asked to act without
// being told to each time — which is what the grounding policy now expects
// of reads. A rule the model cannot apply is worse than no rule: it would
// have to guess which capabilities the permission covers, from the name.
//
// So the effect the registry already records becomes a sentence the model
// can read. It is generated from the field, so a capability added later is
// covered the day it is registered, and nothing here names a vendor, a
// module or a tool.
const writeNotice = " This capability CHANGES data. Never use it unless the " +
	"user asked for that change in this conversation."

// externalNotice is appended to what an EXTERNAL capability declares.
//
// ── Why the model is told, and told structurally ───────────────────────
// The same argument writeNotice makes. The model is handed a name, a
// description and a schema, and nothing in that tells it which
// capabilities answer for systems it cannot otherwise see. Left to infer
// it from the name, it will sometimes infer that it already knows the
// answer — which is exactly how a turn produced a follower count with no
// call behind it.
//
// Generated from the field, so a capability added later is covered the day
// it is registered, and the sentence names no vendor, no module and no
// tool. It is defence in depth and NOT the guarantee: the guarantee is the
// receipt, which does not depend on the model reading anything.
const externalNotice = " This capability reads a system OUTSIDE this product. " +
	"Its result is only true for the moment it ran. Never state anything about " +
	"that system's current state — counts, metrics, what exists there now — " +
	"unless you called this capability in THIS turn and it succeeded."

// DeclaredDescription is what the model is told this tool does.
//
// The description as written, plus the notices its declaration earns. It
// is one function so that the adapter that puts it on the wire and the
// report that prices it cannot disagree about what was sent.
func (d ToolDefinition) DeclaredDescription() string {
	out := d.Description
	if d.Effect == EffectWrite {
		out += writeNotice
	}
	if d.External {
		out += externalNotice
	}
	return out
}

func (d ToolDefinition) Validate() error {
	if !d.Name.Valid() {
		return Invalid("tool name must be 2..4 lowercase dot-separated segments, e.g. system.echo")
	}
	if strings.TrimSpace(d.Title) == "" {
		return Invalid("tool " + d.Name.String() + " needs a title")
	}
	if strings.TrimSpace(d.Description) == "" {
		// The description is what the model reads to decide whether to call
		// it. A tool without one is a tool that will be called at random.
		return Invalid("tool " + d.Name.String() + " needs a description")
	}
	if !d.Effect.Valid() {
		return Invalid("tool " + d.Name.String() + " must declare effect read or write")
	}
	return d.Schema.Validate()
}

/* ── the input schema ────────────────────────────────────────────────── */

// ToolSchema is the input contract, expressed as the subset of JSON Schema
// that this system actually validates.
//
// ── Why a subset and why it is closed ──────────────────────────────────
// The model is told the contract in JSON Schema because that is what the
// OpenAI-compatible protocol carries; inventing a DSL would mean teaching
// every gateway a private language. But *accepting* all of JSON Schema
// would mean either pulling in a validator dependency or writing one, and
// then honestly declaring which drafts and keywords we support.
//
// So the schema is a closed subset: a flat object of scalar properties,
// with `required` and `additionalProperties:false`. It is expressive enough
// for every tool this batch defines and for the shapes the first real tools
// will need (an owner, a repo, a path, a month). A tool that needs nested
// objects or arrays is a change to this type and to Validate, deliberately
// — a schema keyword we send and do not enforce is a contract we do not
// have.
type ToolSchema struct {
	Properties map[string]ToolProperty `json:"properties"`
	Required   []string                `json:"required,omitempty"`
}

// ToolPropertyType is the closed set of scalar types the validator knows.
type ToolPropertyType string

const (
	TypeString  ToolPropertyType = "string"
	TypeNumber  ToolPropertyType = "number"
	TypeInteger ToolPropertyType = "integer"
	TypeBoolean ToolPropertyType = "boolean"
)

func (t ToolPropertyType) Valid() bool {
	switch t {
	case TypeString, TypeNumber, TypeInteger, TypeBoolean:
		return true
	}
	return false
}

type ToolProperty struct {
	Type        ToolPropertyType `json:"type"`
	Description string           `json:"description,omitempty"`
	// MaxLength bounds a string property. Zero means the only bound is the
	// whole-payload ceiling; see MaxToolArgumentsBytes.
	MaxLength int `json:"max_length,omitempty"`
}

func (s ToolSchema) Validate() error {
	if len(s.Properties) == 0 {
		return Invalid("tool schema must declare at least one property")
	}
	for name, p := range s.Properties {
		if name == "" {
			return Invalid("tool schema property needs a name")
		}
		if !p.Type.Valid() {
			return Invalid("tool schema property " + name + " has an unsupported type")
		}
	}
	for _, req := range s.Required {
		if _, ok := s.Properties[req]; !ok {
			return Invalid("tool schema requires " + req + ", which it does not declare")
		}
	}
	return nil
}

// PropertyNames returns the declared properties in a stable order, which is
// what a deterministic wire encoding and a deterministic report both need.
func (s ToolSchema) PropertyNames() []string {
	names := make([]string, 0, len(s.Properties))
	for n := range s.Properties {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// MaxToolArgumentsBytes bounds what a model may hand a tool in one call.
//
// Sixteen kibibytes. The reasoning is the same one behind every other
// ceiling in this module: an unbounded field is a channel, and a channel
// the model controls is one an operator cannot budget for. Arguments are
// identifiers, paths and short strings; 16 KiB is four times more than any
// shape this schema can express legitimately needs, and small enough that
// a runaway generation is refused rather than stored.
const MaxToolArgumentsBytes = 16 << 10

// MaxToolResultBytes bounds what a tool may hand back to the model.
//
// Every byte here is a prompt token on the next provider call of the same
// turn, paid for at input rates. Thirty-two kibibytes is roughly eight
// thousand tokens, which is already a large share of a turn — a tool that
// wants to return more is a tool that should be paginating.
const MaxToolResultBytes = 32 << 10

// ValidateArguments checks one call's arguments against the schema and
// returns them decoded.
//
// The order of the checks is the order of the failures worth telling apart:
// too big before parsing (so a hostile payload is refused without being
// decoded), malformed before shape, unknown properties before missing ones
// (a typo'd name would otherwise be reported as a missing field), and types
// last.
//
// Unknown properties are REJECTED rather than ignored. A model that invents
// `path` when the schema says `file` has misunderstood the contract, and
// silently dropping the field would run the tool on the wrong input and
// report success.
func (s ToolSchema) ValidateArguments(raw string) (map[string]any, error) {
	if len(raw) > MaxToolArgumentsBytes {
		return nil, ToolError(ToolErrInvalidArguments, fmt.Sprintf(
			"arguments are %d bytes, above the %d byte limit", len(raw), MaxToolArgumentsBytes))
	}
	// An absent object and an empty one are the same request. Some gateways
	// send "" for a no-argument call, which json.Unmarshal rejects.
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil, ToolError(ToolErrInvalidArguments, "arguments are not a JSON object")
	}

	for name := range fields {
		if _, ok := s.Properties[name]; !ok {
			return nil, ToolError(ToolErrInvalidArguments, "unknown argument "+name)
		}
	}
	for _, req := range s.Required {
		if _, ok := fields[req]; !ok {
			return nil, ToolError(ToolErrInvalidArguments, "argument "+req+" is required")
		}
	}

	out := make(map[string]any, len(fields))
	for name, rawValue := range fields {
		p := s.Properties[name]
		value, err := decodeProperty(name, p, rawValue)
		if err != nil {
			return nil, err
		}
		out[name] = value
	}
	return out, nil
}

func decodeProperty(name string, p ToolProperty, raw json.RawMessage) (any, error) {
	typeErr := func() error {
		return ToolError(ToolErrInvalidArguments,
			"argument "+name+" must be a "+string(p.Type))
	}
	switch p.Type {
	case TypeString:
		var v string
		if json.Unmarshal(raw, &v) != nil {
			return nil, typeErr()
		}
		if p.MaxLength > 0 && utf8.RuneCountInString(v) > p.MaxLength {
			return nil, ToolError(ToolErrInvalidArguments, fmt.Sprintf(
				"argument %s is longer than %d characters", name, p.MaxLength))
		}
		return v, nil
	case TypeBoolean:
		var v bool
		if json.Unmarshal(raw, &v) != nil {
			return nil, typeErr()
		}
		return v, nil
	case TypeNumber:
		var v float64
		if json.Unmarshal(raw, &v) != nil {
			return nil, typeErr()
		}
		return v, nil
	case TypeInteger:
		// The quote check comes first, and it is not redundant. Decoding a
		// JSON string into a json.Number SUCCEEDS in the standard library
		// whenever the string happens to look like a number, so `"3"` would
		// otherwise pass as an integer — a type check that accepts the wrong
		// type is worse than no type check, because it reports confidence.
		if len(raw) > 0 && raw[0] == '"' {
			return nil, typeErr()
		}
		var v json.Number
		if json.Unmarshal(raw, &v) != nil {
			return nil, typeErr()
		}
		n, err := v.Int64()
		if err != nil {
			// 3.5 decodes fine as a number and is not an integer. The
			// distinction is the whole reason the two types are separate.
			return nil, typeErr()
		}
		return n, nil
	default:
		return nil, typeErr()
	}
}

/* ── the call and its result ─────────────────────────────────────────── */

// ToolCall is one request from the model to run a tool.
//
// ID is the provider's correlation token and is opaque to us. It is what
// ties a result back to the request that asked for it, and sending back a
// different one is how a turn starts answering the wrong question — see
// TestToolResultCarriesTheProviderCallID.
//
// Arguments is the raw JSON string exactly as the model produced it, kept
// unparsed so the audit trail records what was asked rather than what we
// managed to read.
type ToolCall struct {
	ID        string   `json:"id"`
	Name      ToolName `json:"name"`
	Arguments string   `json:"arguments"`
}

// ToolOutput is what an executor returns on success: a structured document,
// serialised once by the executor's caller.
//
// A map rather than a string because the protocol back to the model is JSON
// either way, and letting each executor format its own reply is how six
// tools end up with six conventions the model has to guess between.
type ToolOutput map[string]any

/* ── the error vocabulary ────────────────────────────────────────────── */

// ToolErrorCode names why a tool call did not produce a result.
//
// Five of the seven are RECOVERABLE: they are handed back to the model as
// the content of a `tool` message, so it can apologise, correct itself, or
// try a different call. That is the whole reason they are distinguishable —
// a model told "error" learns nothing, a model told "unknown argument path"
// can fix it.
//
// The other two are not tool errors at all: ToolErrRoundLimit is this
// system stopping the loop, and ToolErrTurnStopped is the turn's context
// ending underneath calls that had been asked for. Both end the turn. See
// app/send.go.
type ToolErrorCode string

const (
	// ToolErrNotFound: the model named a tool the registry does not have.
	// Usually a hallucinated name.
	ToolErrNotFound ToolErrorCode = "tool_not_found"
	// ToolErrNotAuthorized: the tool exists and this agent may not use it.
	// Reported to the model as a refusal, never as absence — a model told
	// "no such tool" would keep inventing variations of the name.
	ToolErrNotAuthorized ToolErrorCode = "tool_not_authorized"
	// ToolErrInvalidArguments: the arguments failed the schema.
	ToolErrInvalidArguments ToolErrorCode = "tool_invalid_arguments"
	// ToolErrExecutionFailed: the executor ran and returned an error.
	ToolErrExecutionFailed ToolErrorCode = "tool_execution_failed"
	// ToolErrTimeout: the executor did not finish inside its deadline.
	ToolErrTimeout ToolErrorCode = "tool_timeout"
	// ToolErrRoundLimit: the turn reached its ceiling of tool rounds. Fatal
	// to the turn, and deliberately not recoverable: handing this back to
	// the model would invite exactly the loop it exists to stop.
	ToolErrRoundLimit ToolErrorCode = "tool_round_limit"
	// ToolErrTurnStopped: the turn's context ended — a clock or a reader
	// going away — while calls the model had asked for were still waiting
	// to run.
	//
	// NO REQUESTED TOOL DISAPPEARS SILENTLY, and this is the second place
	// that could have let one. The ceiling has recorded its refusals since
	// the receipt existed; this path did not, because nothing read that
	// record: a stopped turn could not be continued, so the fact that its
	// pending work was missing was invisible. Making the infrastructure
	// deadline resumable is what made it matter — a continuation is told
	// what is still pending, and "nothing is pending" would have been a
	// lie told to a model that is about to act on it.
	//
	// Not recoverable, for the same reason the ceiling is not: the turn is
	// over, and handing this to the model would invite it to try again
	// inside a turn that has already ended.
	ToolErrTurnStopped ToolErrorCode = "tool_turn_stopped"
)

// Recoverable reports whether the model gets to see this and carry on.
func (c ToolErrorCode) Recoverable() bool {
	return c != ToolErrRoundLimit && c != ToolErrTurnStopped
}

// ToolFailure is a tool call that did not succeed, in the form both the
// audit trail and the model receive.
type ToolFailure struct {
	Code    ToolErrorCode `json:"code"`
	Message string        `json:"message"`
}

func (f *ToolFailure) Error() string { return string(f.Code) + ": " + f.Message }

// ToolError builds a failure. It is an `error` so an executor can return it
// directly, and a typed value so the loop can branch on the code.
func ToolError(code ToolErrorCode, msg string) *ToolFailure {
	return &ToolFailure{Code: code, Message: msg}
}

/* ── authorization ───────────────────────────────────────────────────── */

// AgentTool is one authorization: this agent may use this tool.
//
// ── Why the row exists at all ──────────────────────────────────────────
// Presence in the registry is availability, not permission. The default is
// DENY and the absence of a row is the denial; there is no `enabled`
// column, because a disabled authorization and no authorization are the
// same fact and storing both invites them to disagree.
//
// ── Why ToolName has no foreign key ────────────────────────────────────
// The definition lives in code. A foreign key would need a table of tools
// kept in step with the binary by hand, which is a second source of truth
// wearing the costume of integrity. What the database does enforce is what
// it actually knows: the agent exists, the name is well formed, and the
// pair is unique. Whether the name still resolves is a question for the
// registry, asked on every read — see app/tools.go.
type AgentTool struct {
	ID          uuid.UUID `json:"id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	AgentID     uuid.UUID `json:"agent_id"`
	ToolName    ToolName  `json:"tool_name"`
	CreatedAt   time.Time `json:"created_at"`
}

/* ── the audit record ────────────────────────────────────────────────── */

// ToolCallRecord is one executed (or refused) tool call, as it is stored.
//
// ── What it must be able to answer, later ──────────────────────────────
// Which tool, which call, what input, what result, success or failure,
// when, for which agent, in which conversation, in which turn. Every one of
// those is a column or is reachable in one join from one.
//
// ── Why Arguments and Result are nilable ───────────────────────────────
// Not because they are optional — a successful call has both — but because
// this record has to be redactable later without changing its shape. A tool
// that one day handles something sensitive needs a way to keep the fact of
// the call while dropping its payload; nil is that way, and Redacted says
// which of the two nil means. Nothing redacts anything today, and that is
// exactly why the affordance is designed in now rather than retrofitted
// onto rows that already exist.
type ToolCallRecord struct {
	ID uuid.UUID `json:"id"`
	// MessageID is the assistant turn this call belongs to. It is the turn's
	// record, so truncate and regenerate take the call with them — the same
	// lifecycle as the context report, for the same reason.
	MessageID   uuid.UUID `json:"message_id"`
	WorkspaceID uuid.UUID `json:"workspace_id"`
	// ConversationID is denormalised from the message on purpose: the
	// transcript reads a whole thread's calls at once, and reaching them
	// through messages would be a join on every open.
	ConversationID uuid.UUID `json:"conversation_id"`
	// Round is which provider round asked for it, 1-based.
	Round int `json:"round"`
	// ProviderCallID is the gateway's tool_call id, recorded because it is
	// the only thing that correlates our record with the gateway's own logs.
	ProviderCallID string         `json:"provider_call_id"`
	ToolName       ToolName       `json:"tool_name"`
	Arguments      *string        `json:"arguments"`
	Result         *string        `json:"result"`
	Status         ToolCallStatus `json:"status"`
	// Effect is what the capability's definition said AT THE MOMENT IT RAN.
	//
	// Stored rather than looked up, because the registry describes the
	// build running now and a receipt is a statement about a turn that
	// already happened. A capability whose effect changed, or which was
	// dropped from the build, must not be able to rewrite what a past turn
	// is reported to have done.
	Effect ToolEffect `json:"effect"`
	// External is what the definition declared at the moment of execution.
	// Stored rather than looked up, for the reason migration 0020 gives:
	// the registry describes the build running now, not the build that ran
	// then.
	External bool `json:"external"`
	// EffectRef is the entity this call touched, when the capability
	// reported one. Nil is the common case and means "no identity was
	// reported" — never "unknown, guess". See domain/effect_ref.go.
	EffectRef    *EffectRef    `json:"effect_ref,omitempty"`
	ErrorCode    ToolErrorCode `json:"error_code,omitempty"`
	ErrorMessage string        `json:"error_message,omitempty"`
	DurationMS   int           `json:"duration_ms"`
	Redacted     bool          `json:"redacted"`
	CreatedAt    time.Time     `json:"created_at"`
}

type ToolCallStatus string

const (
	ToolCallOK    ToolCallStatus = "ok"
	ToolCallError ToolCallStatus = "error"
	// ToolCallNotExecuted: the model asked for it and the runtime refused
	// before it ran — an unauthorized capability, arguments that did not
	// validate. It did not fail at its job; it never got one.
	//
	// Kept apart from `error` because a receipt has to be able to say
	// whether something was ATTEMPTED, and collapsing the two makes that
	// unanswerable.
	ToolCallNotExecuted ToolCallStatus = "not_executed"
)

func (s ToolCallStatus) Valid() bool {
	return s == ToolCallOK || s == ToolCallError || s == ToolCallNotExecuted
}

/* ── write receipts ──────────────────────────────────────────────────── */

// WriteExecutionStatus is what the SYSTEM knows about one requested write,
// independently of anything the model wrote in prose.
type WriteExecutionStatus string

const (
	// WriteRequested: the model asked. Nothing more is claimed.
	WriteRequested WriteExecutionStatus = "REQUESTED"
	// WriteExecuted: the capability ran and reported success. This is the
	// only value that may be presented to a person as a completed change.
	WriteExecuted WriteExecutionStatus = "EXECUTED"
	// WriteFailed: it ran and failed.
	WriteFailed WriteExecutionStatus = "FAILED"
	// WriteNotExecuted: it was refused before running.
	WriteNotExecuted WriteExecutionStatus = "NOT_EXECUTED"
)

// WriteExecution is one write capability's receipt.
//
// It carries no arguments and no result, so a Confidential capability
// produces exactly the same receipt as any other: the trail says WHAT ran
// and HOW it ended, never what money it touched.
type WriteExecution struct {
	ToolCallID uuid.UUID            `json:"tool_call_id"`
	Capability ToolName             `json:"capability"`
	Status     WriteExecutionStatus `json:"status"`
	OccurredAt time.Time            `json:"occurred_at"`
	DurationMS int                  `json:"duration_ms"`
	// ErrorCode is present on FAILED and NOT_EXECUTED. It is a code, not a
	// message, because a message can carry the very data redaction removed.
	ErrorCode ToolErrorCode `json:"error_code,omitempty"`
	// Ref identifies WHAT this write touched, when the capability reported
	// it. It is the difference between "a create ran" and "THIS room
	// exists", and it is what makes a resume able to continue instead of
	// starting over. Absent on most writes, and absent is safe: a resume
	// with no ref has to read.
	//
	// It carries no name and no content by construction — see
	// domain.EffectRef on why the id is a UUID.
	Ref *EffectRef `json:"ref,omitempty"`
}

// WriteReceipt is one assistant turn's answer to "did anything change?".
//
// ── Why the product needs this and not the model's sentence ────────────
// A live financial agent answered "8 transações importadas" in a turn with
// zero tool calls, against a ledger holding zero transactions. Nothing was
// written and the person was told otherwise, because the absence of a tool
// call is invisible at the presentation layer.
//
// So the answer is made explicit, for every turn, whether or not anything
// ran. Executed == 0 is a statement, not a missing field, and a surface
// that renders this cannot present a fabricated claim as a confirmed one
// without ignoring it on purpose.
type WriteReceipt struct {
	MessageID uuid.UUID        `json:"message_id"`
	Writes    []WriteExecution `json:"writes"`
	Executed  int              `json:"executed"`
	Failed    int              `json:"failed"`
	Refused   int              `json:"refused"`
}

// Confirmed reports whether this turn actually changed anything.
//
// The single predicate every surface must route through. "The model said
// so" is not one of its inputs.
func (r WriteReceipt) Confirmed() bool { return r.Executed > 0 }

// NewWriteReceipt derives a turn's receipt from its audit records.
//
// Read-effect calls are skipped: a receipt answers "did anything CHANGE",
// and a listing that ran is not a change.
func NewWriteReceipt(messageID uuid.UUID, records []ToolCallRecord) WriteReceipt {
	r := WriteReceipt{MessageID: messageID, Writes: []WriteExecution{}}
	for _, rec := range records {
		if rec.Effect != EffectWrite {
			continue
		}
		w := WriteExecution{
			ToolCallID: rec.ID,
			Capability: rec.ToolName,
			OccurredAt: rec.CreatedAt,
			DurationMS: rec.DurationMS,
			ErrorCode:  rec.ErrorCode,
			// Carried only when the stored ref is still well formed. A row
			// that somehow holds a malformed one is reported as having no
			// identity rather than a broken one: the fallback is a read,
			// and a read is always correct.
			Ref: validRef(rec.EffectRef),
		}
		switch rec.Status {
		case ToolCallOK:
			w.Status = WriteExecuted
			r.Executed++
		case ToolCallError:
			w.Status = WriteFailed
			r.Failed++
		case ToolCallNotExecuted:
			w.Status = WriteNotExecuted
			r.Refused++
		default:
			w.Status = WriteRequested
		}
		r.Writes = append(r.Writes, w)
	}
	return r
}

// validRef passes a ref through only if it still satisfies the contract.
//
// Defence in depth rather than paranoia about the database: the column is
// a uuid and a CHECK bounds the type, so a bad value should be impossible.
// If one appears anyway, the honest report is "no identity", because an
// identifier that cannot be trusted is worse than none — it would send a
// resume to continue the wrong entity with full confidence.
func validRef(r *EffectRef) *EffectRef {
	if r == nil || !r.Valid() {
		return nil
	}
	return r
}
