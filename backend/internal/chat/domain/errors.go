package domain

import "errors"

// This mirrors finance/domain/errors.go by design. Each bounded context
// owns its own error vocabulary — sharing one would create exactly the
// cross-context import the architecture forbids, and would couple two
// contexts that must be able to evolve (and be extracted) separately.
// Thirty duplicated lines is the cheaper half of that trade.

type ErrorKind string

const (
	KindInvalid   ErrorKind = "invalid"
	KindNotFound  ErrorKind = "not_found"
	KindConflict  ErrorKind = "conflict"
	KindUpstream  ErrorKind = "upstream"
	KindNotConfig ErrorKind = "not_configured"
	// KindBudget is the system refusing a turn under a limit the user
	// configured. It is deliberately its own kind: it is neither a bad
	// request (the request is fine), nor a conflict with stored state, nor
	// an upstream failure — the provider was never asked.
	KindBudget ErrorKind = "budget"
	// KindToolLoop is the system stopping a turn that kept asking for tools.
	// Its own kind for the same reason as KindBudget: the request was fine,
	// the provider answered, and nothing is broken — we decided to stop.
	KindToolLoop ErrorKind = "tool_loop"
)

type Error struct {
	Kind    ErrorKind
	Message string
	// Code overrides the wire code the HTTP layer would derive from Kind.
	// It exists so one kind can carry several machine-readable reasons —
	// a client must be able to tell "you are out of tokens" from "we could
	// not price this turn" without reading the prose.
	Code string
}

func (e *Error) Error() string { return e.Message }

func Invalid(msg string) *Error   { return &Error{Kind: KindInvalid, Message: msg} }
func NotFound(what string) *Error { return &Error{Kind: KindNotFound, Message: what + " not found"} }
func Conflict(msg string) *Error  { return &Error{Kind: KindConflict, Message: msg} }

// MemoryConsolidationDisabled refuses to ask the model about a thread when
// the agent's memory policy says it may not be asked.
//
// A conflict rather than a forbidden: the caller is allowed here and the
// request is well formed — the agent is in a state that does not permit the
// operation, which is the same shape as refusing to delete an agent that
// still has memories. The code travels beside the sentence so a client can
// tell this 409 from any other without parsing prose.
func MemoryConsolidationDisabled(msg string) *Error {
	return &Error{Kind: KindConflict, Message: msg, Code: CodeMemoryConsolidationDisabled}
}

// CodeMemoryConsolidationDisabled is the machine-readable reason. Exported
// because the interface branches on it to point at the settings card
// instead of showing a generic failure.
const CodeMemoryConsolidationDisabled = "memory_consolidation_disabled"

// Upstream wraps a failure that came from the LLM provider rather than from
// us — a refused key, a rate limit, an unreachable endpoint. It maps to 502
// at the HTTP boundary so a provider outage is never reported as a bug in
// this service.
func Upstream(msg string) *Error { return &Error{Kind: KindUpstream, Message: msg} }

// NotConfigured means the operation needs a piece of deployment config that
// is absent — currently only SECRETS_KEY. The fix is an env var, so this
// maps to 503 rather than 500: retrying is pointless until an operator acts.
func NotConfigured(msg string) *Error { return &Error{Kind: KindNotConfig, Message: msg} }

// BudgetExceeded refuses a turn under a configured limit. `code` is the
// machine-readable reason (domain.BudgetBlockReason), carried separately
// from the sentence so the UI can branch on one and show the other.
func BudgetExceeded(code, msg string) *Error {
	return &Error{Kind: KindBudget, Message: msg, Code: code}
}

// ToolLoopStopped ends a turn that reached the tool round ceiling.
//
// It carries ToolErrRoundLimit as its wire code, so a client branches on
// the same vocabulary the audit trail and the model-facing failures use,
// rather than on a second name for the same event.
func ToolLoopStopped(msg string) *Error {
	return &Error{Kind: KindToolLoop, Message: msg, Code: string(ToolErrRoundLimit)}
}

// IsKind reports whether err is a domain error of the given kind.
// Use this at the HTTP boundary to map domain errors to status codes.
func IsKind(err error, k ErrorKind) bool {
	var de *Error
	if errors.As(err, &de) {
		return de.Kind == k
	}
	return false
}
