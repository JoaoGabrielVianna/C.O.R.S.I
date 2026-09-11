// Package config loads runtime configuration from environment variables.
//
// Twelve-factor style: no file-based config, only env. Defaults live next
// to each field so the binary boots with sane behavior when a value is
// missing (except for explicitly required secrets, which fail fast).
//
// Each platform sub-package owns its own typed Config struct; this package
// composes them so the env parser populates the whole tree in one pass.
//
// Auth deliberately lives outside this repo (see README): there is no
// AUTH_* configuration here.
package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"

	"github.com/corsi/backend/internal/platform/logger"
	"github.com/corsi/backend/internal/platform/observability"
	"github.com/corsi/backend/internal/platform/postgres"
	"github.com/corsi/backend/internal/platform/secrets"
	"github.com/corsi/backend/internal/platform/workspace"
)

type Config struct {
	HTTP          HTTP
	Postgres      postgres.Config
	Log           logger.Config
	Observability observability.Config
	Workspace     workspace.Config
	Secrets       secrets.Config
	Modules       Modules
}

type HTTP struct {
	Addr string `env:"HTTP_ADDR" envDefault:":8080"`
}

// Modules carries the few runtime switches that belong to a bounded context
// rather than to a platform capability.
//
// It is a struct of plain scalars, declared here, and that is the point:
// this package must never import a module. `Platform → Module` is a
// forbidden direction, so what crosses is a bool that the composition root
// hands over, not a type the module owns.
type Modules struct {
	// ChatInternalTools admits the Agents module's built-in diagnostic
	// tools into its registry.
	//
	// Default false, which is the production answer. `system.echo` returns
	// the text it was given: it proves the tool machinery end to end and is
	// useless to a user, so a deployment that configures nothing must not
	// offer it as a capability. Set it to true for local development and in
	// the test harness, where exercising the machinery is the point.
	//
	// Turning it off does not hide a tool in the interface — it removes it
	// from the catalogue, which is what makes every existing DENY rule
	// cover the case without being told about it. See
	// internal/chat/adapters/tools.Options.
	ChatInternalTools bool `env:"CHAT_ENABLE_INTERNAL_TOOLS" envDefault:"false"`

	// ChatDisablePromptCache switches provider-side prompt caching OFF.
	//
	// ── Why the variable is an opt-OUT ─────────────────────────────────
	// Caching is on by default because the evidence says it should be: a
	// real request through this deployment's own gateway returned 18.387 of
	// 18.829 prompt tokens as a cache read, and the tool catalogue — half
	// of every input token this system sends — sits inside the cached
	// prefix. A default that had to be switched on would mean paying full
	// price until somebody remembered.
	//
	// It exists because caching changes the shape of the body sent to an
	// external gateway. If that gateway regresses, an operator should be one
	// environment variable away from the old body, not one deploy.
	//
	// Turning it off is not a downgrade in what the model receives. The
	// same characters arrive in the same order either way; only the billing
	// of the prefix changes.
	ChatDisablePromptCache bool `env:"CHAT_DISABLE_PROMPT_CACHE" envDefault:"false"`

	// The deployment's Meta Threads app, from the Meta developer console.
	//
	// ── Why these are configuration and not a stored row ───────────────
	// They identify THIS DEPLOYMENT to Meta, not a workspace to us: the
	// same three values serve every workspace on the installation, they are
	// issued by a console a person logs into, and they are rotated by
	// editing an environment. A user's own token is the opposite of all
	// three, and it lives sealed in meta_threads.connections.
	//
	// All three empty is a valid, running deployment: the tools are still
	// registered, and every attempt to connect refuses with an actionable
	// 503 rather than the module quietly not existing. A capability a
	// person cannot see is one they cannot ask for.
	//
	// The SECRET is read here and never leaves the integration's
	// application layer. It is not logged, not rendered and not returned.
	MetaThreadsAppID       string `env:"META_THREADS_APP_ID"`
	MetaThreadsAppSecret   string `env:"META_THREADS_APP_SECRET"`
	MetaThreadsRedirectURI string `env:"META_THREADS_REDIRECT_URI"`

	// FinanceTimezone is the zone in which a finance REPORTING PERIOD is
	// cut: which instant "hoje" starts at, where "este mês" begins and
	// ends.
	//
	// ── Why this has to be configuration and cannot be inferred ────────
	// The screens compute their windows in the BROWSER's zone, so a month
	// has always meant the operator's local month. A conversation has no
	// browser. Leaving it to the server's clock would make "quanto gastei
	// hoje" answer about a different day for the three hours a night when
	// UTC has already rolled over and São Paulo has not — silently, and
	// with a number that looks perfectly plausible.
	//
	// Asking the MODEL for the boundaries is not an option either: nothing
	// in a turn tells it today's date, so it would produce a confident
	// guess. The tools resolve every period here instead, and report the
	// window they used.
	//
	// The default is the operator's zone rather than UTC because a wrong
	// default that looks right is worse than an obvious one, and every
	// existing row in this database was cut in it.
	FinanceTimezone string `env:"FINANCE_TIMEZONE" envDefault:"America/Sao_Paulo"`
}

func Load() (Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return Config{}, fmt.Errorf("load config: %w", err)
	}
	return c, nil
}
