package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/corsi/backend/internal/integrations/telegram/domain"
	"github.com/corsi/backend/internal/integrations/telegram/ports"
)

/* ── what goes back to the phone ─────────────────────────────────────── */

// deliver turns one persisted turn into one or more Telegram messages.
//
// ══════════════════════════════════════════════════════════════════════
//
//	ONE C.O.R.S.I. MESSAGE, N TELEGRAM MESSAGES
//
// ══════════════════════════════════════════════════════════════════════
//
// The assistant turn is already written by the time this runs, whole and
// untruncated. Everything here is transport: splitting, the evidence
// footer, and the button.
//
// ── Why a failed turn still gets delivered ─────────────────────────────
// Because a turn can fail AFTER having done something — the round ceiling,
// a budget refusal, a gateway that dropped mid-stream — and in every one
// of those the message is persisted, the audit rows are written, and the
// tools that ran really ran. Dropping the answer and sending only an error
// is the bug the SSE route had: the executions were real, recorded, and
// invisible to the reader. Same rule here, same reason.
func (s *Service) deliver(ctx context.Context, chatID int64, res *ports.TurnResult, turnErr error) error {
	if res == nil {
		// Nothing was persisted: the turn died before it reached the
		// model. There is no answer and no receipt to hand over, so the
		// operator gets the generic sentence and the log gets the detail.
		return s.fail(ctx, chatID, "turn", turnErr)
	}

	body := strings.TrimSpace(res.Text)
	if body == "" {
		// A real, persisted turn with no words — an aborted stream, a
		// ceiling reached on the first round. Stated plainly rather than
		// papered over: inventing a sentence here would be this surface
		// producing content the runtime did not.
		body = copyEmptyAnswer
	}
	if footer := s.footer(res); footer != "" {
		body += "\n\n" + footer
	}

	chunks := domain.Chunk(body)
	for i, c := range chunks {
		out := ports.OutboundMessage{TelegramChatID: chatID, Text: c}
		// The button rides on the LAST chunk only. On an earlier one it
		// would sit in the middle of the answer, and Telegram keeps an
		// inline keyboard attached to its own message — so pressing it
		// after reading the rest would mean scrolling back.
		if i == len(chunks)-1 && res.Resumable {
			out.Buttons = []ports.Button{{
				Label: copyResumeButton,
				Data:  domain.EncodeCallback(domain.CallbackResume, res.MessageID),
			}}
		}
		if err := s.bot.SendMessage(ctx, out); err != nil {
			// Partial delivery. The turn is persisted and complete in
			// C.O.R.S.I.; what failed is the phone. Reported to the log
			// with the position so the gap is diagnosable, and NOT retried:
			// a retry would duplicate the chunks that already landed.
			s.log.Error("telegram send chunk",
				"chat_id", chatID, "chunk", i+1, "of", len(chunks), "err", err)
			return err
		}
	}
	return nil
}

// footer is the evidence line, or lines, under an answer.
//
// ── Why this exists on a surface that is "just an MVP" ─────────────────
// Because the defect it guards against is not hypothetical and not
// web-specific. A finance agent reported "8 transações importadas" in a
// turn with zero tool calls; a content agent reported "1.535 seguidores,
// 40.055 views" in a turn with zero tool calls, against real figures of
// 163 and 224. In both, every layer behaved and the PRODUCT rendered prose
// with nothing beside it.
//
// Shipping a new surface that renders prose with nothing beside it would
// reproduce exactly that, on a device where the operator is least able to
// go and check. The counts come from the runtime's receipts, derived from
// execution records — nothing here reads the answer.
func (s *Service) footer(res *ports.TurnResult) string {
	var parts []string
	if w := formatWriteReceipt(res.WritesExecuted, res.WritesFailed, res.WritesRefused); w != "" {
		parts = append(parts, w)
	}
	if r := formatReadReceipt(res.ExternalReadStatus, res.ExternalReadAvailable); r != "" {
		parts = append(parts, r)
	}
	if res.Resumable {
		parts = append(parts, copyResumable)
	}
	return strings.Join(parts, "\n")
}

/* ── the three ways this package talks ───────────────────────────────── */

// say sends one plain message, chunked.
func (s *Service) say(ctx context.Context, chatID int64, text string) error {
	for _, c := range domain.Chunk(text) {
		if err := s.bot.SendMessage(ctx, ports.OutboundMessage{
			TelegramChatID: chatID, Text: c,
		}); err != nil {
			return err
		}
	}
	return nil
}

// refuse answers a refusal this package decided.
//
// The only refusal with a sentence of its own is "not paired", because it
// is the only one with an action attached. Everything else gets the
// generic failure, on purpose: a refusal that explained itself would
// describe this deployment to whoever triggered it.
func (s *Service) refuse(ctx context.Context, chatID int64, err error) error {
	var de *domain.Error
	if errors.As(err, &de) && de.Kind == domain.KindNotPaired {
		s.log.Info("telegram refused: not paired", "chat_id", chatID)
		return s.say(ctx, chatID, copyNotPaired)
	}
	return s.fail(ctx, chatID, "refused", err)
}

// fail logs the real problem and tells the user nothing about it.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THE LOG IS SPECIFIC; THE PHONE IS NOT
//
// ══════════════════════════════════════════════════════════════════════
//
// A Telegram message must never carry a stack trace, a database error, a
// tool payload, a gateway response body or a secret. Those are exactly
// what an error's text tends to contain, and `err.Error()` on a phone is
// how a connection string ends up in somebody's chat history.
//
// The `err` value goes to slog, which this deployment controls.
func (s *Service) fail(ctx context.Context, chatID int64, what string, err error) error {
	s.log.Error("telegram "+what, "chat_id", chatID, "err", err)
	// Best effort. If the phone cannot be reached either, the original
	// error is what is worth returning.
	if sendErr := s.say(ctx, chatID, copyTurnFailed); sendErr != nil {
		s.log.Error("telegram send failure notice", "chat_id", chatID, "err", sendErr)
	}
	return err
}

/* ── the operational record ──────────────────────────────────────────── */

// logTurn records that a turn happened, and how it ended.
//
// ── What is here, and what is deliberately absent ──────────────────────
// Ids, a terminal reason, a duration, receipt counts. NOT the message, not
// the answer and not a tool payload: message content is not logged by
// default, and "by default" here means there is no flag that turns it on.
//
// The Telegram ids are numeric and are safe operational metadata — they
// are how a support question about "my phone stopped answering" becomes a
// grep. The conversation id is the join to the transcript, which is where
// content legitimately lives, under whatever access that database has.
func (s *Service) logTurn(kind string, b *domain.Binding, ca *domain.ChatAgent, res *ports.TurnResult, err error, started time.Time) {
	attrs := []any{
		"kind", kind,
		"telegram_chat_id", b.TelegramChatID,
		"conversation_id", ca.ConversationID,
		"agent_id", ca.AgentID,
		"duration_ms", time.Since(started).Milliseconds(),
	}
	if res != nil {
		attrs = append(attrs,
			"message_id", res.MessageID,
			"finish_reason", res.FinishReason,
			"resumable", res.Resumable,
			"writes_executed", res.WritesExecuted,
			"writes_failed", res.WritesFailed,
			"writes_refused", res.WritesRefused,
			"external_read", res.ExternalReadStatus,
			"answer_chars", len(res.Text),
		)
	}
	if err != nil {
		attrs = append(attrs, "err", err)
		s.log.Warn("telegram turn ended with an error", attrs...)
		return
	}
	s.log.Info("telegram turn", attrs...)
}
