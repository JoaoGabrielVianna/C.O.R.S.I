package domain

import "errors"

// The error vocabulary mirrors finance and chat rather than inventing a
// third one: the HTTP adapters in this codebase all translate the same
// three kinds, and a context with its own taxonomy would need its own
// translation table for no gain.

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

func IsKind(err error, k ErrorKind) bool {
	var de *Error
	if errors.As(err, &de) {
		return de.Kind == k
	}
	return false
}
