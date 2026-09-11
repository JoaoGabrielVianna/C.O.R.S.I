//go:build litellm

// The real external boundary: this suite talks to an actual LiteLLM.
//
// ── It is NOT part of any gate ─────────────────────────────────────────
// Guarded by its own build tag, so `go test ./...`, `make test` and
// `make ci` never compile it. The gate must stay deterministic and offline;
// a suite that fails when someone else's gateway is down is a suite that
// teaches people to ignore red.
//
// ── How to run it ──────────────────────────────────────────────────────
//
//	LITELLM_BASE_URL=... LITELLM_API_KEY=... TEST_POSTGRES_DSN=... \
//	  go test -tags='integration litellm' -run TestLive ./internal/chat/
//
// The key comes from the environment and nowhere else. It is never a
// literal here, never written to a fixture, and never logged: what this
// file asserts about it is its *shape* and its *effects*, never its value.
//
// ── Why it wires the real service ──────────────────────────────────────
// The point of X1 is not that the adapter can talk to LiteLLM in
// isolation. It is that the whole chain holds:
//
//	Context Builder → LiteLLM → accounting → chat.messages → Inspector
//
// so this builds the production service with the production adapter and
// drives it through the production route.
package chat

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/adapters/httpapi"
	"github.com/corsi/backend/internal/chat/adapters/llm"
	"github.com/corsi/backend/internal/chat/adapters/references"
	"github.com/corsi/backend/internal/chat/adapters/repo"
	"github.com/corsi/backend/internal/chat/adapters/tools"
	"github.com/corsi/backend/internal/chat/app"
	"github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/platform/postgres"
	"github.com/corsi/backend/internal/platform/secrets"
	"github.com/corsi/backend/internal/platform/workspace"
)

/* ── harness ─────────────────────────────────────────────────────────── */

type liveEnv struct {
	t     *testing.T
	r     chi.Router
	llm   *llm.Client
	creds ports.Credentials
	ws    uuid.UUID
	model string
}

func liveCreds(t *testing.T) ports.Credentials {
	t.Helper()
	base, key := os.Getenv("LITELLM_BASE_URL"), os.Getenv("LITELLM_API_KEY")
	if base == "" || key == "" {
		t.Skip("LITELLM_BASE_URL / LITELLM_API_KEY not set; skipping the real-boundary suite")
	}
	return ports.Credentials{BaseURL: base, APIKey: key}
}

// newLiveEnv wires the production service over a real database with the
// REAL adapter. Everything below the transport is production code, and the
// LLM port is not faked — which is the entire point.
func newLiveEnv(t *testing.T) *liveEnv {
	t.Helper()
	creds := liveCreds(t)
	d := dsn(t)
	freshDB(t, d)

	pool, err := postgres.Open(t.Context(), postgres.Config{
		DSN: d, MaxConns: 4, MinConns: 1,
		MaxConnLifetime: time.Hour, MaxConnIdleTime: 30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	sealer, err := secrets.New(secrets.Config{
		Key: base64.StdEncoding.EncodeToString([]byte("corsi-test-key-not-a-real-secret")),
	})
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}

	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	client := llm.New(log)
	svc := app.NewService(repo.New(pool), postgres.NewTxManager(pool), client, sealer,
		tools.MustNew(tools.Options{Internal: true}), references.MustNew(), log)
	h := httpapi.NewHandler(svc, log)

	root := chi.NewRouter()
	root.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			id, err := uuid.Parse(req.Header.Get("X-Test-Workspace-Id"))
			if err != nil {
				http.Error(w, "test header missing", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, req.WithContext(workspace.WithWorkspaceID(req.Context(), id)))
		})
	})
	root.Route("/chat", func(r chi.Router) { h.Mount(r) })

	model := os.Getenv("LITELLM_MODEL")
	if model == "" {
		model = "claude-haiku-4-5"
	}
	return &liveEnv{t: t, r: root, llm: client, creds: creds, ws: uuid.New(), model: model}
}

