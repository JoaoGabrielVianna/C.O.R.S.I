package llm

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

/* ── the request body ────────────────────────────────────────────────── */

func marshalRequest(t *testing.T, req ports.CompletionRequest) map[string]any {
	t.Helper()
	raw, err := json.Marshal(completionRequest{
		Model:         req.Model,
		Messages:      toWireMessages(req.Messages),
		Temperature:   req.Temperature,
		MaxTokens:     req.MaxTokens,
		Stream:        true,
		StreamOptions: &streamOptions{IncludeUsage: true},
		User:          req.User,
		Metadata:      req.Metadata,
		Tools:         toWireTools(req.Tools),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

// The backward-compatibility guarantee of the whole batch, asserted on the
// bytes: an agent with no authorized tools sends the body X1 verified
// against a real gateway, with no new keys at all.
func TestAgentWithoutToolsSendsNoToolsField(t *testing.T) {
	body := marshalRequest(t, ports.CompletionRequest{
		Model:    "test-model",
		Messages: []ports.ChatMessage{{Role: "user", Content: "olá"}},
	})

	if _, present := body["tools"]; present {
		t.Fatal("a `tools` key was sent for an agent that has none")
	}
	if _, present := body["tool_choice"]; present {
		t.Fatal("`tool_choice` must never be sent: the default is what we want")
	}

	messages := body["messages"].([]any)
	first := messages[0].(map[string]any)
	if len(first) != 2 {
		t.Fatalf("a plain message carries %d keys (%v), want exactly role and content", len(first), first)
	}
	if first["role"] != "user" || first["content"] != "olá" {
		t.Fatalf("message = %v", first)
	}
}

// An assistant message that only asks for a tool still carries `content`.
// Dropping the key on an empty string is the kind of "cleanup" several
// gateways reject outright.
func TestAssistantToolCallKeepsAnEmptyContentKey(t *testing.T) {
	body := marshalRequest(t, ports.CompletionRequest{
		Messages: []ports.ChatMessage{{
			Role: "assistant",
			ToolCalls: []domain.ToolCall{
				{ID: "call_1", Name: "system.echo", Arguments: `{"text":"a"}`},
			},
		}},
	})
	msg := body["messages"].([]any)[0].(map[string]any)
	if _, present := msg["content"]; !present {
		t.Fatal("the content key was dropped from an assistant tool call")
	}
}

func TestToolDeclarationShape(t *testing.T) {
	body := marshalRequest(t, ports.CompletionRequest{
		Tools: []domain.ToolDefinition{{
			Name:        "system.echo",
			Description: "Returns the text it was given.",
			Effect:      domain.EffectRead,
			Schema: domain.ToolSchema{
				Properties: map[string]domain.ToolProperty{
					"text": {Type: domain.TypeString, Description: "the text", MaxLength: 2000},
				},
				Required: []string{"text"},
			},
		}},
	})

	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v, want one declaration", body["tools"])
	}
	decl := tools[0].(map[string]any)
	if decl["type"] != "function" {
		t.Errorf(`type = %v, want "function"`, decl["type"])
	}
	fn := decl["function"].(map[string]any)
	if fn["name"] != "system__echo" {
		t.Errorf("name = %v, want the wire-encoded name", fn["name"])
	}
	params := fn["parameters"].(map[string]any)
	if params["type"] != "object" {
		t.Errorf("parameters.type = %v", params["type"])
	}
	// additionalProperties:false is what makes a gateway's constrained
	// decoding refuse an invented field before we have to.
	if params["additionalProperties"] != false {
		t.Error("additionalProperties must be sent as false")
	}
	required := params["required"].([]any)
	if len(required) != 1 || required[0] != "text" {
		t.Errorf("required = %v", required)
	}
	prop := params["properties"].(map[string]any)["text"].(map[string]any)
	if prop["type"] != "string" || prop["maxLength"] != float64(2000) {
		t.Errorf("property = %v", prop)
	}
}

// A tool result rides as a `tool` message correlated by id. The id is the
// only thing that ties an answer to the question that asked for it.
func TestToolResultCarriesTheProviderCallID(t *testing.T) {
	body := marshalRequest(t, ports.CompletionRequest{
		Messages: []ports.ChatMessage{{
			Role:       "tool",
			Content:    `{"text":"a"}`,
			ToolCallID: "call_abc123",
		}},
	})
	msg := body["messages"].([]any)[0].(map[string]any)
	if msg["role"] != "tool" {
		t.Errorf("role = %v", msg["role"])
	}
	if msg["tool_call_id"] != "call_abc123" {
		t.Errorf("tool_call_id = %v, want the id the model sent", msg["tool_call_id"])
	}
}

/* ── the name encoding ───────────────────────────────────────────────── */

// Canonical names are dotted; the protocol documents a function-name
// character set that does not include a dot. The encoding is confined to
// this file and must round-trip exactly, or a call for one tool executes
// another.
func TestWireNameRoundTrip(t *testing.T) {
	names := []domain.ToolName{
		"system.echo",
		"github.repository.read",
		"finance.transaction.create",
		"github.pull_request.comment.create",
	}
	for _, n := range names {
		wire := toWireName(n)
		if strings.Contains(wire, ".") {
			t.Errorf("toWireName(%q) = %q, which still contains a dot", n, wire)
		}
		if got := fromWireName(wire); got != n {
			t.Errorf("round trip of %q gave %q", n, got)
		}
	}
}

/* ── streamed tool calls ─────────────────────────────────────────────── */

func streamOf(frames ...string) *sseStream {
	return newSSEStream(io.NopCloser(strings.NewReader(strings.Join(frames, "\n") + "\n")))
}

func drainEvents(t *testing.T, s *sseStream) []ports.StreamEvent {
	t.Helper()
	var out []ports.StreamEvent
	for i := 0; i < 50; i++ {
		ev, err := s.Recv()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		out = append(out, ev)
	}
	t.Fatal("the stream never ended")
	return nil
}

// The shape a real OpenAI-compatible gateway streams: the id and the name
// arrive once, and the argument JSON arrives a few characters at a time.
// Only the concatenation is parseable, and assembling it is the adapter's
// job — the application layer must never see a fragment.
func TestFragmentedToolCallIsAssembled(t *testing.T) {
	s := streamOf(
		`data: {"choices":[{"index":0,"delta":{"role":"assistant","content":""}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"system__echo","arguments":""}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"te"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"xt\":\"ol"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"á\"}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: {"choices":[{"index":0,"delta":{}}],"usage":{"prompt_tokens":40,"completion_tokens":12}}`,
		`data: [DONE]`,
	)

	events := drainEvents(t, s)

	var calls []domain.ToolCall
	var finish string
	var usage *ports.Usage
	for _, ev := range events {
		if len(ev.ToolCalls) > 0 {
			if calls != nil {
				t.Fatal("tool calls were emitted more than once")
			}
			calls = ev.ToolCalls
		}
		if ev.FinishReason != "" {
			finish = ev.FinishReason
		}
		if ev.Usage != nil {
			usage = ev.Usage
		}
	}

	if finish != "tool_calls" {
		t.Fatalf("finish reason = %q", finish)
	}
	if len(calls) != 1 {
		t.Fatalf("assembled %d calls, want 1", len(calls))
	}
	if calls[0].ID != "call_1" {
		t.Errorf("id = %q", calls[0].ID)
	}
	// Decoded back from the wire form, which is what the rest of the system
	// works with.
	if calls[0].Name != "system.echo" {
		t.Errorf("name = %q, want the canonical dotted name", calls[0].Name)
	}
	if calls[0].Arguments != `{"text":"olá"}` {
		t.Errorf("arguments = %q, want the fragments concatenated", calls[0].Arguments)
	}
	// The usage frame still has to survive, tool calls or not — this is the
	// trap X1 found, in the presence of the new code path.
	if usage == nil || usage.PromptTokens != 40 || usage.CompletionTokens != 12 {
		t.Fatalf("usage = %+v, want the gateway's numbers", usage)
	}
}

// A model may ask for several tools in one turn. `index` is the only field
// present on every fragment, so it is what groups them, and the order it
// gives is the order they are run in.
func TestParallelToolCallsAreGroupedByIndex(t *testing.T) {
	s := streamOf(
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_b","function":{"name":"system__echo","arguments":"{\"text\":\"b\"}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"name":"system__echo","arguments":"{\"text\":\"a\"}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	)
	var calls []domain.ToolCall
	for _, ev := range drainEvents(t, s) {
		if len(ev.ToolCalls) > 0 {
			calls = ev.ToolCalls
		}
	}
	if len(calls) != 2 {
		t.Fatalf("assembled %d calls, want 2", len(calls))
	}
	if calls[0].ID != "call_a" || calls[1].ID != "call_b" {
		t.Fatalf("calls are not in index order: %q then %q", calls[0].ID, calls[1].ID)
	}
}

// A gateway that puts the last argument fragment and the terminal reason in
// the same frame must not lose that fragment.
func TestFinalFragmentInTheTerminalFrameIsKept(t *testing.T) {
	s := streamOf(
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"system__echo","arguments":"{\"text\":"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	)
	var calls []domain.ToolCall
	for _, ev := range drainEvents(t, s) {
		if len(ev.ToolCalls) > 0 {
			calls = ev.ToolCalls
		}
	}
	if len(calls) != 1 || calls[0].Arguments != `{"text":"a"}` {
		t.Fatalf("calls = %+v, want the last fragment included", calls)
	}
}

// A gateway that streams tool calls and then closes without a terminal
// reason is out of spec. Dropping the calls would turn that into a turn
// that silently did nothing, so they are delivered on the way out.
func TestToolCallsSurviveAStreamThatEndsWithoutAFinishReason(t *testing.T) {
	s := streamOf(
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"system__echo","arguments":"{\"text\":\"a\"}"}}]}}]}`,
		`data: [DONE]`,
	)
	var calls []domain.ToolCall
	var finish string
	for _, ev := range drainEvents(t, s) {
		if len(ev.ToolCalls) > 0 {
			calls = ev.ToolCalls
			finish = ev.FinishReason
		}
	}
	if len(calls) != 1 {
		t.Fatalf("assembled %d calls, want the pending one delivered", len(calls))
	}
	if finish != "tool_calls" {
		t.Fatalf("finish reason = %q, want tool_calls stated explicitly", finish)
	}
}

// A fragment group that never received a name cannot be resolved to a tool.
// Delivering it would reach the application layer as a call for the empty
// tool, which is a worse failure than one that never arrived.
func TestANamelessFragmentGroupIsDropped(t *testing.T) {
	s := streamOf(
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"arguments":"{}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	)
	for _, ev := range drainEvents(t, s) {
		if len(ev.ToolCalls) > 0 {
			t.Fatalf("a nameless call was delivered: %+v", ev.ToolCalls)
		}
	}
}

// An ordinary answer must be byte-for-byte the stream it always was: no
// tool calls, no extra events, same deltas.
func TestAnOrdinaryStreamIsUnchangedByTheToolCode(t *testing.T) {
	s := streamOf(
		`data: {"choices":[{"index":0,"delta":{"role":"assistant","content":"Ok"}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: {"choices":[{"index":0,"delta":{}}],"usage":{"prompt_tokens":17,"completion_tokens":4}}`,
		`data: [DONE]`,
	)
	events := drainEvents(t, s)
	if len(events) != 3 {
		t.Fatalf("got %d events, want delta, finish and usage", len(events))
	}
	for _, ev := range events {
		if len(ev.ToolCalls) > 0 {
			t.Fatalf("an ordinary answer produced tool calls: %+v", ev.ToolCalls)
		}
	}
	if events[0].Delta != "Ok" || events[1].FinishReason != "stop" || events[2].Usage == nil {
		t.Fatalf("events = %+v", events)
	}
}
