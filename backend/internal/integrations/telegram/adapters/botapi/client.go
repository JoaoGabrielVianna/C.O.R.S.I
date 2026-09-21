// Package botapi speaks Telegram's Bot API over HTTP.
//
// ── Why a hand-written client and not a library ────────────────────────
// Because this integration uses FIVE methods of an API with well over a
// hundred, and the repo already answers this question the same way: the
// GitHub integration has its own client for the same reason. A library
// would add a dependency, its transitive tree and its release cadence to
// the build, in exchange for methods nothing calls and a type system for
// updates this product refuses to handle.
//
// The concrete cost of a library here would also be a real one: the update
// types it exposes carry `from.username`, `first_name`, and the whole
// contact surface. This client decodes the numeric ids and the text, and
// there is no struct field for anything else — which is how "never
// authorize on a username" stops being a rule somebody has to follow.
//
// ── The token ──────────────────────────────────────────────────────────
// It lives in the base URL, because that is where Telegram put it:
// `https://api.telegram.org/bot<TOKEN>/method`. That makes it part of
// every request path, which makes it exactly the thing that must never
// reach a log line or an error string. See `scrub`.
package botapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/corsi/backend/internal/integrations/telegram/domain"
	"github.com/corsi/backend/internal/integrations/telegram/ports"
)

// DefaultBaseURL is Telegram's own host. A field rather than a constant in
// the client so the integration suite can point a real client at a fake
// server — the same arrangement `github.connections.api_base_url` makes,
// and for the same reason: the client is the part most likely to be wrong,
// and a test that stubbed it would prove nothing.
const DefaultBaseURL = "https://api.telegram.org"

type Client struct {
	http    *http.Client
	baseURL string
	token   string
	log     *slog.Logger
}

type Options struct {
	Token   string
	BaseURL string
	Logger  *slog.Logger
	// HTTP replaces the transport. Nil means one built here, with no
	// overall timeout: long polling holds a request open for a minute by
	// design, and a client-wide timeout would cut it every time. Each call
	// carries its own context deadline instead.
	HTTP *http.Client
}

func New(o Options) (*Client, error) {
	if strings.TrimSpace(o.Token) == "" {
		return nil, domain.NotConfigured("TELEGRAM_BOT_TOKEN is empty")
	}
	base := o.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	hc := o.HTTP
	if hc == nil {
		hc = &http.Client{Transport: http.DefaultTransport}
	}
	return &Client{http: hc, baseURL: strings.TrimRight(base, "/"), token: o.Token, log: o.Logger}, nil
}

/* ── the wire ────────────────────────────────────────────────────────── */

// envelope is the shape every Bot API response has.
type envelope struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	ErrorCode   int             `json:"error_code"`
}

// call performs one Bot API method.
//
// ── Why every error goes through scrub ─────────────────────────────────
// The URL contains the token. `http.Client` puts the URL into the error it
// returns for a connection failure, a redirect problem and a context
// deadline — so the straightforward `return err` publishes the bot token
// into whatever logs that error. Every return path here is scrubbed, and
// the test asserts it on a client whose token is a recognisable string.
func (c *Client) call(ctx context.Context, method string, body any, out any) error {
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal %s: %w", method, err)
		}
		payload = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(method), payload)
	if err != nil {
		return c.scrub(fmt.Errorf("build %s request: %w", method, err))
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// The single most important scrub in this file: a dial failure's
		// error text is the full URL.
		return c.scrub(domain.Upstream(fmt.Sprintf("telegram %s: %v", method, err)))
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	// Bounded read. A Bot API response is small; an unbounded read from a
	// host this process does not control is a memory footgun.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return c.scrub(domain.Upstream(fmt.Sprintf("telegram %s: read response: %v", method, err)))
	}

	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return c.scrub(domain.Upstream(fmt.Sprintf("telegram %s: status %d, unreadable response", method, resp.StatusCode)))
	}
	if !env.OK {
		// `description` is Telegram's own prose. It is safe to log — it
		// never contains the token, which travelled in the path — and it
		// is the only way to diagnose a 400 from the Bot API. It does NOT
		// go to a user: see app/reply.go.
		return c.scrub(domain.Upstream(fmt.Sprintf("telegram %s: %d %s", method, env.ErrorCode, env.Description)))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return c.scrub(fmt.Errorf("decode %s result: %w", method, err))
	}
	return nil
}

const maxResponseBytes = 8 << 20

func (c *Client) endpoint(method string) string {
	return c.baseURL + "/bot" + c.token + "/" + method
}

// scrub removes the token from anything on its way out of this package.
//
// It is belt and braces on purpose. The token should not be in an error at
// all, and this is the guarantee that it is not in one nobody predicted —
// a redirect, a proxy error, a future method that formats a URL.
func (c *Client) scrub(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if !strings.Contains(msg, c.token) {
		return err
	}
	// Rebuilt rather than wrapped: wrapping keeps the original, and the
	// original is the thing that contains the token.
	return errors.New(strings.ReplaceAll(msg, c.token, redacted))
}

// redacted is what a scrubbed token reads as. A visible marker rather than
// an empty string, so a scrubbed log line says a secret was removed
// instead of looking like a malformed URL.
const redacted = "<TELEGRAM_BOT_TOKEN>"

/* ── the five methods ────────────────────────────────────────────────── */

func (c *Client) GetMe(ctx context.Context) (ports.BotIdentity, error) {
	var out struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	if err := c.call(ctx, "getMe", nil, &out); err != nil {
		return ports.BotIdentity{}, err
	}
	return ports.BotIdentity{ID: out.ID, Username: out.Username}, nil
}