func (e *liveEnv) do(method, path string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var br io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal: %v", err)
		}
		br = strings.NewReader(string(raw))
	}
	req := httptest.NewRequest(method, path, br).WithContext(e.t.Context())
	if br != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Test-Workspace-Id", e.ws.String())
	rec := httptest.NewRecorder()
	e.r.ServeHTTP(rec, req)
	return rec
}

// seed creates the provider → agent → conversation chain against the real
// gateway. The credential is sealed into a THROWAWAY database that the run
// destroys; it is never written to the dev or production one.
func (e *liveEnv) seed(instructions string) seeded {
	e.t.Helper()
	rec := e.do("POST", "/chat/providers", map[string]any{
		"name":          "LiteLLM X1 " + uuid.NewString()[:8],
		"base_url":      e.creds.BaseURL,
		"api_key":       e.creds.APIKey,
		"default_model": e.model,
	})
	wantStatus(e.t, rec, http.StatusCreated)
	provider := decode[map[string]any](e.t, rec)

	rec = e.do("POST", "/chat/agents", map[string]any{
		"provider_id":   provider["id"],
		"name":          "X1 " + uuid.NewString()[:8],
		"system_prompt": instructions,
		"model":         e.model,
		"max_tokens":    32,
		"temperature":   0,
	})
	wantStatus(e.t, rec, http.StatusCreated)
	agent := decode[map[string]any](e.t, rec)

	rec = e.do("POST", "/chat/conversations", map[string]any{"agent_id": agent["id"]})
	wantStatus(e.t, rec, http.StatusCreated)
	conv := decode[map[string]any](e.t, rec)

	return seeded{
		providerID:     provider["id"].(string),
		agentID:        agent["id"].(string),
		conversationID: conv["id"].(string),
	}
}

/* ── the read endpoints ──────────────────────────────────────────────── */

