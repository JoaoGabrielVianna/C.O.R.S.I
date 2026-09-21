package app

import (
	"context"
	"errors"

	"github.com/corsi/backend/internal/integrations/telegram/domain"
	"github.com/corsi/backend/internal/integrations/telegram/ports"
)

/* ── button presses ──────────────────────────────────────────────────── */

// handleCallback serves an inline button.
//
// ── The order of operations, and why the spinner is cleared first ──────
// Telegram shows a loading state on a pressed inline button until
// `answerCallbackQuery` is called, and gives about ten seconds before it
// gives up. A resume can take minutes. So the spinner is cleared BEFORE
// the work starts, and the result arrives as an ordinary message — the
// alternative is a button that looks stuck for the whole turn and then
// reports a timeout that did not happen.
func (s *Service) handleCallback(ctx context.Context, c *ports.InboundCallback) error {
	// Cleared unconditionally, including for a press this bot is about to
	// refuse. A stranger pressing a button they should not have gets their
	// spinner cleared and nothing else: leaving it spinning would be a
	// signal, and this path deliberately emits none.
	if err := s.bot.AnswerCallbackQuery(ctx, c.CallbackID, ""); err != nil {
		s.log.Warn("telegram answer callback", "chat_id", c.TelegramChatID, "err", err)
	}

	if c.ChatType != "" && c.ChatType != chatTypePrivate {
		s.log.Info("telegram non-private callback refused",
			"chat_id", c.TelegramChatID, "chat_type", c.ChatType)
		return nil
	}

	// ── The binding first, ALWAYS ───────────────────────────────────
	//
	// Before the callback data is even parsed. A press from an unpaired
	// Telegram account must not be able to reach a decode path, let alone
	// a runtime call: the sequence is "who are you" and only then "what
	// did you press".
	b, err := s.requireBinding(ctx, c.TelegramUserID, c.TelegramChatID)
	if err != nil {
		return s.refuse(ctx, c.TelegramChatID, err)
	}

	cb, err := domain.DecodeCallback(c.Data)
	if err != nil {
		// Malformed or unknown button. Logged, and answered with nothing:
		// the sender is paired, so this is a stale keyboard from an older
		// build rather than an attack, and a message about an unrecognised
		// button is noise.
		s.log.Info("telegram unrecognised callback", "chat_id", c.TelegramChatID)
		return nil
	}

	switch cb.Kind {
	case domain.CallbackSelectAgent:
		name, err := s.selectAgent(ctx, b, cb.ID)
		if err != nil {
			var de *domain.Error
			if errors.As(err, &de) && de.Kind == domain.KindNotFound {
				// The id named an agent this workspace does not have —
				// either it was deleted, or the callback was forged. The
				// same answer for both: the list is the authority, and it
				// is one tap away.
				s.log.Info("telegram agent selection refused",
					"chat_id", c.TelegramChatID, "agent_id", cb.ID)
				return s.say(ctx, c.TelegramChatID, copyAgentGone)
			}
			return s.fail(ctx, c.TelegramChatID, "select agent", err)
		}
		return s.say(ctx, c.TelegramChatID, formatAgentSelected(name))

	case domain.CallbackResume:
		return s.resumeTurn(ctx, c, b, cb.ID)
	}
	return nil
}
