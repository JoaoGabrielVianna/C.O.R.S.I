package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

// Memory consolidation: reading a conversation that already happened and
// proposing what is worth carrying into the next one.
//
// ── The normative sentence ─────────────────────────────────────────────
//
//	Memory consolidation only considers conversation state that existed
//	when the user requested consolidation.
//
// There is no continuous capture, no watching of later turns, and no way
// to ask an agent to remember something that has not been said yet. The
// operation reads a snapshot, proposes, and ends.
//
// ── Why it is an application operation and not a tool ──────────────────
// A tool exists so a MODEL can decide to call it. Here the user decides, by
// typing a command, and the backend orchestrates. Registering this as a
// tool would add a declaration on every turn's prompt, an authorization to
// manage and an audit row to write, all to serve a decision the model never
// gets to make. Owner decision D1.
//
// ── What it may never do ───────────────────────────────────────────────
// Write a memory. It produces candidates, and nothing else. Persistence
// happens afterwards, through the confirmation the user gives, over the
// route that already existed for saving a memory.
//
// ── Why it costs money and says so ─────────────────────────────────────
// It is one provider call, gated by the same daily budget a turn is gated
// by, and it writes an auxiliary receipt so what it spent is counted like
// everything else. The result carries its own usage, because this
// is the only operation in the module that spends money without producing a
// visible answer, and a cost the user cannot see is a cost they cannot
// govern.

// ConsolidationInputBudgetChars bounds what the conversation may contribute
// to the extraction prompt.
//
// ── Why it is not the chat's context budget ────────────────────────────
// They are different operations with different economics. A turn's budget
// is spent every turn, forever, on memory and sources that must leave room
// for the conversation itself. This is spent once, on demand, and the
// conversation IS the input. Reusing MemoryBudgetChars would starve the
// thing being read; reusing the turn's whole envelope would make an
// occasional operation the most expensive request in the system.
//
// Twelve thousand characters is roughly three thousand tokens: a long
// working session, and a bounded bill for an operation the user triggers by
// hand. What does not fit is reported as fewer considered messages rather
// than trimmed into a sentence that says something else.
const ConsolidationInputBudgetChars = 12000

// ConsolidationMaxOutputTokens caps the auxiliary call's output.
//
// ── What the old number got wrong ──────────────────────────────────────
// It was 700, sized for the SHAPE OF THE ANSWER: five candidates, each a
// fact and a short reason, plus the JSON around them. That reasoning was
// right about the answer and silently wrong about the field, because
// `max_tokens` does not bound the answer. It bounds everything the model
// emits, and a reasoning model emits thinking first — from the same
// budget, billed at the same rate, before a single character of answer.
//
// So on a reasoning model the ceiling was spent before the form was filled
// in. The operation was charged in full and returned nothing.
//
// ── The measurements this number comes from ────────────────────────────
// Real conversations, real gateway, receipts in chat.messages:
//
//	claude-haiku-4-5   prompt 5217   completion  327 · 371   (no reasoning)
//	gpt-5-mini         prompt 3049   completion  700         ← the ceiling,
//	                                                            0 chars out
//	gpt-5-mini         prompt 3049   completion 1338         5 candidates,
//	                                                            with room
//
// The input side is capped at ConsolidationInputBudgetChars, so ~3000
// prompt tokens is the top of the range this operation can present. 1338 is
// what the worst measured case actually needed; 3000 is a bit over twice
// that. The answer alone cannot plausibly exceed it either: five candidates
// at their maximum lengths is the theoretical ceiling of the form.
//
// ── Why the raise costs nothing on the models that were fine ───────────
// `max_tokens` is a cap, not a purchase. Claude emitted 327 tokens under a
// 700 ceiling and emits 327 under this one. Nothing that was working pays
// more; the only spend that changed is the spend that was previously thrown
// away on a truncated call.
//
// ── Why this is not gated on a model capability ────────────────────────
// It was the first design, and the gateway refused it. `/model/info`
// reports `supports_reasoning: true` for gpt-5-mini AND for
// claude-haiku-4-5 AND for deepseek-reasoner, so the flag does not separate
// the model that needed headroom from the two that did not. And since a
// higher cap costs nothing on a model that does not reach it, gating would
// buy no saving — only a network read before every consolidation and a new
// question to answer when it fails. Ceremony, not engineering.
//
// The boundary probe behind this number also rejected `reasoning_effort`.
const ConsolidationMaxOutputTokens = 3000

