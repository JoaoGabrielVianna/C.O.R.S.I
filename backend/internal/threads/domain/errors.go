package domain

import "fmt"

// Kind is the vocabulary of failure this context reports.
//
// Deliberately small and deliberately NOT http status codes: the same fact
// has to be renderable as a 404 by an HTTP adapter and as an invalid-
// argument refusal by the tool adapter, and a domain that spoke either
// dialect would force the other one to translate backwards. Identical to
// the Job Radar vocabulary, because it is the same three facts.
type Kind string

const (
	// KindInvalid: the request was understood and is not allowed. An empty
	// title, an unknown status, an update that changes nothing.
	KindInvalid Kind = "invalid"
	// KindNotFound: no such row in this workspace. Note what it does NOT
	// distinguish — "does not exist anywhere" and "exists in a workspace
	// that is not yours" are the same answer on purpose, because telling
	// them apart would confirm the existence of another workspace's record.
	KindNotFound Kind = "not_found"
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
