// Package poller is the LONG POLLING transport.
//
// ══════════════════════════════════════════════════════════════════════
//
//	TRANSPORT, AND ONLY TRANSPORT
//
// ══════════════════════════════════════════════════════════════════════
//
// It gets updates from Telegram and hands each one to the application
// layer. It decides nothing about what an update means, and the
// application layer cannot tell how an update arrived. That separation is
// what lets a webhook be added later as a sibling of this package —
// an HTTP handler that calls the same `Handler` with the same normalized
// update — without a line of domain semantics moving.
//
// ── Why long polling for the MVP ───────────────────────────────────────
// Because the connection is OUTBOUND. The backend calls Telegram, so it
// works from a laptop behind NAT with no public URL, no tunnel, no ngrok
// and no inbound firewall rule — which is exactly the shape of "C.O.R.S.I.
// runs locally and the operator's phone is somewhere else". A webhook
// needs public ingress with a valid certificate, which is infrastructure
// this product should adopt when it wants it, not to make a first
// integration testable.
package poller

import (
	"context"
	"log/slog"
	"time"

	"github.com/corsi/backend/internal/integrations/telegram/ports"
)

// Handler is what the poller hands an update to. The application service
// satisfies it.
type Handler interface {
	HandleUpdate(ctx context.Context, up ports.Update) error
}

// pollTimeout is how long Telegram holds a getUpdates request open with
// nothing to say.
//
// Fifty seconds rather than the maximum: Telegram documents up to 50 for
// long polling, and a value at the edge of what a proxy or a mobile
// network will keep alive is a value that produces spurious timeouts that
// look like failures.
const pollTimeout = 50 * time.Second

// requestSlack is how much longer than the poll this process waits before
// deciding the request itself is stuck. Without it the context would
// expire at exactly the moment Telegram was about to answer.
const requestSlack = 20 * time.Second

// backoffStart and backoffMax bound retries after a failed poll.
//
// Telegram being unreachable is an ordinary condition — a flaky network,
// a rate limit, a restart. A tight retry loop against it produces a log
// file per minute and, if the cause is a 429, makes the rate limit worse.
const (
	backoffStart = 2 * time.Second
	backoffMax   = 60 * time.Second
)

type Poller struct {
	bot     ports.BotAPI
	cursor  ports.CursorRepo
	handler Handler
	log     *slog.Logger
}

func New(bot ports.BotAPI, cursor ports.CursorRepo, h Handler, log *slog.Logger) *Poller {
	return &Poller{bot: bot, cursor: cursor, handler: h, log: log}
}

// Run polls until ctx is cancelled.
//
// It returns nil on cancellation: a poller that was asked to stop did not
// fail, and reporting otherwise would make every clean shutdown log an
// error.
func (p *Poller) Run(ctx context.Context) error {
	offset, err := p.cursor.Load(ctx)
	if err != nil {
		return err
	}
	p.log.Info("telegram poller started", "offset", offset)

	backoff := backoffStart
	for {
		if ctx.Err() != nil {
			p.log.Info("telegram poller stopped")
			return nil
		}

		pollCtx, cancel := context.WithTimeout(ctx, pollTimeout+requestSlack)
		updates, err := p.bot.GetUpdates(pollCtx, offset, pollTimeout)
		cancel()

		if err != nil {
			if ctx.Err() != nil {
				p.log.Info("telegram poller stopped")
				return nil
			}
			// The error has already been scrubbed of the token by the
			// client. See botapi.scrub.
			p.log.Warn("telegram getUpdates failed; backing off",
				"backoff", backoff.String(), "err", err)
			if !sleep(ctx, backoff) {
				return nil
			}
			backoff = min(backoff*2, backoffMax)
			continue
		}
		backoff = backoffStart

		if len(updates) == 0 {
			continue
		}

		// ── The cursor advances BEFORE the work ─────────────────────
		//
		// At-most-once, chosen deliberately. See the migration header:
		// replaying a message on restart means a second paid provider call
		// and a second execution of writes the first turn already
		// performed, which is the orphaned-duplicate failure Safe Resume
		// exists to prevent, arriving by a different door.
		//
		// A failure to PERSIST the cursor stops the batch. Processing
		// updates whose confirmation was not stored is how a crash
		// replays them.
		highest := updates[len(updates)-1].UpdateID
		for _, u := range updates {
			if u.UpdateID > highest {
				highest = u.UpdateID
			}
		}
		next := highest + 1
		if err := p.cursor.Save(ctx, next); err != nil {
			p.log.Error("telegram cursor save failed; batch not processed", "err", err)
			if !sleep(ctx, backoffStart) {
				return nil
			}
			continue
		}
		offset = next

		p.dispatch(ctx, updates)
	}
}

// dispatch runs the batch.
//
// ── Sequential, and why that is not a throughput mistake ───────────────
// A batch is what one person typed while the previous poll was open —
// usually one update. Running them concurrently would buy nothing and
// would race the per-chat turn guard into refusing the operator's own
// second message as "busy" when it was never meant to be concurrent.
//
// The context is detached from the poll's own deadline but not from
// shutdown: a turn must not be cut short because the next getUpdates is
// due, and must not outlive a shutdown request forever. The turn's real
// bound is app.DefaultTurnTimeout, applied inside the service.
func (p *Poller) dispatch(ctx context.Context, updates []ports.Update) {
	for _, u := range updates {
		if ctx.Err() != nil {
			return
		}
		if err := p.handler.HandleUpdate(ctx, u); err != nil {
			// Already reported to the user by whoever decided it, and
			// already logged with detail. This line is the correlation
			// point: it is the only one that knows the update id.
			p.log.Warn("telegram update handling failed", "update_id", u.UpdateID, "err", err)
		}
	}
}

// sleep waits, or returns false if the context ended first.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