// consolidationOutputCeiling is what one consolidation may actually ask for.
//
// Two bounds, and they answer different questions:
//
//	ConsolidationMaxOutputTokens   what this FORM needs, measured
//	agent.MaxTokens                what this agent's owner already agreed
//	                               to pay for one call
//
// The smaller wins. That is the difference between raising a ceiling and
// raising every bill: an agent configured at 512 keeps 512, and the only
// agents that see more are the ones already set high enough to have been
// paying for it on every ordinary turn.
//
// A zero or negative setting cannot reach here — domain.Agent.Validate
// refuses it — but the guard is written anyway, because a ceiling that
// silently became "no output at all" would look exactly like the bug this
// whole hotfix is about.
func consolidationOutputCeiling(agent *domain.Agent) int {
	if agent == nil || agent.MaxTokens <= 0 {
		return ConsolidationMaxOutputTokens
	}
	if agent.MaxTokens < ConsolidationMaxOutputTokens {
		return agent.MaxTokens
	}
	return ConsolidationMaxOutputTokens
}

// Consolidation runs at the agent's OWN temperature, and this comment is
// the record of why it stopped having one of its own.
//
// ── What it used to do, and what that cost ─────────────────────────────
// It sent a fixed 0.2, on the reasoning that reading a thread and
// reporting what it contains is not a place for invention. Sound as
// far as it went, and wrong in one way that mattered: a hardcoded scalar
// is a claim about what every model will accept, and this module has no
// way to verify that claim.
//
// It was false. A gpt-5 family model accepts temperature=1 and nothing
// else, and answers 400 to anything else — so `/lembrar` failed for every
// agent on one of those models while ordinary conversation with the same
// agent worked perfectly.
//
// ── The invariant that replaces the constant ───────────────────────────
//
//	If you can talk to an agent, you can ask it to consolidate.
//
// The agent's temperature is the only value PROVEN acceptable for that
// agent's model: every ordinary turn already sends it and succeeds. Reusing
// it is not a compromise, it is the one locally knowable correct answer.
//
// ── What was given up, said plainly ────────────────────────────────────
// On models that would have accepted it, extraction now runs at whatever
// the agent is configured with instead of at 0.2. That is a real loss of a
// small nudge toward determinism. Recovering it needs a model-capability
// surface the platform does not have — implementing one inside this module
// would be exactly the "provider protocol inside a module" the
// architecture forbids. Recorded as a candidate, not smuggled in here.
//
// MaxTokens is still overridden, and correctly: a token ceiling is legal
// for every model. Only the temperature was a claim we could not make.

// consolidationMessageLimit bounds the rows read before the character
// budget is applied. It is a guard against loading a thread of ten thousand
// messages to then keep the last twelve, not a second window: the budget
// below is what decides how much is actually sent.
const consolidationMessageLimit = 200

// ConsolidateMemoryInput is one request to read a thread and propose.
type ConsolidateMemoryInput struct {
	WorkspaceID    uuid.UUID
	ConversationID uuid.UUID
	// UpToSeq is the last message the user could see when they asked. Zero
	// or negative means "whatever the thread contains now", which is the
	// same guarantee by another route: rows written after this read simply
	// do not exist yet.
	//
	// It is a ceiling and never a floor. A value beyond the end of the
	// thread selects everything that exists and invents nothing.
	UpToSeq int64
}

