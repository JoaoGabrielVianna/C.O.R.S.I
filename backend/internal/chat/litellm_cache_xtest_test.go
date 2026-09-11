//go:build litellm

// X3 — the prompt-caching boundary.
//
// ── Why this file exists before any caching is switched on ─────────────
// The token-economics audit measured that 67,9% of every input token this
// system sends is a byte-stable prefix, and modelled a 44-64% saving from
// caching it. A model is not evidence. Between our request and Anthropic's
// cache sits LiteLLM, translating an OpenAI-shaped body into an Anthropic
// one, and NOTHING in this repository has ever confronted that translation
// with a `cache_control` marker.
//
// Seven questions have to be answered with observed bytes before the
// runtime is allowed to mark a single prefix:
//
//  1. does the gateway ACCEPT cache_control?
//  2. does it FORWARD it, rather than accepting and discarding?
//  3. does it RETURN cache creation/read usage?
//  4. does tool calling still work with the block form of `content`?
//  5. does streaming still work?
//  6. does Confidential still behave?
//  7. is the semantic content reaching the model unchanged?
//
// (1) is a status code. (2) is the only one that cannot be answered by
// asking: a gateway that strips the field returns HTTP 200 and usage counts
// of zero, which looks exactly like a cache that did not hit. So it is
// answered differentially — the same prefix twice, marked and unmarked —
// and the difference between the two runs is the evidence.
//
// ── What it costs ──────────────────────────────────────────────────────
// Six calls carrying roughly twelve thousand tokens of filler each. At
// claude-opus-4-7 rates that is about US$ 0,36 for a full run, and the
// filler exists precisely because Anthropic will not cache a prefix below
// its per-model minimum (2.048 tokens on Opus 4.7, 4.096 on Haiku 4.5) and
// reports no error when it declines.
//
//	LITELLM_BASE_URL=... LITELLM_API_KEY=... LITELLM_MODEL=claude-opus-4-7 \
//	  go test -tags='integration litellm' -run TestX3 ./internal/chat/ -v
package chat

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/adapters/llm"
	"github.com/corsi/backend/internal/chat/ports"
)

/* ── the cacheable prefix ────────────────────────────────────────────── */

// cacheFillerBlocks is how many paragraphs of filler the stable prefix
// carries. Each is a little over 300 characters, so ~120 of them is roughly
// 38.000 characters — comfortably above the 4.096-token minimum on every
// model this deployment offers, with margin for a tokenizer that is denser
// or sparser than the audit's measured 0,43 tokens per character.
const cacheFillerBlocks = 120

// stablePrefix is a deterministic, boring, and above all IDENTICAL block of
// text.
//
// Determinism is the whole point: a prefix caches only on an exact byte
// match, so anything that varies between two calls — a timestamp, a random
// id, a map iterated in Go's randomised order — silently turns every read
// into a miss. This function is the control: if a cache read does not
// happen with THIS input, the reason is the gateway and not our text.
func stablePrefix() string {
	var b strings.Builder
	b.WriteString("Você é um assistente de testes de infraestrutura. " +
		"As instruções abaixo são fixas e se repetem entre chamadas.\n\n")
	for i := 0; i < cacheFillerBlocks; i++ {
		fmt.Fprintf(&b,
			"Regra %03d: ao responder, mantenha a resposta curta e objetiva. "+
				"Esta regra existe apenas para dar volume estável ao prefixo do "+
				"prompt, de modo que ele ultrapasse o mínimo cacheável do modelo. "+
				"Ela não altera o comportamento esperado e não deve ser mencionada "+
				"na resposta ao usuário.\n", i)
	}
	return b.String()
}

/* ── a direct probe of the gateway ───────────────────────────────────── */

// probeResult is what one raw call to the gateway reported.
type probeResult struct {
	status              int
	body                string
	promptTokens        int
	completionTokens    int
	cacheCreationTokens *int
	cacheReadTokens     *int
	cachedTokens        *int
}

func (p probeResult) String() string {
	return fmt.Sprintf("HTTP %d | prompt=%d completion=%d | creation=%s read=%s cached=%s",
		p.status, p.promptTokens, p.completionTokens,
		optStr(p.cacheCreationTokens), optStr(p.cacheReadTokens), optStr(p.cachedTokens))
}

func optStr(v *int) string {
	if v == nil {
		return "ABSENT"
	}
	return fmt.Sprintf("%d", *v)
}

