package domain

import "fmt"

// Kind is the vocabulary of failure this integration reports.
//
// Deliberately small, and deliberately not HTTP status codes: the same fact
// has to render as a 404 to a management route and as a recoverable tool
// failure to a model, and a domain that spoke either dialect would force
// the other to translate backwards.
type Kind string

const (
	// KindInvalid: the request was understood and is not allowed.
	KindInvalid Kind = "invalid"
	// KindNotConnected: this workspace has no Meta Threads connection. Its
	// own kind rather than a not-found, because it is the one failure with
	// an obvious next action — connect — and every surface branches on it.
	KindNotConnected Kind = "not_connected"
	// KindNotFound: no such post, or none this credential can see. The two
	// are one answer on purpose.
	KindNotFound Kind = "not_found"
	// KindScopeMissing: the credential is real and does not carry the
	// permission this call needs. Distinct from Unauthorized because the
	// fix is different: reconnect and grant the scope, rather than
	// reconnect because the token died.
	KindScopeMissing Kind = "scope_missing"
	// KindTokenExpired: the stored token is past its lifetime.
	KindTokenExpired Kind = "token_expired"
	// KindUpstream: Meta answered, and answered with a failure. Reported as
	// itself so nothing downstream mistakes an outage for an empty result —
	// see the note in tools.go on why that distinction is load-bearing.
	KindUpstream Kind = "upstream"
	// KindNotConfigured: this deployment has no Meta app configured, so no
	// OAuth flow can even begin. Not the operator's mistake and not a bug:
	// an honest 503 with an actionable sentence.
	KindNotConfigured Kind = "not_configured"
)

type Error struct {
	Kind    Kind
	Message string
}

func (e *Error) Error() string { return string(e.Kind) + ": " + e.Message }

func Invalid(format string, a ...any) *Error {
	return &Error{Kind: KindInvalid, Message: fmt.Sprintf(format, a...)}
}

func NotConnected() *Error {
	return &Error{Kind: KindNotConnected, Message: "this workspace has no Meta Threads connection"}
}

func NotFound(format string, a ...any) *Error {
	return &Error{Kind: KindNotFound, Message: fmt.Sprintf(format, a...)}
}

func ScopeMissing(s Scope) *Error {
	return &Error{Kind: KindScopeMissing, Message: "the connected Meta Threads account did not grant " +
		s.String() + "; reconnect and allow it"}
}

func TokenExpired() *Error {
	return &Error{Kind: KindTokenExpired, Message: "the Meta Threads token has expired; reconnect the account"}
}

func Upstream(format string, a ...any) *Error {
	return &Error{Kind: KindUpstream, Message: fmt.Sprintf(format, a...)}
}

func NotConfigured() *Error {
	return &Error{Kind: KindNotConfigured, Message: "this deployment has no Meta Threads app configured " +
		"(META_THREADS_APP_ID, META_THREADS_APP_SECRET, META_THREADS_REDIRECT_URI)"}
}
