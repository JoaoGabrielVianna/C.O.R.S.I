package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/swaggest/swgui/v5emb"

	"github.com/corsi/backend/api/openapi"
	"github.com/corsi/backend/internal/chat"
	"github.com/corsi/backend/internal/finance"
	githubint "github.com/corsi/backend/internal/integrations/github"
	"github.com/corsi/backend/internal/integrations/metathreads"
	metathreadsapp "github.com/corsi/backend/internal/integrations/metathreads/app"
	"github.com/corsi/backend/internal/jobradar"
	"github.com/corsi/backend/internal/platform/config"
	"github.com/corsi/backend/internal/platform/health"
	"github.com/corsi/backend/internal/platform/httpserver"
	"github.com/corsi/backend/internal/platform/logger"
	"github.com/corsi/backend/internal/platform/metrics"
	"github.com/corsi/backend/internal/platform/observability"
	"github.com/corsi/backend/internal/platform/postgres"
	"github.com/corsi/backend/internal/platform/preflight"
	"github.com/corsi/backend/internal/platform/secrets"
	"github.com/corsi/backend/internal/platform/workspace"
	"github.com/corsi/backend/internal/releases"
	"github.com/corsi/backend/internal/threads"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	log := logger.New(cfg.Log)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	shutdownObs, err := observability.Setup(ctx, cfg.Observability)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownObs(shutdownCtx)
	}()

	pool, err := postgres.Open(ctx, cfg.Postgres)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Metrics first so its middleware can wrap every route mounted below.
	reg := metrics.New()
	if err := reg.RegisterDBCollector(pool); err != nil {
		return err
	}

	router := httpserver.NewRouter(log)
	// HTTP metrics must be registered before any routes are mounted so chi
	// runs it for every handler. The middleware skips /metrics itself.
	router.Use(reg.HTTPMiddleware())

	router.Mount("/health", health.Handler(pool))
	router.Get("/openapi.yaml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		_, _ = w.Write(openapi.FinanceSpec)
	})
	router.Mount("/swagger", v5emb.New("C.O.R.S.I Finance API", "/openapi.yaml", "/swagger/"))
	router.Mount("/metrics", reg.Handler())

	// One middleware instance shared by every module: the workspace rules
	// and their counters must be identical across bounded contexts.
	wsMiddleware := workspace.Middleware(cfg.Workspace, reg.Workspace())

	// A blank SECRETS_KEY yields a disabled sealer rather than an error, so
	// the API still boots for anyone who has not configured chat yet. The
	// chat module turns that into a clear 503 the moment someone tries to
	// store a provider credential.
	sealer, err := secrets.New(cfg.Secrets)
	if err != nil {
		return err
	}
	if !sealer.Enabled() {
		log.Warn("SECRETS_KEY not set — chat provider credentials cannot be stored or read")
	}

	// The zone finance periods are cut in, resolved once at boot.
	//
	// A bad name fails the boot rather than falling back, and that is the
	// point: the fallback would be UTC, every window would be three hours
	// off, and every figure the agent reported would be plausible and
	// wrong. A deployment that cannot name its own timezone should not
	// serve financial answers.
	financeLoc, err := time.LoadLocation(cfg.Modules.FinanceTimezone)
	if err != nil {
		return fmt.Errorf("FINANCE_TIMEZONE %q is not a known timezone: %w", cfg.Modules.FinanceTimezone, err)
	}

	financeMod := finance.New(finance.Deps{
		Pool:                pool,
		Logger:              log,
		WorkspaceMiddleware: wsMiddleware,
		ReportingTimezone:   financeLoc,
	})
	financeMod.Register(router)

	// ── The composition root's one privilege ────────────────────────
	//
	// This is the only file allowed to know both Agents and GitHub. The
	// integration is constructed first, its capabilities are collected, and
	// they are handed to the chat module as values satisfying an interface
	// the chat module declared. Neither package imports the other, and the
	// arrow that would make them a cycle exists only here, as a variable.
	githubMod := githubint.New(githubint.Deps{
		Pool:                pool,
		Logger:              log,
		Sealer:              sealer,
		WorkspaceMiddleware: wsMiddleware,
	})
	githubMod.Register(router)

	// The tools are registered unconditionally, and that is deliberate.
	// Their availability is not a deployment switch: an agent still needs a
	// grant, and a grant still needs a connection and an authorized
	// repository behind it. A workspace with no GitHub connection sees the
	// tools in the catalogue and every call refuses with an actionable
	// sentence — which is a truer description of the system than hiding
	// them, and it is the same choice the registry makes everywhere else:
	// a capability the user cannot see is one they cannot revoke.
	//
	// InternalTools is off unless a deployment asks for it, so the
	// catalogue a production instance builds contains these seven and no
	// diagnostic pretending to be a feature.
	if cfg.Modules.ChatInternalTools {
		log.Warn("CHAT_ENABLE_INTERNAL_TOOLS is on — the agent tool catalogue includes diagnostics that are not product capabilities")
	}
	// Job Radar. A MODULE rather than an integration: it owns its own
	// entities and its own schema, and it talks to no external system. The
	// composition root treats it exactly like GitHub for the one purpose
	// they share — both hand over values satisfying the Agents module's
	// ports.Tool, and neither is imported by Agents.
	//
	// Its tools include a WRITE (job_radar.opportunity.move), which is the
	// first one in this system. Nothing about the wiring changes for that:
	// the effect is declared in the tool's own definition, the interface
	// shows it, and an agent still cannot run it without a grant.
	jobRadarMod := jobradar.New(jobradar.Deps{
		Pool:                pool,
		Logger:              log,
		WorkspaceMiddleware: wsMiddleware,
	})
	jobRadarMod.Register(router)

	// Meta Threads. An INTEGRATION, like GitHub: it owns a credential and a
	// protocol for one external system and no business rule at all.
	//
	// ── The name, and the confusion it is arranged to prevent ───────
	// There are two unrelated things here with "threads" in the name, and
	// this is the only file that constructs both:
	//
	//	threadsMod       C.O.R.S.I. Threads. A MODULE. Our ideas and
	//	                 drafts, our Postgres, tools threads.thread.*
	//	metaThreadsMod   Meta's social network. An INTEGRATION. Their
	//	                 posts, read-only, tools meta_threads.*
	//
	// Neither package imports the other, and neither can: the module is
	// internal state we write, the integration is external evidence we
	// read. An agent granted both is what makes them comparable, and that
	// agent is the only place they meet.
	//
	// Every capability it offers is a READ. Publishing to Meta is not
	// implemented and cannot be reached: the port declares no write verb
	// and the OAuth scopes this integration can request contain none.
	//
	// Registered unconditionally even when no Meta app is configured, for
	// the reason GitHub gives: an agent still needs a grant, a grant still
	// needs a connection behind it, and a workspace with none sees the
	// capabilities in the catalogue and gets an actionable refusal.
	metaThreadsMod := metathreads.New(metathreads.Deps{
		Pool:                pool,
		Logger:              log,
		Sealer:              sealer,
		WorkspaceMiddleware: wsMiddleware,
		App: metathreadsapp.AppConfig{
			AppID:       cfg.Modules.MetaThreadsAppID,
			AppSecret:   cfg.Modules.MetaThreadsAppSecret,
			RedirectURI: cfg.Modules.MetaThreadsRedirectURI,
		},
	})
	metaThreadsMod.Register(router)
	if !metaThreadsMod.Configured() {
		log.Warn("META_THREADS_APP_ID/SECRET/REDIRECT_URI not set — the Meta Threads capabilities are registered and no workspace can connect an account until they are")
	}

	// Threads. A MODULE, like Job Radar: it owns its own entities and its
	// own schema, and it talks to no external system.
	//
	// It is the first module wired here with NO Register call, and that is
	// a decision rather than an omission: Threads ships no screen in this
	// sprint, so it has no HTTP routes, and a route with no caller is a
	// surface nobody is testing. Its entire interface is a conversation
	// with an agent that holds the grants — which reaches the same
	// application service, through the same seam, as any route would.
	threadsMod := threads.New(threads.Deps{
		Pool:   pool,
		Logger: log,
	})

	// The catalogue is the concatenation of what every capability owner
	// offers. Order is irrelevant — the registry sorts by name — and a
	// duplicate name across two owners refuses the whole registry at
	// start-up rather than silently letting one shadow the other.
	agentTools := append(githubMod.Tools(), jobRadarMod.Tools()...)
	agentTools = append(agentTools, threadsMod.Tools()...)
	agentTools = append(agentTools, metaThreadsMod.Tools()...)
	// Finance. The module was already constructed above, for its screens;
	// this line is the whole of what making it operable by conversation
	// costs at the composition root. Finance does not import Agents, Agents
	// does not import Finance, and the tools reach the same application
	// service the HTTP handlers do.
	agentTools = append(agentTools, financeMod.Tools()...)

	// The other half of the same arrangement: who can say what an entity is
	// CALLED, so a conversation can be about it. Two providers now — Job
	// Radar and Threads — and adding the second cost exactly this line:
	// the resolver, the freshness policy and the hydrator all arrive
	// through interfaces Agents already declared. GitHub would add a
	// repository resolver here and nowhere else. Agents still names none of
	// them.
	referenceResolvers := append(jobRadarMod.ReferenceResolvers(), threadsMod.ReferenceResolvers()...)
	referenceResolvers = append(referenceResolvers, financeMod.ReferenceResolvers()...)

	chatMod := chat.New(chat.Deps{
		Pool:                pool,
		Logger:              log,
		Sealer:              sealer,
		WorkspaceMiddleware: wsMiddleware,
		InternalTools:       cfg.Modules.ChatInternalTools,
		Tools:               agentTools,
		ReferenceResolvers:  referenceResolvers,
		// The kill switch on an external boundary. Default off, so the
		// default behaviour is to cache — see config.Modules.
		DisablePromptCache: cfg.Modules.ChatDisablePromptCache,
	})
	chatMod.Register(router)

	// Release history. It takes no sealer and no tools: it stores what the
	// other modules shipped, and knows their names only as rows in its own
	// tables.
	releasesMod := releases.New(releases.Deps{
		Pool:                pool,
		Logger:              log,
		WorkspaceMiddleware: wsMiddleware,
	})
	releasesMod.Register(router)

	// After every module has mounted, and it has to be after: chi copies
	// these handlers into the sub-routers that exist when they are set, and
	// each module is a sub-router. See UseErrorEnvelope.
	httpserver.UseErrorEnvelope(router, log)

	// Count registered routes once after wiring.
	var routes int
	_ = chi.Walk(router, func(string, string, http.Handler, ...func(http.Handler) http.Handler) error {
		routes++
		return nil
	})
	reg.SetRouteCount(routes)

	// ── Who is already on this address? ─────────────────────────────
	//
	// Asked before binding, because binding is not the check it looks like.
	// A listener can take ::1 while another product holds 127.0.0.1 on the
	// same port, both succeeding, and the dev proxy then reaches whichever
	// the resolver picked. See platform/preflight.
	//
	// A foreign occupant refuses the boot; another C.O.R.S.I. only warns.
	// Nothing is ever killed.
	if err := preflight.Guard(ctx, cfg.HTTP.Addr, health.Application, log); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.HTTP.Addr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("server starting", "addr", cfg.HTTP.Addr, "routes", routes)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
