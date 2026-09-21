package app

import "sync"

/* ── one active turn per Telegram chat ───────────────────────────────── */

// ══════════════════════════════════════════════════════════════════════
//
//	SERIALISE BY REFUSING, NOT BY QUEUEING
//
// ══════════════════════════════════════════════════════════════════════
//
// The rule: a Telegram chat may have ONE turn running. A second message
// arriving while the first is still being answered is REFUSED with a
// sentence, not queued and not run alongside.
//
// ── Why refusing beats serialising, for this product ───────────────────
// A queue would answer both messages, eventually, in order. That sounds
// strictly better until you notice what the second message usually IS: a
// person who thinks the first one did not go through, retyping it. A queue
// runs that twice — two paid provider calls, and two executions of
// whatever writes the turn performs. Refusing tells them the first one is
// alive, which is the information they were actually asking for.
//
// It also keeps the failure modes finite. A queue needs a depth, an
// eviction rule, a timeout per entry and an answer for what happens to
// queued work when the process restarts. None of that is T1, and a
// distributed queue is explicitly out of scope.
//
// ── What this guard does NOT cover, and why ────────────────────────────
// Commands. /status, /help, /agents and agent selection stay available
// while a turn runs, because an operator waiting on a long answer should
// be able to ask what is happening. They write little and read less, and
// none of them touches the running conversation: selecting a different
// agent selects a DIFFERENT conversation, so the turn in flight is
// unaffected.
//
// ── Scope: this process ────────────────────────────────────────────────
// An in-memory set, which is exactly right for a deployment that runs one
// API process and one poller. Two processes polling the same bot token
// would already be broken for a reason that has nothing to do with this
// guard — Telegram delivers an update to ONE getUpdates caller, and the
// cursor is a single row.

// inflight tracks which Telegram chats currently have a turn running.
type inflight struct {
	mu  sync.Mutex
	set map[int64]struct{}
}

func newInflight() *inflight { return &inflight{set: map[int64]struct{}{}} }

// acquire claims the chat's single turn slot.
//
// It returns a release function rather than expecting a matching `release`
// call, so the caller's `defer` cannot name the wrong chat. Second return
// false means somebody else holds it.
func (i *inflight) acquire(chatID int64) (release func(), ok bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if _, busy := i.set[chatID]; busy {
		return nil, false
	}
	i.set[chatID] = struct{}{}
	return func() {
		i.mu.Lock()
		delete(i.set, chatID)
		i.mu.Unlock()
	}, true
}

// running reports whether a turn is in flight, without claiming anything.
// Read by /status, which must not take the slot it is reporting on.
func (i *inflight) running(chatID int64) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	_, busy := i.set[chatID]
	return busy
}
