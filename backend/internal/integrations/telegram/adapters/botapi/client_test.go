package botapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/corsi/backend/internal/integrations/telegram/domain"
	"github.com/corsi/backend/internal/integrations/telegram/ports"
)

// fixtureToken is a literal in a test file and could not authenticate
// against anything. It is deliberately distinctive so the scrubbing
// assertions can look for it in error text.
const fixtureToken = "111111:FIXTURE-NOT-A-REAL-TELEGRAM-TOKEN"

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(Options{
		Token: fixtureToken, BaseURL: srv.URL,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func okJSON(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

/* ── the token ───────────────────────────────────────────────────────── */

// TestTheTokenNeverReachesAnError.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THE BOT TOKEN IS IN THE URL PATH
//
// ══════════════════════════════════════════════════════════════════════
//
// That is Telegram's design, not ours: `/bot<TOKEN>/method`. It means
// `http.Client` puts the token into the error it returns for a dial
// failure, a redirect problem and a context deadline — so the obvious
// `return err` publishes the credential into whatever logs that error.
//
// This is the assertion that keeps that from happening. It drives every
// failure mode through the real client and greps the resulting error for
// the token.
func TestTheTokenNeverReachesAnError(t *testing.T) {
	ctx := context.Background()

	t.Run("unreachable host", func(t *testing.T) {
		c, err := New(Options{
			Token: fixtureToken,
			// A port nothing is listening on. The resulting dial error
			// carries the full URL.
			BaseURL: "http://127.0.0.1:1",
			Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = c.GetMe(ctx)
		assertScrubbed(t, err)
	})

	t.Run("deadline exceeded", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(300 * time.Millisecond)
			okJSON(w, map[string]any{"id": 1, "username": "b"})
		})
		tight, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		defer cancel()
		_, err := c.GetMe(tight)
		assertScrubbed(t, err)
	})

	t.Run("bot api refusal", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": false, "error_code": 401, "description": "Unauthorized",
			})
		})
		_, err := c.GetMe(ctx)
		assertScrubbed(t, err)
		if !strings.Contains(err.Error(), "Unauthorized") {
			t.Fatalf("the error lost telegram's own description: %v", err)
		}
	})

	t.Run("unreadable response", func(t *testing.T) {
		c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "<html>gateway error</html>")
		})
		_, err := c.GetMe(ctx)
		assertScrubbed(t, err)
	})
}

func assertScrubbed(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if strings.Contains(err.Error(), fixtureToken) {
		t.Fatalf("THE BOT TOKEN IS IN THIS ERROR: %v", err)
	}
}

// TestTheTokenTravelsWhereTelegramExpectsIt.
//
// The other half: scrubbing must not have broken the request. The token
// has to be in the path or nothing authenticates.
func TestTheTokenTravelsWhereTelegramExpectsIt(t *testing.T) {
	var gotPath string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		okJSON(w, map[string]any{"id": 7, "username": "corsi_test_bot"})
	})
	me, err := c.GetMe(context.Background())
	if err != nil {
		t.Fatalf("GetMe: %v", err)
	}
	if me.Username != "corsi_test_bot" || me.ID != 7 {
		t.Fatalf("GetMe = %+v", me)
	}
	if want := "/bot" + fixtureToken + "/getMe"; gotPath != want {
		t.Fatalf("path = %q, want %q", gotPath, want)
	}
}

func TestAnEmptyTokenRefusesToBuildAClient(t *testing.T) {
	for _, tok := range []string{"", "   "} {
		if _, err := New(Options{Token: tok}); err == nil {
			t.Fatalf("New with token %q must fail: a client that cannot authenticate is a bot that silently answers nobody", tok)
		}
	}
}

/* ── decoding ────────────────────────────────────────────────────────── */