// TestLiveCatalogAndPricing checks the three read endpoints the module
// depends on, through the adapter rather than through curl.
func TestLiveCatalogAndPricing(t *testing.T) {
	creds := liveCreds(t)
	client := llm.New(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	ctx := t.Context()

	models, err := client.Models(ctx, creds)
	if err != nil {
		t.Fatalf("Models: %v", err)
	}
	if len(models) == 0 {
		t.Fatalf("the gateway returned an empty catalog; the parser found no `data[].id`")
	}
	t.Logf("catalog: %d models, first id = %q", len(models), models[0].ID)

	prices, err := client.ModelPrices(ctx, creds)
	if err != nil {
		t.Fatalf("ModelPrices: %v", err)
	}
	if len(prices) == 0 {
		t.Fatalf("the gateway returned no priced models; /model/info was parsed into nothing")
	}

	// Every model the catalog offers must be priceable by name, or
	// priceAtTurn silently records an unpriced turn for a model the user
	// can actually select.
	for _, m := range models {
		p, ok := prices[m.ID]
		if !ok {
			t.Errorf("model %q is selectable but has no price entry", m.ID)
			continue
		}
		// The unit. Per-single-token dollars land far below 1; a per-million
		// rate card would land far above it. This is the assertion that
		// would have caught a six-orders-of-magnitude cost error.
		if p.InputCostPerToken <= 0 || p.InputCostPerToken > 0.001 {
			t.Errorf("model %q input price %v is outside the per-single-token range; accounting multiplies tokens×price directly",
				m.ID, p.InputCostPerToken)
		}
		if p.OutputCostPerToken <= 0 || p.OutputCostPerToken > 0.001 {
			t.Errorf("model %q output price %v is outside the per-single-token range", m.ID, p.OutputCostPerToken)
		}
		t.Logf("price %-20s in=%v out=%v", m.ID, p.InputCostPerToken, p.OutputCostPerToken)
	}
}

// TestLiveKeyInfo verifies the premise the roadmap rests a hard stop on:
// that a scoped virtual key can read its own record without a master key.
func TestLiveKeyInfo(t *testing.T) {
	creds := liveCreds(t)
	client := llm.New(slog.New(slog.NewJSONHandler(io.Discard, nil)))

	spend, err := client.KeyInfo(t.Context(), creds)
	if err != nil {
		t.Fatalf("KeyInfo: %v", err)
	}
	if spend.KeyAlias == "" {
		t.Errorf("key_alias came back empty; the parser read the wrong envelope")
	}
	if spend.Spend < 0 {
		t.Errorf("spend = %v", spend.Spend)
	}
	if len(spend.Models) == 0 {
		t.Errorf("the key reports no model scope")
	}
	// MaxBudget is a *float64 precisely so "no cap" and "a cap of zero" stay
	// distinguishable. Which one this key is, is reported rather than
	// asserted — it is a property of the key, not of the code.
	cap := "none"
	if spend.MaxBudget != nil {
		cap = fmt.Sprintf("%v", *spend.MaxBudget)
	}
	t.Logf("key alias=%q spend=%v max_budget=%s models=%d",
		spend.KeyAlias, spend.Spend, cap, len(spend.Models))
}

/* ── errors ──────────────────────────────────────────────────────────── */

// TestLiveErrorTranslation drives two real failures through the adapter and
// checks they arrive as actionable boundary errors rather than as raw
// gateway noise.
func TestLiveErrorTranslation(t *testing.T) {
	creds := liveCreds(t)
	client := llm.New(slog.New(slog.NewJSONHandler(io.Discard, nil)))

	t.Run("a model the key may not use", func(t *testing.T) {
		_, err := client.Stream(t.Context(), ports.CompletionRequest{
			Creds: creds, Model: "modelo-que-nao-existe-x1", MaxTokens: 4,
			Messages: []ports.ChatMessage{{Role: "user", Content: "oi"}},
		})
		if err == nil {
			t.Fatalf("a nonexistent model was accepted")
		}
		if !strings.Contains(err.Error(), "LLM provider returned") {
			t.Fatalf("error was not translated at the boundary: %v", err)
		}
		t.Logf("translated: %v", err)
	})

	t.Run("a credential the gateway rejects", func(t *testing.T) {
		bad := ports.Credentials{BaseURL: creds.BaseURL, APIKey: "sk-not-a-valid-key-x1"}
		_, err := client.Models(t.Context(), bad)
		if err == nil {
			t.Fatalf("an invalid key was accepted")
		}
		if !strings.Contains(err.Error(), "check the API key") {
			t.Fatalf("the message does not say what to fix: %v", err)
		}
		// The rejected key must not be echoed back in the message.
		if strings.Contains(err.Error(), "sk-not-a-valid-key-x1") {
			t.Fatalf("the credential leaked into the error: %v", err)
		}
	})
}

/* ── the whole chain ─────────────────────────────────────────────────── */

// TestLiveAccountingEndToEnd is the test X1 exists for.
//
// One real turn, through the production route, against the real gateway,
// and then every link of the chain checked against the next:
//
//	the builder's estimate      → persisted as estimated_prompt_tokens
//	the gateway's usage frame   → persisted as prompt/completion tokens
//	the gateway's rate card     → persisted as the two per-token prices
//	tokens × prices             → persisted as cost
//	all of it                   → readable by the Inspector, after the fact
func TestLiveAccountingEndToEnd(t *testing.T) {
	e := newLiveEnv(t)
	s := e.seed("Responda com uma única palavra.")

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages",
		map[string]any{"content": "diga ok"})
	wantStatus(t, rec, http.StatusOK)

	frames := parseSSE(t, rec.Body.String())
	if _, isErr := frameOf(frames, "error"); isErr {
		t.Fatalf("the real turn failed: %v", rec.Body.String())
	}
	t.Logf("SSE frames: %v", eventNames(frames))

	msgs := e.messages()
	if len(msgs) != 2 {
		t.Fatalf("transcript has %d messages, want the question and the answer", len(msgs))
	}
	turn := msgs[1]

	/* ── the provider actually reported usage ──────────────────────── */

	if turn.UsageSource != "provider" {
		t.Fatalf("usage_source = %q, want provider — stream_options.include_usage did not produce a usage frame",
			turn.UsageSource)
	}
	if turn.PromptTokens <= 0 || turn.CompletionTokens <= 0 {
		t.Fatalf("provider usage = %d/%d; both must be positive on a real turn",
			turn.PromptTokens, turn.CompletionTokens)
	}

	/* ── the estimate survived beside the measurement ──────────────── */

	if turn.EstimatedPromptTokens == nil {
		t.Fatalf("the builder's estimate was not persisted")
	}
	if turn.ContextReport == nil {
		t.Fatalf("the turn carries no context report")
	}
	if turn.ContextReport.TotalEstimatedTokens != *turn.EstimatedPromptTokens {
		t.Fatalf("report total %d != estimated_prompt_tokens %d",
			turn.ContextReport.TotalEstimatedTokens, *turn.EstimatedPromptTokens)
	}
	t.Logf("input: estimated %d, provider %d (delta %+d) | output %d",
		*turn.EstimatedPromptTokens, turn.PromptTokens,
		turn.PromptTokens-*turn.EstimatedPromptTokens, turn.CompletionTokens)

	/* ── the model that answered is the model that was priced ──────── */

	if turn.Model != e.model {
		t.Fatalf("requested %q, the turn recorded %q — pricing looks up the recorded id",
			e.model, turn.Model)
	}

	/* ── the rate card was frozen, and the cost is what it implies ─── */

	if turn.InputCostPerToken == nil || turn.OutputCostPerToken == nil {
		t.Fatalf("the turn stored no rate card; /model/info did not resolve %q", turn.Model)
	}
	if turn.Cost == nil {
		t.Fatalf("the turn stored no cost")
	}
	want := float64(turn.PromptTokens)**turn.InputCostPerToken +
		float64(turn.CompletionTokens)**turn.OutputCostPerToken
	if !nearly(*turn.Cost, want) {
		t.Fatalf("cost = %v, but tokens × frozen rates = %v", *turn.Cost, want)
	}

	// Sanity on the unit, from the other direction: a turn this small
	// cannot plausibly cost a dollar. If the gateway ever switched to
	// per-million pricing, this is where it would show up as an absurdity.
	if *turn.Cost <= 0 || *turn.Cost > 0.01 {
		t.Fatalf("a %d/%d token turn cost %v; the pricing unit is not what accounting assumes",
			turn.PromptTokens, turn.CompletionTokens, *turn.Cost)
	}
	t.Logf("cost: %.10f USD (in %v/tok, out %v/tok)",
		*turn.Cost, *turn.InputCostPerToken, *turn.OutputCostPerToken)

	/* ── and the aggregation reports the same figure ───────────────── */

	rec = e.do("GET", "/chat/conversations/"+s.conversationID+"/usage", nil)
	wantStatus(t, rec, http.StatusOK)
	report := decode[usageReport](t, rec)
	if !nearly(report.EstimatedCost, *turn.Cost) {
		t.Fatalf("usage aggregation reports %v, the turn stored %v", report.EstimatedCost, *turn.Cost)
	}
	if report.TotalTokens != int64(turn.PromptTokens+turn.CompletionTokens) {
		t.Fatalf("aggregation totals %d tokens, the turn recorded %d",
			report.TotalTokens, turn.PromptTokens+turn.CompletionTokens)
	}
	if !report.Priced {
		t.Fatalf("the aggregation reports the turn as unpriced")
	}
}

