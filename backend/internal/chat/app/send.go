package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

// TurnSink is the driving side of a streamed turn: the transport tells the
// caller what arrived, as it arrives. The HTTP adapter implements it over
// Server-Sent Events.
//
// Either method returning an error means the consumer is gone (a closed
// browser tab, a dropped connection). That ends the turn, but not the
// bookkeeping: whatever text arrived is still persisted.
//
// Reasoning and Delta are separate methods rather than one tagged call
// because the consumer renders them differently — one is a collapsible
// aside, the other is the answer.
type TurnSink interface {
	Reasoning(text string) error
	Delta(text string) error
	// Tool announces a tool call starting and then finishing.
	//
	// It exists because a tool round is dead air on the wire: the model
	// stops talking, the backend runs something, and the reader sees a
	// stalled stream with no way to tell it from a hung gateway. This is the
	// only channel that can say which of the two is happening while it is
	// happening — the persisted record explains it afterwards, which is too
	// late to be an interface.
	//
	// Additive: a turn with no tools emits none of these, so the frames a
	// pre-tools client knows about are exactly the frames it receives.
	Tool(ev ToolEvent) error
}

// ToolEvent is one tool call's progress, as the reader sees it.
//
// No arguments and no result. The transcript needs to say what is running
// and how it went; the payloads are in the audit trail, behind a read the
// user asks for, which is the boundary that lets that record be redactable
// later without a live frame having already published it.
type ToolEvent struct {
	CallID string `json:"call_id"`
	Name   string `json:"name"`
	// Status is "running" when the call starts, then the call's own
	// outcome: "ok", "error", or "not_executed" for one the runtime
	// refused before it ran.
	//
	// It carries the SAME vocabulary the audit row does, on purpose. The
	// live frame and the reloaded transcript are two records of one event,
	// and a refusal that read `error` here and `not_executed` there would
	// make them disagree about what happened.
	Status     string `json:"status"`
	ErrorCode  string `json:"error_code,omitempty"`
	DurationMS int    `json:"duration_ms,omitempty"`
}

const toolStatusRunning = "running"

type SendMessageInput struct {
	WorkspaceID    uuid.UUID
	ConversationID uuid.UUID
	Content        string
	// References is the capability selection the user attached to this turn.
	//
	// Nil and empty are the SAME input and both mean "no explicit
	// selection": the turn behaves exactly as it did before this field
	// existed, declaring every authorized tool. Telling the two apart would
	// create an invisible difference between a client that omits the field
	// and one that sends `[]`, which is a distinction nobody could see and
	// everybody would eventually get wrong.
	//
	// A non-empty selection NARROWS. It never grants: see app/references.go.
	References []domain.TurnReference
	// ContextReferences are the ENTITIES this turn is about.
	//
	// A different field from References because it is a different fact, and
	// the two must never be merged: that one narrows capabilities, this one
	// narrows nothing and grants nothing. Every entry is resolved against
	// this workspace before the turn is written — an id the workspace
	// cannot see refuses the request. See app/context_references.go.
	ContextReferences []domain.ContextReference
}

// persistTimeout bounds the write-back that happens after a turn ends. It
// runs on a context detached from the request, so it needs a deadline of
// its own or a stalled database would leak the goroutine.
const persistTimeout = 10 * time.Second

// maxToolRounds is how many times one turn may ask for tools.
//
// ── Why there is a ceiling at all ──────────────────────────────────────
// A tool result goes back to the model, and the model may answer with
// another tool call. Nothing in that cycle terminates on its own: a model
// that misreads a result can ask for the same thing forever, and every
// iteration is a paid provider call with a larger prompt than the last.
// Without a ceiling the failure mode is not a wrong answer, it is a bill.
//
// ── What the number actually buys, measured ────────────────────────────
// `round` counts PROVIDER CALLS, from 1, and the ceiling is checked after
// the model asks for tools and before they run. So four permits:
//
//	provider call 1  asks for tools   1 >= 4 is false  → tools RUN
//	provider call 2  asks for tools   2 >= 4 is false  → tools RUN
//	provider call 3  asks for tools   3 >= 4 is false  → tools RUN
//	provider call 4  asks for tools   4 >= 4 is TRUE   → the turn stops
//
// Four provider calls, and at most THREE rounds of tool execution.
//
// ── Why it was three, and why three was not enough ─────────────────────
// Three permitted two execution rounds, which is ONE indirection: find the
// thing, then act on it. A live battery showed that the most ordinary
// operation a knowledge base has does not fit in one indirection. "Mark
// the rice as bought" is
//
//	list the artifacts → read the one that matches → update the item
//
// three DEPENDENT steps, because the item's id does not exist until the
// second call has returned. The third was refused every time, and the same
// shape refused `relation.create` outright: both endpoints must exist
// before the relation can name them, so creating them consumed the budget
// that the relation itself needed. Zero relations were created in the
// entire battery.
//
// The correction matters and is worth stating exactly: the limit was never
// about VOLUME. The same battery watched a single round carry five
// independent `item.add` calls, because a model emits independent calls in
// parallel. What it bounds is DEPTH — how many times the model may look at
// a result before deciding the next call. Three rounds buys two
// indirections: discover, read, act.
//
// ── Why four and not more ──────────────────────────────────────────────
// Because four is what the observed failures need and nothing beyond them
// asked for more. The worst case stays knowable before it happens: four
// times `max_tokens` of output, plus a prompt that grows by the tool
// results. Raising it further would be buying depth nobody has shown a use
// for, and every round is a paid provider call with a larger prompt than
// the last.
//
// It is a constant, not a setting. A per-agent knob would be a number the
// user has no basis to choose and every reason to raise, which is the
// wrong direction for a limit whose purpose is to be reached rarely and
// noticed when it is. If evidence appears that real work needs more, the
// evidence is the reason to change the constant — which is exactly what
// happened here.
const maxToolRounds = 4