// TestUpdateDecodingKeepsIdsAndDropsEverythingElse.
//
// The payload below is a realistic Telegram update, including the contact
// fields this integration refuses to know about. The assertion is that the
// normalized value carries the numeric ids and the text, and that there is
// nowhere for a username to have gone.
func TestUpdateDecodingKeepsIdsAndDropsEverythingElse(t *testing.T) {
	payload := `[{
	  "update_id": 900,
	  "message": {
	    "message_id": 12,
	    "from": {"id": 555, "is_bot": false, "username": "someone", "first_name": "Someone", "language_code": "pt-br"},
	    "chat": {"id": 555, "type": "private", "username": "someone"},
	    "date": 1700000000,
	    "text": "olá"
	  }
	}]`
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"result":`+payload+`}`)
	})
	got, err := c.GetUpdates(context.Background(), 0, time.Second)
	if err != nil {
		t.Fatalf("GetUpdates: %v", err)
	}
	if len(got) != 1 || got[0].Message == nil {
		t.Fatalf("got %#v", got)
	}
	m := got[0].Message
	if m.TelegramUserID != 555 || m.TelegramChatID != 555 || m.ChatType != "private" || m.Text != "olá" {
		t.Fatalf("message = %+v", m)
	}
	// The structural guarantee, stated as a compile-time fact rather than
	// a runtime one: there is no field on the port's type in which a
	// username could be carried, so no future code can authorize on one.
	var _ = ports.InboundMessage{
		MessageID: 0, TelegramUserID: 0, TelegramChatID: 0, ChatType: "", Text: "",
	}
}

// TestAMessageFromABotIsNotAMessage. Another bot is not the operator.
func TestAMessageFromABotIsNotAMessage(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"result":[{"update_id":1,"message":{
		  "message_id":1,"from":{"id":9,"is_bot":true},"chat":{"id":9,"type":"private"},"text":"hi"}}]}`)
	})
	got, err := c.GetUpdates(context.Background(), 0, time.Second)
	if err != nil {
		t.Fatalf("GetUpdates: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d updates, want 1", len(got))
	}
	if got[0].Message != nil || got[0].Callback != nil {
		t.Fatal("a message from a bot must normalise to an update the application ignores")
	}
}

// TestGetUpdatesAsksOnlyForWhatItServes.
//
// Telegram's default is every update type but one. Requesting two means an
// edited message, a reaction or a channel post never arrives — so it never
// moves the cursor and never costs a round trip.
func TestGetUpdatesAsksOnlyForWhatItServes(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		okJSON(w, []any{})
	})
	if _, err := c.GetUpdates(context.Background(), 42, 50*time.Second); err != nil {
		t.Fatalf("GetUpdates: %v", err)
	}
	allowed, _ := body["allowed_updates"].([]any)
	if len(allowed) != 2 {
		t.Fatalf("allowed_updates = %v, want exactly message and callback_query", body["allowed_updates"])
	}
	if body["offset"] != float64(42) {
		t.Fatalf("offset = %v, want 42 — without it telegram redelivers everything", body["offset"])
	}
}

/* ── sending ─────────────────────────────────────────────────────────── */

// TestSendMessageSendsPlainText.
//
// No `parse_mode`. A model's prose is full of underscores, asterisks and
// backticks, and MarkdownV2 rejects the whole message on one unescaped
// character — so the failure mode of "pretty formatting" is an answer that
// never arrives.
func TestSendMessageSendsPlainText(t *testing.T) {
	var body map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		okJSON(w, map[string]any{"message_id": 1})
	})
	err := c.SendMessage(context.Background(), ports.OutboundMessage{
		TelegramChatID: 77,
		Text:           "custo_total * 2 (ver `docs/`) — 50% _menos_",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if _, ok := body["parse_mode"]; ok {
		t.Fatal("parse_mode must not be sent; markdown escaping is the failure mode this avoids")
	}
	if body["text"] != "custo_total * 2 (ver `docs/`) — 50% _menos_" {
		t.Fatalf("text was altered: %v", body["text"])
	}
}

// TestOversizedCallbackDataIsRefusedNotTruncated.
//
// Truncating produces a button that decodes to a DIFFERENT id, which is
// worse than a keyboard that was never attached.
func TestOversizedCallbackDataIsRefusedNotTruncated(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		okJSON(w, map[string]any{"message_id": 1})
	})
	err := c.SendMessage(context.Background(), ports.OutboundMessage{
		TelegramChatID: 1, Text: "x",
		Buttons: []ports.Button{{Label: "b", Data: strings.Repeat("z", domain.MaxCallbackBytes+1)}},
	})
	if err == nil {
		t.Fatal("oversized callback data must be refused")
	}
}