// messages reads the transcript of the only conversation in this env.
func (e *liveEnv) messages() []apiMessage {
	e.t.Helper()
	rec := e.do("GET", "/chat/conversations", nil)
	wantStatus(e.t, rec, http.StatusOK)
	page := decode[conversationPage](e.t, rec)
	if len(page.Items) == 0 {
		e.t.Fatalf("no conversations")
	}
	id, _ := page.Items[0]["id"].(string)
	rec = e.do("GET", "/chat/conversations/"+id+"/messages", nil)
	wantStatus(e.t, rec, http.StatusOK)
	return decode[struct {
		Items []apiMessage `json:"items"`
	}](e.t, rec).Items
}

/* ── spend movement ──────────────────────────────────────────────────── */

// TestLiveSpendMovesAfterATurn relates our frozen figure to the gateway's
// own billing.
//
// It deliberately does NOT assert equality. The two numbers answer
// different questions — ours is "what this turn cost at the rate card we
// read", the gateway's is "what it billed", and caching, tiered pricing and
// reasoning tokens can legitimately separate them. What must be true is
// that spend moves, and that the two land in the same order of magnitude.
func TestLiveSpendMovesAfterATurn(t *testing.T) {
	e := newLiveEnv(t)
	s := e.seed("Responda com uma única palavra.")
	client := llm.New(slog.New(slog.NewJSONHandler(io.Discard, nil)))

	before, err := client.KeyInfo(t.Context(), e.creds)
	if err != nil {
		t.Fatalf("KeyInfo before: %v", err)
	}

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages",
		map[string]any{"content": "diga ok"})
	wantStatus(t, rec, http.StatusOK)

	turn := e.messages()[1]
	if turn.Cost == nil {
		t.Fatalf("no cost stored")
	}

	// LiteLLM writes spend asynchronously; give it a moment before reading.
	var after ports.KeySpend
	for i := 0; i < 10; i++ {
		time.Sleep(2 * time.Second)
		after, err = client.KeyInfo(t.Context(), e.creds)
		if err != nil {
			t.Fatalf("KeyInfo after: %v", err)
		}
		if after.Spend > before.Spend {
			break
		}
	}

	delta := after.Spend - before.Spend
	t.Logf("gateway spend moved %+.10f; we stored %.10f for the same turn", delta, *turn.Cost)
	if delta <= 0 {
		t.Logf("NOTE: spend did not move within the window. This is a gateway bookkeeping " +
			"observation, not a defect in our accounting; recorded rather than asserted.")
		return
	}
	ratio := delta / *turn.Cost
	if ratio < 0.1 || ratio > 10 {
		t.Errorf("our figure and the gateway's differ by %.1fx (ours %.10f, theirs %.10f); "+
			"that is beyond rounding and suggests a unit or semantic mismatch",
			ratio, *turn.Cost, delta)
	}
}

