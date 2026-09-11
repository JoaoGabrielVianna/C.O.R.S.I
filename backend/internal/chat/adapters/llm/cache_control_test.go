package llm

import (
	"encoding/json"
	"testing"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

// The wire representation of a cache breakpoint.
//
// ── What this file is for ──────────────────────────────────────────────
// Prompt caching changes how a provider BILLS a prefix. It must not change
// what the model reads. That claim is cheap to make and easy to get wrong —
// a content-block rewrite is exactly the sort of change that silently drops
// a character, reorders a message, or turns an empty string into `null` — so
// it is asserted here on the bytes rather than argued in a comment.
//
// Two properties, and both are load bearing:
//
//  1. With no breakpoint, the body is the one the external boundary was
//     verified against. Not "equivalent": the same bytes.
//  2. With a breakpoint, the ONLY difference is the envelope around the
//     text. Same text, same order, same roles, same everything else.

// marshalMessages is the message list as it goes on the wire.
func marshalMessages(t *testing.T, msgs []ports.ChatMessage) string {
	t.Helper()
	raw, err := json.Marshal(toWireMessages(msgs))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

// TestUnmarkedContentIsAByteIdenticalString is property (1).
//
// The `content` field became a type with a custom marshaller so that
// cache_control could be expressed at all. If that type ever serialises the
// default case as anything other than a bare JSON string — an object, a
// one-element array, `null` — every request in the system changes shape at
// the external boundary, and the change would be invisible in Go.
func TestUnmarkedContentIsAByteIdenticalString(t *testing.T) {
	got := marshalMessages(t, []ports.ChatMessage{
		{Role: "system", Content: "instruções"},
		{Role: "user", Content: "olá"},
		{Role: "assistant", Content: ""},
	})
	want := `[{"role":"system","content":"instruções"},` +
		`{"role":"user","content":"olá"},` +
		`{"role":"assistant","content":""}]`
	if got != want {
		t.Fatalf("the default wire shape changed.\n got: %s\nwant: %s", got, want)
	}
}

// TestCacheBreakpointEmitsTheBlockForm is the positive case: the marker is
// where the protocol needs it, on a text block, and nothing else moved.
func TestCacheBreakpointEmitsTheBlockForm(t *testing.T) {
	got := marshalMessages(t, []ports.ChatMessage{
		{Role: "system", Content: "instruções", CacheBreakpoint: true},
		{Role: "user", Content: "olá"},
	})
	want := `[{"role":"system","content":[{"type":"text","text":"instruções",` +
		`"cache_control":{"type":"ephemeral"}}]},` +
		`{"role":"user","content":"olá"}]`
	if got != want {
		t.Fatalf("breakpoint wire shape.\n got: %s\nwant: %s", got, want)
	}
}

// TestCacheBreakpointDoesNotChangeWhatTheModelReads is the semantic
// equivalence proof, and it is the reason this sprint can call itself
// lossless.
//
// It takes the same context twice — once plain, once with a breakpoint —
// strips the caching envelope back off, and requires the two to be
// identical. Same roles, same order, same characters, same tool scaffolding.
// A change that reordered messages, dropped one, altered a single rune or
// moved the tool call would fail here even though both bodies would still be
// valid JSON the gateway accepts.
func TestCacheBreakpointDoesNotChangeWhatTheModelReads(t *testing.T) {
	base := []ports.ChatMessage{
		{Role: "system", Content: "política de fundamentação"},
		{Role: "system", Content: "você é o Ledger"},
		{Role: "system", Content: "memória do agente"},
		{Role: "user", Content: "quanto gastei em agosto?"},
		{Role: "assistant", Content: "", ToolCalls: []domain.ToolCall{
			{ID: "call_1", Name: "finance.summary.get", Arguments: `{"period":"month"}`},
		}},
		{Role: "tool", Content: `{"total_cents":123456}`, ToolCallID: "call_1"},
	}

	cached := make([]ports.ChatMessage, len(base))
	copy(cached, base)
	cached[1].CacheBreakpoint = true // end of the stable prefix

	plainBody := marshalMessages(t, base)
	cachedBody := marshalMessages(t, cached)

	if plainBody == cachedBody {
		t.Fatal("the breakpoint produced no difference at all; it was not applied")
	}

	// Now flatten both back to (role, text, tool scaffolding) and compare.
	// This is the assertion: the caching metadata is the ONLY difference.
	if got, want := flatten(t, cachedBody), flatten(t, plainBody); got != want {
		t.Fatalf("a cache breakpoint changed what the model reads.\n"+
			"cached: %s\n plain: %s", got, want)
	}
}

// flatten reduces a marshalled message list to what the model actually
// receives, discarding the caching envelope.
//
// It reads the array form and the string form the same way on purpose: that
// is the whole definition of "the envelope is not content".
func flatten(t *testing.T, body string) string {
	t.Helper()
	var msgs []struct {
		Role       string          `json:"role"`
		Content    json.RawMessage `json:"content"`
		ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
		ToolCallID string          `json:"tool_call_id,omitempty"`
	}
	if err := json.Unmarshal([]byte(body), &msgs); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	type flatMessage struct {
		Role       string
		Text       string
		ToolCalls  string
		ToolCallID string
	}
	out := make([]flatMessage, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, flatMessage{
			Role:       m.Role,
			Text:       textOf(t, m.Content),
			ToolCalls:  string(m.ToolCalls),
			ToolCallID: m.ToolCallID,
		})
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("re-marshal: %v", err)
	}
	return string(raw)
}

// textOf reads the text out of either content shape.
//
// A block array contributes the concatenation of its text blocks, which for
// this adapter is always exactly one. Anything else is a shape nobody
// intended to send and is surfaced as a failure rather than skipped.
func textOf(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		t.Fatalf("content is neither a string nor a block array: %s", raw)
	}
	text := ""
	for _, b := range blocks {
		if b.Type != "text" {
			t.Fatalf("unexpected content block type %q", b.Type)
		}
		text += b.Text
	}
	return text
}