// SendMessage records the user's turn, streams the model's reply into sink,
// and records that too.
//
// ── The turn is a loop now ─────────────────────────────────────────────
// It used to be one provider call. With tools it is one *or more*: the
// model may answer, or it may ask to run something and answer afterwards.
// The backend is the orchestrator of that loop and the sole authority on
// what may run — the gateway is asked to complete text, never to decide
// permissions.
//
//	context → provider → tool calls? ── no ──→ answer
//	                          │ yes
//	                          ↓
//	              validate · authorize · execute
//	                          ↓
//	                    results → provider → …
//
// An agent with no authorized tools never enters the loop a second time and
// sends a request body identical to the one it sent before tools existed.
//
// The answer is persisted whatever happens — a clean finish, a provider
// error mid-stream, or the reader hanging up. A half-written reply that
// vanished on refresh would be worse than one stored with the reason it
// stopped, so the write-back runs on a context detached from the request.
//
// Errors returned before the first Delta are ordinary request failures and
// the transport can still answer with a status code. After that the headers
// are long gone, and the transport reports the failure inside the stream.
func (s *Service) SendMessage(ctx context.Context, in SendMessageInput, sink TurnSink) (*domain.Message, error) {
	content := strings.TrimSpace(in.Content)
	if content == "" {
		return nil, domain.Invalid("content required")
	}

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

	// ── The turn's capability scope ─────────────────────────────────
	//
	// Resolved here, before anything is written and before the budget is
	// even consulted, because an impossible selection is a bad request and
	// a bad request must not leave a turn behind. Nothing has happened yet:
	// no row, no provider call, no spend.
	//
	// The read is issued only for a turn that made a selection. A turn
	// without one takes the same path it always took — the authorization
	// read still happens further down, after the question is persisted, so
	// a failure there still degrades the same way it did before. Hoisting
	// it unconditionally would have moved that failure to the other side of
	// the write for every turn in the system, to serve the turns that do
	// not have a selection.
	var (
		toolDefs []domain.ToolDefinition
		selected []domain.TurnReference
		// withheld is how many authorized tools this turn deliberately did
		// not declare. Reported in the context report so the Inspector can
		// say "3 of 7" without holding names and without asking the grants
		// as they stand today, which is a different question.
		withheld int
	)
	if len(in.References) > 0 {
		authorized, err := s.authorizedTools(ctx, in.WorkspaceID, agent.ID)
		if err != nil {
			return nil, err
		}
		scoped, refs, err := s.scopeTools(authorized, in.References)
		if err != nil {
			return nil, err
		}
		toolDefs, selected, withheld = scoped, refs, len(authorized)-len(scoped)
	}

	// ── The turn's subjects ─────────────────────────────────────────
	//
	// Admitted here, beside the capability scope and for the same reason:
	// an attachment this workspace cannot resolve is a bad request, and a
	// bad request must not leave a turn behind. Nothing has been written
	// yet.
	//
	// This is the ONLY door: a reference that is not admitted here never
	// reaches storage, never reaches the model, and never reaches a
	// renderer. Which is what makes the isolation argument a single
	// paragraph rather than an audit of every read.
	attached, err := s.admitContextReferences(ctx, in.WorkspaceID, in.ContextReferences)
	if err != nil {
		return nil, err
	}

	// ── Budget preflight ────────────────────────────────────────────
	//
	// Before the question is written and before the provider is called.
	//
	// The order is deliberate and is the opposite of the one used for
	// gateway failures. A gateway failure is not the user's doing, so their
	// question is persisted and the thread keeps a record of what happened.
	// A budget refusal is this system's own decision, taken on information
	// it had before the turn started — so nothing is written, the thread is
	// left exactly as it was, and the question goes back to the composer
	// that still holds it.
	//
	// The ceiling handed over has a real output side (agent.MaxTokens is a
	// hard limit the gateway honours) and no input side: the prompt
	// estimate needs a context, and a context needs the question persisted.
	// See domain.BudgetEvaluation.NextTurnFits for what that makes it mean.
	//
	// What comes back is a gate rather than a verdict, because with tools a
	// turn can call the provider more than once and every one of those calls
	// has to pass. See turnGate.
	gate, err := s.budgetPreflight(ctx, agent, creds, domain.TurnCeiling{
		MaxOutputTokens: agent.MaxTokens,
	})
	if err != nil {
		return nil, err
	}
	if ev := gate.evaluate(); ev.Blocked {
		return nil, budgetRefusal(ev)
	}

	userMsg := &domain.Message{
		ID:             uuid.New(),
		WorkspaceID:    in.WorkspaceID,
		ConversationID: conv.ID,
		Role:           domain.RoleUser,
		Content:        content,
		// Frozen with the turn, and only when the user actually made a
		// selection. Nil here is the legacy record, indistinguishable from
		// every turn written before this column existed — which is correct,
		// because they mean the same thing.
		References: selected,
		// The subjects THIS turn attached, as the provider named them at
		// this moment. The thread's own subjects are not copied here: they
		// belong to the conversation, and duplicating them onto every row
		// would make a later edit of one of them disagree with the other.
		ContextReferences: attached,
	}
	if err := userMsg.Validate(); err != nil {
		return nil, err
	}
	if err := s.repos.Messages.Create(ctx, userMsg); err != nil {
		return nil, err
	}

	// First turn in an untitled thread names it. Best-effort: a failed
	// rename is a cosmetic problem and must not cost the user their answer.
	if conv.Title == "" {
		if _, err := s.repos.Conversations.Rename(ctx, in.WorkspaceID, conv.ID, domain.DeriveTitle(content)); err != nil {
			s.log.Warn("derive conversation title", "conversation_id", conv.ID, "err", err)
		}
	}

	return s.answerTurn(ctx, answerInput{
		Conv:     conv,
		Agent:    agent,
		Creds:    creds,
		Gate:     gate,
		Current:  userMsg,
		Attached: attached,
		ToolDefs: toolDefs,
		Scoped:   len(in.References) > 0,
		Withheld: withheld,
	}, sink)
}

