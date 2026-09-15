package domain

import "fmt"

// Kind is the vocabulary of failure this context reports.
//
// Deliberately small and deliberately NOT http status codes: the same
// fact has to be renderable as a 404 by an HTTP adapter and as an
// invalid-argument refusal by the tool adapter, and a domain that spoke
// either dialect would force the other one to translate backwards.
// Identical to the Job Radar and Threads vocabularies, because it is the
// same two facts.
type Kind string

const (
	// KindInvalid: the request was understood and is not allowed. An
	// empty name, an unknown kind, an importance of nine, a relation the
	// matrix does not have.
	KindInvalid Kind = "invalid"
	// KindNotFound: no such row in this workspace. Note what it does NOT
	// distinguish: "does not exist anywhere" and "exists in a workspace
	// that is not yours" are the same answer on purpose, because telling
	// them apart would confirm the existence of another workspace's
	// record.
	KindNotFound Kind = "not_found"
)

// ══════════════════════════════════════════════════════════════════════
//
//	THE RULE EVERY MESSAGE IN THIS PACKAGE FOLLOWS
//
// ══════════════════════════════════════════════════════════════════════
//
// An error message here may name: a field, a limit, a closed vocabulary,
// an id, a count. It may NOT quote stored content: no memory text, no
// artifact body, no summary, no source transcript, no room description.
//
// ── Why this is a rule and not a habit ─────────────────────────────────
// Because every failure this package produces is copied twice on its way
// out. It reaches the model as the body of a `tool` message, and it is
// written to `chat.tool_calls.error_message`, which is NOT covered by the
// redaction that `ToolDefinition.Confidential` performs: that flag drops
// `arguments` and `result`, and leaves the error text in the clear. So a
// message that interpolated a memory's content would persist that content
// in the audit trail of a capability specifically marked as one whose
// payload must not be kept.
//
//	Invalid("content is longer than %d characters", MaxMemoryContent)   yes
//	Invalid("content %q is too long", m.Content)                        never
//
// The one echo that IS allowed is a rejected ENUM TOKEN, bounded by
// quoteToken, because "unknown kind" without saying which word was
// rejected teaches the model nothing and it will send the same word
// again. See maxEchoedToken.
//
// privacy_test.go enforces this by driving every validation failure with
// a canary in every free-text field and asserting the canary never
// appears.

type Error struct {
	Kind    Kind
	Message string
}

func (e *Error) Error() string { return string(e.Kind) + ": " + e.Message }

func Invalid(format string, a ...any) *Error {
	return &Error{Kind: KindInvalid, Message: fmt.Sprintf(format, a...)}
}

func NotFound(format string, a ...any) *Error {
	return &Error{Kind: KindNotFound, Message: fmt.Sprintf(format, a...)}
}
