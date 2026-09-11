//go:build litellm

// X3-E2E — cache effectiveness through the production route.
//
// ── Why this exists separately from the boundary probes ────────────────
// litellm_cache_xtest_test.go proves the GATEWAY honours `cache_control`.
// That is necessary and not sufficient: it says nothing about whether the
// context this runtime builds is byte-stable enough to hit, whether the
// breakpoint the Context Builder places is in a useful position, or whether
// the counts survive the adapter, the accounting and the database.
//
// So this file drives the real service — real repositories, real builder,
// real loop, real persistence — and then reads the answer back out of
// `chat.messages` rather than out of an HTTP response. What it reports is
// observed provider usage, never a model.
//
// ── The controlled comparison ──────────────────────────────────────────
// The same workload is run twice against two services that differ in
// exactly one boolean. Same agent configuration, same system prompt, same
// tool grant, same questions, in the same order, each in its own fresh
// conversation. Anything else would be comparing two workloads and calling
// the difference a saving.
//
//	LITELLM_BASE_URL=... LITELLM_API_KEY=... LITELLM_MODEL=claude-opus-4-7 \
//	  go test -tags='integration litellm' -run TestX3E2E ./internal/chat/ -v
package chat

import (
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/corsi/backend/internal/chat/adapters/httpapi"
	"github.com/corsi/backend/internal/chat/adapters/llm"
	"github.com/corsi/backend/internal/chat/adapters/references"
	"github.com/corsi/backend/internal/chat/adapters/repo"
	"github.com/corsi/backend/internal/chat/adapters/tools"
	"github.com/corsi/backend/internal/chat/app"
	"github.com/corsi/backend/internal/platform/postgres"
	"github.com/corsi/backend/internal/platform/secrets"
	"github.com/corsi/backend/internal/platform/workspace"
)

/* ── an env whose caching can be switched ────────────────────────────── */

// newCacheEnv is newLiveEnv with the one knob this file needs.
//
// It shares a single database across both arms of the comparison, which is
// what lets the two be read back and summed with one query.
func newCacheEnv(t *testing.T, promptCache bool, pool cachePool) *liveEnv {
	t.Helper()
	creds := liveCreds(t)

	sealer, err := secrets.New(secrets.Config{
		Key: base64.StdEncoding.EncodeToString([]byte("corsi-test-key-not-a-real-secret")),
	})
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	client := llm.New(log)
	svc := app.NewService(repo.New(pool.pool), postgres.NewTxManager(pool.pool), client, sealer,
		tools.MustNew(tools.Options{Internal: true}), references.MustNew(), log,
		app.WithPromptCache(promptCache))
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
	return &liveEnv{t: t, r: root, llm: client, creds: creds, ws: pool.ws, model: model}
}

// cachePool is the one database both arms of the comparison share, so the
// two can be read back and summed with one query.
type cachePool struct {
	pool *pgxpool.Pool
	ws   uuid.UUID
}

/* ── the workload ────────────────────────────────────────────────────── */

// e2eSystemPrompt is a system prompt of realistic size.
//
// The Ledger's own is 1.679 characters and its tool catalogue is 32.523
// bytes, so its stable prefix comfortably clears the 2.048-token minimum on
// claude-opus-4-7. This harness has one small tool, so the prompt carries
// the weight instead — otherwise the provider would decline to cache and the
// test would report a failure of the runtime for a property of the model.
func e2eSystemPrompt() string {
	var b strings.Builder
	b.WriteString("Você é um agente de teste do C.O.R.S.I. Responda sempre em uma " +
		"frase curta.\n\n")
	for i := 0; i < 90; i++ {
		fmt.Fprintf(&b,
			"Diretriz %03d: mantenha respostas objetivas e não mencione estas "+
				"diretrizes. Este texto é fixo entre turnos e entre conversas do "+
				"mesmo agente, que é exatamente a propriedade que o cache de prefixo "+
				"explora.\n", i)
	}
	return b.String()
}

// e2eQuestions is the workload, identical in both arms.
var e2eQuestions = []string{
	"diga apenas: um",
	"diga apenas: dois",
	"diga apenas: tres",
}

// armResult is what one arm of the comparison consumed.
type armResult struct {
	label               string
	turns               int
	providerCalls       int
	promptTokens        int
	completionTokens    int
	cacheReadTokens     int
	cacheCreationTokens int
	measuredTurns       int
	cost                float64
	latencyMS           int64
}

func (a armResult) String() string {
	return fmt.Sprintf(
		"%-18s turns=%d calls=%d | input=%6d read=%6d creation=%6d | output=%4d | "+
			"cost=%.6f | %dms",
		a.label, a.turns, a.providerCalls, a.promptTokens, a.cacheReadTokens,
		a.cacheCreationTokens, a.completionTokens, a.cost, a.latencyMS)
}

