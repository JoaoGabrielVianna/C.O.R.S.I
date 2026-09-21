package poller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/corsi/backend/internal/integrations/telegram/ports"
)

/* ── doubles ─────────────────────────────────────────────────────────── */

type fakeBot struct {
	mu sync.Mutex
	// batches is served one per GetUpdates call; the poller blocks on the
	// last one so the loop does not spin.
	batches [][]ports.Update
	calls   int
	offsets []int64
	// failFirst makes the first call fail, to exercise the backoff path.
	failFirst bool
}

func (b *fakeBot) GetMe(context.Context) (ports.BotIdentity, error) {
	return ports.BotIdentity{ID: 1, Username: "t"}, nil
}

func (b *fakeBot) GetUpdates(ctx context.Context, offset int64, _ time.Duration) ([]ports.Update, error) {
	b.mu.Lock()
	b.calls++
	n := b.calls
	b.offsets = append(b.offsets, offset)
	fail := b.failFirst && n == 1
	var batch []ports.Update
	if n-1 < len(b.batches) && !fail {
		batch = b.batches[n-1]
	}
	b.mu.Unlock()

	if fail {
		return nil, errors.New("telegram is unreachable")
	}
	if batch == nil {
		// Nothing left to serve. Block until shutdown rather than
		// returning empty forever, which would make the test a busy loop.
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return batch, nil
}

func (b *fakeBot) SendMessage(context.Context, ports.OutboundMessage) error { return nil }
func (b *fakeBot) AnswerCallbackQuery(context.Context, string, string) error {
	return nil
}

func (b *fakeBot) seenOffsets() []int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]int64, len(b.offsets))
	copy(out, b.offsets)
	return out
}

type fakeCursor struct {
	mu     sync.Mutex
	value  int64
	writes []int64
	// failSave makes Save fail, to prove a batch whose confirmation could
	// not be stored is not processed.
	failSave bool
}

func (c *fakeCursor) Load(context.Context) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value, nil
}

func (c *fakeCursor) Save(_ context.Context, v int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failSave {
		return errors.New("database is down")
	}
	c.value = v
	c.writes = append(c.writes, v)
	return nil
}

func (c *fakeCursor) saved() []int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]int64, len(c.writes))
	copy(out, c.writes)
	return out
}

type recordingHandler struct {
	mu   sync.Mutex
	seen []int64
	// order records whether the cursor had already been written when each
	// update was handled. That is the at-most-once claim, observed.
	cursor *fakeCursor
	before []int64
}

func (h *recordingHandler) HandleUpdate(_ context.Context, u ports.Update) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seen = append(h.seen, u.UpdateID)
	if h.cursor != nil {
		v, _ := h.cursor.Load(context.Background())
		h.before = append(h.before, v)
	}
	return nil
}

func (h *recordingHandler) handled() []int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]int64, len(h.seen))
	copy(out, h.seen)
	return out
}

func msg(id int64) ports.Update {
	return ports.Update{UpdateID: id, Message: &ports.InboundMessage{
		TelegramUserID: 1, TelegramChatID: 1, ChatType: "private", Text: "oi",
	}}
}

