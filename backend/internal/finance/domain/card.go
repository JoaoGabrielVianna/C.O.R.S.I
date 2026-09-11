package domain

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Card struct {
	ID          uuid.UUID   `json:"id"`
	WorkspaceID uuid.UUID   `json:"workspace_id"`
	Name        string      `json:"name"`
	Institution string      `json:"institution"`
	Network     CardNetwork `json:"network"`
	Variant     string      `json:"variant"`
	Last4       string      `json:"last4"`
	LimitCents  int64       `json:"limit_cents"`
	ClosingDay  int         `json:"closing_day"`
	DueDay      int         `json:"due_day"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
	DeletedAt   *time.Time  `json:"deleted_at,omitempty"`
}

var last4Re = regexp.MustCompile(`^[0-9]{4}$`)

func (c *Card) Validate() error {
	if c.WorkspaceID == uuid.Nil {
		return Invalid("workspace_id required")
	}
	if n := strings.TrimSpace(c.Name); n == "" || len(n) > 80 {
		return Invalid("name must be 1..80 chars")
	}
	if i := strings.TrimSpace(c.Institution); i == "" || len(i) > 80 {
		return Invalid("institution must be 1..80 chars")
	}
	if !c.Network.Valid() {
		return Invalid("network must be visa, mastercard, elo, or amex")
	}
	if len(c.Variant) > 40 {
		return Invalid("variant must be <= 40 chars")
	}
	if !last4Re.MatchString(c.Last4) {
		return Invalid("last4 must be exactly 4 digits")
	}
	if c.LimitCents < 0 {
		return Invalid("limit_cents must be >= 0")
	}
	if c.ClosingDay < 1 || c.ClosingDay > 28 {
		return Invalid("closing_day must be 1..28")
	}
	if c.DueDay < 1 || c.DueDay > 28 {
		return Invalid("due_day must be 1..28")
	}
	return nil
}
