package llm

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

// Fixtures captured from a real LiteLLM proxy, not written from our own
// structs.
//
// ── Why this file exists apart from stream_test.go ─────────────────────
// Every other test of this adapter is written against what the adapter
// expects, which confirms the parser against itself. The bodies below were
// produced by a real gateway and pasted in verbatim, so they are the only
// place where the external contract — rather than our belief about it —
// is asserted.
//
// ── What has been captured ─────────────────────────────────────────────
// The captures below are wire evidence taken from a real LiteLLM
// deployment with a dedicated key. The live suite that produced them is
// `internal/chat/litellm_live_test.go`, behind its own build tag.
//
// ── This file never talks to the network ───────────────────────────────
// It replays recorded bytes, so the gate stays deterministic and offline.
// Nothing here carries a credential: the streams were captured from
// requests whose Authorization header is simply not part of the response.

// realLiteLLMUnauthorizedBody is the exact body a LiteLLM proxy returns for
// a request carrying no Authorization header.
//
// Captured 2026-08-09 from the deployment the workspace's provider rows
// point at, with `curl` and no credential of any kind.
//
//	GET /models  →  401
const realLiteLLMUnauthorizedBody = `{"error":{"message":"Authentication Error, No api key passed in.","type":"auth_error","param":"None","code":"401"}}`

// TestRealLiteLLMAuthErrorIsParsed proves the adapter reads the shape the
// real gateway actually sends, rather than the shape we assumed it sends.
//
// The two are compatible here — LiteLLM's error envelope is the nested
// `{"error":{"message":…}}` form extractErrorMessage handles first — and
// that compatibility is now a fact on record instead of an expectation.
func TestRealLiteLLMAuthErrorIsParsed(t *testing.T) {
	got := extractErrorMessage([]byte(realLiteLLMUnauthorizedBody))
	want := "Authentication Error, No api key passed in."
	if got != want {
		t.Fatalf("extractErrorMessage on a real LiteLLM 401 = %q, want %q", got, want)
	}
}

// TestRealLiteLLMUnauthorizedBecomesAnActionableUpstreamError takes the same
// captured body through the whole boundary translation, which is what the
// user actually ends up seeing.
//
// Three things have to survive that trip, and each has failed in some
// version of this kind of code: the failure must be classified as the
// provider's rather than ours (502, not 500), it must carry the gateway's
// own words, and it must point at the thing to fix.
func TestRealLiteLLMUnauthorizedBecomesAnActionableUpstreamError(t *testing.T) {
	res := &http.Response{
		StatusCode: http.StatusUnauthorized,
		Body:       io.NopCloser(strings.NewReader(realLiteLLMUnauthorizedBody)),
	}

	err := upstreamStatusErr(res)

	// The provider failed, not us. Anything other than KindUpstream would
	// put a gateway outage into this service's error budget and answer the
	// user with a 500 they cannot act on.
	if !domain.IsKind(err, domain.KindUpstream) {
		t.Fatalf("a 401 from the gateway produced %T (%v), want a domain upstream error", err, err)
	}

	msg := err.Error()
	if !strings.Contains(msg, "401") {
		t.Fatalf("the status is missing from %q", msg)
	}
	if !strings.Contains(msg, "No api key passed in") {
		t.Fatalf("the gateway's own words are missing from %q", msg)
	}
	if !strings.Contains(msg, "check the API key stored for this provider") {
		t.Fatalf("the message does not say what to fix: %q", msg)
	}
	// The raw body must not be echoed wholesale — that is how an HTML error
	// page ends up in a log line.
	if strings.Contains(msg, `"type":"auth_error"`) {
		t.Fatalf("the raw error envelope leaked into the message: %q", msg)
	}
}

/* ── the streamed turn ───────────────────────────────────────────────── */