// answerTurn is everything a turn does once the question it answers is
// settled: read the window, compose the context, drive the loop, persist.
//
// ── Why it is shared ───────────────────────────────────────────────────
// Two entry points reach it. SendMessage settles the question by WRITING
// one; ResumeTurn settles it by pointing at a question that was already
// written and answered badly. Everything after that is identical, and a
// second copy of it would be the place where resume quietly stopped
// carrying memory, or evidence, or the budget gate.
func (s *Service) answerTurn(ctx context.Context, in answerInput, sink TurnSink) (*domain.Message, error) {
	conv, agent, creds := in.Conv, in.Agent, in.Creds
	toolDefs, withheld := in.ToolDefs, in.Withheld

	history, err := s.repos.Messages.ListRecent(ctx, conv.WorkspaceID, conv.ID, agent.HistoryLimit)
	if err != nil {
		return nil, err
	}
	history = withoutInterruptedTurn(history, in.Resume)

	// Read after the question is persisted and before the context is built.
	// A failure in either degrades the turn instead of ending it: see
	// memoriesForTurn and sourcesForTurn.
	memories, memoryUnavailable := s.memoriesForTurn(ctx, conv.WorkspaceID, agent.ID)
	sources, sourcesUnavailable := s.sourcesForTurn(ctx, conv.WorkspaceID, agent.ID)

	// What this conversation's earlier tools observed, bounded by the same
	// window that was just read. Passed the history so the two cannot
	// disagree about which turns are in scope — see evidenceForTurn.
	// The turn's subjects are passed so a stale reading of one of them is
	// not replayed as though it were still true. See dropReadingsOfSubjects.
	//
	// The same read also yields what those turns DID, which is a different
	// question with a different answer and no second query: see turnEvidence.
	subjects := turnContextReferences(conv, in.Attached)
	evidence := s.evidenceForTurn(ctx, conv.WorkspaceID, conv.ID, history, subjects)

	// Tools do NOT degrade, unlike the two reads above. See authorizedTools:
	// carrying on without them would turn a turn that needed a capability
	// into one that guesses instead, and the read that failed is the same
	// read the per-call authorization depends on.
	//
	// Skipped when the turn made a selection, because the scope was already
	// resolved from this same read before anything was written. Reading
	// again would be asking the same question twice and would let a revoke
	// land between the two answers.
	if !in.Scoped {
		toolDefs, err = s.authorizedTools(ctx, conv.WorkspaceID, agent.ID)
		if err != nil {
			return nil, err
		}
	}

	// ── The subjects' present state ─────────────────────────────────
	//
	// After the authorization read and before the context is composed,
	// because it depends on the first and is an input to the second. It is
	// handed `toolDefs` rather than issuing its own grant read: hydration is
	// authorized by the same grants, resolved once, so it and the tool
	// executor cannot end this turn disagreeing about what was permitted
	// when it started. See hydrateSubjects.
	//
	// This is the whole answer to the freshness failure. The history may
	// still contain the model's own earlier sentence about this subject; what
	// changes is that the turn no longer depends on the model deciding to go
	// and check. The present is simply present.
	hydrated := s.hydrateSubjects(ctx, conv.WorkspaceID, agent.ID, conv.ID, subjects, toolDefs)

	// What this turn sends is the Context Builder's decision, not this
	// function's. See context.go: this one orchestrates a turn, that one
	// composes it, and the report is what will let the UI explain it.
	built := BuildContext(ContextInput{
		Agent:               agent,
		Memories:            memories,
		MemoryUnavailable:   memoryUnavailable,
		Sources:             sources,
		SourcesUnavailable:  sourcesUnavailable,
		Tools:               toolDefs,
		ToolsWithheld:       withheld,
		Evidence:            evidence.Tools,
		Execution:           evidence.Execution,
		EvidenceUnavailable: evidence.Unavailable,
		History:             history,
		Current:             in.Current,
		// Present only on a resume, and it is the block that tells the model
		// the work is HALF DONE rather than not started. See resume.go.
		Resume: in.Resume,
		// What this turn attached, plus what the thread is about. The merge
		// is why "essa vaga" still resolves on the fourth message of a
		// conversation opened from a card.
		ContextReferences:  subjects,
		HydratedReferences: hydrated,
		// The deployment's answer, not the turn's. Off produces the request
		// body this module sent before caching existed, byte for byte.
		PromptCache: s.promptCache,
	})
	s.log.Debug("context built",
		"conversation_id", conv.ID,
		"messages", len(built.Messages),
		"memories", len(memories),
		"sources", len(sources),
		"tools", len(toolDefs),
		"tools_withheld", withheld,
		"evidence", len(evidence.Tools.Items),
		"execution_turns", len(evidence.Execution.Turns),
		"resume", in.Resume != nil,
		"hydrated_references", len(hydrated),
		"characters", built.Report.TotalCharacters,
		"estimated_tokens", built.Report.TotalEstimatedTokens)

	turn := s.runTurn(ctx, runTurnInput{
		Conversation: conv,
		Agent:        agent,
		Creds:        creds,
		Built:        built,
		Tools:        toolDefs,
		Gate:         in.Gate,
	}, sink)

	if turn.openFailure != nil {
		// The very first call never reached the model. Record the failed turn
		// so the thread shows the question and why it went unanswered, then
		// report it upward — the transport still has a status code to give.
		s.persistAssistant(ctx, conv, agent, creds, turn.assistantTurn)
		return nil, turn.openFailure
	}

	assistant := s.persistAssistant(ctx, conv, agent, creds, turn.assistantTurn)
	return assistant, turn.err
}

