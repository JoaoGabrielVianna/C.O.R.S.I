package app

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/integrations/telegram/domain"
	"github.com/corsi/backend/internal/integrations/telegram/ports"
)

/* ── an ordinary message becomes an ordinary turn ────────────────────── */

// handleConversationText is the path a normal message takes.
//
//	telegram update
//	  → resolve binding          (who, and WHICH WORKSPACE)
//	  → resolve active agent     (still exists?)
//	  → resolve conversation     (this binding + this agent)
//	  → claim the chat's turn slot
//	  → the EXISTING C.O.R.S.I. chat runtime
//	  → reply, with the turn's own receipts
//
// Everything between "claim" and "reply" is somebody else's code, and that
// is the design: grants, budget, tools, caching, cost telemetry, the
// terminal vocabulary and both receipts are the runtime's, identical to
// what the web client gets.
func (s *Service) handleConversationText(ctx context.Context, m *ports.InboundMessage, text string) error {
	b, err := s.requireBinding(ctx, m.TelegramUserID, m.TelegramChatID)
	if err != nil {
		return s.refuse(ctx, m.TelegramChatID, err)
	}
	if b.ActiveAgentID == nil {
		return s.say(ctx, m.TelegramChatID, copyNoAgentSelected)
	}
	agent, err := s.findAgent(ctx, b)
	if err != nil {
		return s.fail(ctx, m.TelegramChatID, "resolve active agent", err)
	}
	if agent == nil {
		return s.say(ctx, m.TelegramChatID, copyAgentGone)
	}
	ca, err := s.conversationFor(ctx, b, *agent)
	if err != nil {
		return s.fail(ctx, m.TelegramChatID, "resolve conversation", err)
	}

	// ── One turn per chat ───────────────────────────────────────────
	//
	// Claimed AFTER the cheap resolutions and BEFORE the expensive call.
	// Claiming earlier would make /agents-style mistakes hold the slot;
	// claiming later would leave a window where two provider calls start.
	release, ok := s.inflight.acquire(m.TelegramChatID)
	if !ok {
		return s.say(ctx, m.TelegramChatID, copyBusy)
	}
	defer release()

	turnCtx, cancel := context.WithTimeout(ctx, s.turnTTL)
	defer cancel()

	started := time.Now()
	res, err := s.runtime.Send(turnCtx, ports.SendInput{
		WorkspaceID:    b.WorkspaceID,
		ConversationID: ca.ConversationID,
		Content:        text,
	})
	s.logTurn("send", b, ca, res, err, started)

	if err := s.chatAgents.Touch(context.WithoutCancel(ctx), ca.ID); err != nil {
		// Cosmetic ordering data. A failure here must not cost the
		// operator an answer that has already been produced and paid for.
		s.log.Warn("telegram touch chat agent", "chat_agent_id", ca.ID, "err", err)
	}
	return s.deliver(ctx, m.TelegramChatID, res, err)
}

/* ── safe resume ─────────────────────────────────────────────────────── */

// resumeTurn continues an interrupted turn through the runtime's EXISTING
// safe resume path.
//
// ══════════════════════════════════════════════════════════════════════
//
//	CONTINUE THE PENDING WORK; NEVER REPEAT AN EXECUTED WRITE
//
// ══════════════════════════════════════════════════════════════════════
//
// ── What this is NOT, and the incident that makes it matter ────────────
// It does not send the word "Continuar" as a user message. That is the
// exact mistake the runtime's resume path exists to remove: a turn that
// had already created a Room and an Artifact hit the round ceiling, the
// operator typed "Try again", and "again" means START OVER — so the next
// turn created a second Room and a second Artifact, with the first pair
// orphaned.
//
// A resume writes NO user message. The original question is still the
// question, and what the new attempt gains is a block, built from audit
// records, saying which writes already executed and with which effect
// refs. None of that could be reconstructed here, and none of it is
// reconstructed here: this function calls the runtime and the runtime does
// all of it.
//
// ── Why the conversation is not in the callback ────────────────────────
// It is resolved from the binding's active agent, server-side. So a forged
// callback cannot name another conversation even in principle — there is
// no field for it. The message id it does carry is then checked by the
// runtime against that conversation's actual last turn, which is what
// makes a forged or stale id a refusal rather than a replay of history out
// of order.
//
// ── Idempotence ────────────────────────────────────────────────────────
// Pressing the button twice is refused the second time, by the runtime,
// for the reason it refuses any non-last turn: the conversation has moved
// on. This package adds no idempotence logic of its own — a second
// mechanism would be free to disagree with the first.
func (s *Service) resumeTurn(ctx context.Context, c *ports.InboundCallback, b *domain.Binding, messageID uuid.UUID) error {
	agent, err := s.findAgent(ctx, b)
	if err != nil {
		return s.fail(ctx, c.TelegramChatID, "resolve active agent", err)
	}
	if agent == nil {
		return s.say(ctx, c.TelegramChatID, copyAgentGone)
	}
	ca, err := s.chatAgents.Find(ctx, b.ID, agent.ID)
	if err != nil {
		return s.fail(ctx, c.TelegramChatID, "resolve conversation", err)
	}
	if ca == nil {
		return s.say(ctx, c.TelegramChatID, copyResumeRefused)
	}

	release, ok := s.inflight.acquire(c.TelegramChatID)
	if !ok {
		return s.say(ctx, c.TelegramChatID, copyBusy)
	}
	defer release()

	turnCtx, cancel := context.WithTimeout(ctx, s.turnTTL)
	defer cancel()

	started := time.Now()
	res, err := s.runtime.Resume(turnCtx, ports.ResumeInput{
		WorkspaceID:    b.WorkspaceID,
		ConversationID: ca.ConversationID,
		MessageID:      messageID,
	})
	s.logTurn("resume", b, ca, res, err, started)

	// A refused resume is not a failure of this integration and must not
	// read like one. The runtime refuses with an ordinary invalid-input
	// error whenever the named turn is not the conversation's last, is not
	// an answer, or did not stop in a resumable way — which is exactly
	// what a second press looks like.
	if err != nil && res == nil && errors.Is(err, ports.ErrNotResumable) {
		return s.say(ctx, c.TelegramChatID, copyResumeRefused)
	}
	return s.deliver(ctx, c.TelegramChatID, res, err)
}
