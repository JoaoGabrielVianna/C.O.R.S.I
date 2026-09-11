package domain

import (
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Category struct {
	ID          uuid.UUID  `json:"id"`
	WorkspaceID uuid.UUID  `json:"workspace_id"`
	Name        string     `json:"name"`
	Type        EntryType  `json:"type"`
	Color       string     `json:"color"`
	Icon        string     `json:"icon"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

var hexColorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func (c *Category) Validate() error {
	if c.WorkspaceID == uuid.Nil {
		return Invalid("workspace_id required")
	}
	if n := strings.TrimSpace(c.Name); n == "" || len(n) > 80 {
		return Invalid("name must be 1..80 chars")
	}
	if !c.Type.Valid() {
		return Invalid("type must be income or expense")
	}
	if !hexColorRe.MatchString(c.Color) {
		return Invalid("color must match #rrggbb")
	}
	if i := strings.TrimSpace(c.Icon); i == "" || len(i) > 40 {
		return Invalid("icon must be 1..40 chars")
	}
	return nil
}