// answerInput is what answerTurn needs once the question is settled.
type answerInput struct {
	Conv  *domain.Conversation
	Agent *domain.Agent
	Creds ports.Credentials
	Gate  *turnGate
	// Current is the user turn being answered. On a resume it is the
	// ORIGINAL question, which is already in the window — the resume does
	// not write a new one, because nobody asked anything new.
	Current  *domain.Message
	Attached []domain.ContextReference
	ToolDefs []domain.ToolDefinition
	// Scoped says the caller already resolved the tool set from an explicit
	// selection, so answerTurn must not read the grants a second time.
	Scoped   bool
	Withheld int
	// Resume describes the interrupted turn this one continues. Nil on an
	// ordinary turn, which is every turn the module had before resume
	// existed.
	Resume *ResumeContext
}

// withoutInterruptedTurn drops the attempt a resume is replacing.
//
// ── Why the wire must not carry it ─────────────────────────────────────
// Measured against the real gateway, and it is not a preference:
//
//	400 AnthropicException — "This model does not support assistant
//	message prefill. The conversation must end with a user message."
//
// A resume replays [user, assistant, user, assistant(interrupted)] and
// then appends its block as a SYSTEM message. Providers hoist system
// messages out of the list, so what Anthropic actually receives ends on an
// assistant turn — which it reads as a prefill and refuses outright. Every
// resume failed this way, and the scripted test suite could not see it
// because a fake gateway enforces no such rule.
//
// ── Why dropping it is right and not merely convenient ─────────────────
// The interrupted turn is a HALF-FINISHED ANSWER to the question being
// answered again. Replaying it invites the model to continue its own
// sentence instead of doing the work, which is the very thing the provider
// is guarding against. Everything that attempt actually accomplished is in
// the resume block, stated as facts with effect refs rather than as prose
// that trailed off — which is a better record of it than the prose was.
//
// The TRANSCRIPT is untouched. The interrupted turn stays visible to the
// reader, with its receipt; this only decides what one request replays.
func withoutInterruptedTurn(history []domain.Message, rc *ResumeContext) []domain.Message {
	if rc == nil {
		return history
	}
	out := make([]domain.Message, 0, len(history))
	for _, m := range history {
		if m.ID == rc.MessageID {
			continue
		}
		out = append(out, m)
	}
	return out
}

/* ── the loop ────────────────────────────────────────────────────────── */

type runTurnInput struct {
	Conversation *domain.Conversation
	Agent        *domain.Agent
	Creds        ports.Credentials
	Built        BuiltContext
	Tools        []domain.ToolDefinition
	Gate         *turnGate
}

type runTurnResult struct {
	assistantTurn
	// err is a provider fault the caller reports upward. Nil for a turn that
	// merely ended badly (aborted, round limit) rather than failed.
	err error
	// openFailure is set only when the FIRST provider call never opened, the
	// one case where nothing has been streamed and a status code is still
	// possible.
	openFailure error
}