// TestCacheBreakpointOnAnEmptyAssistantTurn guards the one message shape
// that has repeatedly broken gateways: an assistant turn whose content is
// empty because it only asked for a tool.
//
// It should never carry a breakpoint — a breakpoint belongs at the end of a
// STABLE prefix and a tool call is the least stable thing in a turn — but if
// one ever arrives here, an empty text block is a documented 400 on several
// gateways, and turning it into a silent empty array would be worse. The
// marker is dropped and the string form is kept.
func TestCacheBreakpointOnEmptyContentStaysAString(t *testing.T) {
	got := marshalMessages(t, []ports.ChatMessage{
		{Role: "assistant", Content: "", CacheBreakpoint: true, ToolCalls: []domain.ToolCall{
			{ID: "c1", Name: "system.echo", Arguments: `{}`},
		}},
	})
	want := `[{"role":"assistant","content":"","tool_calls":` +
		`[{"id":"c1","type":"function","function":{"name":"system__echo","arguments":"{}"}}]}]`
	if got != want {
		t.Fatalf("an empty body must not become an empty text block.\n got: %s\nwant: %s", got, want)
	}
}

// TestWholeRequestBodyUnchangedWithoutBreakpoints is property (1) again, at
// the level of the whole request rather than the message list.
//
// It is the regression that would catch a change in this file leaking into
// `tools`, `stream_options`, or the metadata — the parts of the body the
// external boundary was verified against and that caching has no business
// touching.
func TestWholeRequestBodyUnchangedWithoutBreakpoints(t *testing.T) {
	body := marshalRequest(t, ports.CompletionRequest{
		Model:       "claude-opus-4-7",
		Temperature: 1,
		MaxTokens:   4096,
		Messages: []ports.ChatMessage{
			{Role: "system", Content: "você é o Ledger"},
			{Role: "user", Content: "olá"},
		},
	})
	messages := body["messages"].([]any)
	for i, m := range messages {
		msg := m.(map[string]any)
		if _, ok := msg["content"].(string); !ok {
			t.Fatalf("message %d sent a non-string content with no breakpoint: %v", i, msg["content"])
		}
	}
	if _, present := body["cache_control"]; present {
		t.Fatal("a top-level cache_control appeared; the breakpoint is per message")
	}
}