// GetUpdates is long polling.
//
// `offset` confirms everything below it: Telegram will not redeliver an
// update whose id is less than the offset the bot last sent. That is the
// protocol's own at-least-once acknowledgement, and it is why the cursor
// this integration persists is the thing that decides the delivery
// guarantee — see the migration header.
//
// `allowed_updates` is explicit, and short. Telegram's default is "every
// update type except chat_member", which would deliver edits, reactions
// and channel posts to a handler that refuses all of them — bandwidth and
// cursor movement spent on events this product does not serve.
func (c *Client) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]ports.Update, error) {
	body := map[string]any{
		"timeout":         int(timeout.Seconds()),
		"allowed_updates": []string{"message", "callback_query"},
	}
	if offset > 0 {
		body["offset"] = offset
	}
	var raw []rawUpdate
	if err := c.call(ctx, "getUpdates", body, &raw); err != nil {
		return nil, err
	}
	out := make([]ports.Update, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.normalize())
	}
	return out, nil
}

func (c *Client) SendMessage(ctx context.Context, m ports.OutboundMessage) error {
	body := map[string]any{
		"chat_id": m.TelegramChatID,
		"text":    m.Text,
		// ── No parse_mode, deliberately ─────────────────────────────
		//
		// Telegram's MarkdownV2 requires escaping eighteen characters,
		// and an unescaped one makes the API reject the whole message.
		// The text here is a model's prose: it contains underscores,
		// asterisks, hyphens, parentheses and backticks as a matter of
		// course. Sending it as plain text means it always arrives.
		// Correct content beats pretty rendering, and a broken escaper
		// loses both.
		"disable_web_page_preview": true,
	}
	if len(m.Buttons) > 0 {
		rows := make([][]map[string]string, 0, len(m.Buttons))
		for _, b := range m.Buttons {
			if len(b.Data) > domain.MaxCallbackBytes {
				// Refused rather than truncated. Truncating produces a
				// button that decodes to a different id, which is worse
				// than a keyboard that was never attached.
				return domain.Invalid("callback data exceeds telegram's limit")
			}
			rows = append(rows, []map[string]string{{"text": b.Label, "callback_data": b.Data}})
		}
		body["reply_markup"] = map[string]any{"inline_keyboard": rows}
	}
	return c.call(ctx, "sendMessage", body, nil)
}

func (c *Client) AnswerCallbackQuery(ctx context.Context, callbackID, text string) error {
	body := map[string]any{"callback_query_id": callbackID}
	if text != "" {
		body["text"] = text
	}
	return c.call(ctx, "answerCallbackQuery", body, nil)
}

/* ── decoding, and what is deliberately not decoded ──────────────────── */

// rawUpdate is the wire shape, narrowed.
//
// Compare it with Telegram's actual Update object: this struct has no
// `edited_message`, no `channel_post`, no `inline_query`, no
// `my_chat_member`, and — the one that matters — no `username`,
// `first_name` or `language_code` on the sender. A field that does not
// exist cannot be read by a future change that "just needs the name for
// the greeting", which is how a display name turns into an identity.
type rawUpdate struct {
	UpdateID int64 `json:"update_id"`
	Message  *struct {
		MessageID int64 `json:"message_id"`
		From      *struct {
			ID    int64 `json:"id"`
			IsBot bool  `json:"is_bot"`
		} `json:"from"`
		Chat *struct {
			ID   int64  `json:"id"`
			Type string `json:"type"`
		} `json:"chat"`
		Text string `json:"text"`
	} `json:"message"`
	CallbackQuery *struct {
		ID   string `json:"id"`
		From *struct {
			ID    int64 `json:"id"`
			IsBot bool  `json:"is_bot"`
		} `json:"from"`
		Message *struct {
			Chat *struct {
				ID   int64  `json:"id"`
				Type string `json:"type"`
			} `json:"chat"`
		} `json:"message"`
		Data string `json:"data"`
	} `json:"callback_query"`
}

// normalize lifts the wire shape into the port's.
//
// An update missing the parts this integration needs — no sender, no chat
// — becomes an update with neither a Message nor a Callback, which the
// application layer ignores. That is the right failure: a malformed update
// should be dropped, not guessed at.
func (r rawUpdate) normalize() ports.Update {
	u := ports.Update{UpdateID: r.UpdateID}
	switch {
	case r.Message != nil && r.Message.From != nil && r.Message.Chat != nil:
		if r.Message.From.IsBot {
			// A bot talking to this bot. Not the operator, by definition.
			return u
		}
		u.Message = &ports.InboundMessage{
			MessageID:      r.Message.MessageID,
			TelegramUserID: r.Message.From.ID,
			TelegramChatID: r.Message.Chat.ID,
			ChatType:       r.Message.Chat.Type,
			Text:           r.Message.Text,
		}
	case r.CallbackQuery != nil && r.CallbackQuery.From != nil &&
		r.CallbackQuery.Message != nil && r.CallbackQuery.Message.Chat != nil:
		if r.CallbackQuery.From.IsBot {
			return u
		}
		u.Callback = &ports.InboundCallback{
			CallbackID:     r.CallbackQuery.ID,
			TelegramUserID: r.CallbackQuery.From.ID,
			TelegramChatID: r.CallbackQuery.Message.Chat.ID,
			ChatType:       r.CallbackQuery.Message.Chat.Type,
			Data:           r.CallbackQuery.Data,
		}
	}
	return u
}

// compile-time proof that the client satisfies the port.
var _ ports.BotAPI = (*Client)(nil)