// runTurn drives the provider/tool loop until the model stops asking for
// tools, the ceiling is reached, the budget refuses another call, or the
// turn dies.
func (s *Service) runTurn(ctx context.Context, in runTurnInput, sink TurnSink) runTurnResult {
	var (
		reply     strings.Builder
		reasoning strings.Builder
		rounds    []providerRound
		records   []pendingToolCall
		result    runTurnResult
	)
	report := in.Built.Report
	messages := in.Built.Messages
	// Calibrated to the model this turn is being sent to, so the per-round
	// estimates use the same density the builder's own blocks used.
	est := EstimatorFor(in.Agent.Model)
	// addedChars is what the tool exchange has added to the request so far.
	// It is what makes round N's prompt estimate different from round 1's.
	addedChars := 0

	// Reasoning time is measured from the moment the first request goes out
	// to the first token of the actual answer, and recorded only if the model
	// reasoned at all. That window is what the user experiences as waiting,
	// so it is the honest number to show next to "thought for".
	streamStart := time.Now()
	reasoningMS := 0

	finish := domain.FinishStop
	errText := ""

	for round := 1; ; round++ {
		// ── the budget gate, before every call after the first ──────
		//
		// This ran once, when one turn meant one call. It runs per
		// call now, because a tool round is a provider call and a limit that
		// only applied to the first one would be a limit tools walk around.
		// The gate counts what THIS turn has already spent, which is not yet
		// in the database — see turnGate.record.
		if round > 1 {
			if ev := in.Gate.evaluate(); ev.Blocked {
				finish = domain.FinishError
				refusal := budgetRefusal(ev)
				errText = refusal.Error()
				result.err = refusal
				s.log.Info("tool round refused by budget",
					"agent_id", in.Agent.ID, "round", round, "reason", ev.Reason)
				break
			}
		}

		call := s.streamOneCall(ctx, in, messages, sink, &reply, &reasoning, &reasoningMS, streamStart)
		rounds = append(rounds, providerRound{
			Opened:                call.opened,
			Usage:                 call.usage,
			Content:               call.content,
			Reasoning:             call.reasoning,
			EstimatedPromptTokens: report.TotalEstimatedTokens + est.Tokens(addedChars),
			AddedCharacters:       addedChars,
			FinishReason:          call.finish,
		})
		if call.opened {
			// The gate is told what happened before the next round is
			// considered, so "used" includes this turn's own spend.
			in.Gate.record(rounds[len(rounds)-1], call.usage)
		}

		if call.openErr != nil {
			finish, errText = domain.FinishError, call.openErr.Error()
			if round == 1 {
				result.openFailure = call.openErr
			} else {
				result.err = call.openErr
			}
			break
		}
		if call.recvErr != nil {
			finish, errText, result.err = domain.FinishError, call.recvErr.Error(), call.recvErr
			break
		}
		if call.aborted {
			finish, errText = domain.FinishAborted, ""
			break
		}

		if len(call.toolCalls) == 0 {
			// The ordinary end of a turn: the model answered. `tool_calls`
			// with an empty list is a gateway saying it wants tools and
			// naming none, which there is nothing to do about but stop.
			finish = call.finish
			if finish == domain.FinishToolCalls {
				s.log.Warn("provider reported tool_calls with no calls",
					"conversation_id", in.Conversation.ID)
				finish = domain.FinishStop
			}
			break
		}

		// ── the ceiling ─────────────────────────────────────────────
		//
		// Checked before the tools run, not after: executing a round we are
		// not going to be able to answer would spend the tool's time and the
		// next call's tokens to arrive at the same stop.
		if round >= maxToolRounds {
			finish = domain.FinishToolRoundLimit
			errText = "this turn asked to use tools more than " +
				itoa(maxToolRounds) + " times and was stopped"
			result.err = domain.ToolLoopStopped(errText)
			s.log.Info("tool round limit reached",
				"conversation_id", in.Conversation.ID, "rounds", round,
				"refused_calls", len(call.toolCalls))

			// ── The calls we are refusing are still calls that were asked
			//    for ──────────────────────────────────────────────────
			//
			// NO REQUESTED TOOL DISAPPEARS SILENTLY.
			//
			// Until this existed, the ceiling broke before the execution
			// loop and the model's last request left no trace at all: the
			// audit held the rounds that ran and nothing about the one
			// that was stopped. A receipt could then say "two writes
			// executed" for a turn in which the model asked for three,
			// and the third was not failed, not refused, not anywhere.
			//
			// The runtime HAS taken a decision about these calls, so they
			// are recorded as what that decision was: NOT_EXECUTED, which
			// is the status the four gates already use for "the model
			// asked and we turned it away before it did anything".
			//
			// Nothing is executed and nothing is fabricated. There is no
			// Result, because there was no result; the duration is zero,
			// because nothing ran. NewWriteReceipt maps this to
			// WriteNotExecuted and counts it under Refused, so Executed
			// keeps meaning what it has always meant.
			//
			// The sink is told too, with the same terminal frame shape a
			// refused call already produces, so a live client renders the
			// stopped request instead of watching it vanish.
			for _, tc := range call.toolCalls {
				refusal := domain.ToolError(domain.ToolErrRoundLimit, errText)
				records = append(records, pendingToolCall{
					Round: round,
					Outcome: toolOutcome{
						Call:    tc,
						Content: encodeToolFailure(refusal),
						Status:  domain.ToolCallNotExecuted,
						Failure: refusal,
					},
				})
				_ = sink.Tool(ToolEvent{
					CallID:    tc.ID,
					Name:      tc.Name.String(),
					Status:    string(domain.ToolCallNotExecuted),
					ErrorCode: string(domain.ToolErrRoundLimit),
				})
			}
			break
		}
		// A user who pressed stop gets no new round, and no tool runs.
		if ctx.Err() != nil {
			finish, errText = domain.FinishAborted, ""
			break
		}

		// ── run what was asked for ──────────────────────────────────
		assistantAsk := ports.ChatMessage{
			Role:      string(domain.RoleAssistant),
			Content:   call.content,
			ToolCalls: call.toolCalls,
		}
		messages = append(messages, assistantAsk)
		addedChars += askCharacters(assistantAsk)

		roundTools := make([]domain.RoundToolCall, 0, len(call.toolCalls))
		for _, tc := range call.toolCalls {
			// Announced before it runs, so a slow tool reads as work in
			// progress rather than as a stalled stream. A write failure here
			// is ignored on purpose: the reader being gone is detected by the
			// context check at the top of the next round, and abandoning a
			// tool halfway to report a lost frame would leave the turn in a
			// worse state than finishing it.
			_ = sink.Tool(ToolEvent{CallID: tc.ID, Name: tc.Name.String(), Status: toolStatusRunning})

			outcome := s.executeToolCall(ctx, in.Tools, tc)

			// Taken from the outcome rather than derived from whether a
			// failure is attached, so the frame cannot describe the call
			// differently from the row that records it.
			done := ToolEvent{
				CallID: tc.ID, Name: tc.Name.String(),
				Status: string(outcome.Status), DurationMS: outcome.DurationMS,
			}
			if outcome.Failure != nil {
				done.ErrorCode = string(outcome.Failure.Code)
			}
			_ = sink.Tool(done)

			messages = append(messages, ports.ChatMessage{
				Role:       roleTool,
				Content:    outcome.Content,
				ToolCallID: tc.ID,
			})
			addedChars += utf8.RuneCountInString(outcome.Content)

			rt := domain.RoundToolCall{
				Name:       tc.Name,
				Status:     outcome.Status,
				DurationMS: outcome.DurationMS,
			}
			if outcome.Failure != nil {
				rt.ErrorCode = outcome.Failure.Code
			}
			roundTools = append(roundTools, rt)
			records = append(records, pendingToolCall{Round: round, Outcome: outcome})
		}
		rounds[len(rounds)-1].Tools = roundTools
	}

	// A turn that reasoned and then stopped before answering still spent
	// that time; measure it to the end rather than reporting zero.
	if reasoningMS == 0 && reasoning.Len() > 0 {
		reasoningMS = int(time.Since(streamStart).Milliseconds())
	}

	// The tool exchange is added to the report as one block, after the fact,
	// because the builder ran before any of it existed. Both directions are
	// counted: what the model asked for is re-sent on every later call of the
	// turn, exactly like the answers it got, and both are billed as input.
	if addedChars > 0 {
		items := 0
		for _, r := range rounds {
			items += len(r.Tools)
		}
		addToBlock(&report, est, domain.BlockToolResults, items, addedChars)
	}

	result.assistantTurn = assistantTurn{
		Content:     reply.String(),
		Reasoning:   reasoning.String(),
		ReasoningMS: reasoningMS,
		Finish:      finish,
		ErrText:     errText,
		Price:       in.Gate.price,
		Report:      &report,
		rounds:      rounds,
		tools:       records,
	}
	return result
}

