package app

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

// Safe resume: continuing a turn that stopped with work already done.
//
// ══════════════════════════════════════════════════════════════════════
//
//	CONTINUE THE PENDING WORK; NEVER REPEAT AN EXECUTED WRITE
//
// ══════════════════════════════════════════════════════════════════════
//
// ── The incident ───────────────────────────────────────────────────────
// A turn created a Room and an Artifact and then hit the round ceiling
// before adding the two items it had been asked for. The user typed "Try
// again". That went in as an ORDINARY USER MESSAGE, which is both what the
// product offered and the worst possible instruction: "again" means start
// over. The next turn created a second Room and a second Artifact, and the
// first pair is still there, orphaned.
//
// Two things were missing and this file adds both.
//
//  1. A way to say CONTINUE rather than REDO. There was none: the error
//     banner offered "close", so the only route back was typing into the
//     composer, and anything typed there is a new question.
//  2. Enough information to continue with. The receipt proved two creates
//     had run; the capabilities are Confidential so their payloads were
//     never replayed, and the interrupted prose was cut off before it named
//     anything. See domain/effect_ref.go, which is the other half.
//
// ── What a resume is, precisely ────────────────────────────────────────
// It is a SECOND ATTEMPT AT THE SAME QUESTION, not a new one. No user
// message is written, because the user did not ask anything new: the
// original question is still in the window and is still what is being
// answered. What the new turn gains is a block saying what the interrupted
// attempt already accomplished, what it did not, and why it stopped.
//
// ── Why it is not Palace-specific, and must not become so ──────────────
// Nothing here names a module, a capability or an entity type. It reads the
// audit trail, which records effects the same way for every capability in
// the product, and it renders them in the vocabulary the receipt already
// uses. A finance import interrupted halfway would resume by the same path.

/* ── what a resume is allowed to continue ────────────────────────────── */

// ResumableFinish is the set of terminal reasons a turn may be resumed from.
//
// ── Why the ceiling ────────────────────────────────────────────────────
// Because the ceiling is the one terminal that is guaranteed to have left
// work both DONE and PENDING, by construction: it only fires after at least
// one round has executed, and it fires precisely because the model asked
// for more. That is the shape a resume is for.
//
// ── Why the deadline joined it ─────────────────────────────────────────
// This set said `tool_round_limit` and nothing else, and the sentence that
// excluded everything else read: "an aborted turn stopped because the user
// wanted it stopped." That premise was true of the value and false of the
// turns wearing it. R1 measured 18 turns recorded as `aborted` in the live
// database and found 14 of them were a 30-second router deadline killing a
// turn that was working — one of them three writes deep, whose operator
// then typed "Resposta interrompida. continue" into the composer, which is
// the exact route this file exists to remove.
//
// So the fix is not to widen `aborted`. It is that a deadline is not an
// abort, and now says so: see domain.FinishDeadline. Nobody chose it, the
// work may be half done, and everything a safe continuation needs —
// receipt, effect refs, the original question — was already being persisted
// on that path.
//
// ── What stays out, and why each ───────────────────────────────────────
// `aborted` stays out and must: it is a person or a closed tab, and a turn
// nobody is waiting for should not offer to finish itself. A gateway
// failure may have left the model mid-thought about work it never
// described. A budget refusal is a limit doing its job, and resuming past
// it would walk around the thing the user set. Widening this set further is
// a decision, not a convenience.
func ResumableFinish(reason domain.FinishReason) bool {
	return reason == domain.FinishToolRoundLimit || reason == domain.FinishDeadline
}

// ResumeContext is what the continuing turn is told about the attempt it
// continues.
//
// Every field comes from execution records. Nothing here is derived from
// what the model said, and nothing carries a payload.
type ResumeContext struct {
	// MessageID is the interrupted assistant turn. The link the brief asks
	// for: a resume is bound to one attempt, not to "whatever happened
	// recently".
	MessageID uuid.UUID
	// Reason is why that attempt stopped, in the runtime's own vocabulary.
	Reason domain.FinishReason
	// Receipt is that turn's writes, with their effect refs. It is the
	// receipt, unmodified — the same value the API and the interface show.
	Receipt domain.WriteReceipt
}

