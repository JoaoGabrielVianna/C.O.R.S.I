package domain

import (
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Provider is a configured OpenAI-compatible LLM endpoint.
//
// APIKeyCipher is the sealed token and is json:"-" — it must never reach a
// response body. APIKeyHint is the display-safe remnant ("...a3f9") that
// lets the UI confirm which key is stored. The plaintext token exists only
// inside a request that is setting it, and inside the LLM adapter that is
// spending it; it is never a field on this struct.
type Provider struct {
	ID           uuid.UUID  `json:"id"`
	WorkspaceID  uuid.UUID  `json:"workspace_id"`
	Name         string     `json:"name"`
	BaseURL      string     `json:"base_url"`
	APIKeyCipher []byte     `json:"-"`
	APIKeyHint   string     `json:"api_key_hint"`
	DefaultModel string     `json:"default_model"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	DeletedAt    *time.Time `json:"deleted_at,omitempty"`
}

func (p *Provider) Validate() error {
	if p.WorkspaceID == uuid.Nil {
		return Invalid("workspace_id required")
	}
	if n := strings.TrimSpace(p.Name); n == "" || len(n) > 80 {
		return Invalid("name must be 1..80 chars")
	}
	if err := validateBaseURL(p.BaseURL); err != nil {
		return err
	}
	if len(p.APIKeyCipher) == 0 {
		return Invalid("api_key required")
	}
	if m := strings.TrimSpace(p.DefaultModel); m == "" || len(m) > 120 {
		return Invalid("default_model must be 1..120 chars")
	}
	return nil
}

// NormalizeBaseURL trims trailing slashes so the LLM adapter can append
// "/chat/completions" without producing a double slash. LiteLLM tolerates
// it; some proxies in front of it do not.
func NormalizeBaseURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func validateBaseURL(raw string) error {
	s := strings.TrimSpace(raw)
	if s == "" || len(s) > 500 {
		return Invalid("base_url must be 1..500 chars")
	}
	u, err := url.Parse(s)
	if err != nil {
		return Invalid("base_url is not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return Invalid("base_url must start with http:// or https://")
	}
	if u.Host == "" {
		return Invalid("base_url must include a host")
	}
	return nil
}