// roleTool is the provider protocol's role for a tool result. It is not a
// domain.Role: nothing with this role is ever persisted as a message, so
// adding it to the stored enum would be widening a type to describe
// something that never reaches it.
const roleTool = "tool"

// callResult is what one provider call produced.
type callResult struct {
	opened    bool
	openErr   error
	recvErr   error
	aborted   bool
	content   string
	reasoning string
	finish    domain.FinishReason
	usage     *ports.Usage
	toolCalls []domain.ToolCall
}

// streamOneCall runs exactly one completion, forwarding what it produces to
// the sink and to the turn's accumulators.
//
// reply and reasoning are the TURN's builders, not this call's: a turn that
// says something, runs a tool and then finishes the thought is one answer in
// the transcript, not three. The per-call strings are returned separately
// because the wire needs them to echo the model's own tool request back.
func (s *Service) streamOneCall(
	ctx context.Context,
	in runTurnInput,
	messages []ports.ChatMessage,
	sink TurnSink,
	reply, reasoning *strings.Builder,
	reasoningMS *int,
	streamStart time.Time,
) callResult {
	var out callResult

	stream, err := s.llm.Stream(ctx, ports.CompletionRequest{
		Creds:       in.Creds,
		Model:       in.Agent.Model,
		Messages:    messages,
		Temperature: in.Agent.Temperature,
		MaxTokens:   in.Agent.MaxTokens,
		// Empty for an agent with no authorized tools, which is what keeps
		// the request body byte-identical to the pre-tools one.
		Tools: in.Tools,
		// Spend attribution: the gateway bills the key, but tagging the turn
		// with its agent and conversation lets its own spend logs break the
		// total down the same way our local tallies do.
		User: in.Agent.ID.String(),
		Metadata: map[string]string{
			"agent_id":        in.Agent.ID.String(),
			"conversation_id": in.Conversation.ID.String(),
			"workspace_id":    in.Conversation.WorkspaceID.String(),
		},
	})
	if err != nil {
		// StreamOpened stays false on purpose: the request may never have
		// reached the model, so what it consumed is unknown, and unknown is
		// what gets written. See newTurnAccounting.
		out.openErr = err
		return out
	}
	defer func() { _ = stream.Close() }()
	out.opened = true
	out.finish = domain.FinishStop

	var callContent, callReasoning strings.Builder

	for {
		ev, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			// A cancelled request surfaces here as a read error. That is an
			// abort, not a provider fault, and should not be reported as one.
			if ctx.Err() != nil {
				out.aborted = true
			} else {
				out.recvErr = recvErr
			}
			break
		}

		if ev.Usage != nil {
			out.usage = ev.Usage
		}
		if ev.FinishReason != "" {
			out.finish = domain.FinishReason(ev.FinishReason)
		}
		if len(ev.ToolCalls) > 0 {
			out.toolCalls = ev.ToolCalls
		}

		if ev.Reasoning != "" {
			callReasoning.WriteString(ev.Reasoning)
			reasoning.WriteString(ev.Reasoning)
			if err := sink.Reasoning(ev.Reasoning); err != nil {
				out.aborted = true
				break
			}
		}

		if ev.Delta == "" {
			continue
		}
		// First answer token closes the reasoning window.
		if *reasoningMS == 0 && reasoning.Len() > 0 {
			*reasoningMS = int(time.Since(streamStart).Milliseconds())
		}

		callContent.WriteString(ev.Delta)
		reply.WriteString(ev.Delta)
		if err := sink.Delta(ev.Delta); err != nil {
			// The reader is gone. Stop pulling tokens we are paying for.
			out.aborted = true
			break
		}
	}

	out.content = callContent.String()
	out.reasoning = callReasoning.String()
	return out
}