// realLiteLLMStream is a complete streamed completion, captured verbatim
// from a real LiteLLM on 2026-08-10.
//
// Sanitised in exactly one way: the per-request `id` was replaced with a
// placeholder. Nothing else was touched — the field order, the empty
// `delta:{}` objects, the framing and the trailing sentinel are the
// gateway's own bytes.
//
// The request that produced it was the one this adapter builds: `stream`
// and `stream_options.include_usage` both true, plus `user` and `metadata`.
// The gateway accepted all of them.
//
// Three things in here are worth naming, because each contradicts a
// plausible assumption someone could make while editing the parser:
//
//  1. The opening frame carries `role` AND `content` together. There is no
//     separate role-only frame to skip in this dialect.
//  2. `finish_reason` sits on the CHOICE, beside `delta`, not inside it.
//  3. The usage frame still carries a choice — `[{"index":0,"delta":{}}]` —
//     rather than the empty `choices` array one might expect. A parser that
//     recognised the usage frame by "no choices" would silently drop it,
//     and every turn would record zero tokens.
const realLiteLLMStream = `data: {"id":"chatcmpl-x1","object":"chat.completion.chunk","created":1786383587,"model":"claude-haiku-4-5","choices":[{"index":0,"delta":{"role":"assistant","content":"Ok"}}]}

data: {"id":"chatcmpl-x1","object":"chat.completion.chunk","created":1786383587,"model":"claude-haiku-4-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: {"id":"chatcmpl-x1","created":1786383587,"model":"claude-haiku-4-5","object":"chat.completion.chunk","choices":[{"index":0,"delta":{}}],"usage":{"completion_tokens":4,"prompt_tokens":17,"total_tokens":21,"completion_tokens_details":{"reasoning_tokens":0,"text_tokens":4},"prompt_tokens_details":{"cached_tokens":0,"text_tokens":17,"cache_write_tokens":0,"cache_creation_tokens":0,"cache_creation_token_details":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0}},"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}

data: [DONE]

`

// TestRealLiteLLMStreamIsParsed replays the captured turn through the
// production parser and asserts the exact sequence of events it produces.
//
// This is the assertion the whole of X1 was for: until it existed, the
// parser had only ever been confronted with frames written from its own
// structs.
func TestRealLiteLLMStreamIsParsed(t *testing.T) {
	s := newSSEStream(io.NopCloser(strings.NewReader(realLiteLLMStream)))
	defer func() { _ = s.Close() }()

	var (
		answer strings.Builder
		finish string
		usage  *ports.Usage
		events int
	)
	for {
		ev, err := s.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		events++
		answer.WriteString(ev.Delta)
		if ev.FinishReason != "" {
			finish = ev.FinishReason
		}
		if ev.Usage != nil {
			usage = ev.Usage
		}
	}

	if got := answer.String(); got != "Ok" {
		t.Fatalf("answer = %q, want %q", got, "Ok")
	}
	if finish != "stop" {
		t.Fatalf("finish_reason = %q, want %q", finish, "stop")
	}
	if usage == nil {
		t.Fatalf("no usage event: include_usage produced a frame the parser dropped")
	}
	if usage.PromptTokens != 17 || usage.CompletionTokens != 4 {
		t.Fatalf("usage = %d/%d, want 17/4", usage.PromptTokens, usage.CompletionTokens)
	}
	// Three meaningful events: the content, the finish, and the usage. A
	// fourth would mean the parser is emitting something empty.
	if events != 3 {
		t.Fatalf("parser emitted %d events, want 3", events)
	}
}

// TestRealLiteLLMUsageFrameSurvivesItsChoice pins the trap of item 3 above,
// on its own, so a refactor that "simplifies" the skip condition fails here
// with a message that explains itself rather than in a cost report weeks
// later.
func TestRealLiteLLMUsageFrameSurvivesItsChoice(t *testing.T) {
	const usageFrameOnly = `data: {"choices":[{"index":0,"delta":{}}],"usage":{"completion_tokens":4,"prompt_tokens":17,"total_tokens":21}}

data: [DONE]

`
	s := newSSEStream(io.NopCloser(strings.NewReader(usageFrameOnly)))
	defer func() { _ = s.Close() }()

	ev, err := s.Recv()
	if err != nil {
		t.Fatalf("the usage frame was swallowed: %v", err)
	}
	if ev.Usage == nil {
		t.Fatalf("usage frame parsed into an event without usage: %+v", ev)
	}
	if ev.Delta != "" || ev.FinishReason != "" {
		t.Fatalf("the usage frame produced content it does not carry: %+v", ev)
	}
}