func run(t *testing.T, bot *fakeBot, cur *fakeCursor, h Handler, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	p := New(bot, cur, h, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := p.Run(ctx); err != nil {
		t.Fatalf("Run returned %v; a cancelled poller did not fail", err)
	}
}

/* ── the claims ──────────────────────────────────────────────────────── */

// TestTheCursorAdvancesBeforeTheWork.
//
// ══════════════════════════════════════════════════════════════════════
//
//	AT-MOST-ONCE, BY CONSTRUCTION
//
// ══════════════════════════════════════════════════════════════════════
//
// The direction is the whole decision and it is easy to reverse by
// accident, because "process then confirm" is the shape everybody writes
// first. Reversed, a crash mid-turn replays the message on restart: a
// second paid provider call and a second execution of whatever writes the
// first turn already performed.
//
// Observed rather than asserted structurally: the handler reads the cursor
// at the moment it is called, and must find it already past the update it
// is being handed.
func TestTheCursorAdvancesBeforeTheWork(t *testing.T) {
	cur := &fakeCursor{}
	bot := &fakeBot{batches: [][]ports.Update{{msg(10), msg(11), msg(12)}}}
	h := &recordingHandler{cursor: cur}

	run(t, bot, cur, h, 400*time.Millisecond)

	if got := h.handled(); len(got) != 3 {
		t.Fatalf("handled %v, want three updates", got)
	}
	for i, v := range h.before {
		if v != 13 {
			t.Fatalf("update %d was handled with the cursor at %d; it must already be 13 "+
				"(the highest id + 1) BEFORE any work runs", i, v)
		}
	}
	if got := cur.saved(); len(got) != 1 || got[0] != 13 {
		t.Fatalf("cursor writes = %v, want exactly [13]", got)
	}
}

// TestAFailedCursorWriteStopsTheBatch.
//
// Processing updates whose confirmation was not stored is how a crash
// replays them. So the batch is dropped and re-fetched, not run.
func TestAFailedCursorWriteStopsTheBatch(t *testing.T) {
	cur := &fakeCursor{failSave: true}
	bot := &fakeBot{batches: [][]ports.Update{{msg(5)}}}
	h := &recordingHandler{}

	run(t, bot, cur, h, 400*time.Millisecond)

	if got := h.handled(); len(got) != 0 {
		t.Fatalf("handled %v with an unpersisted cursor; the batch must not be processed", got)
	}
}

// TestTheOffsetConfirmsTheBatch.
//
// Telegram's own acknowledgement: `offset` on the next getUpdates is what
// stops redelivery. A poller that never sent one would re-read the same
// updates forever, which looks exactly like the bot answering every
// message twice.
func TestTheOffsetConfirmsTheBatch(t *testing.T) {
	cur := &fakeCursor{}
	bot := &fakeBot{batches: [][]ports.Update{{msg(100), msg(101)}}}

	run(t, bot, cur, &recordingHandler{}, 400*time.Millisecond)

	offs := bot.seenOffsets()
	if len(offs) < 2 {
		t.Fatalf("getUpdates was called %d times; want at least two", len(offs))
	}
	if offs[0] != 0 {
		t.Fatalf("first offset = %d, want 0 — a fresh deployment has confirmed nothing", offs[0])
	}
	if offs[1] != 102 {
		t.Fatalf("second offset = %d, want 102 (highest id + 1)", offs[1])
	}
}

// TestAStoredCursorIsResumedFrom. A restart must not re-read what the
// previous process already took responsibility for.
func TestAStoredCursorIsResumedFrom(t *testing.T) {
	cur := &fakeCursor{value: 777}
	bot := &fakeBot{}

	run(t, bot, cur, &recordingHandler{}, 200*time.Millisecond)

	offs := bot.seenOffsets()
	if len(offs) == 0 || offs[0] != 777 {
		t.Fatalf("first offset = %v, want 777 from storage", offs)
	}
}

// TestAFailedPollBacksOffAndRecovers.
//
// Telegram being unreachable is an ordinary condition. A tight retry loop
// against it produces a log file per minute and, when the cause is a 429,
// makes the rate limit worse.
func TestAFailedPollBacksOffAndRecovers(t *testing.T) {
	cur := &fakeCursor{}
	bot := &fakeBot{failFirst: true, batches: [][]ports.Update{nil, {msg(1)}}}
	h := &recordingHandler{}

	start := time.Now()
	run(t, bot, cur, h, 4*time.Second)
	elapsed := time.Since(start)

	if elapsed < backoffStart {
		t.Fatalf("recovered in %v without waiting the %v backoff", elapsed, backoffStart)
	}
	if got := h.handled(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("handled %v after recovery, want [1]", got)
	}
}

// TestCancellationIsNotAFailure. A poller that was asked to stop did not
// fail, and reporting otherwise would make every clean shutdown log an
// error.
func TestCancellationIsNotAFailure(t *testing.T) {
	cur := &fakeCursor{}
	bot := &fakeBot{}
	ctx, cancel := context.WithCancel(context.Background())
	p := New(bot, cur, &recordingHandler{}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v on cancellation, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}