// askCharacters is what echoing the model's own tool request back costs.
//
// The arguments dominate; the id and the name are a handful of characters
// each and are counted because they are sent.
func askCharacters(m ports.ChatMessage) int {
	n := utf8.RuneCountInString(m.Content)
	for _, c := range m.ToolCalls {
		n += utf8.RuneCountInString(c.ID) +
			utf8.RuneCountInString(c.Name.String()) +
			utf8.RuneCountInString(c.Arguments)
	}
	return n
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

/* ── persistence ─────────────────────────────────────────────────────── */

// providerRound is what one call inside a turn consumed and produced. It is
// the raw material the accounting is computed from, once, at the end.
type providerRound struct {
	// Opened is the line between "we know tokens were consumed but not how
	// many" and "we do not know that anything was consumed at all".
	Opened                bool
	Usage                 *ports.Usage
	Content               string
	Reasoning             string
	EstimatedPromptTokens int
	AddedCharacters       int
	FinishReason          domain.FinishReason
	Tools                 []domain.RoundToolCall
}

// pendingToolCall is one executed call, waiting for the message id it will
// be filed under. Tool calls happen before the assistant turn is written, so
// the audit rows are held here and inserted with it — which is what gives
// them the turn's lifecycle instead of one of their own.
type pendingToolCall struct {
	Round   int
	Outcome toolOutcome
}

// assistantTurn is what a finished turn produced. Grouped into a struct
// because persistAssistant otherwise takes seven positional arguments, four
// of which are strings — an easy call to get silently wrong.
type assistantTurn struct {
	Content     string
	Reasoning   string
	ReasoningMS int
	Finish      domain.FinishReason
	ErrText     string
	// Price is the rate card the budget preflight already fetched, when it
	// ran. Reusing it is what keeps a budgeted agent at one rate-card call
	// per turn instead of two. Nil means nobody has looked yet.
	Price *ports.Price
	// Report is the account of what this turn carried, stamped onto the
	// message so it survives everything that produced it.
	Report *domain.ContextReport
	// rounds is one entry per provider call, in order. Empty only for a turn
	// that never got as far as calling anything.
	rounds []providerRound
	// tools is the audit trail this turn generated.
	tools []pendingToolCall
}

// streamOpened reports whether ANY call of this turn reached the model.
func (t assistantTurn) streamOpened() bool {
	for _, r := range t.rounds {
		if r.Opened {
			return true
		}
	}
	return false
}

// persistAssistant writes the assistant turn on a context detached from the
// request, so a hung-up client still leaves a complete record. It never
// returns an error: by this point the turn has already happened, and the
// caller has nothing useful to do about a failed write except log it.
func (s *Service) persistAssistant(
	ctx context.Context,
	conv *domain.Conversation,
	agent *domain.Agent,
	creds ports.Credentials,
	turn assistantTurn,
) *domain.Message {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), persistTimeout)
	defer cancel()

	price := s.priceForTurn(writeCtx, creds, agent.Model, turn)
	acct, roundReports := aggregateTurn(turn.rounds, price, EstimatorFor(agent.Model))

	report := turn.Report
	if report != nil && len(roundReports) > 1 {
		// Recorded only when there was more than one call. A turn with one
		// round is fully described by the blocks above it, and an array of
		// one would be noise on every ordinary turn ever stored.
		withRounds := *report
		withRounds.Rounds = roundReports
		report = &withRounds
	}

	msg := &domain.Message{
		ID:             uuid.New(),
		WorkspaceID:    conv.WorkspaceID,
		ConversationID: conv.ID,
		Role:           domain.RoleAssistant,
		Content:        turn.Content,
		Reasoning:      turn.Reasoning,
		ReasoningMS:    turn.ReasoningMS,
		// The model this turn was actually sent to, stamped now. Reading it
		// off the agent at *display* time would make every past answer
		// claim to have come from whatever the agent is configured with
		// today, which is how a statement starts lying about its own past.
		Model:                 agent.Model,
		FinishReason:          string(turn.Finish),
		Error:                 truncate(turn.ErrText, 2000),
		UsageSource:           acct.Source,
		PromptTokens:          acct.PromptTokens,
		CompletionTokens:      acct.CompletionTokens,
		InputCostPerToken:     acct.InputCostPerToken,
		OutputCostPerToken:    acct.OutputCostPerToken,
		Cost:                  acct.Cost,
		EstimatedPromptTokens: acct.EstimatedPromptTokens,
		// Nil when the gateway did not report them, which is every turn this
		// system recorded before the adapter learned to read them. Nil is
		// ABSENT and must never be rendered as a zero cache hit.
		CacheReadTokens:     acct.CacheReadTokens,
		CacheCreationTokens: acct.CacheCreationTokens,
		ReasoningTokens:     acct.ReasoningTokens,
		// The snapshot, written once with the row it describes. Its
		// lifecycle is the message's: truncate and regenerate hard-delete
		// the turn, and the report goes with it rather than outliving what
		// it was an account of.
		ContextReport: report,
	}

	if err := s.repos.Messages.Create(writeCtx, msg); err != nil {
		s.log.Error("persist assistant message", "conversation_id", conv.ID, "err", err)
		return nil
	}
	s.persistToolCalls(writeCtx, conv, msg, turn.tools)
	if err := s.repos.Conversations.TouchActivity(writeCtx, conv.WorkspaceID, conv.ID); err != nil {
		s.log.Warn("touch conversation activity", "conversation_id", conv.ID, "err", err)
	}
	return msg
}

