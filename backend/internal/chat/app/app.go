// Package app holds application services — the orchestrators that drive
// domain logic and coordinate outbound ports inside a unit-of-work.
//
// Services depend on ports (interfaces) only, not adapters. They are the
// only layer allowed to open a transaction via TxManager.WithinTx; domain
// objects know nothing about transactions, HTTP, or SQL.
//
// This service is also the only place a provider API key exists in
// plaintext, and only for the duration of one outbound call: it is unsealed
// from storage, handed to the LLM port, and never returned upward.
package app

import (
	"log/slog"

	"github.com/corsi/backend/internal/chat/adapters/repo"
	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/platform/postgres"
	"github.com/corsi/backend/internal/platform/secrets"
)

type Service struct {
	repos  *repo.Repositories
	txm    *postgres.TxManager
	llm    ports.LLM
	sealer *secrets.Sealer
	// tools is the catalogue of capabilities that exist in this build. It is
	// read-only and composed at start-up; see adapters/tools.
	tools ports.ToolRegistry
	// references resolves the ENTITIES a conversation is about. A different
	// registry from `tools` and deliberately so: that one answers "what may
	// this agent do", this one answers "what is this thread about". See
	// domain/context_reference.go on why the two must not merge.
	//
	// Nil is a build that can resolve no subjects; attaching one is then
	// refused rather than stored unresolved.
	references ports.ContextReferenceRegistry
	// promptCache says whether a turn asks the provider to cache the stable
	// head of its prompt. See WithPromptCache.
	promptCache bool
	log         *slog.Logger
}

// NewService wires the application layer.
//
// `tools` may be nil, and a nil registry means a deployment with no
// capabilities at all rather than a deployment that crashes on the first
// turn. Every existing caller that predates tools gets exactly that, which
// is also exactly what it had.
func NewService(repos *repo.Repositories, txm *postgres.TxManager, llm ports.LLM, sealer *secrets.Sealer, tools ports.ToolRegistry, references ports.ContextReferenceRegistry, log *slog.Logger, opts ...ServiceOption) *Service {
	if tools == nil {
		tools = emptyRegistry{}
	}
	// `references` is left nil rather than swapped for an empty stand-in.
	// A nil registry and one that resolves nothing are the same behaviour
	// here — both refuse every attachment — and admitContextReferences says
	// so in one place, so a second empty type would only be a second thing
	// to keep in step.
	s := &Service{repos: repos, txm: txm, llm: llm, sealer: sealer,
		tools: tools, references: references, log: log,
		// Default ON, on measured evidence rather than optimism: X3 drove a
		// real request through this deployment's own gateway and observed
		// 18.387 of 18.829 prompt tokens served from cache on the repeat.
		promptCache: true,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// ServiceOption tunes the service at construction.
//
// Variadic on purpose: every caller that predates an option keeps working
// unchanged, which is what lets a deployment switch arrive without touching
// a dozen test harnesses.
type ServiceOption func(*Service)

// WithPromptCache turns provider-side prompt caching on or off.
//
// ── Why a switch exists for something that is on by default ────────────
// Two reasons, and neither is doubt about the evidence.
//
// It is a kill switch on an EXTERNAL boundary. Caching changes the shape of
// the request body sent to a gateway this project does not control, and a
// gateway that regresses should cost an operator one environment variable,
// not a deploy.
//
// And it is the only way to measure. A before/after on the same workload
// needs both halves runnable from the same binary; without a switch, the
// "before" number would have to come from a different build, and comparing
// two builds is how a saving gets claimed that was really a workload
// difference.
//
// Off is byte-identical to the pre-caching request: no marker, no content
// blocks, no `cache_control` anywhere. See
// TestUnmarkedContentIsAByteIdenticalString.
func WithPromptCache(enabled bool) ServiceOption {
	return func(s *Service) { s.promptCache = enabled }
}

// credentialsFor unseals a provider's stored key for one outbound call.
//
// Every failure mode here is a deployment problem rather than a user
// mistake, so each gets its own message: an operator who rotated
// SECRETS_KEY should be told exactly that, not handed a generic 500.
func (s *Service) credentialsFor(p *domain.Provider) (ports.Credentials, error) {
	if !s.sealer.Enabled() {
		return ports.Credentials{}, domain.NotConfigured(
			"SECRETS_KEY is not set on the server, so stored provider keys cannot be read")
	}
	key, err := s.sealer.Open(p.APIKeyCipher)
	if err != nil {
		s.log.Error("unseal provider key", "provider_id", p.ID, "err", err)
		return ports.Credentials{}, domain.NotConfigured(
			"the stored API key for this provider could not be decrypted — SECRETS_KEY may have changed since it was saved; re-enter the key")
	}
	return ports.Credentials{BaseURL: p.BaseURL, APIKey: key}, nil
}

// requireSealer guards the write paths that need to *store* a key.
func (s *Service) requireSealer() error {
	if !s.sealer.Enabled() {
		return domain.NotConfigured(
			"SECRETS_KEY is not set on the server; generate one with `openssl rand -base64 32` and restart the API before storing provider credentials")
	}
	return nil
}

// clampList keeps list bounds sane regardless of what the caller passed.
func clampList(limit, offset int) ports.ListFilter {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return ports.ListFilter{Limit: limit, Offset: offset}
}