// TestRealLiteLLMTotalTokensIsTheSum records what the gateway reported
// about a field this adapter does not keep.
//
// `ports.Usage` carries prompt and completion and drops `total_tokens`. The
// capture above shows 17 + 4 = 21, so nothing was lost in this scenario —
// and this test says exactly that, scoped to the scenario, rather than
// claiming the identity holds universally.
//
// What the same capture also shows is why the claim must stay scoped:
// alongside the totals the gateway reports `cached_tokens`,
// `cache_creation_tokens` and `reasoning_tokens`. All zero here. A turn
// that used prompt caching or extended thinking could relate the three
// numbers differently, and this test would not notice.
/* ── the whole usage frame ───────────────────────────────────────────── */

// TestRealLiteLLMUsageFrameIsPreservedWhole replays the captured frame and
// asserts that every count it carries survives into ports.Usage.
//
// ── What this pins, and why it is worth a test of its own ──────────────
// The adapter used to declare only `prompt_tokens` and `completion_tokens`,
// so `encoding/json` silently dropped the rest of this frame — including
// every field that says whether prompt caching did anything. The token
// economics audit could not answer "what fraction of the input was a cache
// read?" for that reason and no other.
//
// Every value in the capture is zero, which makes this test the sharper of
// the two: it can only pass if the fields were PRESENT and read, because an
// unread field would also be zero. The distinction is asserted through the
// pointers, not through the values.
func TestRealLiteLLMUsageFrameIsPreservedWhole(t *testing.T) {
	s := newSSEStream(io.NopCloser(strings.NewReader(realLiteLLMStream)))
	defer func() { _ = s.Close() }()

	var usage *ports.Usage
	for {
		ev, err := s.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		if ev.Usage != nil {
			usage = ev.Usage
		}
	}
	if usage == nil {
		t.Fatalf("no usage event")
	}

	if usage.PromptTokens != 17 || usage.CompletionTokens != 4 {
		t.Fatalf("totals = %d/%d, want 17/4", usage.PromptTokens, usage.CompletionTokens)
	}

	// Each of these is reported by the real gateway. A nil here means the
	// adapter is dropping a field the provider is paying attention to.
	optional := []struct {
		name string
		got  *int
		want int
	}{
		{"total_tokens", usage.TotalTokens, 21},
		{"prompt_tokens_details.cached_tokens", usage.CachedTokens, 0},
		{"cache_read_input_tokens", usage.CacheReadTokens, 0},
		{"cache_creation_input_tokens", usage.CacheCreationTokens, 0},
		{"completion_tokens_details.reasoning_tokens", usage.ReasoningTokens, 0},
	}
	for _, f := range optional {
		if f.got == nil {
			t.Errorf("%s came back ABSENT; the real gateway reports it and the adapter dropped it", f.name)
			continue
		}
		if *f.got != f.want {
			t.Errorf("%s = %d, want %d", f.name, *f.got, f.want)
		}
	}

	// The frame reported cache counts, so cache WAS observed on this call —
	// even though every count is zero. That is the distinction the whole
	// optional-usage design exists to preserve.
	if !usage.CacheObserved() {
		t.Errorf("CacheObserved() = false on a frame that reports cache_read_input_tokens; " +
			"a measured zero is a measurement")
	}
}

