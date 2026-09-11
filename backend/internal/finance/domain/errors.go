package domain

import "errors"

type ErrorKind string

const (
	KindInvalid  ErrorKind = "invalid"
	KindNotFound ErrorKind = "not_found"
	KindConflict ErrorKind = "conflict"
)

type Error struct {
	Kind    ErrorKind
	Message string
}

func (e *Error) Error() string { return e.Message }

func Invalid(msg string) *Error   { return &Error{Kind: KindInvalid, Message: msg} }
func NotFound(what string) *Error { return &Error{Kind: KindNotFound, Message: what + " not found"} }
func Conflict(msg string) *Error  { return &Error{Kind: KindConflict, Message: msg} }

// IsKind reports whether err is a domain error of the given kind.
// Use this at the HTTP boundary to map domain errors to status codes.
func IsKind(err error, k ErrorKind) bool {
	var de *Error
	if errors.As(err, &de) {
		return de.Kind == k
	}
	return false
}
