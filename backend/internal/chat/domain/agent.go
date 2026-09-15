package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// Agent is a named, reusable configuration of one model.
//
// Conversations reference an agent instead of copying its settings, so
// editing the system prompt changes every future turn while leaving the
// recorded history untouched.
type Agent struct {
	ID           uuid.UUID `json:"id"`
	WorkspaceID  uuid.UUID `json:"workspace_id"`
	ProviderID   uuid.UUID `json:"provider_id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	SystemPrompt string    `json:"system_prompt"`
	Model        string    `json:"model"`
	// Temperature is the operator's explicit sampling preference, or nil
	// when they expressed none.
	//
	// ── Why it is optional and must stay optional ──────────────────────
	// Because the platform cannot know what a model accepts, and a default
	// it invents is a guess that is wrong somewhere. Measured against the
	// real gateway: claude-opus-4-7 rejects 0.0, 0.5 and 0.7 outright and
	// accepts only 1, while claude-haiku-4-5 accepts all four. The
	// constraint is per-model and VALUE-EXACT, not a range.
	//
	// So nil is not "zero" and not "unknown": it is the operator having no
	// preference, which means nothing is put on the wire and the provider
	// applies its own default — valid by construction for every model,
	// including ones that do not exist yet.
	//
	// An explicit value is honoured exactly as given, including one the
	// model will refuse. That refusal is correct: it is what was asked for,
	// and the gateway names the problem precisely.
	Temperature  *float32 `json:"temperature"`
	MaxTokens    int      `json:"max_tokens"`
	HistoryLimit int      `json:"history_limit"`
	Accent       string   `json:"accent"`
	// Budget is what this agent may consume in a UTC day. Both limits are
	// optional and independent; an agent with neither behaves exactly as it
	// did before budgets existed. See domain/budget.go.
	Budget Budget `json:"budget"`
	// MemoryPolicy is whether this agent may be asked to propose memories,
	// and the user's addendum to the rules such a proposal follows. It never
	// reaches an ordinary turn. See domain/memory_policy.go.
	MemoryPolicy MemoryPolicy `json:"memory_policy"`
	CreatedAt    time.Time    `json:"created_at"`
	UpdatedAt    time.Time    `json:"updated_at"`
	DeletedAt    *time.Time   `json:"deleted_at,omitempty"`
}

// Defaults applied when a create request omits the tuning knobs. They sit
// here rather than in the HTTP layer so every entry point agrees.
// There is deliberately NO DefaultTemperature.
//
// There used to be one, at 0.7, and it is the whole reason this field is
// now optional: it guaranteed a 502 on the first turn of any agent created
// on claude-opus-*. A default here would be this package asserting a fact
// about models it has never heard of. See Agent.Temperature.
const (
	DefaultMaxTokens    = 4096
	DefaultHistoryLimit = 40
	DefaultAccent       = "cyan"
)

func (a *Agent) Validate() error {
	if a.WorkspaceID == uuid.Nil {
		return Invalid("workspace_id required")
	}
	if a.ProviderID == uuid.Nil {
		return Invalid("provider_id required")
	}
	if n := strings.TrimSpace(a.Name); n == "" || len(n) > 80 {
		return Invalid("name must be 1..80 chars")
	}
	if len(a.Description) > 280 {
		return Invalid("description must be <= 280 chars")
	}
	if len(a.SystemPrompt) > 20000 {
		return Invalid("system_prompt must be <= 20000 chars")
	}
	if m := strings.TrimSpace(a.Model); m == "" || len(m) > 120 {
		return Invalid("model must be 1..120 chars")
	}
	// A sanity bound on an explicit value, and NOT a compatibility claim:
	// a number inside this range is still refused by some models. What is
	// valid is the provider's to say, and it says so at call time.
	if a.Temperature != nil && (*a.Temperature < 0 || *a.Temperature > 2) {
		return Invalid("temperature must be 0..2")
	}
	if a.MaxTokens < 1 || a.MaxTokens > 200000 {
		return Invalid("max_tokens must be 1..200000")
	}
	if a.HistoryLimit < 1 || a.HistoryLimit > 200 {
		return Invalid("history_limit must be 1..200")
	}
	if len(a.Accent) > 20 {
		return Invalid("accent must be <= 20 chars")
	}
	if err := a.Budget.Validate(); err != nil {
		return err
	}
	return a.MemoryPolicy.Validate()
}
