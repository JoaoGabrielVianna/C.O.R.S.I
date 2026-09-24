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
	telegramint "github.com/corsi/backend/internal/integrations/telegram"
	"github.com/corsi/backend/internal/jobradar"
	"github.com/corsi/backend/internal/palace"
	"github.com/corsi/backend/internal/platform/config"
	"github.com/corsi/backend/internal/platform/health"
	"github.com/corsi/backend/internal/platform/httpserver"
	"github.com/corsi/backend/internal/platform/identity"
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

	// ── Identity, and why it is registered HERE ─────────────────────
	//
	// Before a single route is mounted, so chi runs it for every handler
	// in the product. Authentication is default-DENY: a module added later
	// is protected because nobody had to remember to protect it, and the
	// only addresses that answer without a session are the four in
	// identity.PublicPaths, each justified where it is listed.
	//
	// It sits ABOVE the workspace middleware, which every module applies
	// inside its own Route. The two answer different questions — identity
	// says WHO, workspace says WHICH TENANT — and the header that answers
	// the second was never able to answer the first: it accepts any
	// syntactically valid UUID and the frontend sends a public constant.
	//
	// A missing credential fails the boot rather than disabling the gate.
	// There is no AUTH_ENABLED, on purpose: a switch is the shape of the
	// defect this deployment already paid for once, where a value absent
	// from the orchestrator's stored spec produced a healthy process with
	// a silently missing capability.
	identitySvc, err := identity.New(cfg.Identity, identity.NewPostgresStore(pool), log, nil)
	if err != nil {
		return err
	}
	router.Use(identitySvc.Middleware())
	identitySvc.Register(router)

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

	// Palace. A MODULE, like Threads and Job Radar: it owns its own
	// entities and its own schema, and it talks to no external system.
	//
	// It is wired with BOTH of its surfaces: the capabilities below, and
	// the read-only routes registered a few lines down. The text that used
	// to sit here said the opposite — that Palace had no screen and
	// therefore no Register call — and it was already false when the
	// routes landed, three lines above the call that contradicts it.
	//
	// ── The name collision this file is the only place to see ───────
	// Palace has `memories` and `sources`, and so does Chat. They are
	// unrelated:
	//
	//	chat.memories        the AGENT's memory, injected by budget,
	//	                     scoped to one agent. How it behaves.
	//	palace.memories      the OPERATOR's knowledge, read on demand,
	//	                     scoped to the workspace. What they know.
	//
	// Neither package imports the other, and no code bridges them. An
	// agent granted the palace.* capabilities is what makes the second
	// reachable, and that agent is the only place they meet.
	palaceMod := palace.New(palace.Deps{
		Pool:                pool,
		Logger:              log,
		WorkspaceMiddleware: wsMiddleware,
	})
	// Palace has TWO surfaces, and they are not equivalent. The
	// capabilities below are how anything is written, by an agent the
	// operator granted one at a time. These routes are read only: screens
	// that show the operator their own record, withholding strictly more
	// than a capability does. Neither knows about the other, and both reach
	// the same application service.
	palaceMod.Register(router)

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
	// Palace. Twenty-three capabilities, every one of them Confidential
	// and none of them External: they read and write the operator's own
	// record, in our own database. A Palace read can therefore never make
	// a turn VERIFIED_EXTERNAL_READ, which is exactly what that receipt
	// is for.
	agentTools = append(agentTools, palaceMod.Tools()...)

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
		// How turns end, by terminal reason, on the registry every other
		// family already hangs off. See metrics.Registry.Chat.
		TurnMetrics: reg.Chat(),
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

	// ── Telegram ────────────────────────────────────────────────────
	//
	// An INTEGRATION, like GitHub and Meta Threads: it owns a credential
	// and a protocol for one external system and no business rule at all.
	//
	// It is the first one wired here that DRIVES rather than being driven.
	// GitHub hands Agents a capability; Telegram hands nobody anything and
	// instead receives the chat runtime, adapted by telegramruntime.go —
	// the interface is the integration's, the implementation is the
	// composition root's, and neither package imports the other. Read that
	// file before this block; it is where the argument lives.
	//
	// ── The switch is the secret's presence, and nothing else ───────
	// No TELEGRAM_ENABLED. An empty token constructs no module, starts no
	// poller and opens no outbound connection, and the rest of C.O.R.S.I.
	// runs exactly as it does with the variable absent — which is the
	// state every existing deployment is in right now.
	//
	// Registered with NO Register call, like Threads: this integration
	// serves no browser. Its management surface is cmd/telegramctl, and a
	// route with no caller is a surface nobody is testing.
	var telegramMod *telegramint.Module
	if cfg.Modules.TelegramBotToken != "" {
		telegramMod, err = telegramint.New(telegramint.Deps{
			Pool:        pool,
			Logger:      log,
			Token:       cfg.Modules.TelegramBotToken,
			Runtime:     newTelegramRuntime(chatMod.Service(), log),
			TurnTimeout: cfg.Modules.TelegramTurnTimeout,
		})
		if err != nil {
			// A configured-but-unbuildable integration fails the boot. The
			// operator asked for Telegram; a process that came up without
			// it would be a bot that silently answers nobody, which is the
			// failure mode hardest to notice from the outside.
			return fmt.Errorf("telegram integration: %w", err)
		}
	} else {
		log.Info("TELEGRAM_BOT_TOKEN not set — the telegram integration is off")
	}

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

	// ── The Telegram poller ─────────────────────────────────────────
	//
	// A long-lived goroutine rather than a route, because the connection
	// is OUTBOUND: this process calls Telegram and holds the request open,
	// which is what lets the operator's phone reach a backend behind NAT
	// with no public URL and no tunnel.
	//
	// ── Why a failed poller does NOT kill the process ───────────────
	// Because the API is the product and the phone is a convenience. A bot
	// token revoked at BotFather, or Telegram being unreachable, must not
	// take down the finance screens, the web chat and the health endpoint
	// the platform is watching. It is logged as an error — loudly, and
	// with the reason — and the rest of C.O.R.S.I. keeps serving.
	//
	// Retries inside the loop already cover the transient cases; reaching
	// here means the failure survived them, or the token itself is bad.
	telegramDone := make(chan struct{})
	if telegramMod != nil {
		go func() {
			defer close(telegramDone)
			if err := telegramMod.Run(ctx); err != nil {
				log.Error("telegram integration stopped", "err", err)
			}
		}()
	} else {
		close(telegramDone)
	}

	select {
	case <-ctx.Done():
		log.Info("shutdown signal received")
	case err := <-errCh:
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	shutdownErr := srv.Shutdown(shutdownCtx)

	// Wait for the poller to notice the cancelled context and return.
	//
	// Bounded, because a turn in flight can legitimately be mid-provider
	// call and this process should not hang on it forever. Exceeding the
	// bound is logged rather than escalated: the turn's own write-back
	// runs on a detached context with its own deadline, so what is at risk
	// is the outbound Telegram message, not the persisted record.
	if telegramMod != nil {
		select {
		case <-telegramDone:
		case <-time.After(15 * time.Second):
			log.Warn("telegram integration did not stop within the shutdown window")
		}
	}
	return shutdownErr
}