// MemoryCandidates is what one consolidation produced.
type MemoryCandidates struct {
	Candidates []domain.MemoryCandidate `json:"candidates"`
	// ConsideredMessages is how many turns actually reached the model,
	// after the input budget. It is reported because "I read your
	// conversation" and "I read the last four messages of it" are different
	// claims, and only one of them may be made silently.
	ConsideredMessages int `json:"considered_messages"`
	// EffectiveUpToSeq is the newest seq that was actually considered. It
	// is what provenance records — never the seq that was requested, which
	// may name a message the budget left out.
	EffectiveUpToSeq int64 `json:"effective_up_to_seq"`
	// Usage is what this operation cost. Absent when no provider call was
	// made, which is the honest answer for a conversation with nothing in
	// it: no call, no charge, no usage.
	Usage *OperationUsage `json:"usage,omitempty"`
}

// OperationUsage is the cost of one auxiliary operation, in the same terms
// the rest of the module reports cost.
//
// No total_tokens: the module reports prompt and completion separately
// everywhere, because the two are priced differently and a single number
// would hide which side a turn was heavy on. Cost is a pointer for the
// reason every cost in this module is one — unknown is not zero.
type OperationUsage struct {
	Model            string   `json:"model"`
	PromptTokens     int      `json:"prompt_tokens"`
	CompletionTokens int      `json:"completion_tokens"`
	UsageSource      string   `json:"usage_source"`
	CostUSD          *float64 `json:"cost_usd"`
}