/* ── the read ────────────────────────────────────────────────────────── */

// ResumeInput asks to continue one interrupted turn.
type ResumeInput struct {
	WorkspaceID    uuid.UUID
	ConversationID uuid.UUID
	// MessageID is the turn to continue. Required, and checked against the
	// conversation's actual last turn: a resume that could name any past
	// message would be a way to replay history out of order.
	MessageID uuid.UUID
}

// ResumeTurn answers the same question again, knowing what the first
// attempt already did.
//
// ── What it refuses, and why each refusal matters ──────────────────────
// A turn that is not the last one: the conversation has moved on, and
// continuing a stale attempt would act on a plan the user has since
// changed. A turn that did not stop in a resumable way: see
// ResumableFinish. A conversation whose last turn is the user's: there is
// nothing to continue, and the ordinary path already handles it.
func (s *Service) ResumeTurn(ctx context.Context, in ResumeInput, sink TurnSink) (*domain.Message, error) {
	conv, err := s.repos.Conversations.FindByID(ctx, in.WorkspaceID, in.ConversationID)
	if err != nil {
		return nil, err
	}
	agent, err := s.repos.Agents.FindByID(ctx, in.WorkspaceID, conv.AgentID)
	if err != nil {
		return nil, err
	}
	provider, err := s.repos.Providers.FindByID(ctx, in.WorkspaceID, agent.ProviderID)
	if err != nil {
		return nil, err
	}
	creds, err := s.credentialsFor(provider)
	if err != nil {
		return nil, err
	}

	// The window is read before anything is decided, because every check
	// below is about where the named turn sits in it.
	history, err := s.repos.Messages.ListRecent(ctx, in.WorkspaceID, conv.ID, agent.HistoryLimit)
	if err != nil {
		return nil, err
	}
	target, question, err := resumeTarget(history, in.MessageID)
	if err != nil {
		return nil, err
	}

	// The budget is checked exactly as it is for a new turn. A resume is a
	// paid provider call and must not be a way around a limit.
	gate, err := s.budgetPreflight(ctx, agent, creds, domain.TurnCeiling{
		MaxOutputTokens: agent.MaxTokens,
	})
	if err != nil {
		return nil, err
	}
	if ev := gate.evaluate(); ev.Blocked {
		return nil, budgetRefusal(ev)
	}

	// The interrupted turn's own receipt, read from the audit trail rather
	// than reconstructed. This is where the effect refs come from.
	receipts, err := s.repos.ToolCalls.WriteReceiptsFor(ctx, in.WorkspaceID, []uuid.UUID{target.ID})
	if err != nil {
		return nil, err
	}

	return s.answerTurn(ctx, answerInput{
		Conv:  conv,
		Agent: agent,
		Creds: creds,
		Gate:  gate,
		// The ORIGINAL question. No new user message is written: nobody
		// asked anything new, and writing one would be the "Try again"
		// mistake with better manners.
		Current: question,
		Resume: &ResumeContext{
			MessageID: target.ID,
			Reason:    domain.FinishReason(target.FinishReason),
			Receipt:   receipts[target.ID],
		},
	}, sink)
}

// resumeTarget finds the interrupted turn and the question it was
// answering, refusing everything that is not a clean continuation.
func resumeTarget(history []domain.Message, want uuid.UUID) (target, question *domain.Message, err error) {
	if len(history) == 0 {
		return nil, nil, domain.Invalid("this conversation has no turns to continue")
	}
	last := &history[len(history)-1]
	if last.ID != want {
		// Named a turn that is not the last one. Either the conversation
		// moved on or the client is confused; both are the caller's to fix,
		// and neither is something to resolve by guessing.
		return nil, nil, domain.Invalid("only the conversation's last turn can be continued")
	}
	if last.Role != domain.RoleAssistant {
		return nil, nil, domain.Invalid("the last turn is not an answer, so there is nothing to continue")
	}
	if !ResumableFinish(domain.FinishReason(last.FinishReason)) {
		return nil, nil, domain.Invalid("this turn did not stop in a way that can be continued")
	}
	// The question is the user turn immediately before it. Walking back
	// rather than assuming index-1 keeps this correct for a window whose
	// oldest entry happens to be an answer.
	for i := len(history) - 2; i >= 0; i-- {
		if history[i].Role == domain.RoleUser {
			return last, &history[i], nil
		}
	}
	return nil, nil, domain.Invalid("the interrupted turn has no question to continue")
}