// TestUsageFrameWithoutCacheFieldsStaysAbsent is the other half of the same
// contract, and the one that protects every cache figure from being an
// average over unknowns.
//
// A gateway that reports only the two totals must leave the optional counts
// nil. If they came back as zeros, a turn nobody measured would be
// indistinguishable from a turn that missed the cache entirely, and a hit
// rate computed across a fleet would quietly include every call made before
// caching existed.
func TestUsageFrameWithoutCacheFieldsStaysAbsent(t *testing.T) {
	const minimal = `data: {"choices":[{"index":0,"delta":{}}],"usage":{"prompt_tokens":17,"completion_tokens":4}}

data: [DONE]

`
	s := newSSEStream(io.NopCloser(strings.NewReader(minimal)))
	defer func() { _ = s.Close() }()

	ev, err := s.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	u := ev.Usage
	if u == nil {
		t.Fatalf("usage frame produced no usage")
	}
	if u.PromptTokens != 17 || u.CompletionTokens != 4 {
		t.Fatalf("totals = %d/%d", u.PromptTokens, u.CompletionTokens)
	}

	absent := map[string]*int{
		"TotalTokens":         u.TotalTokens,
		"CachedTokens":        u.CachedTokens,
		"CacheReadTokens":     u.CacheReadTokens,
		"CacheCreationTokens": u.CacheCreationTokens,
		"ReasoningTokens":     u.ReasoningTokens,
	}
	for name, v := range absent {
		if v != nil {
			t.Errorf("%s = %d on a frame that never mentioned it; ABSENT was turned into a measurement",
				name, *v)
		}
	}
	if u.CacheObserved() {
		t.Errorf("CacheObserved() = true on a frame carrying no cache field at all")
	}
}

// realCachedUsageFrame is the usage frame of a real CACHE HIT, captured
// verbatim from the same LiteLLM on 2026-08-30 by
// TestX3CaptureCachedUsageFrame.
//
// ── Why this fixture had to be captured separately ─────────────────────
// The stream captured above was taken before this system had ever sent a
// `cache_control` marker, so every cache number in it is zero. A zero
// cannot distinguish a parser that reads the right field from one that
// reads the wrong field and gets zero anyway, which is exactly the mistake
// that hid prompt caching from the cost report for the module's whole life.
//
// This frame has 13.965 tokens on the read side, so the fields it exercises
// are the ones that would be silently wrong. Sanitised in one way only: the
// per-request `id` was replaced. Note what it also records — a real cached
// call reports `cache_creation_tokens: 0` inside `prompt_tokens_details`
// while `cached_tokens` carries the read count, which is why the adapter
// reads the top-level Anthropic names first.
const realCachedUsageFrame = `data: {"id":"chatcmpl-x3","created":1788065803,"model":"claude-opus-4-7","object":"chat.completion.chunk","choices":[{"index":0,"delta":{}}],"usage":{"completion_tokens":8,"prompt_tokens":13976,"total_tokens":13984,"completion_tokens_details":{"reasoning_tokens":0,"text_tokens":8},"prompt_tokens_details":{"cached_tokens":13965,"text_tokens":11,"cache_write_tokens":0,"cache_creation_tokens":0,"cache_creation_token_details":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":0}},"cache_creation_input_tokens":0,"cache_read_input_tokens":13965,"inference_geo":"global"}}

data: [DONE]

`

// TestUsageFrameWithARealCacheHit replays the captured cache hit.
//
// This is the assertion X3 exists for on the parsing side: until it
// existed, nothing had ever confronted this adapter with a non-zero cache
// count, and the audit could not answer what fraction of a prompt was
// served from cache for that reason and no other.
func TestUsageFrameWithARealCacheHit(t *testing.T) {
	s := newSSEStream(io.NopCloser(strings.NewReader(realCachedUsageFrame)))
	defer func() { _ = s.Close() }()

	var u *ports.Usage
	for {
		ev, err := s.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		if ev.Usage != nil {
			u = ev.Usage
		}
	}
	if u == nil {
		t.Fatalf("no usage")
	}
	if u.PromptTokens != 13976 || u.CompletionTokens != 8 {
		t.Fatalf("totals = %d/%d, want 13976/8", u.PromptTokens, u.CompletionTokens)
	}
	if u.CacheReadTokens == nil || *u.CacheReadTokens != 13965 {
		t.Fatalf("CacheReadTokens = %v, want 13965", u.CacheReadTokens)
	}
	if u.CachedTokens == nil || *u.CachedTokens != 13965 {
		t.Fatalf("CachedTokens = %v, want 13965", u.CachedTokens)
	}
	// A real cache HIT reports zero creation. Present, and zero — which is a
	// measurement, and the reason the field is a pointer.
	if u.CacheCreationTokens == nil || *u.CacheCreationTokens != 0 {
		t.Fatalf("CacheCreationTokens = %v, want a present 0", u.CacheCreationTokens)
	}
	if u.ReasoningTokens == nil || *u.ReasoningTokens != 0 {
		t.Fatalf("ReasoningTokens = %v, want a present 0", u.ReasoningTokens)
	}
	if !u.CacheObserved() {
		t.Fatalf("CacheObserved() = false on a call that read 13965 tokens from cache")
	}

	// 99,9% of the prompt came from cache. Recorded as an assertion because
	// it is the number the whole sprint turns on: if a future change breaks
	// prefix stability, this ratio is what collapses first.
	if ratio := float64(*u.CacheReadTokens) / float64(u.PromptTokens); ratio < 0.99 {
		t.Fatalf("only %.1f%% of the prompt was served from cache", ratio*100)
	}
}

