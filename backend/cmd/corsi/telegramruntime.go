package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	chatapp "github.com/corsi/backend/internal/chat/app"
	chatdomain "github.com/corsi/backend/internal/chat/domain"
	telegramports "github.com/corsi/backend/internal/integrations/telegram/ports"
	"github.com/corsi/backend/internal/platform/workspace"
)

/* ── the one arrow that would be a cycle anywhere else ───────────────── */

// ══════════════════════════════════════════════════════════════════════
//
//	THE COMPOSITION ROOT'S PRIVILEGE, USED IN THE OTHER DIRECTION
//
// ══════════════════════════════════════════════════════════════════════
//
// This file is the only place in the repository that names both Agents and
// Telegram. That is the same privilege main.go already exercises for
// GitHub — but the arrow points the other way, and the difference is worth
// stating:
//
//	GitHub    Agents declares chat/ports.Tool. GitHub implements it.
//	          Agents is DRIVEN. The integration is a capability.
//
//	Telegram  Telegram declares telegram/ports.Runtime. THIS FILE
//	          implements it, over the Agents application service.
//	          Agents DRIVES nothing; it is driven, by a phone.
//
// In both cases the interface belongs to the consumer and neither package
// imports the other. `internal/integrations/telegram` contains no
// reference to `internal/chat`, and `internal/chat` has never heard of
// Telegram.
//
// ── Why this adapter is deliberately dumb ──────────────────────────────
// It maps types and nothing else. Every judgement it might be tempted to
// make — is this turn resumable, did anything get written, was anything
// read externally — is asked of the Agents module instead, because a copy
// of any of those rules here is a copy that will one day disagree with the
// original. `ResumableFinish` is the clearest case: it is a policy that
// has already changed once (`deadline` joined `tool_round_limit` on
// evidence), and a second implementation would have kept offering the old
// answer.

// telegramRuntime adapts the Agents application service to the interface
// the Telegram integration declared.
type telegramRuntime struct {
	svc chatService
	log *slog.Logger
}

// chatService is the slice of the Agents service this adapter uses.
//
// An interface rather than *chatapp.Service so the set of methods a
// messaging surface reaches is written down in one place and cannot grow
// by accident: a future change that wanted, say, memory consolidation from
// Telegram would have to add a line here, in front of a reviewer.
type chatService interface {
	ListAgents(ctx context.Context, workspaceID uuid.UUID, limit, offset int) ([]chatdomain.Agent, error)
	CreateConversation(ctx context.Context, in chatapp.CreateConversationInput) (*chatdomain.Conversation, error)
	GetConversation(ctx context.Context, workspaceID, id uuid.UUID) (*chatdomain.Conversation, error)
	SendMessage(ctx context.Context, in chatapp.SendMessageInput, sink chatapp.TurnSink) (*chatdomain.Message, error)
	ResumeTurn(ctx context.Context, in chatapp.ResumeInput, sink chatapp.TurnSink) (*chatdomain.Message, error)
	WriteReceipts(ctx context.Context, workspaceID uuid.UUID, messageIDs []uuid.UUID) (map[uuid.UUID]chatdomain.WriteReceipt, error)
	ReadReceipts(ctx context.Context, workspaceID, conversationID uuid.UUID, messageIDs []uuid.UUID) (map[uuid.UUID]chatdomain.ReadReceipt, error)
}

func newTelegramRuntime(svc chatService, log *slog.Logger) *telegramRuntime {
	return &telegramRuntime{svc: svc, log: log}
}

// agentPageSize bounds the agent list a phone is offered.
//
// Not pagination: a person choosing from an inline keyboard is not going
// to page. It is a sane ceiling on a list that in this product is single
// digits, and a workspace that ever exceeds it has a product problem this
// number will make visible.
const agentPageSize = 50

func (r *telegramRuntime) ListAgents(ctx context.Context, workspaceID uuid.UUID) ([]telegramports.Agent, error) {
	agents, err := r.svc.ListAgents(r.scoped(ctx, workspaceID), workspaceID, agentPageSize, 0)
	if err != nil {
		return nil, err
	}
	out := make([]telegramports.Agent, 0, len(agents))
	for _, a := range agents {
		out = append(out, telegramports.Agent{ID: a.ID, Name: a.Name, Description: a.Description})
	}
	return out, nil
}

func (r *telegramRuntime) CreateConversation(ctx context.Context, workspaceID, agentID uuid.UUID, title string) (uuid.UUID, error) {
	conv, err := r.svc.CreateConversation(r.scoped(ctx, workspaceID), chatapp.CreateConversationInput{
		WorkspaceID: workspaceID,
		AgentID:     agentID,
		Title:       title,
	})
	if err != nil {
		return uuid.Nil, err
	}
	return conv.ID, nil
}