/* ── tool calling, against the real gateway ──────────────────────────── */

// TestLiveToolCallingEndToEnd is the Release Closure gate for Agents v1.0.0.
//
// Tool calling was built against the documented OpenAI-compatible
// contract and declared the specific external boundary NOT VERIFIED,
// because nothing had ever confronted it with a gateway. This is the test
// that confronts it, and it drives the whole chain through the PRODUCTION
// route rather than poking the adapter:
//
//	CORSI declares tools[]  →  the model asks for one
//	                        →  we resolve the name
//	                        →  we check the grant
//	                        →  we validate the arguments
//	                        →  we run system.echo
//	                        →  we send the result back
//	                        →  the model answers
//
// Everything it asserts is a property of that chain, not of a fixture.
//
// ── What it costs ──────────────────────────────────────────────────────
// Two provider calls with max_tokens 64 and a three-word instruction. In
// the X1 measurements a turn of this size cost around US$ 0,00004.
func TestLiveToolCallingEndToEnd(t *testing.T) {
	e := newLiveEnv(t)
	// The instruction is deliberately blunt. The gate is the transport and
	// the loop, not the model's judgement about when a tool is warranted,
	// and a model that decides to answer in prose would fail this test for
	// a reason that is not about compatibility.
	s := e.seedForTools("Use a ferramenta system.echo com o texto exato que o usuário pedir. " +
		"Depois de receber o resultado, responda em uma frase curta.")

	rec := e.do("POST", "/chat/agents/"+s.agentID+"/tools",
		map[string]any{"tool_name": "system.echo"})
	wantStatus(t, rec, http.StatusOK)

	rec = e.do("POST", "/chat/conversations/"+s.conversationID+"/messages",
		map[string]any{"content": `Ecoe exatamente: corsi-x2`})
	wantStatus(t, rec, http.StatusOK)

	frames := parseSSE(t, rec.Body.String())
	if f, isErr := frameOf(frames, "error"); isErr {
		t.Fatalf("the real tool turn failed: %s", f.data)
	}
	t.Logf("SSE frames: %v", eventNames(frames))

	/* ── the tool actually ran, and we said so on the wire ─────────── */

	var toolFrames []map[string]any
	for _, f := range frames {
		if f.event != "tool" {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(f.data), &payload); err != nil {
			t.Fatalf("tool frame is not JSON: %s", f.data)
		}
		toolFrames = append(toolFrames, payload)
	}
	if len(toolFrames) < 2 {
		t.Fatalf("%d tool frames; the model never asked for the tool. "+
			"Either the gateway did not accept the declaration, or the "+
			"tool_calls were not parsed. Frames: %v", len(toolFrames), eventNames(frames))
	}
	if toolFrames[0]["status"] != "running" || toolFrames[1]["status"] != "ok" {
		t.Fatalf("tool frames = %v; want a running followed by an ok", toolFrames)
	}
	if toolFrames[0]["name"] != "system.echo" {
		t.Fatalf("the frame names %v; the canonical name did not survive the wire encoding",
			toolFrames[0]["name"])
	}

	/* ── the audit trail records the real call ─────────────────────── */

	calls := e.toolCallsOf(s.conversationID)
	if len(calls) == 0 {
		t.Fatalf("no tool call was recorded")
	}
	c := calls[0]
	if c.Status != "ok" {
		t.Fatalf("the call failed: %s / %s", c.ErrorCode, c.ErrorMessage)
	}
	if c.ToolName != "system.echo" {
		t.Fatalf("tool_name = %q, want the canonical dotted name", c.ToolName)
	}
	// The gateway's own id, preserved. This is what correlates our record
	// with its logs, and answering with a different one is the bug that
	// makes a turn respond to the wrong question.
	if c.ProviderCallID == "" {
		t.Fatalf("the gateway sent no tool_call id, or we dropped it")
	}
	if c.Arguments == nil || !strings.Contains(*c.Arguments, "corsi-x2") {
		t.Fatalf("arguments = %v; the model's own JSON did not survive fragmentation", c.Arguments)
	}
	if c.Result == nil || !strings.Contains(*c.Result, "corsi-x2") {
		t.Fatalf("result = %v", c.Result)
	}
	t.Logf("tool call: id=%s args=%s result=%s (%dms)",
		c.ProviderCallID, *c.Arguments, *c.Result, c.DurationMS)

	/* ── the model continued, and the turn is one answer ───────────── */

	msgs := e.messages()
	if len(msgs) != 2 {
		t.Fatalf("transcript has %d messages, want the question and ONE answer", len(msgs))
	}
	turn := msgs[1]
	if turn.FinishReason != "stop" {
		t.Fatalf("finish_reason = %q, want stop: the loop did not reach a final answer",
			turn.FinishReason)
	}
	if strings.TrimSpace(turn.Content) == "" {
		t.Fatalf("the model produced no final answer after the tool result")
	}
	t.Logf("final answer: %q", turn.Content)

	/* ── accounting summed BOTH provider calls ─────────────────────── */

	if turn.ContextReport == nil {
		t.Fatalf("the turn carries no context report")
	}
	rounds := turn.ContextReport.Rounds
	if len(rounds) < 2 {
		t.Fatalf("the report records %d rounds, want at least 2: a tool turn is "+
			"two provider calls and both were billed", len(rounds))
	}
	sumPrompt, sumCompletion := 0, 0
	for _, r := range rounds {
		sumPrompt += r.PromptTokens
		sumCompletion += r.CompletionTokens
		t.Logf("round %d: %d in / %d out, source=%s, finish=%s, tools=%d",
			r.Round, r.PromptTokens, r.CompletionTokens, r.UsageSource, r.FinishReason, len(r.Tools))
	}
	if turn.PromptTokens != sumPrompt || turn.CompletionTokens != sumCompletion {
		t.Fatalf("the turn recorded %d/%d but its rounds sum to %d/%d",
			turn.PromptTokens, turn.CompletionTokens, sumPrompt, sumCompletion)
	}
	// The second call must carry more prompt than the first: it also carries
	// the tool exchange. If it does not, the result never reached the model.
	if rounds[1].PromptTokens <= rounds[0].PromptTokens {
		t.Fatalf("round 2 prompt (%d) is not larger than round 1 (%d); "+
			"the tool result did not reach the model",
			rounds[1].PromptTokens, rounds[0].PromptTokens)
	}
	if turn.Cost == nil || *turn.Cost <= 0 {
		t.Fatalf("cost = %v on a turn that made two billed calls", turn.Cost)
	}
	if *turn.Cost > 0.02 {
		t.Fatalf("a two-call turn cost %v; the pricing unit is not what accounting assumes", *turn.Cost)
	}
	t.Logf("turn: %d in / %d out over %d calls, cost %.10f USD",
		turn.PromptTokens, turn.CompletionTokens, len(rounds), *turn.Cost)
}

