package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

// The executor's own tests.
//
// They live here rather than in the integration suite because the two
// properties that matter most — a deadline and a cancellation — need an
// executor that can hang, and the built-in tool deliberately cannot. A tool
// that could block would be a worse fixture for everything else.

/* ── a controllable tool ─────────────────────────────────────────────── */

type scriptedTool struct {
	name domain.ToolName
	run  func(ctx context.Context, args map[string]any) (domain.ToolOutput, error)
}

func (s scriptedTool) Definition() domain.ToolDefinition {
	return domain.ToolDefinition{
		Name:        s.name,
		Title:       "Scripted",
		Description: "A tool the test drives.",
		Effect:      domain.EffectRead,
		Schema: domain.ToolSchema{
			Properties: map[string]domain.ToolProperty{"text": {Type: domain.TypeString}},
			Required:   []string{"text"},
		},
	}
}

func (s scriptedTool) Execute(ctx context.Context, args map[string]any) (domain.ToolOutput, error) {
	return s.run(ctx, args)
}

type fixedRegistry map[domain.ToolName]ports.Tool

func (r fixedRegistry) Lookup(n domain.ToolName) (ports.Tool, bool) {
	t, ok := r[n]
	return t, ok
}

func (r fixedRegistry) Definitions() []domain.ToolDefinition {
	out := make([]domain.ToolDefinition, 0, len(r))
	for _, t := range r {
		out = append(out, t.Definition())
	}
	return out
}

// serviceWith builds the smallest service that can run a tool: no database,
// no gateway, just the registry and a logger.
func serviceWith(tool ports.Tool) *Service {
	return &Service{
		tools: fixedRegistry{tool.Definition().Name: tool},
		log:   slog.New(slog.NewJSONHandler(io.Discard, nil)),
	}
}

func call(name, args string) domain.ToolCall {
	return domain.ToolCall{ID: "call_1", Name: domain.ToolName(name), Arguments: args}
}

/* ── the four gates, in order ────────────────────────────────────────── */

// The order is not decorative: authorization is checked before the
// arguments are even parsed, so an unauthorized call never reaches a
// validator that might be persuaded to do work.
func TestTheGatesRunInOrder(t *testing.T) {
	tool := scriptedTool{name: "test.tool", run: func(context.Context, map[string]any) (domain.ToolOutput, error) {
		t.Fatal("the executor ran for a call that should have been refused")
		return nil, nil
	}}
	svc := serviceWith(tool)
	authorized := []domain.ToolDefinition{tool.Definition()}

	// Not in the registry at all.
	got := svc.executeToolCall(context.Background(), authorized, call("test.absent", `{"text":"a"}`))
	if got.Failure == nil || got.Failure.Code != domain.ToolErrNotFound {
		t.Fatalf("unknown tool = %+v, want tool_not_found", got.Failure)
	}

	// In the registry, not granted — and the arguments are invalid, which
	// must NOT be what it is told: the refusal comes first.
	got = svc.executeToolCall(context.Background(), nil, call("test.tool", `{"nonsense"`))
	if got.Failure == nil || got.Failure.Code != domain.ToolErrNotAuthorized {
		t.Fatalf("ungranted tool = %+v, want tool_not_authorized before validation", got.Failure)
	}

	// Granted, and now the arguments are checked.
	got = svc.executeToolCall(context.Background(), authorized, call("test.tool", `{"nonsense"`))
	if got.Failure == nil || got.Failure.Code != domain.ToolErrInvalidArguments {
		t.Fatalf("bad arguments = %+v, want tool_invalid_arguments", got.Failure)
	}
}

// Being in the registry is availability. Being in the authorized slice is
// permission. Conflating the two is the single mistake this design exists
// to make impossible, so it is asserted directly.
func TestRegistrationIsNotPermission(t *testing.T) {
	ran := false
	tool := scriptedTool{name: "test.tool", run: func(context.Context, map[string]any) (domain.ToolOutput, error) {
		ran = true
		return domain.ToolOutput{"ok": true}, nil
	}}
	svc := serviceWith(tool)

	got := svc.executeToolCall(context.Background(), nil, call("test.tool", `{"text":"a"}`))
	if ran {
		t.Fatal("a registered but ungranted tool was executed")
	}
	// NOT_EXECUTED rather than error: the tool did not fail at its job, it
	// never got one. The distinction is what lets a write receipt say
	// whether something was attempted, and a refusal that looked like a
	// failure would report an agent as broken when it was correctly stopped.
	if got.Status != domain.ToolCallNotExecuted || got.Failure.Code != domain.ToolErrNotAuthorized {
		t.Fatalf("outcome = %+v", got)
	}
}