// persistToolCalls files the turn's audit rows under the message they belong
// to.
//
// Best-effort, and deliberately so: the answer is already stored, the user
// already has it, and failing the turn over its own audit trail would trade
// the product for the paperwork. A failure is logged loudly, because a
// missing trail is a real gap and a silent one would be worse.
func (s *Service) persistToolCalls(ctx context.Context, conv *domain.Conversation, msg *domain.Message, calls []pendingToolCall) {
	if len(calls) == 0 {
		return
	}
	records := make([]domain.ToolCallRecord, 0, len(calls))
	for _, c := range calls {
		rec := domain.ToolCallRecord{
			WorkspaceID:    conv.WorkspaceID,
			ConversationID: conv.ID,
			MessageID:      msg.ID,
			Round:          c.Round,
			ProviderCallID: truncate(c.Outcome.Call.ID, 128),
			ToolName:       c.Outcome.Call.Name,
			Status:         c.Outcome.Status,
			DurationMS:     c.Outcome.DurationMS,
		}

		// ── Does this capability carry something that must not be kept? ──
		//
		// Asked of the REGISTRY rather than of the call, because the call is
		// a request from a model and the definition is a fact about the
		// program. A tool that has been removed from the build cannot be
		// looked up — and then nothing is stored either way, since a call
		// that never resolved has no payload worth keeping.
		//
		// There is no list of module names here and there must never be
		// one: the flag travels with the tool.
		confidential := false
		// Effect is captured here, from the definition that was live when
		// the call ran, and stored beside the outcome. Reading it back from
		// the registry later would describe today's build rather than the
		// turn being reported on — see ToolCallRecord.Effect.
		//
		// External is captured on the same terms and for the same reason —
		// see ToolCallRecord.External and migration 0020. A capability the
		// build no longer has stays `false`, which is the value that claims
		// the least: an unresolvable call produced no evidence either way,
		// and defaulting it true would let a removed tool make a past turn
		// look verified.
		rec.Effect = domain.EffectRead
		rec.External = false
		if tool, ok := s.tools.Lookup(c.Outcome.Call.Name); ok {
			def := tool.Definition()
			confidential = def.Confidential
			rec.Effect = def.Effect
			rec.External = def.External
		}

		// Recorded whatever the redaction decides below. The ref is a type
		// and a UUID — it cannot carry what redaction removes — and it is
		// the only thing that lets a later turn continue this work instead
		// of repeating it. See domain/effect_ref.go.
		rec.EffectRef = c.Outcome.EffectRef

		if confidential {
			// Arguments and Result stay nil, and Redacted says that nil
			// means "withheld" rather than "there was nothing". Everything
			// the trail is for — which tool, which round, how long, ok or
			// error — is above and is untouched.
			rec.Redacted = true
			// The ref is NOT redacted, and that is the whole point. It is a
			// type and a UUID, it cannot carry what redaction removes, and
			// it is the only thing that lets a later turn continue this
			// work instead of repeating it.
		} else {
			rec.Result = c.Outcome.Result
			// Stored as the model produced them. The audit trail records what
			// was asked, not what we managed to parse out of it — an
			// unparseable argument string is exactly the case somebody will
			// want to read later.
			args := truncate(c.Outcome.Call.Arguments, domain.MaxToolArgumentsBytes)
			rec.Arguments = &args
		}
		if c.Outcome.Failure != nil {
			rec.ErrorCode = c.Outcome.Failure.Code
			rec.ErrorMessage = truncate(c.Outcome.Failure.Message, 500)
		}
		records = append(records, rec)
	}
	if err := s.repos.ToolCalls.CreateMany(ctx, records); err != nil {
		s.log.Error("persist tool calls", "conversation_id", conv.ID,
			"message_id", msg.ID, "calls", len(records), "err", err)
	}
}

// priceForTurn answers with the rate card the preflight already read, or
// goes and reads one. Same question, asked once per turn.
func (s *Service) priceForTurn(ctx context.Context, creds ports.Credentials, model string, turn assistantTurn) *ports.Price {
	if turn.Price != nil {
		return turn.Price
	}
	return s.priceAtTurn(ctx, creds, model, turn.streamOpened())
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