// TestLiveToolNameEncoding answers the question the unit suite could not: does this
// gateway accept a dotted function name, or is the `__` encoding load
// bearing?
//
// It asserts nothing about which answer is right. It RECORDS the answer, so
// the encoding is kept or dropped on evidence rather than on caution.
func TestLiveToolNameEncoding(t *testing.T) {
	creds := liveCreds(t)
	client := llm.New(slog.New(slog.NewJSONHandler(io.Discard, nil)))
	model := os.Getenv("LITELLM_MODEL")
	if model == "" {
		model = "claude-haiku-4-5"
	}

	// The adapter always encodes, so this probes the gateway directly with
	// each form and reports which ones it accepts.
	for _, form := range []string{"system__echo", "system.echo"} {
		t.Run(form, func(t *testing.T) {
			body := map[string]any{
				"model":      model,
				"max_tokens": 16,
				"stream":     false,
				"messages":   []map[string]string{{"role": "user", "content": "diga ok"}},
				"tools": []map[string]any{{
					"type": "function",
					"function": map[string]any{
						"name":        form,
						"description": "Returns the text it was given.",
						"parameters": map[string]any{
							"type":                 "object",
							"properties":           map[string]any{"text": map[string]any{"type": "string"}},
							"required":             []string{"text"},
							"additionalProperties": false,
						},
					},
				}},
			}
			raw, _ := json.Marshal(body)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
				strings.TrimSuffix(creds.BaseURL, "/")+"/chat/completions", strings.NewReader(string(raw)))
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+creds.APIKey)

			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			defer func() { _ = res.Body.Close() }()
			// Only the status and a short, key-free excerpt are logged.
			excerpt, _ := io.ReadAll(io.LimitReader(res.Body, 400))
			t.Logf("function name %q → HTTP %d | %s", form, res.StatusCode,
				strings.ReplaceAll(string(excerpt), creds.APIKey, "[redacted]"))
		})
	}
	_ = client
}

