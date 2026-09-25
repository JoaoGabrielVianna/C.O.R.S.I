package domain

import "fmt"

// Kind is the vocabulary of failure this context reports.
//
// Same three as every other bounded context here, and deliberately not HTTP
// status codes: the HTTP adapter renders a 404, and anything else that ever
// drives this module renders whatever it renders. A domain that spoke one
// adapter's dialect would force the other to translate backwards.
type Kind string

const (
	// KindInvalid: the request was understood and is not allowed. An unknown
	// category, a view a watch cannot have, a file that is not a PNG.
	KindInvalid Kind = "invalid"
	// KindNotFound: no such row in this workspace. "Does not exist anywhere"
	// and "exists in a workspace that is not yours" are the same answer on
	// purpose — telling them apart would confirm another workspace's row.
	KindNotFound Kind = "not_found"
	// KindConflict: the request contradicts a state the row is already in.
	// Putting an archived garment into a new look is the one that matters
	// here.
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