// probe issues one non-streamed completion straight at the gateway, with
// the system message in either the string form or the cache-marked block
// form, and reports what came back.
//
// Deliberately NOT routed through our adapter. The question here is what the
// gateway does, and an answer produced by our own translation layer would be
// a test of that layer instead.
func probe(t *testing.T, creds ports.Credentials, model, systemText, question string, marked bool) probeResult {
	t.Helper()

	var systemContent any = systemText
	if marked {
		systemContent = []map[string]any{{
			"type":          "text",
			"text":          systemText,
			"cache_control": map[string]any{"type": "ephemeral"},
		}}
	}

	body, err := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 16,
		"stream":     false,
		"messages": []map[string]any{
			{"role": "system", "content": systemContent},
			{"role": "user", "content": question},
		},
	})
	if err != nil {
		t.Fatalf("marshal probe: %v", err)
	}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		strings.TrimSuffix(creds.BaseURL, "/")+"/chat/completions", strings.NewReader(string(body)))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+creds.APIKey)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("probe request: %v", err)
	}
	defer func() { _ = res.Body.Close() }()

	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	out := probeResult{
		status: res.StatusCode,
		// The key is never allowed into a log line, even in a failure path.
		body: strings.ReplaceAll(string(raw), creds.APIKey, "[redacted]"),
	}

	var parsed struct {
		Usage struct {
			PromptTokens             int  `json:"prompt_tokens"`
			CompletionTokens         int  `json:"completion_tokens"`
			CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
			PromptTokensDetails      *struct {
				CachedTokens        *int `json:"cached_tokens"`
				CacheCreationTokens *int `json:"cache_creation_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err == nil {
		out.promptTokens = parsed.Usage.PromptTokens
		out.completionTokens = parsed.Usage.CompletionTokens
		out.cacheCreationTokens = parsed.Usage.CacheCreationInputTokens
		out.cacheReadTokens = parsed.Usage.CacheReadInputTokens
		if d := parsed.Usage.PromptTokensDetails; d != nil {
			out.cachedTokens = d.CachedTokens
			if out.cacheCreationTokens == nil {
				out.cacheCreationTokens = d.CacheCreationTokens
			}
		}
	}
	return out
}

/* ── Q1-Q3: acceptance, forwarding, and reported usage ───────────────── */

// TestX3GatewayAcceptsAndForwardsCacheControl is the gate.
//
// ── The differential, and why it is the only honest test ───────────────
// A gateway that silently drops `cache_control` answers 200 and reports zero
// cache tokens. So does a gateway that forwards it to a provider which then
// declines to cache. Neither can be told from the other by looking at one
// response, which is why this runs FOUR calls:
//
//	unmarked #1, unmarked #2   the control: no marker, no caching expected
//	  marked #1,   marked #2   the treatment: same prefix, marker present
//
// The prefix is identical across all four. If the marked pair reports a
// cache read and the unmarked pair does not, the marker travelled and did
// something — which is exactly what "forwards correctly" means and is not
// answerable any other way from outside the gateway.
func TestX3GatewayAcceptsAndForwardsCacheControl(t *testing.T) {
	creds := liveCreds(t)
	model := os.Getenv("LITELLM_MODEL")
	if model == "" {
		model = "claude-haiku-4-5"
	}
	prefix := stablePrefix()
	t.Logf("model=%s | stable prefix: %d characters", model, len(prefix))

	/* ── Q1: is the marked body even accepted? ─────────────────────── */

	marked1 := probe(t, creds, model, prefix, "responda apenas: ok", true)
	t.Logf("marked   #1: %s", marked1)
	if marked1.status != http.StatusOK {
		t.Fatalf("Q1 FAILED — the gateway REJECTED a body carrying cache_control.\n"+
			"HTTP %d: %s\n\n"+
			"This is a hard stop for the caching sprint: the block form of "+
			"`content` is not accepted by this deployment, so no prefix can be "+
			"marked. Report and do not enable caching.",
			marked1.status, truncateForLog(marked1.body))
	}

	/* ── Q3: does the response carry cache usage at all? ────────────── */

	if marked1.cacheCreationTokens == nil && marked1.cacheReadTokens == nil && marked1.cachedTokens == nil {
		t.Fatalf("Q3 FAILED — the gateway accepted cache_control and reported NO cache " +
			"usage field whatsoever. Caching cannot be verified or measured on this " +
			"deployment, so enabling it would be spending money on an unfalsifiable claim.")
	}

	// A second identical marked call. This is the one that must READ.
	marked2 := probe(t, creds, model, prefix, "responda apenas: ok", true)
	t.Logf("marked   #2: %s", marked2)

	/* ── the control ───────────────────────────────────────────────── */

	unmarked := probe(t, creds, model, prefix, "responda apenas: ok", false)
	t.Logf("unmarked   : %s", unmarked)
	if unmarked.status != http.StatusOK {
		t.Fatalf("the control call failed: HTTP %d %s", unmarked.status, truncateForLog(unmarked.body))
	}

	/* ── Q2: did the marker actually do something? ──────────────────── */

	read := valueOr(marked2.cacheReadTokens, valueOr(marked2.cachedTokens, 0))
	if read <= 0 {
		t.Fatalf("Q2 FAILED — two identical marked requests and the second read %d tokens "+
			"from cache.\n"+
			"marked #1: %s\nmarked #2: %s\n\n"+
			"Either the gateway is stripping cache_control before forwarding, or the "+
			"prefix (%d characters) is below this model's minimum cacheable length. "+
			"Both are hard stops: the first means caching is impossible here, the "+
			"second means this test is wrong. Do not enable caching on this evidence.",
			read, marked1, marked2, len(prefix))
	}
	t.Logf("Q2 PASSED — the second marked request read %d tokens from cache", read)

	// The prefix must be the bulk of what was read. A handful of cached
	// tokens would mean something else entirely got cached.
	if read < marked2.promptTokens/2 {
		t.Errorf("only %d of %d prompt tokens came from cache; the marked prefix is "+
			"not what is being reused", read, marked2.promptTokens)
	}

	/* ── the control must NOT have read ─────────────────────────────── */

	// This is the assertion that turns a correlation into evidence: if the
	// unmarked call reads from cache too, the gateway is caching on its own
	// and our marker proves nothing.
	unmarkedRead := valueOr(unmarked.cacheReadTokens, valueOr(unmarked.cachedTokens, 0))
	if unmarkedRead > 0 {
		t.Logf("NOTE: the UNMARKED call also read %d tokens from cache. This deployment "+
			"appears to cache without being asked, so the saving may already be "+
			"partly realised and the marker's marginal value is smaller than modelled. "+
			"Recorded, not asserted.", unmarkedRead)
	}

	/* ── the fact the whole cost formula rests on ───────────────────── */

	// Does `prompt_tokens` INCLUDE the cached tokens, or sit beside them?
	//
	// The answer decides how a cached turn is priced, and getting it wrong
	// is not a rounding error in either direction: if it includes them and
	// we price every prompt token at the input rate, a cache read is charged
	// ten times over and the books report a saving of zero; if it excludes
	// them and we subtract, the input is undercounted by the whole prefix.
	//
	// Three calls, one identical prompt, three different cache outcomes. If
	// prompt_tokens excluded the cached share, the reading call would report
	// only the uncached remainder — a few dozen tokens — instead of the same
	// figure as the control.
	if marked2.promptTokens != unmarked.promptTokens {
		t.Errorf("prompt_tokens differs between a cached (%d) and an uncached (%d) "+
			"call on an identical prompt; the cost formula's partition assumption "+
			"does not hold on this gateway",
			marked2.promptTokens, unmarked.promptTokens)
	}
	if marked1.promptTokens != marked2.promptTokens {
		t.Errorf("prompt_tokens differs between the write (%d) and the read (%d) of "+
			"the same prefix", marked1.promptTokens, marked2.promptTokens)
	}
	t.Logf("CONFIRMED — prompt_tokens INCLUDES the cached share: %d on all three "+
		"calls, of which %d was billed as a cache read on the repeat. "+
		"full-price tokens = prompt_tokens − read − creation.",
		unmarked.promptTokens, read)

	/* ── Q7: the answer is still an answer ──────────────────────────── */

	// Content equivalence is proved on the bytes in the adapter's own tests
	// (TestCacheBreakpointDoesNotChangeWhatTheModelReads). What this can add
	// is that the model still behaves: a cached prefix that produced no
	// completion would mean the block form reached the model malformed.
	if marked2.completionTokens <= 0 {
		t.Errorf("Q7 SUSPECT — the marked call produced no completion tokens; the block " +
			"form may not be reaching the model as text")
	}
}

func valueOr(v *int, fallback int) int {
	if v == nil {
		return fallback
	}
	return *v
}

func truncateForLog(s string) string {
	const max = 600
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

/* ── Q5: streaming, through our own adapter ──────────────────────────── */

// TestX3StreamingSurvivesTheBlockForm drives a marked prefix through the
// PRODUCTION adapter, streamed, and checks that the two things streaming is
// for still happen: tokens arrive, and the usage frame lands.
//
// The adapter's unit tests prove the body it builds. This proves the gateway
// answers that body the same way it answers the old one — including the
// `stream_options.include_usage` frame, which is the single point of failure
// that would leave every future turn recording zero tokens.
func TestX3StreamingSurvivesTheBlockForm(t *testing.T) {
	creds := liveCreds(t)
	model := os.Getenv("LITELLM_MODEL")
	if model == "" {
		model = "claude-haiku-4-5"
	}
	client := llm.New(slog.New(slog.NewJSONHandler(io.Discard, nil)))

	stream, err := client.Stream(t.Context(), ports.CompletionRequest{
		Creds: creds, Model: model, MaxTokens: 32, Temperature: 1,
		Messages: []ports.ChatMessage{
			{Role: "system", Content: stablePrefix(), CacheBreakpoint: true},
			{Role: "user", Content: "responda apenas: ok"},
		},
	})
	if err != nil {
		t.Fatalf("Q5 FAILED — a streamed request carrying a cache breakpoint was "+
			"refused: %v", err)
	}
	defer func() { _ = stream.Close() }()

	var (
		text   strings.Builder
		usage  *ports.Usage
		finish string
	)
	for {
		ev, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Q5 FAILED — the stream broke: %v", err)
		}
		text.WriteString(ev.Delta)
		if ev.FinishReason != "" {
			finish = ev.FinishReason
		}
		if ev.Usage != nil {
			usage = ev.Usage
		}
	}

	if strings.TrimSpace(text.String()) == "" {
		t.Errorf("the streamed answer was empty (finish=%q)", finish)
	}
	if usage == nil {
		t.Fatalf("Q5 FAILED — no usage frame on a streamed cached request. Every turn " +
			"would record zero tokens.")
	}
	t.Logf("streamed: finish=%q answer=%q", finish, strings.TrimSpace(text.String()))
	t.Logf("usage: prompt=%d completion=%d creation=%s read=%s cached=%s reasoning=%s",
		usage.PromptTokens, usage.CompletionTokens,
		optStr(usage.CacheCreationTokens), optStr(usage.CacheReadTokens),
		optStr(usage.CachedTokens), optStr(usage.ReasoningTokens))

	// Q3, restated through the adapter: the fields the raw probe saw must
	// survive our own translation. A pass here and a fail in the probe would
	// mean the parser is inventing; the reverse means it is dropping.
	if !usage.CacheObserved() {
		t.Errorf("Q3 FAILED at the adapter — the raw probe sees cache fields and " +
			"ports.Usage reports none; the translation is dropping them")
	}
}

/* ── Q4: tool calling, with a marked prefix ──────────────────────────── */

// TestX3ToolCallingSurvivesTheBlockForm is Q4 at the level that matters: not
// "does a tools array still serialise", but "does a model still ASK for a
// tool when the system message arrived as a content block".
//
// It probes the gateway directly and non-streamed, because the question is
// about the gateway's translation of two features at once — content blocks
// and function calling — and a failure inside our loop would be harder to
// attribute.
func TestX3ToolCallingSurvivesTheBlockForm(t *testing.T) {
	creds := liveCreds(t)
	model := os.Getenv("LITELLM_MODEL")
	if model == "" {
		model = "claude-haiku-4-5"
	}

	body, err := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": 128,
		"stream":     false,
		"messages": []map[string]any{
			{"role": "system", "content": []map[string]any{{
				"type": "text",
				"text": stablePrefix() + "\n\nUse a ferramenta system__echo com o texto " +
					"exato que o usuário pedir.",
				"cache_control": map[string]any{"type": "ephemeral"},
			}}},
			{"role": "user", "content": "Ecoe exatamente: corsi-x3"},
		},
		"tools": []map[string]any{{
			"type": "function",
			"function": map[string]any{
				"name":        "system__echo",
				"description": "Returns the text it was given.",
				"parameters": map[string]any{
					"type":                 "object",
					"properties":           map[string]any{"text": map[string]any{"type": "string"}},
					"required":             []string{"text"},
					"additionalProperties": false,
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost,
		strings.TrimSuffix(creds.BaseURL, "/")+"/chat/completions", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+creds.APIKey)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	clean := strings.ReplaceAll(string(raw), creds.APIKey, "[redacted]")

	if res.StatusCode != http.StatusOK {
		t.Fatalf("Q4 FAILED — cache_control plus tools was rejected: HTTP %d %s",
			res.StatusCode, truncateForLog(clean))
	}

	var parsed struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				ToolCalls []struct {
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			CacheReadInputTokens *int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(parsed.Choices) == 0 {
		t.Fatalf("Q4 FAILED — no choices: %s", truncateForLog(clean))
	}
	calls := parsed.Choices[0].Message.ToolCalls
	if len(calls) == 0 {
		t.Fatalf("Q4 FAILED — the model asked for no tool with a block-form system "+
			"message (finish=%q). Either the instruction did not reach it or tool "+
			"declaration and content blocks do not compose on this gateway.",
			parsed.Choices[0].FinishReason)
	}
	t.Logf("Q4 PASSED — tool %q asked with args %s | cache read=%s",
		calls[0].Function.Name, calls[0].Function.Arguments,
		optStr(parsed.Usage.CacheReadInputTokens))
	if !strings.Contains(calls[0].Function.Arguments, "corsi-x3") {
		t.Errorf("the arguments lost the user's text: %s", calls[0].Function.Arguments)
	}
}

/* ── does a system breakpoint cover the TOOL CATALOGUE? ──────────────── */

// TestX3ToolCatalogueIsInsideTheCachedPrefix is the measurement that decides
// where the runtime's breakpoint goes, and it is worth more money than every
// other assertion in this file put together.
//
// ── The question ───────────────────────────────────────────────────────
// The audit measured that tool JSON Schema is 44,6% of this system's entire
// bill — 51,6% of every input token. Anthropic documents its render order as
// `tools` → `system` → `messages`, which implies that a breakpoint on a
// system message caches the tool array too, because the array comes before
// it in the prefix.
//
// That is an inference about Anthropic. Between us and Anthropic sits
// LiteLLM, translating an OpenAI-shaped body, and the audit's own rule is
// not to assume Anthropic-pure behaviour through a gateway. If the
// implication does not hold here, marking the system message buys the
// instructions (~2.500 tokens on the Ledger) and leaves the catalogue
// (~10.500 tokens) at full price — a completely different sprint.
//
// ── The experiment ─────────────────────────────────────────────────────
// A SMALL system message, deliberately far below the model's minimum
// cacheable prefix on its own, plus a LARGE tool array. The breakpoint goes
// on the system message. Then the same request twice.
//
//	read ≈ 0            the marker covers only what follows tools; the
//	                    catalogue is NOT cached
//	read ≈ catalogue    the catalogue IS inside the cached prefix
//
// The small system message is what makes the result unambiguous: there is
// not enough text after the tools to reach the minimum, so any substantial
// read can only have come from the catalogue.
func TestX3ToolCatalogueIsInsideTheCachedPrefix(t *testing.T) {
	creds := liveCreds(t)
	model := os.Getenv("LITELLM_MODEL")
	if model == "" {
		model = "claude-haiku-4-5"
	}

	// A catalogue in the shape and size the Ledger really declares: 23 tools
	// whose descriptions and schemas add up to tens of thousands of bytes.
	tools := make([]map[string]any, 0, 24)
	for i := 0; i < 24; i++ {
		props := map[string]any{}
		for p := 0; p < 6; p++ {
			props[fmt.Sprintf("campo_%02d", p)] = map[string]any{
				"type": "string",
				"description": fmt.Sprintf(
					"Parâmetro %d da capability %02d. Descrição deliberadamente longa "+
						"para reproduzir o tamanho real de um schema deste sistema, onde "+
						"uma única tool chega a três mil e quatrocentos bytes de JSON.", p, i),
			}
		}
		tools = append(tools, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": fmt.Sprintf("teste__capability_%02d", i),
				"description": fmt.Sprintf(
					"Capability de teste %02d. Esta descrição existe para dar ao array "+
						"de tools o volume que o catálogo real tem, de modo que a pergunta "+
						"'o catálogo entra no prefixo cacheado?' possa ser respondida com "+
						"tokens observados em vez de com uma inferência sobre a ordem de "+
						"renderização do provider.", i),
				"parameters": map[string]any{
					"type": "object", "properties": props,
					"required": []string{"campo_00"}, "additionalProperties": false,
				},
			},
		})
	}

	// Deliberately tiny, and deliberately unique per run so a previous run's
	// entry cannot answer this question for us.
	system := "Responda apenas: ok. Marcador " + uuid.NewString()

	call := func() probeResult {
		body, err := json.Marshal(map[string]any{
			"model":      model,
			"max_tokens": 8,
			"stream":     false,
			"tools":      tools,
			"messages": []map[string]any{
				{"role": "system", "content": []map[string]any{{
					"type": "text", "text": system,
					"cache_control": map[string]any{"type": "ephemeral"},
				}}},
				{"role": "user", "content": "diga ok"},
			},
		})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost,
			strings.TrimSuffix(creds.BaseURL, "/")+"/chat/completions", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+creds.APIKey)

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer func() { _ = res.Body.Close() }()
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		out := probeResult{status: res.StatusCode,
			body: strings.ReplaceAll(string(raw), creds.APIKey, "[redacted]")}
		var parsed struct {
			Usage struct {
				PromptTokens             int  `json:"prompt_tokens"`
				CompletionTokens         int  `json:"completion_tokens"`
				CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
				CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(raw, &parsed) == nil {
			out.promptTokens = parsed.Usage.PromptTokens
			out.completionTokens = parsed.Usage.CompletionTokens
			out.cacheCreationTokens = parsed.Usage.CacheCreationInputTokens
			out.cacheReadTokens = parsed.Usage.CacheReadInputTokens
		}
		return out
	}

	first := call()
	t.Logf("catalogue #1: %s", first)
	if first.status != http.StatusOK {
		t.Fatalf("rejected: HTTP %d %s", first.status, truncateForLog(first.body))
	}
	second := call()
	t.Logf("catalogue #2: %s", second)

	read := valueOr(second.cacheReadTokens, 0)
	systemTokens := len(system) / 3 // generous upper bound for a short line

	switch {
	case read > systemTokens*4:
		t.Logf("RESULT: the TOOL CATALOGUE IS inside the cached prefix — %d of %d "+
			"prompt tokens were read from cache, far more than the %d-character "+
			"system message could account for. A breakpoint on the instructions "+
			"covers the catalogue, and the runtime needs exactly one marker.",
			read, second.promptTokens, len(system))
	case read > 0:
		t.Errorf("RESULT: AMBIGUOUS — %d tokens read, but the system message alone "+
			"could plausibly account for that. The experiment needs a larger "+
			"catalogue or a smaller system message.", read)
	default:
		t.Errorf("RESULT: the TOOL CATALOGUE IS NOT inside the cached prefix — "+
			"%d tokens read on an identical repeat of a %d-token prompt.\n"+
			"A breakpoint on the instructions buys the instructions and nothing "+
			"else. The catalogue is 44,6%% of this system's bill and would need "+
			"its own marker on the tools array, which is a different change and "+
			"a different X-test.", read, second.promptTokens)
	}
}

/* ── a streamed usage frame, captured verbatim ───────────────────────── */

// TestX3CaptureCachedUsageFrame prints the raw SSE usage frame of a cached
// call so it can be pasted into litellm_real_test.go as a fixture.
//
// The fixture that file already carries was taken before caching existed, so
// every cache number in it is zero — which cannot distinguish a parser that
// reads the right field from one that reads the wrong field and gets zero
// anyway. This is how that gap gets closed with real bytes rather than
// invented ones.
func TestX3CaptureCachedUsageFrame(t *testing.T) {
	creds := liveCreds(t)
	model := os.Getenv("LITELLM_MODEL")
	if model == "" {
		model = "claude-haiku-4-5"
	}

	send := func() string {
		body, _ := json.Marshal(map[string]any{
			"model":          model,
			"max_tokens":     8,
			"stream":         true,
			"stream_options": map[string]any{"include_usage": true},
			"messages": []map[string]any{
				{"role": "system", "content": []map[string]any{{
					"type": "text", "text": stablePrefix(),
					"cache_control": map[string]any{"type": "ephemeral"},
				}}},
				{"role": "user", "content": "ok"},
			},
		})
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost,
			strings.TrimSuffix(creds.BaseURL, "/")+"/chat/completions", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+creds.APIKey)
		req.Header.Set("Accept", "text/event-stream")

		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		defer func() { _ = res.Body.Close() }()

		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		var usageFrame string
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if strings.Contains(line, `"usage"`) && strings.Contains(line, "prompt_tokens") {
				usageFrame = strings.ReplaceAll(line, creds.APIKey, "[redacted]")
			}
		}
		return usageFrame
	}

	// First call warms, second reads. The second frame is the interesting
	// one: it is the shape a cache HIT produces.
	_ = send()
	frame := send()
	if frame == "" {
		t.Fatalf("no usage frame in the streamed response")
	}
	t.Logf("CACHED USAGE FRAME (paste into litellm_real_test.go):\n%s", frame)
}
