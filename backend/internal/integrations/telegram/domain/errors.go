package domain

// This mirrors the error vocabulary every other bounded context owns —
// github/domain/errors.go, chat/domain/errors.go. Sharing one would create
// exactly the cross-context import the architecture forbids.
//
// ── The one rule that is specific to this context ──────────────────────
// A message from this vocabulary can end up on somebody's phone. So every
// sentence here is written to be safe in front of a person who may not be
// the operator: no ids, no internal state, no vendor error text. What a
// Telegram user is told and what the log records are produced by two
// different code paths on purpose — see app/reply.go.

type ErrorKind string

const (
	KindInvalid ErrorKind = "invalid"
	// KindNotPaired is the refusal that protects the workspace. Its own
	// kind rather than a generic "forbidden" because it is the one the
	// product answers with an instruction — "send /start" — and every
	// other refusal answers with nothing.
	KindNotPaired ErrorKind = "not_paired"
	KindNotFound  ErrorKind = "not_found"
	KindConflict  ErrorKind = "conflict"
	// KindBusy is the concurrency refusal: this Telegram chat already has
	// a turn running. Not an error in the sense of something broken — it
	// is the serialisation rule doing its job.
	KindBusy ErrorKind = "busy"
	// KindUpstream is Telegram itself failing.
	KindUpstream ErrorKind = "upstream"
	// KindNotConfigured is deployment configuration missing.
	KindNotConfigured ErrorKind = "not_configured"
)

type Error struct {
	Kind    ErrorKind
	Message string
}

func (e *Error) Error() string { return e.Message }

func Invalid(msg string) *Error       { return &Error{Kind: KindInvalid, Message: msg} }
func NotPaired(msg string) *Error     { return &Error{Kind: KindNotPaired, Message: msg} }
func NotFound(what string) *Error     { return &Error{Kind: KindNotFound, Message: what + " not found"} }
func Conflict(msg string) *Error      { return &Error{Kind: KindConflict, Message: msg} }
func Busy(msg string) *Error          { return &Error{Kind: KindBusy, Message: msg} }
func Upstream(msg string) *Error      { return &Error{Kind: KindUpstream, Message: msg} }
func NotConfigured(msg string) *Error { return &Error{Kind: KindNotConfigured, Message: msg} }
