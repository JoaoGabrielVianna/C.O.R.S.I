// Package render holds the small JSON read/write helpers shared by HTTP
// adapters. Keeps handlers free of repetitive encoding boilerplate.
package render

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const maxBodyBytes = 1 << 20 // 1 MiB

// DecodeJSON reads the request body as JSON into dst. Returns a user-readable
// error suitable for surfacing as a 400.
func DecodeJSON(r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var syn *json.SyntaxError
		switch {
		case errors.As(err, &syn):
			return fmt.Errorf("malformed json at byte %d", syn.Offset)
		case errors.Is(err, io.EOF):
			return errors.New("empty body")
		default:
			return err
		}
	}
	if dec.More() {
		return errors.New("body must contain exactly one json object")
	}
	return nil
}

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}