// ConsolidateMemory proposes what is worth remembering from a thread.
//
// The order of the steps is the safety argument, and it is deliberate:
//
//	resolve      → does this thread exist, for this workspace?
//	policy       → may this agent be asked at all?          409, no call
//	snapshot     → what existed when the user asked?
//	nothing?     → answer honestly.                          no call, no charge
//	budget       → may we spend?                             refused, no call
//	call         → one completion, no tools
//	receipt      → what it cost, recorded before anything can fail
//	parse        → believe nothing that cannot be read
//	dedupe       → say what is already known
//
// Everything that can refuse does so before the provider is reached, so a
// refusal never costs anything. Everything that can fail after the provider
// answers happens after the receipt is written, so a failure never hides a
// bill.
func (s *Service) ConsolidateMemory(ctx context.Context, in ConsolidateMemoryInput) (*MemoryCandidates, error) {
	conv, err := s.repos.Conversations.FindByID(ctx, in.WorkspaceID, in.ConversationID)
	if err != nil {
		return nil, err
	}
	agent, err := s.repos.Agents.FindByID(ctx, in.WorkspaceID, conv.AgentID)
	if err != nil {
		return nil, err
	}

	// ── policy ──────────────────────────────────────────────────────
	//
	// Before anything is read and long before anything is spent. A 409
	// rather than a 403: the request is well formed and the caller is
	// allowed here — the agent is simply in a state that does not permit
	// the operation, which is the same shape as refusing to delete an agent
	// that still has memories. The code beside it is what the UI branches
	// on without reading the sentence.
	if !agent.MemoryPolicy.Proposes() {
		return nil, domain.MemoryConsolidationDisabled(
			"a consolidação de memória está desligada para este agente; ligue em Configurações do agente, em Política de memória")
	}

	// ── the snapshot ────────────────────────────────────────────────
	upTo := in.UpToSeq
	if upTo <= 0 {
		upTo = maxSeq
	}
	history, err := s.repos.Messages.ListUpToSeq(ctx, in.WorkspaceID, conv.ID, upTo, consolidationMessageLimit)
	if err != nil {
		return nil, err
	}
	selected := selectConsolidationWindow(history)

	// ── nothing to read ─────────────────────────────────────────────
	//
	// An empty thread, a thread of aborted turns, or a ceiling below its
	// first message. All three are answered without a provider call: paying
	// tokens to be told there was no conversation is a bill for nothing.
	if len(selected) == 0 {
		return &MemoryCandidates{Candidates: []domain.MemoryCandidate{}}, nil
	}

	provider, err := s.repos.Providers.FindByID(ctx, in.WorkspaceID, agent.ProviderID)
	if err != nil {
		return nil, err
	}
	creds, err := s.credentialsFor(provider)
	if err != nil {
		return nil, err
	}

	// ── budget ──────────────────────────────────────────────────────
	//
	// The same gate a turn passes, with a ceiling of its own: this call's
	// output is capped at ConsolidationMaxOutputTokens, not at the agent's
	// max_tokens, so the limit is evaluated against what this operation can
	// actually consume.
	gate, err := s.budgetPreflight(ctx, agent, creds, domain.TurnCeiling{
		MaxOutputTokens: ConsolidationMaxOutputTokens,
	})
	if err != nil {
		return nil, err
	}
	if ev := gate.evaluate(); ev.Blocked {
		return nil, budgetRefusal(ev)
	}

	// ── the call ────────────────────────────────────────────────────
	messages := consolidationMessages(agent, selected)
	promptChars := 0
	for _, m := range messages {
		promptChars += utf8.RuneCountInString(m.Content)
	}

	answer, usage, opened, callErr := s.runConsolidationCall(ctx, agent, conv, creds, messages)

	// ── the receipt ─────────────────────────────────────────────────
	//
	// Written before the answer is even looked at, and on a context
	// detached from the request. The tokens were bought the moment the
	// provider produced them: a parser that fails afterwards, or a user who
	// closed the dialog, must not be able to make that spend disappear.
	var reported *OperationUsage
	if opened {
		reported = s.recordConsolidationReceipt(ctx, conv, agent, creds, gate, consolidationReceipt{
			EstimatedPromptTokens: EstimateTokens(promptChars),
			Usage:                 usage,
			Answer:                answer,
		})
	}

	if callErr != nil {
		return nil, callErr
	}

	// ── parse ───────────────────────────────────────────────────────
	//
	// An answer with no characters at all is told apart from one that
	// arrived and could not be read, because they are different failures
	// with different fixes and the reader has to be pointed at the right
	// one.
	//
	// "Unreadable format" sends whoever is debugging to the parser. The
	// case that actually happens is emptiness: a reasoning model can spend
	// the whole of ConsolidationMaxOutputTokens thinking and emit no answer
	// text, so the operation is billed in full and produces nothing. The
	// parser is fine; the ceiling was too small for that model. Reporting
	// the second as the first cost real time when this was diagnosed.
	if strings.TrimSpace(answer) == "" {
		s.log.Warn("memory consolidation: the model returned no answer text",
			"conversation_id", conv.ID, "agent_id", agent.ID, "model", agent.Model,
			"max_output_tokens", ConsolidationMaxOutputTokens)
		return nil, domain.Upstream(
			"o modelo não devolveu nenhum texto: provavelmente gastou todo o limite de saída " +
				"desta operação antes de responder, o que acontece com modelos de raciocínio. " +
				"A operação foi cobrada e nenhuma memória foi criada")
	}
	candidates, dropped, err := domain.ParseMemoryCandidates(answer)
	if err != nil {
		s.log.Warn("memory consolidation: unreadable answer",
			"conversation_id", conv.ID, "agent_id", agent.ID,
			"answer_characters", utf8.RuneCountInString(answer))
		return nil, domain.Upstream(
			"o modelo respondeu em um formato que não deu para ler; a operação foi cobrada e nenhuma memória foi criada")
	}
	if dropped > 0 {
		// Counted, not shown: the response has no field for it, and the
		// alternative to a log line here is a silent cut.
		s.log.Info("memory consolidation: candidates dropped",
			"conversation_id", conv.ID, "agent_id", agent.ID, "dropped", dropped)
	}

	// ── dedupe ──────────────────────────────────────────────────────
	if err := s.markDuplicates(ctx, in.WorkspaceID, agent.ID, candidates); err != nil {
		return nil, err
	}

	s.log.Info("memory consolidation",
		"conversation_id", conv.ID, "agent_id", agent.ID,
		"considered_messages", len(selected), "candidates", len(candidates))

	return &MemoryCandidates{
		Candidates:         candidates,
		ConsideredMessages: len(selected),
		EffectiveUpToSeq:   selected[len(selected)-1].Seq,
		Usage:              reported,
	}, nil
}