// seedForTools is seed() with enough output budget for a tool round trip.
// The 32-token ceiling the accounting tests use is too small for a model to
// emit a tool call and then an answer.
func (e *liveEnv) seedForTools(instructions string) seeded {
	e.t.Helper()
	rec := e.do("POST", "/chat/providers", map[string]any{
		"name":          "LiteLLM X2 " + uuid.NewString()[:8],
		"base_url":      e.creds.BaseURL,
		"api_key":       e.creds.APIKey,
		"default_model": e.model,
	})
	wantStatus(e.t, rec, http.StatusCreated)
	provider := decode[map[string]any](e.t, rec)

	rec = e.do("POST", "/chat/agents", map[string]any{
		"provider_id":   provider["id"],
		"name":          "X2 " + uuid.NewString()[:8],
		"system_prompt": instructions,
		"model":         e.model,
		"max_tokens":    64,
		"temperature":   0,
	})
	wantStatus(e.t, rec, http.StatusCreated)
	agent := decode[map[string]any](e.t, rec)

	rec = e.do("POST", "/chat/conversations", map[string]any{"agent_id": agent["id"]})
	wantStatus(e.t, rec, http.StatusCreated)
	conv := decode[map[string]any](e.t, rec)

	return seeded{
		providerID:     provider["id"].(string),
		agentID:        agent["id"].(string),
		conversationID: conv["id"].(string),
	}
}

func (e *liveEnv) toolCallsOf(conversationID string) []apiToolCall {
	e.t.Helper()
	rec := e.do("GET", "/chat/conversations/"+conversationID+"/tool-calls", nil)
	wantStatus(e.t, rec, http.StatusOK)
	return decode[struct {
		Items []apiToolCall `json:"items"`
	}](e.t, rec).Items
}