/* ── success ─────────────────────────────────────────────────────────── */

func TestASuccessfulCallReturnsJSONAndRecordsIt(t *testing.T) {
	tool := scriptedTool{name: "test.tool", run: func(_ context.Context, args map[string]any) (domain.ToolOutput, error) {
		return domain.ToolOutput{"echo": args["text"]}, nil
	}}
	got := serviceWith(tool).executeToolCall(context.Background(),
		[]domain.ToolDefinition{tool.Definition()}, call("test.tool", `{"text":"olá"}`))

	if got.Status != domain.ToolCallOK || got.Failure != nil {
		t.Fatalf("outcome = %+v", got)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(got.Content), &decoded); err != nil {
		t.Fatalf("the model was sent something that is not JSON: %q", got.Content)
	}
	if decoded["echo"] != "olá" {
		t.Fatalf("content = %q", got.Content)
	}
	if got.Result == nil || *got.Result != got.Content {
		t.Fatal("the audit trail did not record what the tool returned")
	}
}

/* ── failure ─────────────────────────────────────────────────────────── */

// An executor's own failure is reported as such and reaches the model in
// the same shape as every other failure, so it reads one format.
func TestAnExecutorFailureIsReportedToTheModel(t *testing.T) {
	tool := scriptedTool{name: "test.tool", run: func(context.Context, map[string]any) (domain.ToolOutput, error) {
		return nil, domain.ToolError(domain.ToolErrExecutionFailed, "the repository does not exist")
	}}
	got := serviceWith(tool).executeToolCall(context.Background(),
		[]domain.ToolDefinition{tool.Definition()}, call("test.tool", `{"text":"a"}`))

	if got.Failure == nil || got.Failure.Code != domain.ToolErrExecutionFailed {
		t.Fatalf("outcome = %+v", got)
	}
	if got.Result != nil {
		t.Error("a failed call recorded a result")
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(got.Content), &decoded); err != nil {
		t.Fatalf("the failure sent to the model is not JSON: %q", got.Content)
	}
	if decoded["error"] != string(domain.ToolErrExecutionFailed) {
		t.Errorf("error code sent to the model = %q", decoded["error"])
	}
	if !strings.Contains(decoded["message"], "does not exist") {
		t.Errorf("the model was not told what went wrong: %q", decoded["message"])
	}
}

// A plain error — one that is not a ToolFailure — is still an execution
// failure and still reaches the model. An executor is not required to know
// this package's vocabulary.
func TestAPlainErrorIsStillAnExecutionFailure(t *testing.T) {
	tool := scriptedTool{name: "test.tool", run: func(context.Context, map[string]any) (domain.ToolOutput, error) {
		return nil, io.ErrUnexpectedEOF
	}}
	got := serviceWith(tool).executeToolCall(context.Background(),
		[]domain.ToolDefinition{tool.Definition()}, call("test.tool", `{"text":"a"}`))
	if got.Failure == nil || got.Failure.Code != domain.ToolErrExecutionFailed {
		t.Fatalf("outcome = %+v", got)
	}
}

// Every byte a tool returns becomes a prompt token on the next call of the
// same turn. A tool that wants to return more than the ceiling is told so
// through the ordinary failure path, so the model can ask for less.
func TestAnOversizedResultIsRefused(t *testing.T) {
	tool := scriptedTool{name: "test.tool", run: func(context.Context, map[string]any) (domain.ToolOutput, error) {
		return domain.ToolOutput{"blob": strings.Repeat("x", domain.MaxToolResultBytes+1)}, nil
	}}
	got := serviceWith(tool).executeToolCall(context.Background(),
		[]domain.ToolDefinition{tool.Definition()}, call("test.tool", `{"text":"a"}`))

	if got.Failure == nil || got.Failure.Code != domain.ToolErrExecutionFailed {
		t.Fatalf("outcome = %+v, want the size refused", got.Failure)
	}
	if !strings.Contains(got.Failure.Message, "more data than one turn may carry") {
		t.Errorf("message = %q", got.Failure.Message)
	}
}

/* ── timeout and cancellation ────────────────────────────────────────── */