// maxSeq stands for "no ceiling": seq is a BIGSERIAL, so this is beyond any
// row that can exist.
const maxSeq = int64(1) << 62

/* ── the window ──────────────────────────────────────────────────────── */

// selectConsolidationWindow decides how much of the thread is sent.
//
// Empty turns are dropped first — an aborted stream leaves a row with no
// content, and it says nothing about what the user wants remembered. What
// remains is selected newest-first against the character budget, whole
// messages only, and then put back in reading order: the model has to see a
// conversation, not a set.
//
// Newest-first is the priority and not the order. A consolidation that ran
// out of budget should be missing the beginning of a long session, not its
// conclusion, because the decisions worth remembering are usually the ones
// most recently reached.
func selectConsolidationWindow(history []domain.Message) []domain.Message {
	withContent := make([]domain.Message, 0, len(history))
	for _, m := range history {
		if strings.TrimSpace(m.Content) != "" {
			withContent = append(withContent, m)
		}
	}

	// Reversed, so selectWithinBudget consumes the most recent first. The
	// shared helper is the same one Memory and Sources use, which is what
	// keeps "whole items only, and one oversized item does not hide the
	// rest" from being reimplemented per feature.
	newestFirst := make([]domain.Message, len(withContent))
	for i, m := range withContent {
		newestFirst[len(withContent)-1-i] = m
	}

	selected, _ := selectWithinBudget(newestFirst, ConsolidationInputBudgetChars, 0,
		func(m domain.Message) int { return consolidationLineChars(m) })

	chronological := make([]domain.Message, len(selected))
	for i, m := range selected {
		chronological[len(selected)-1-i] = m
	}
	return chronological
}

// consolidationLineChars is what one message costs the prompt, label
// included, because the label is sent too.
func consolidationLineChars(m domain.Message) int {
	return utf8.RuneCountInString(consolidationLabel(m.Role)) +
		utf8.RuneCountInString(m.Content) + 2
}

func consolidationLabel(role domain.Role) string {
	if role == domain.RoleUser {
		return "Usuário: "
	}
	return "Agente: "
}

/* ── the prompt ──────────────────────────────────────────────────────── */

// consolidationBasePolicy is the product's rule about what deserves to be
// remembered, and it ships in code.
//
// ── Why the user cannot replace it ─────────────────────────────────────
// An agent whose owner deleted the policy would propose whatever it liked,
// and the first bad batch of memories is the one that teaches a person to
// stop trusting the feature. What a user CAN do is add to it, through
// memory_policy.notes, which is appended below rather than substituted for
// it. The distinction is the whole reason the settings card says
// "orientação adicional" and not "política".
//
// ── Why the agent's own instructions are not used here ─────────────────
// Those describe how the agent talks. This describes what is worth keeping,
// which is a different question — and an agent instructed to be playful
// should not propose playful memories.
const consolidationBasePolicy = `Você está revisando uma conversa que já aconteceu, para decidir o que vale a pena guardar como memória durável do usuário.

Proponha apenas fatos que serão úteis em CONVERSAS FUTURAS:
- preferências relativamente estáveis do usuário;
- objetivos de médio e longo prazo;
- decisões tomadas;
- restrições importantes;
- contexto de projeto que continuará relevante;
- convenções e escolhas que provavelmente precisarão ser lembradas.

NÃO proponha:
- conversa casual, saudação, agradecimento;
- informação efêmera ou que só faz sentido neste momento;
- especulação, hipótese ou algo que o usuário não afirmou nem decidiu;
- inferência que a conversa não sustenta;
- o texto de comandos como /lembrar;
- nada que já esteja na memória do agente.

Regras da resposta:
- escreva cada memória como uma frase curta, autocontida, em terceira pessoa sobre o usuário;
- no máximo 5 propostas, e menos é melhor que forçar;
- se não houver nada que valha a pena guardar, responda com uma lista vazia: [].

Responda SOMENTE com um array JSON, sem texto em volta, no formato:
[{"content": "o fato a lembrar", "reason": "por que será útil depois"}]`

