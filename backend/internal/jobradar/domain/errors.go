package domain

import "fmt"

// Kind is the vocabulary of failure this context reports.
//
// It is deliberately small and deliberately NOT http status codes: the same
// fact has to be renderable as a 404 by the HTTP adapter and as
// `tool_not_found` by the tool adapter, and a domain that spoke either
// dialect would force the other one to translate backwards.
type Kind string

const (
	// KindInvalid: the request was understood and is not allowed. A bad
	// stage name, an empty role, a match percent of 300.
	KindInvalid Kind = "invalid"
	// KindNotFound: no such row in this workspace. Note what it does NOT
	// distinguish — "does not exist anywhere" and "exists in a workspace
	// that is not yours" are the same answer on purpose, because telling
	// them apart would confirm the existence of another workspace's record.
	KindNotFound Kind = "not_found"
	// KindConflict: the request contradicts a state the row is already in.
	KindConflict Kind = "conflict"
)

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

func Conflict(format string, a ...any) *Error {
	return &Error{Kind: KindConflict, Message: fmt.Sprintf(format, a...)}
}