// A hung executor is a delay, never an outage. The deadline is reported as
// a timeout regardless of how the executor phrased it, because what the
// model and the audit trail need to know is that it ran out of time.
func TestAHungToolTimesOut(t *testing.T) {
	tool := scriptedTool{name: "test.tool", run: func(ctx context.Context, _ map[string]any) (domain.ToolOutput, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	svc := serviceWith(tool)

	// A short deadline in place of the ten-second one: the property under
	// test is that the executor's context governs, and a test that waited
	// ten seconds to prove it would be a test nobody runs.
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()

	start := time.Now()
	got := svc.executeToolCall(ctx, []domain.ToolDefinition{tool.Definition()}, call("test.tool", `{"text":"a"}`))
	elapsed := time.Since(start)

	if got.Failure == nil || got.Failure.Code != domain.ToolErrTimeout {
		t.Fatalf("outcome = %+v, want tool_timeout", got.Failure)
	}
	if elapsed > time.Second {
		t.Fatalf("the call took %v; the deadline did not govern", elapsed)
	}
}

// Pressing stop cancels a tool that is already running. Without this the
// user's stop would end the stream and leave an executor working — and
// paying — on their behalf.
func TestStopCancelsARunningTool(t *testing.T) {
	released := make(chan struct{})
	tool := scriptedTool{name: "test.tool", run: func(ctx context.Context, _ map[string]any) (domain.ToolOutput, error) {
		<-ctx.Done()
		close(released)
		return nil, ctx.Err()
	}}
	svc := serviceWith(tool)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	got := svc.executeToolCall(ctx, []domain.ToolDefinition{tool.Definition()}, call("test.tool", `{"text":"a"}`))

	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("the executor never observed the cancellation")
	}
	// A cancellation happens INSIDE gate 4: the tool was running. That is a
	// genuine execution failure, unlike the refusals above.
	if got.Status != domain.ToolCallError {
		t.Fatalf("outcome = %+v, want a failure", got)
	}
}

// The caller's context reaches the executor, values and all.
//
// ── Why this is an invariant and not an implementation detail ──────────
// `ports.Tool.Execute` takes a context and a validated argument map, and
// nothing else. That is enough for a self-contained tool, but a tool backed
// by an external system needs to know which workspace it is serving — and
// the platform already puts that on the request context, where the SSE
// handler, SendMessage and this executor all pass it along.
//
// So an integration-backed tool reads it from there, and its correctness
// depends on this chain staying unbroken. A refactor that replaced the
// context here (rather than deriving from it) would silently strip the
// workspace, and every such tool would start failing closed at runtime with
// no test pointing at the cause. This is that test. It uses an anonymous
// key so nothing in this module has to learn what a workspace is.
func TestTheCallersContextValuesReachTheExecutor(t *testing.T) {
	type probeKey struct{}

	var seen any
	tool := scriptedTool{name: "test.tool", run: func(ctx context.Context, _ map[string]any) (domain.ToolOutput, error) {
		seen = ctx.Value(probeKey{})
		return domain.ToolOutput{}, nil
	}}

	ctx := context.WithValue(context.Background(), probeKey{}, "carried")
	serviceWith(tool).executeToolCall(ctx,
		[]domain.ToolDefinition{tool.Definition()}, call("test.tool", `{"text":"a"}`))

	if seen != "carried" {
		t.Fatal("a value on the caller's context did not reach the executor; " +
			"a tool that resolves its workspace from the context would fail closed")
	}
}

// The executor is bounded even when the caller's context is not. The
// constant is the ceiling every future integration-backed tool inherits, so
// it must actually be applied rather than merely declared.
func TestTheExecutorAppliesItsOwnDeadline(t *testing.T) {
	var deadline time.Time
	var ok bool
	tool := scriptedTool{name: "test.tool", run: func(ctx context.Context, _ map[string]any) (domain.ToolOutput, error) {
		deadline, ok = ctx.Deadline()
		return domain.ToolOutput{}, nil
	}}
	serviceWith(tool).executeToolCall(context.Background(),
		[]domain.ToolDefinition{tool.Definition()}, call("test.tool", `{"text":"a"}`))

	if !ok {
		t.Fatal("the executor ran with no deadline at all")
	}
	if remaining := time.Until(deadline); remaining > toolExecutionTimeout {
		t.Fatalf("deadline is %v away, above the %v ceiling", remaining, toolExecutionTimeout)
	}
}

/* ── the aggregation of a multi-call turn ────────────────────────────── */

// Sums, not the last call — with the per-round breakdown kept beside them,
// because a sum answers "how much" and destroys "why".
func TestAggregateTurnSumsEveryCall(t *testing.T) {
	price := &ports.Price{InputCostPerToken: 1e-6, OutputCostPerToken: 3e-6}
	acct, rounds := aggregateTurn([]providerRound{
		{Opened: true, Usage: &ports.Usage{PromptTokens: 30, CompletionTokens: 8},
			EstimatedPromptTokens: 20, FinishReason: domain.FinishToolCalls},
		{Opened: true, Usage: &ports.Usage{PromptTokens: 90, CompletionTokens: 20},
			EstimatedPromptTokens: 45, AddedCharacters: 100, FinishReason: domain.FinishStop},
	}, price, EstimatorFor(""))

	if acct.PromptTokens != 120 || acct.CompletionTokens != 28 {
		t.Fatalf("tokens = %d/%d, want 120/28", acct.PromptTokens, acct.CompletionTokens)
	}
	if acct.Source != domain.UsageProvider {
		t.Errorf("source = %q", acct.Source)
	}
	want := 120*1e-6 + 28*3e-6
	if acct.Cost == nil || *acct.Cost < want*0.999 || *acct.Cost > want*1.001 {
		t.Fatalf("cost = %v, want %v", acct.Cost, want)
	}
	if acct.EstimatedPromptTokens == nil || *acct.EstimatedPromptTokens != 65 {
		t.Fatalf("estimate = %v, want the two rounds' predictions summed", acct.EstimatedPromptTokens)
	}
	if len(rounds) != 2 || rounds[1].AddedEstimatedTokens != EstimateTokens(100) {
		t.Fatalf("rounds = %+v", rounds)
	}
}

// A single-round turn produces exactly what the pre-tools code produced.
// This is the regression that says the accounting rewrite changed nothing
// for the turns that make up almost all of them.
func TestASingleRoundTurnIsAccountedForExactlyAsBefore(t *testing.T) {
	price := &ports.Price{InputCostPerToken: 1e-6, OutputCostPerToken: 3e-6}
	round := providerRound{
		Opened:                true,
		Usage:                 &ports.Usage{PromptTokens: 41, CompletionTokens: 17},
		EstimatedPromptTokens: 30,
		Content:               "olá",
	}
	acct, rounds := aggregateTurn([]providerRound{round}, price, EstimatorFor(""))

	direct := newTurnAccounting(accountingInput{
		StreamOpened: true, Usage: round.Usage, Price: price,
		EstimatedPromptTokens: round.EstimatedPromptTokens, Content: round.Content,
	})

	if acct.PromptTokens != direct.PromptTokens || acct.CompletionTokens != direct.CompletionTokens {
		t.Fatalf("tokens diverged: %+v vs %+v", acct, direct)
	}
	if acct.Source != direct.Source {
		t.Fatalf("source diverged: %q vs %q", acct.Source, direct.Source)
	}
	if *acct.Cost != *direct.Cost {
		t.Fatalf("cost diverged: %v vs %v", *acct.Cost, *direct.Cost)
	}
	if len(rounds) != 1 {
		t.Fatalf("%d rounds for a single call", len(rounds))
	}
}

// A turn where nothing reached the model is unknown, and stays unknown —
// the same answer it gave before tools existed. The prediction is still
// recorded, because it describes what was about to be sent.
func TestATurnThatNeverOpenedIsUnknown(t *testing.T) {
	acct, rounds := aggregateTurn([]providerRound{
		{Opened: false, EstimatedPromptTokens: 30},
	}, &ports.Price{InputCostPerToken: 1e-6}, EstimatorFor(""))

	if acct.Source != domain.UsageUnknown {
		t.Fatalf("source = %q, want unknown", acct.Source)
	}
	if acct.PromptTokens != 0 || acct.Cost != nil {
		t.Fatalf("a call that never happened recorded consumption: %+v", acct)
	}
	if acct.EstimatedPromptTokens == nil || *acct.EstimatedPromptTokens != 30 {
		t.Fatalf("estimate = %v, want the builder's prediction kept", acct.EstimatedPromptTokens)
	}
	if len(rounds) != 1 || rounds[0].UsageSource != domain.UsageUnknown {
		t.Fatalf("rounds = %+v", rounds)
	}
}