// consolidationMessages builds the two-message request.
//
// A system message carrying the policy, and a user message carrying the
// transcript. The transcript is a user message rather than a pile of
// replayed turns on purpose: the model is being asked to ANALYSE a
// conversation, not to continue one, and replaying the roles would invite
// it to answer the last question instead of reading the thread.
func consolidationMessages(agent *domain.Agent, window []domain.Message) []ports.ChatMessage {
	policy := consolidationBasePolicy
	if notes := strings.TrimSpace(agent.MemoryPolicy.Notes); notes != "" {
		policy += "\n\nOrientação adicional do usuário para este agente, que complementa as regras acima e não as substitui:\n" + notes
	}

	var b strings.Builder
	b.WriteString("Conversa a analisar:\n\n")
	for _, m := range window {
		b.WriteString(consolidationLabel(m.Role))
		b.WriteString(m.Content)
		b.WriteString("\n\n")
	}

	return []ports.ChatMessage{
		{Role: "system", Content: policy},
		{Role: string(domain.RoleUser), Content: b.String()},
	}
}

/* ── the call ────────────────────────────────────────────────────────── */

// runConsolidationCall makes exactly one completion and returns its text.
//
// No tools, no rounds, no sink. `Tools` is left empty, which is what makes
// the request body carry no `tools` field at all — the same guarantee an
// agent without authorizations has, and the reason a model cannot decide to
// do anything but answer.
func (s *Service) runConsolidationCall(
	ctx context.Context,
	agent *domain.Agent,
	conv *domain.Conversation,
	creds ports.Credentials,
	messages []ports.ChatMessage,
) (answer string, usage *ports.Usage, opened bool, err error) {
	stream, err := s.llm.Stream(ctx, ports.CompletionRequest{
		Creds:    creds,
		Model:    agent.Model,
		Messages: messages,
		// The agent's own, never a constant of ours: it is the only
		// temperature proven legal for this agent's model, because every
		// ordinary turn already sends it and succeeds. A fixed value here
		// broke `/lembrar` on gpt-5 models — see the note further up.
		Temperature: agent.Temperature,
		// Never more output than one ordinary turn of this agent already
		// asks for. This is the half of the ceiling that keeps the raise
		// from being a blanket one: the operation's own bound says what this
		// FORM needs, and the agent's own setting says what its owner is
		// willing to pay for a single call. The smaller of the two wins, so
		// an agent deliberately configured small keeps its own limit and
		// nobody's bill grows because a constant elsewhere did.
		MaxTokens: consolidationOutputCeiling(agent),
		User:      agent.ID.String(),
		Metadata: map[string]string{
			"agent_id":        agent.ID.String(),
			"conversation_id": conv.ID.String(),
			"workspace_id":    conv.WorkspaceID.String(),
			// So the gateway's own spend log can tell this apart from a
			// turn, the same way our receipt does locally.
			"operation": "memory_consolidation",
		},
	})
	if err != nil {
		return "", nil, false, err
	}
	defer func() { _ = stream.Close() }()

	var text strings.Builder
	for {
		ev, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			break
		}
		if recvErr != nil {
			// The stream opened, so tokens were bought whatever happened
			// next. `opened` stays true and the receipt is written from
			// whatever usage arrived.
			return text.String(), usage, true, recvErr
		}
		if ev.Usage != nil {
			usage = ev.Usage
		}
		// Reasoning is not collected: it is not the answer, it is not
		// parsed, and holding it would only put the model's chain of
		// thought about a private conversation into a string this operation
		// has no use for.
		text.WriteString(ev.Delta)
	}
	return text.String(), usage, true, nil
}

