package app

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// Cursor is the opaque pagination position for transactions. It encodes
// the last row's (occurred_at, id) tuple so the next page can be fetched
// with `(occurred_at, id) < cursor` against the existing partial index
// `(workspace_id, occurred_at DESC, id) WHERE deleted_at IS NULL`.
//
// Wire form: base64(json{"o":"<rfc3339>","i":"<uuid>"}). Opaque to clients.
//
// Lives in the app layer (not in repo) so the codec sits on the right
// side of the hexagonal boundary: the HTTP handler and the service both
// import app; the repo only sees the typed tuple via TransactionFilter.
type Cursor struct {
	OccurredAt time.Time `json:"o"`
	ID         uuid.UUID `json:"i"`
}

// EncodeCursor produces the opaque string that becomes ?cursor=… on the wire.
func EncodeCursor(c Cursor) string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeCursor parses a wire cursor. Returns an error for empty input,
// malformed base64, or invalid JSON / fields. Callers should surface the
// error as 400 to the client.
func DecodeCursor(s string) (Cursor, error) {
	if s == "" {
		return Cursor{}, errors.New("empty cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, errors.New("cursor: invalid base64")
	}
	var c Cursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return Cursor{}, errors.New("cursor: invalid json")
	}
	if c.OccurredAt.IsZero() || c.ID == uuid.Nil {
		return Cursor{}, errors.New("cursor: missing fields")
	}
	return c, nil
}
