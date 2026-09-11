package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

type Person struct {
	ID          uuid.UUID  `json:"id"`
	WorkspaceID uuid.UUID  `json:"workspace_id"`
	Name        string     `json:"name"`
	Notes       string     `json:"notes"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

func (p *Person) Validate() error {
	if p.WorkspaceID == uuid.Nil {
		return Invalid("workspace_id required")
	}
	if n := strings.TrimSpace(p.Name); n == "" || len(n) > 80 {
		return Invalid("name must be 1..80 chars")
	}
	if len(p.Notes) > 1000 {
		return Invalid("notes must be <= 1000 chars")
	}
	return nil
}