/* ── the receipt ─────────────────────────────────────────────────────── */

type consolidationReceipt struct {
	EstimatedPromptTokens int
	Usage                 *ports.Usage
	// Answer is used only to estimate completion tokens when the gateway
	// reported none. It is never stored: the row this writes has no content
	// by design, and by CHECK.
	Answer string
}

// recordConsolidationReceipt writes what the operation consumed.
//
// On a context detached from the request, with its own deadline, for the
// same reason a turn persists that way: the spend happened, and a client
// that hung up does not undo it.
//
// A failed write is logged loudly and does not fail the operation. The
// alternative — refusing to return candidates the user already paid for
// because their receipt could not be filed — trades the product for the
// paperwork, which is the same call persistToolCalls makes.
func (s *Service) recordConsolidationReceipt(
	ctx context.Context,
	conv *domain.Conversation,
	agent *domain.Agent,
	creds ports.Credentials,
	gate *turnGate,
	in consolidationReceipt,
) *OperationUsage {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), persistTimeout)
	defer cancel()

	price := gate.price
	if price == nil {
		price = s.priceFor(writeCtx, creds, agent.Model)
	}
	acct := newTurnAccounting(accountingInput{
		StreamOpened:          true,
		Usage:                 in.Usage,
		Price:                 price,
		EstimatedPromptTokens: in.EstimatedPromptTokens,
		Content:               in.Answer,
	})

	msg := &domain.Message{
		ID:             uuid.New(),
		WorkspaceID:    conv.WorkspaceID,
		ConversationID: conv.ID,
		Role:           domain.RoleAssistant,
		Kind:           domain.KindAuxiliary,
		Model:          agent.Model,
		// No content and no reasoning. A receipt records what an operation
		// cost, and the conversation it belongs to is not the place to keep
		// what the model said about it.
		UsageSource:           acct.Source,
		PromptTokens:          acct.PromptTokens,
		CompletionTokens:      acct.CompletionTokens,
		InputCostPerToken:     acct.InputCostPerToken,
		OutputCostPerToken:    acct.OutputCostPerToken,
		Cost:                  acct.Cost,
		EstimatedPromptTokens: acct.EstimatedPromptTokens,
		FinishReason:          string(domain.FinishStop),
	}
	if err := msg.Validate(); err != nil {
		s.log.Error("memory consolidation receipt is invalid",
			"conversation_id", conv.ID, "err", err)
		return nil
	}
	if err := s.repos.Messages.Create(writeCtx, msg); err != nil {
		s.log.Error("persist memory consolidation receipt",
			"conversation_id", conv.ID, "agent_id", agent.ID, "err", err)
		return nil
	}

	return &OperationUsage{
		Model:            agent.Model,
		PromptTokens:     acct.PromptTokens,
		CompletionTokens: acct.CompletionTokens,
		UsageSource:      string(acct.Source),
		CostUSD:          acct.Cost,
	}
}

/* ── deduplication ───────────────────────────────────────────────────── */