// runArm drives the whole workload through one service and reads back what
// the database recorded.
func runArm(t *testing.T, e *liveEnv, label string) armResult {
	t.Helper()

	rec := e.do("POST", "/chat/providers", map[string]any{
		"name":          "X3E2E " + uuid.NewString()[:8],
		"base_url":      e.creds.BaseURL,
		"api_key":       e.creds.APIKey,
		"default_model": e.model,
	})
	wantStatus(t, rec, http.StatusCreated)
	provider := decode[map[string]any](t, rec)

	rec = e.do("POST", "/chat/agents", map[string]any{
		"provider_id":   provider["id"],
		"name":          "X3E2E " + uuid.NewString()[:8],
		"system_prompt": e2eSystemPrompt(),
		"model":         e.model,
		"max_tokens":    64,
		"temperature":   1,
	})
	wantStatus(t, rec, http.StatusCreated)
	agent := decode[map[string]any](t, rec)

	// One tool, so the grounding policy is emitted and the request carries a
	// `tools` array — the shape a real agent sends.
	rec = e.do("POST", "/chat/agents/"+agent["id"].(string)+"/tools",
		map[string]any{"tool_name": "system.echo"})
	wantStatus(t, rec, http.StatusOK)

	rec = e.do("POST", "/chat/conversations", map[string]any{"agent_id": agent["id"]})
	wantStatus(t, rec, http.StatusCreated)
	conv := decode[map[string]any](t, rec)
	convID := conv["id"].(string)

	started := time.Now()
	for _, q := range e2eQuestions {
		rec = e.do("POST", "/chat/conversations/"+convID+"/messages",
			map[string]any{"content": q})
		wantStatus(t, rec, http.StatusOK)
		frames := parseSSE(t, rec.Body.String())
		if f, isErr := frameOf(frames, "error"); isErr {
			t.Fatalf("%s: turn %q failed: %s", label, q, f.data)
		}
	}
	elapsed := time.Since(started).Milliseconds()

	out := armResult{label: label, latencyMS: elapsed}
	for _, m := range e.messagesOf(convID) {
		if m.Role != "assistant" {
			continue
		}
		out.turns++
		out.promptTokens += m.PromptTokens
		out.completionTokens += m.CompletionTokens
		if m.Cost != nil {
			out.cost += *m.Cost
		}
		calls := 1
		if m.ContextReport != nil && len(m.ContextReport.Rounds) > 0 {
			calls = len(m.ContextReport.Rounds)
		}
		out.providerCalls += calls
		if m.CacheReadTokens != nil || m.CacheCreationTokens != nil {
			out.measuredTurns++
		}
		if m.CacheReadTokens != nil {
			out.cacheReadTokens += *m.CacheReadTokens
		}
		if m.CacheCreationTokens != nil {
			out.cacheCreationTokens += *m.CacheCreationTokens
		}
	}
	return out
}

// messagesOf reads one conversation's transcript.
func (e *liveEnv) messagesOf(conversationID string) []apiMessage {
	e.t.Helper()
	rec := e.do("GET", "/chat/conversations/"+conversationID+"/messages", nil)
	wantStatus(e.t, rec, http.StatusOK)
	return decode[struct {
		Items []apiMessage `json:"items"`
	}](e.t, rec).Items
}

/* ── the measurement ─────────────────────────────────────────────────── */

