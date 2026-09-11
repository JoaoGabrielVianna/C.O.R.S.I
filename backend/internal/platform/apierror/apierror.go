// Package apierror is the single source of truth for HTTP error responses.
// All handlers should map errors through Write so the wire shape stays uniform.
package apierror

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// Error is the JSON body shape returned for any non-2xx response.
type Error struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

var (
	ErrUnauthorized = New(http.StatusUnauthorized, "unauthorized", "authentication required")
	ErrForbidden    = New(http.StatusForbidden, "forbidden", "access denied")
	ErrInternal     = New(http.StatusInternalServerError, "internal", "internal server error")
)

// Write encodes err as JSON. If err is not already an *Error, it is logged
// and converted to a generic 500 so internal details never leak.
func Write(w http.ResponseWriter, log *slog.Logger, err error) {
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		log.Error("unhandled error", "err", err)
		apiErr = ErrInternal
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(apiErr.Status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": apiErr})
}