// markDuplicates points each candidate at an existing memory with the same
// normalized text, when there is one.
//
// ── Why they are marked and not removed ────────────────────────────────
// A list that quietly dropped what the agent already knows would leave the
// user comparing a shorter list against a memory page and wondering which
// of their facts the model failed to notice. Saying "this one is already
// there" is the auditable answer, and the interface can default it to
// unselected without pretending it was never proposed.
//
// Exact and normalized only: see domain.NormalizeMemoryText for what that
// does and does not catch.
func (s *Service) markDuplicates(ctx context.Context, workspaceID, agentID uuid.UUID, candidates []domain.MemoryCandidate) error {
	if len(candidates) == 0 {
		return nil
	}
	existing, err := s.repos.Memories.ListByAgent(ctx, workspaceID, agentID, MemoryListLimit)
	if err != nil {
		return err
	}
	byText := make(map[string]uuid.UUID, len(existing))
	for _, m := range existing {
		key := domain.NormalizeMemoryText(m.Content)
		if _, seen := byText[key]; !seen {
			byText[key] = m.ID
		}
	}
	for i := range candidates {
		if id, ok := byText[domain.NormalizeMemoryText(candidates[i].Content)]; ok {
			match := id
			candidates[i].DuplicateOf = &match
		}
	}
	return nil
}

/* ── confirmation ────────────────────────────────────────────────────── */

// ConfirmCandidatesInput is what the user decided to keep.
type ConfirmCandidatesInput struct {
	WorkspaceID    uuid.UUID
	ConversationID uuid.UUID
	// Contents are the final texts, as the user approved them. Editing a
	// proposal before accepting it is expected — the distilling is the
	// point — and what gets stored is what they approved, never what the
	// model first wrote.
	Contents []string
	// UpToSeq is the snapshot the proposals were read from, echoed back by
	// the client from `effective_up_to_seq`. It becomes the provenance of
	// every memory this creates.
	UpToSeq int64
}

// MaxConfirmedCandidates bounds one confirmation. It is the same ceiling
// the extraction obeys: the user cannot confirm more proposals than there
// could have been.
const MaxConfirmedCandidates = domain.MaxMemoryCandidates

// ConfirmCandidates saves the proposals the user kept.
//
// ── Why this route exists at all ───────────────────────────────────────
// Because `model_proposed` is a fact about how a memory came to be, and
// facts are not fields a request may set. The manual route writes false and
// has no way to say otherwise; this one writes true and has no
// way to say otherwise either. Which flow ran is what decides, and the flow
// is the address.
//
// ── Why it is under the conversation ───────────────────────────────────
// Provenance is the conversation. Saving through the agent's own memory
// route would mean passing a conversation id as an argument that the
// address does not mention, and the module's rule is that an address
// agrees with the model it addresses.
//
// ── Why it is one transaction ──────────────────────────────────────────
// The user selected a set and pressed one button. Half a set saved, with an
// error explaining that the other half was refused, would leave them
// comparing a dialog they can no longer see against a list that changed.
func (s *Service) ConfirmCandidates(ctx context.Context, in ConfirmCandidatesInput) ([]domain.Memory, error) {
	if len(in.Contents) == 0 {
		return nil, domain.Invalid("no candidate was selected")
	}
	if len(in.Contents) > MaxConfirmedCandidates {
		return nil, domain.Invalid("at most 5 candidates can be confirmed at once")
	}

	conv, err := s.repos.Conversations.FindByID(ctx, in.WorkspaceID, in.ConversationID)
	if err != nil {
		return nil, err
	}

	// The seq is a hint about where a memory came from, and a hint that
	// names a message the thread never had is worse than none.
	var seq *int64
	if in.UpToSeq > 0 {
		s := in.UpToSeq
		seq = &s
	}

	saved := make([]domain.Memory, 0, len(in.Contents))
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		saved = saved[:0]
		for _, content := range in.Contents {
			m, err := s.CreateProposedMemory(ctx, CreateProposedMemoryInput{
				WorkspaceID:          in.WorkspaceID,
				AgentID:              conv.AgentID,
				Content:              content,
				SourceConversationID: conv.ID,
				SourceMessageSeq:     seq,
			})
			if err != nil {
				return err
			}
			saved = append(saved, *m)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return saved, nil
}