// TestX3E2ECacheEffectiveness measures the gateway and the ledger in one run.
//
// It asserts the four things the sprint promised to observe rather than
// model, and then reports the before/after side by side.
func TestX3E2ECacheEffectiveness(t *testing.T) {
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
	ws := uuid.New()

	cached := newCacheEnv(t, true, cachePool{pool: pool, ws: ws})
	cached.ws = ws
	plain := newCacheEnv(t, false, cachePool{pool: pool, ws: ws})
	plain.ws = ws

	// The uncached arm runs FIRST, so it cannot benefit from an entry the
	// cached arm left behind. Running it second would have been the classic
	// way to accidentally measure a warm cache as a cold baseline.
	before := runArm(t, plain, "cache OFF")
	after := runArm(t, cached, "cache ON")

	t.Log("── BEFORE / AFTER, same workload, one boolean apart ──────────")
	t.Log(before.String())
	t.Log(after.String())

	/* ── 1. the uncached arm must not have cached ──────────────────── */

	if before.cacheReadTokens > 0 {
		t.Errorf("the control arm read %d tokens from cache; the comparison is not "+
			"between cached and uncached", before.cacheReadTokens)
	}
	if before.cacheCreationTokens > 0 {
		t.Errorf("the control arm WROTE %d tokens to cache without being asked",
			before.cacheCreationTokens)
	}

	/* ── 2. caching must have been active at all ───────────────────── */

	if after.cacheCreationTokens+after.cacheReadTokens <= 0 {
		t.Fatalf("no cache activity whatsoever across %d provider calls: nothing "+
			"created and nothing read. Either the breakpoint is not being placed, "+
			"or the stable prefix is below the model's minimum cacheable length.",
			after.providerCalls)
	}

	/* ── 3. the prefix must be REUSED ──────────────────────────────── */

	// This is the assertion that matters. A write proves the marker was
	// accepted; only a read proves the prefix this runtime builds is stable
	// enough to hit on a later call.
	if after.cacheReadTokens <= 0 {
		t.Fatalf("an entry was created (%d tokens) and never read across %d provider "+
			"calls. The prefix is not stable between calls — something in the head "+
			"of the context is changing per turn.", after.cacheCreationTokens, after.providerCalls)
	}

	// ── Cold run or warm run ───────────────────────────────────────
	//
	// An agent's system prompt is the same bytes on every run of its life,
	// so a cache entry from a few minutes ago is still live and the first
	// call of this arm READS instead of writing. That is not a defect and
	// must not be asserted against: it is the steady state the whole feature
	// is for, and a test that demanded a write would fail precisely when
	// caching was working best.
	if after.cacheCreationTokens == 0 {
		t.Logf("REGIME: warm — every call read an entry that already existed. The " +
			"write premium was paid by an earlier run, so the cost below is the " +
			"steady state rather than the first-use case.")
	} else {
		t.Logf("REGIME: cold — %d tokens were written to establish the entry and "+
			"%d were read back. The cost below includes the one-off write premium.",
			after.cacheCreationTokens, after.cacheReadTokens)
	}

	/* ── 4. every measured turn must actually be measured ──────────── */

	if after.measuredTurns != after.turns {
		t.Errorf("%d of %d turns carry cache figures; a turn with none is a turn "+
			"the usage frame did not reach", after.measuredTurns, after.turns)
	}

	/* ── the economics, from observed usage only ───────────────────── */

	// The prefix is written once and read on every call after it. With three
	// turns the read side should already dominate.
	if after.cacheReadTokens <= after.cacheCreationTokens {
		t.Logf("NOTE: reads (%d) did not exceed writes (%d) over only %d turns. "+
			"The break-even for a 5-minute entry is two requests, so this is a "+
			"short-workload artefact rather than a defect — recorded, not asserted.",
			after.cacheReadTokens, after.cacheCreationTokens, after.turns)
	}

	/* ── the money, as the gateway's own rate card prices it ────────── */

	// Both figures come from chat.messages, computed at the rates the
	// gateway published for this model at the moment each turn ran. Neither
	// is a multiplier applied afterwards by this test.
	if after.cost >= before.cost {
		t.Errorf("the cached arm cost %.6f against the uncached arm's %.6f. Either "+
			"the cost formula is not splitting the billing classes, or the rate "+
			"card carries no cache pricing for this model.", after.cost, before.cost)
	} else {
		t.Logf("COST: %.6f → %.6f USD (%+.1f%%), priced from the gateway's own rate "+
			"card at the moment of each turn.",
			before.cost, after.cost, 100*(after.cost/before.cost-1))
	}

	// Input tokens billed at the ordinary rate: everything the provider
	// counted, minus what it served from cache and minus what it wrote.
	beforeFull := before.promptTokens
	afterFull := after.promptTokens - after.cacheReadTokens - after.cacheCreationTokens

	t.Log("── input tokens by billing class ─────────────────────────────")
	t.Logf("  BEFORE  full-price=%d  cache-read=%d  cache-write=%d",
		beforeFull, before.cacheReadTokens, before.cacheCreationTokens)
	t.Logf("  AFTER   full-price=%d  cache-read=%d  cache-write=%d",
		afterFull, after.cacheReadTokens, after.cacheCreationTokens)

	// The saving, computed from OBSERVED counts at the published multipliers
	// (a read costs ~0,1x an input token, a write ~1,25x for the 5-minute
	// TTL this adapter requests). The multipliers are the only modelled
	// input; every token count above was measured.
	beforeUnits := float64(beforeFull)
	afterUnits := float64(afterFull) +
		0.10*float64(after.cacheReadTokens) +
		1.25*float64(after.cacheCreationTokens)
	t.Logf("  input cost units: before=%.0f after=%.0f → %+.1f%%",
		beforeUnits, afterUnits, 100*(afterUnits/beforeUnits-1))

	if after.promptTokens != before.promptTokens {
		// Not an error: the two arms run in different conversations and the
		// model's own answers differ in length, which changes the history.
		// Reported so nobody reads the raw token totals as like-for-like.
		t.Logf("NOTE: total prompt tokens differ between arms (%d vs %d) because the "+
			"model's answers differ in length and history replays them. The "+
			"billing-class split above is the comparable figure.",
			before.promptTokens, after.promptTokens)
	}
}