// ConversationExists asks whether a stored mapping still points at
// something.
//
// A not-found is a VALUE here, not an error: the Telegram integration
// stores conversation ids with no foreign key, so a thread deleted in the
// web client is an ordinary, expected state, and the caller turns it into
// a new conversation. Anything that is not a not-found passes through as
// a real failure, because "the database is down" and "you deleted that
// thread" must not produce the same behaviour.
func (r *telegramRuntime) ConversationExists(ctx context.Context, workspaceID, conversationID uuid.UUID) (bool, error) {
	_, err := r.svc.GetConversation(r.scoped(ctx, workspaceID), workspaceID, conversationID)
	if err == nil {
		return true, nil
	}
	var de *chatdomain.Error
	if errors.As(err, &de) && de.Kind == chatdomain.KindNotFound {
		return false, nil
	}
	return false, err
}

// scoped stamps the workspace into the context a turn will run under.
//
// ══════════════════════════════════════════════════════════════════════
//
//	A DRIVING ADAPTER MUST STAMP THE WORKSPACE, AS THE MIDDLEWARE DOES
//
// ══════════════════════════════════════════════════════════════════════
//
// ── The defect this closes, found by the first real write ──────────────
// A capability resolves its workspace from the CONTEXT, not from the
// application input: see palace/tools.workspaceOf, and the identical
// function in github, jobradar, threads and metathreads. Its own comment
// describes the chain as "unbroken from the request through the SSE
// handler and the turn loop into this executor" — a sentence about the
// only transport that existed when it was written.
//
// Telegram has no request. The first real `palace.artifact.create` from a
// phone therefore refused with "this call arrived without a workspace",
// having passed the binding, the grant and the model perfectly. Passing
// WorkspaceID in SendMessageInput was never enough: that field scopes the
// REPOSITORY reads, and the tool executor never sees it.
//
// ── Why the refusal was the right failure ──────────────────────────────
// Worth stating, because it is the reason this was a bug and not an
// incident: the tool refused rather than defaulting. There is no fallback
// workspace that is not somebody's real record, so a missing one is a
// refusal by design — and the WriteReceipt reported FAILED, so the
// product never claimed the note existed. The system told the truth about
// a thing it could not do.
//
// ── Why here and not in the turn loop ──────────────────────────────────
// Because this is the seam that REPLACES the middleware for this
// transport, and the symmetry should be visible: HTTP stamps in
// workspace.Middleware, Telegram stamps here. Stamping inside the Agents
// turn loop from conv.WorkspaceID would fix every future driving adapter
// at once and is the better long-term answer — it is recommended
// separately rather than smuggled into a bug fix, because it changes a
// module whose T1 scope is already approved.
//
// It cannot introduce a mismatch: the conversation was already fetched
// under this exact workspace, so the value stamped is the value that
// scoped the read.
func (r *telegramRuntime) scoped(ctx context.Context, workspaceID uuid.UUID) context.Context {
	return workspace.WithWorkspaceID(ctx, workspaceID)
}

func (r *telegramRuntime) Send(ctx context.Context, in telegramports.SendInput) (*telegramports.TurnResult, error) {
	ctx = r.scoped(ctx, in.WorkspaceID)
	msg, err := r.svc.SendMessage(ctx, chatapp.SendMessageInput{
		WorkspaceID:    in.WorkspaceID,
		ConversationID: in.ConversationID,
		Content:        in.Content,
	}, discardSink{})
	return r.result(ctx, in.WorkspaceID, in.ConversationID, msg, err)
}

func (r *telegramRuntime) Resume(ctx context.Context, in telegramports.ResumeInput) (*telegramports.TurnResult, error) {
	ctx = r.scoped(ctx, in.WorkspaceID)
	msg, err := r.svc.ResumeTurn(ctx, chatapp.ResumeInput{
		WorkspaceID:    in.WorkspaceID,
		ConversationID: in.ConversationID,
		MessageID:      in.MessageID,
	}, discardSink{})
	if msg == nil && err != nil {
		// ── Telling a refusal from a fault ──────────────────────────
		//
		// ResumeTurn refuses with an ordinary invalid-input error when the
		// named turn is not the conversation's last, is not an answer, or
		// did not stop in a resumable way — which is exactly what pressing
		// [Continuar] a second time looks like. That is the ORDINARY
		// outcome and must read as "that turn is finished", never as
		// "something broke".
		//
		// The Telegram package cannot make this distinction itself without
		// importing the Agents error vocabulary, so the adapter that
		// already knows both makes it, and expresses it in the sentinel
		// the port declares.
		var de *chatdomain.Error
		if errors.As(err, &de) && de.Kind == chatdomain.KindInvalid {
			return nil, fmt.Errorf("%w: %s", telegramports.ErrNotResumable, de.Message)
		}
	}
	return r.result(ctx, in.WorkspaceID, in.ConversationID, msg, err)
}