/* ── what the model is told ──────────────────────────────────────────── */

// RenderResumeBlock is the exact text a continuing turn receives.
//
// ── Why the guidance lives here and not in the grounding policy ────────
// Because it applies to a rare kind of turn and is useless on every other.
// The policy sits inside the cached prefix and is paid for by every agent
// with capabilities, on every turn of its life; this block is built only
// when a resume actually happens. Putting resume rules in the policy would
// charge every conversation in the product for a paragraph about a
// situation most of them will never be in.
//
// ── What it may and may not say ────────────────────────────────────────
// It says which capabilities ran and which entities they touched, in the
// same vocabulary the receipt uses, and it says what was refused and why.
// It says nothing about what any of it is called or contains, because it
// has nothing: the refs are types and UUIDs and the payloads were never
// kept. The instruction to READ is not politeness — it is the only way the
// model can learn anything about state, and saying so plainly is what stops
// it inventing the rest.
func RenderResumeBlock(rc ResumeContext) string {
	var b strings.Builder
	b.WriteString(resumeHeader)

	executed := make([]domain.WriteExecution, 0, len(rc.Receipt.Writes))
	pending := make([]domain.WriteExecution, 0, len(rc.Receipt.Writes))
	for _, w := range rc.Receipt.Writes {
		switch w.Status {
		case domain.WriteExecuted:
			executed = append(executed, w)
		default:
			pending = append(pending, w)
		}
	}

	b.WriteString("\n\nAlready done — do NOT do any of this again:")
	if len(executed) == 0 {
		// Stated, not omitted. An empty section reads as "nothing was
		// completed", which is a fact the model needs; a missing section
		// reads as "unknown", which invites it to redo everything.
		b.WriteString("\n- nothing was completed")
	}
	for _, w := range executed {
		b.WriteString("\n- ")
		b.WriteString(string(domain.WriteExecuted))
		b.WriteByte(' ')
		b.WriteString(w.Capability.String())
		if w.Ref != nil {
			b.WriteString(" ref=")
			b.WriteString(w.Ref.String())
		}
	}

	b.WriteString("\n\nNot done — this is the work that is still pending:")
	if len(pending) == 0 {
		b.WriteString("\n- nothing was refused or failed")
	}
	for _, w := range pending {
		b.WriteString("\n- ")
		b.WriteString(string(w.Status))
		b.WriteByte(' ')
		b.WriteString(w.Capability.String())
		if w.ErrorCode != "" {
			b.WriteByte(' ')
			b.WriteString(string(w.ErrorCode))
		}
	}

	b.WriteString("\n\nThe attempt stopped because: ")
	b.WriteString(string(rc.Reason))
	b.WriteString(".")
	return b.String()
}

// resumeHeader states what the block is and the four rules that make a
// continuation safe. Generic: it names no module and no capability.
const resumeHeader = `You are CONTINUING a previous attempt at the user's last request, not starting it over. The attempt below was interrupted after it had already changed things.

Do not repeat any write listed as executed: those changes exist. Do the work that is still pending, and only that. A "ref=" identifies a thing that already exists — use it to act on that thing, and read it with a capability when you need to know anything ABOUT it. This list proves what ran; it carries no titles and no contents, so never describe or restate anything from it without reading first. If a completed write has no ref, find what it produced with a read capability rather than creating a second one.`

/* ── the block ───────────────────────────────────────────────────────── */

// resumeBlockOf is what the assembler appends. Kept beside the renderer so
// the two cannot drift about when a block is emitted at all.
func resumeBlockOf(rc *ResumeContext) (string, bool) {
	if rc == nil {
		return "", false
	}
	return RenderResumeBlock(*rc), true
}

// Compile-time assurance that a resume goes through the same credential
// path as any other turn.
var _ = func(c ports.Credentials) ports.Credentials { return c }