// TestSyntheticCacheWriteFrame is the other half of a cached turn: the call
// that PAYS to establish the entry. Captured shape, values from the write
// side of the same X3 run (13.965 created, nothing read).
func TestSyntheticCacheWriteFrame(t *testing.T) {
	const written = `data: {"choices":[{"index":0,"delta":{}}],"usage":{"completion_tokens":6,"prompt_tokens":13982,"total_tokens":13988,"prompt_tokens_details":{"cached_tokens":0,"cache_creation_tokens":0},"cache_creation_input_tokens":13965,"cache_read_input_tokens":0}}

data: [DONE]

`
	s := newSSEStream(io.NopCloser(strings.NewReader(written)))
	defer func() { _ = s.Close() }()

	ev, err := s.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	u := ev.Usage
	if u.CacheCreationTokens == nil || *u.CacheCreationTokens != 13965 {
		t.Fatalf("CacheCreationTokens = %v, want 13965", u.CacheCreationTokens)
	}
	if u.CacheReadTokens == nil || *u.CacheReadTokens != 0 {
		t.Fatalf("CacheReadTokens = %v, want a present 0 on a write", u.CacheReadTokens)
	}
	if !u.CacheObserved() {
		t.Fatalf("CacheObserved() = false on a call that wrote 13965 tokens to cache")
	}
}

// TestCacheCreationFallsBackToTheDetailsBlock pins the precedence rule
// toPortsUsage documents: the Anthropic-named top-level field wins, and the
// `prompt_tokens_details` restatement is the fallback rather than the
// authority.
//
// The case is real: not every LiteLLM version emits
// `cache_creation_input_tokens`, and a parser that only read the top-level
// name would report a write of zero on a call that paid the write premium.
func TestCacheCreationFallsBackToTheDetailsBlock(t *testing.T) {
	const detailsOnly = `data: {"choices":[{"index":0,"delta":{}}],"usage":{"prompt_tokens":12500,"completion_tokens":180,"prompt_tokens_details":{"cached_tokens":0,"cache_creation_tokens":11800}}}

data: [DONE]

`
	s := newSSEStream(io.NopCloser(strings.NewReader(detailsOnly)))
	defer func() { _ = s.Close() }()

	ev, err := s.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if ev.Usage.CacheCreationTokens == nil || *ev.Usage.CacheCreationTokens != 11800 {
		t.Fatalf("CacheCreationTokens = %v, want 11800 from prompt_tokens_details",
			ev.Usage.CacheCreationTokens)
	}
	// The read side was reported as a real zero and must stay a real zero.
	if ev.Usage.CacheReadTokens == nil || *ev.Usage.CacheReadTokens != 0 {
		t.Fatalf("CacheReadTokens = %v, want a present 0 taken from cached_tokens",
			ev.Usage.CacheReadTokens)
	}
}

func TestRealLiteLLMTotalTokensIsTheSum(t *testing.T) {
	var chunk struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	// The third frame of the captured stream.
	frame := strings.Split(realLiteLLMStream, "data: ")[3]
	if err := json.Unmarshal([]byte(strings.TrimSpace(frame)), &chunk); err != nil {
		t.Fatalf("decode the captured usage frame: %v", err)
	}

	sum := chunk.Usage.PromptTokens + chunk.Usage.CompletionTokens
	if chunk.Usage.TotalTokens != sum {
		t.Fatalf("total_tokens = %d but prompt+completion = %d; dropping the field loses information",
			chunk.Usage.TotalTokens, sum)
	}
}