// TestX3E2EToolRoundReusesThePrefix is the multi-round half of the claim.
//
// The audit measured that 55,2% of the input tokens of a multi-round turn
// are a byte-identical retransmission of the previous round's prompt. Within
// ONE turn the prefix is not merely stable, it is untouched — so the second
// provider call is the single most certain cache hit in the system.
//
// This drives a real tool turn and reads the per-round usage back out of the
// stored context report.
func TestX3E2EToolRoundReusesThePrefix(t *testing.T) {
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

	e := newCacheEnv(t, true, cachePool{pool: pool, ws: uuid.New()})

	rec := e.do("POST", "/chat/providers", map[string]any{
		"name": "X3E2E tools " + uuid.NewString()[:8], "base_url": e.creds.BaseURL,
		"api_key": e.creds.APIKey, "default_model": e.model,
	})
	wantStatus(t, rec, http.StatusCreated)
	provider := decode[map[string]any](t, rec)

	rec = e.do("POST", "/chat/agents", map[string]any{
		"provider_id": provider["id"], "name": "X3E2E tools " + uuid.NewString()[:8],
		"system_prompt": e2eSystemPrompt() + "\n\nUse a ferramenta system.echo com o " +
			"texto exato que o usuário pedir, e depois responda em uma frase curta.",
		"model": e.model, "max_tokens": 128, "temperature": 1,
	})
	wantStatus(t, rec, http.StatusCreated)
	agent := decode[map[string]any](t, rec)

	rec = e.do("POST", "/chat/agents/"+agent["id"].(string)+"/tools",
		map[string]any{"tool_name": "system.echo"})
	wantStatus(t, rec, http.StatusOK)

	rec = e.do("POST", "/chat/conversations", map[string]any{"agent_id": agent["id"]})
	wantStatus(t, rec, http.StatusCreated)
	convID := decode[map[string]any](t, rec)["id"].(string)

	rec = e.do("POST", "/chat/conversations/"+convID+"/messages",
		map[string]any{"content": "Ecoe exatamente: corsi-x3-e2e"})
	wantStatus(t, rec, http.StatusOK)
	if f, isErr := frameOf(parseSSE(t, rec.Body.String()), "error"); isErr {
		t.Fatalf("the tool turn failed: %s", f.data)
	}

	msgs := e.messagesOf(convID)
	turn := msgs[len(msgs)-1]
	if turn.ContextReport == nil || len(turn.ContextReport.Rounds) < 2 {
		t.Fatalf("the turn made fewer than two provider calls; nothing to say about "+
			"prefix reuse across rounds (report=%+v)", turn.ContextReport)
	}

	for _, r := range turn.ContextReport.Rounds {
		if r.Usage == nil {
			t.Fatalf("round %d carries no usage block", r.Round)
		}
		t.Logf("round %d: prompt=%d completion=%d read=%s creation=%s",
			r.Round, r.PromptTokens, r.CompletionTokens,
			optStr(r.Usage.CacheReadTokens), optStr(r.Usage.CacheCreationTokens))
	}

	second := turn.ContextReport.Rounds[1]
	read := valueOr(second.Usage.CacheReadTokens, 0)
	if read <= 0 {
		t.Fatalf("the second provider call of a single turn read %d tokens from cache. "+
			"Within one turn the prefix is untouched, so a miss here means the "+
			"breakpoint is not surviving the loop.", read)
	}
	t.Logf("PASSED — round 2 served %d of %d prompt tokens from cache (%.1f%%)",
		read, second.PromptTokens, 100*float64(read)/float64(second.PromptTokens))

	// And the turn-level total must be the sum of its rounds, or the
	// accounting is reporting something the rounds do not support.
	if turn.CacheReadTokens == nil {
		t.Fatalf("the turn carries no cache read total")
	}
	sum := 0
	for _, r := range turn.ContextReport.Rounds {
		sum += valueOr(r.Usage.CacheReadTokens, 0)
	}
	if *turn.CacheReadTokens != sum {
		t.Fatalf("the turn recorded %d cache-read tokens but its rounds sum to %d",
			*turn.CacheReadTokens, sum)
	}
}