// result assembles the turn's outcome AND its evidence.
//
// ══════════════════════════════════════════════════════════════════════
//
//	NO TOOL RECEIPT, NO EXECUTION CLAIM · READ PROVES STATE
//
// ══════════════════════════════════════════════════════════════════════
//
// The receipts are read here rather than left to the caller, for the
// reason the SSE `done` frame carries them rather than letting the browser
// ask: a surface that has to remember to fetch evidence is a surface that
// will one day render prose without it. That is not hypothetical — it is
// the defect Agents 1.4.0 and 1.6.0 each closed, in a product where every
// layer below was behaving correctly.
//
// ── Why a failed turn still returns a result ───────────────────────────
// Because `done` means "here is what was written", not "the turn
// succeeded". SendMessage returns a persisted message AND a terminal error
// together whenever a turn dies after having already done something: the
// ceiling, a budget refusal, a gateway dropping mid-stream. In all of
// those the message is written, the audit rows are written, and the tools
// that ran really ran. Dropping the message and reporting only the error
// is how executions become real, recorded and invisible.
func (r *telegramRuntime) result(ctx context.Context, workspaceID, conversationID uuid.UUID, msg *chatdomain.Message, turnErr error) (*telegramports.TurnResult, error) {
	if msg == nil {
		return nil, turnErr
	}
	out := &telegramports.TurnResult{
		MessageID:    msg.ID,
		Text:         msg.Content,
		FinishReason: msg.FinishReason,
		// Asked of the Agents module, never re-derived. This predicate has
		// already changed once on evidence, and a copy here would have
		// kept answering the old way.
		Resumable: chatapp.ResumableFinish(chatdomain.FinishReason(msg.FinishReason)),
	}
	if turnErr != nil {
		out.Error = turnErr.Error()
	}

	// ── Detached from the caller's cancellation ─────────────────────
	//
	// The turn may have ended because its deadline expired, which cancels
	// the context it ran under. The receipts are about what ALREADY
	// happened and are the part that matters most on exactly that path —
	// a turn killed mid-work is the one whose writes the operator most
	// needs accounted for.
	evCtx := context.WithoutCancel(ctx)

	if got, err := r.svc.WriteReceipts(evCtx, workspaceID, []uuid.UUID{msg.ID}); err == nil {
		w := got[msg.ID]
		out.WritesExecuted, out.WritesFailed, out.WritesRefused = w.Executed, w.Failed, w.Refused
	} else {
		// Not fatal to the answer, and loudly not silent. The turn is
		// persisted and the operator should still receive it; what is lost
		// is the footer, and the log says so.
		r.log.Warn("telegram runtime: write receipt", "message_id", msg.ID, "err", err)
	}
	if got, err := r.svc.ReadReceipts(evCtx, workspaceID, conversationID, []uuid.UUID{msg.ID}); err == nil {
		rr := got[msg.ID]
		out.ExternalReadStatus = string(rr.Status)
		out.ExternalReadAvailable = rr.Available
	} else {
		r.log.Warn("telegram runtime: read receipt", "message_id", msg.ID, "err", err)
	}
	return out, turnErr
}

// discardSink is the TurnSink a non-streaming caller needs.
//
// ── Why discarding deltas is correct here and not a shortcut ───────────
// The sink is the LIVE channel: it exists so a reader watching a stream
// can see tokens as they arrive and can tell a running tool from a hung
// gateway. Telegram shows one message when the turn is done, so there is
// no live reader and nothing to feed.
//
// Nothing is lost by discarding. The sink is not how the answer is
// recorded — `persistAssistant` writes the message from the runtime's own
// accumulated state — so the persisted turn is byte-identical whether the
// consumer watched it or not. Returning nil from every method also says
// "the consumer is still here", which is what keeps the turn from being
// treated as abandoned.
type discardSink struct{}

func (discardSink) Reasoning(string) error       { return nil }
func (discardSink) Delta(string) error           { return nil }
func (discardSink) Tool(chatapp.ToolEvent) error { return nil }

// compile-time proof that the adapter satisfies the port and that the
// sink satisfies the runtime's.
var (
	_ telegramports.Runtime = (*telegramRuntime)(nil)
	_ chatapp.TurnSink      = discardSink{}
)
