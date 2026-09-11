//go:build integration

// The release gate for prompt caching, through the production route.
//
// ── What is proved here and nowhere else ───────────────────────────────
// `app/context_cache_test.go` proves where the breakpoint lands.
// `adapters/llm/cache_control_test.go` proves what the marker serialises
// to. Neither of them runs a turn.
//
// This file runs turns — real service, real repositories, real builder,
// real loop, real persistence, with the LLM port faked so the REQUEST can
// be inspected — and asserts the two properties a release has to stand on:
//
//	the default deployment marks its stable prefix, once, in the right place
//	the kill switch restores the previous request body
package chat

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/chat/ports"
)

/* ── helpers ─────────────────────────────────────────────────────────── */

const cachePrompt = "Você é um agente de teste do gate de caching."

// breakpointIndexes returns the position of every marked message in a
// captured request.
func breakpointIndexes(req ports.CompletionRequest) []int {
	var out []int
	for i, m := range req.Messages {
		if m.CacheBreakpoint {
			out = append(out, i)
		}
	}
	return out
}

// seedCachingAgent creates an agent that carries instructions and one
// capability — the shape every production agent has, and the shape whose
// prefix is worth caching.
func seedCachingAgent(t *testing.T, e *env, ws uuid.UUID) seeded {
	t.Helper()
	s := e.seedWith(ws, map[string]any{"system_prompt": cachePrompt})
	rec := e.do("POST", "/chat/agents/"+s.agentID+"/tools", ws,
		map[string]any{"tool_name": "system.echo"})
	wantStatus(t, rec, http.StatusOK)
	return s
}

/* ── the default deployment ──────────────────────────────────────────── */

// TestDefaultDeploymentMarksTheStablePrefixOnce is the positive half of the
// release gate.
//
// A deployment that configures nothing must send exactly one breakpoint,
// and it must sit on the agent's own instructions — the last thing in a
// request that is the same bytes on every turn of that agent's life.
// Everything after it is rewritten or appended per turn, so a marker below
// it would be an entry written on every turn and read on none, which turns
// caching from a saving into a 25% surcharge.
func TestDefaultDeploymentMarksTheStablePrefixOnce(t *testing.T) {
	e := newEnv(t)
	s := seedCachingAgent(t, e, e.wsA)

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "olá"})
	wantStatus(t, rec, http.StatusOK)

	req := e.llm.lastRequest
	marks := breakpointIndexes(req)
	if len(marks) != 1 {
		t.Fatalf("%d breakpoints on a default deployment, want exactly one: %v", len(marks), marks)
	}
	msg := req.Messages[marks[0]]
	if msg.Role != "system" {
		t.Fatalf("the breakpoint landed on a %q message", msg.Role)
	}
	if msg.Content != cachePrompt {
		t.Fatalf("the breakpoint is not on the agent's own instructions: %q", msg.Content)
	}
	// There must be something after it. A prefix that ends at the last
	// message caches the whole request, including the part that changes.
	if marks[0] >= len(req.Messages)-1 {
		t.Fatalf("the breakpoint is the last message; nothing volatile follows it")
	}

	// And the turn recorded that it asked. A cache read of zero has two very
	// different causes — the turn never asked, or it asked and the prefix had
	// moved — and the counts alone cannot tell them apart.
	msgs := e.messages(e.wsA, s.conversationID)
	turn := msgs[len(msgs)-1]
	if turn.ContextReport == nil {
		t.Fatalf("no context report")
	}
	if turn.ContextReport.CacheBreakpointAfter != "instructions" {
		t.Fatalf("the report says the prefix ends after %q, want instructions",
			turn.ContextReport.CacheBreakpointAfter)
	}
}

// TestTheToolCatalogueRidesInsideTheMarkedPrefix records WHY one marker is
// enough, at the level of the request rather than of the gateway.
//
// The provider renders `tools` before `system`, so a marker on the
// instructions closes a prefix that already contains the tool declaration —
// which is 51,6% of every input token this system sends. That ordering was
// measured against this deployment's own gateway (18.387 of 18.829 prompt
// tokens read back with a 66-character system message). What this asserts is
// the precondition on our side: the tools are declared on the same request
// as the marked message, not on a later one.
func TestTheToolCatalogueRidesInsideTheMarkedPrefix(t *testing.T) {
	e := newEnv(t)
	s := seedCachingAgent(t, e, e.wsA)

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "olá"})
	wantStatus(t, rec, http.StatusOK)

	req := e.llm.lastRequest
	if len(req.Tools) == 0 {
		t.Fatalf("the request declared no tools; this test proves nothing about the catalogue")
	}
	if len(breakpointIndexes(req)) != 1 {
		t.Fatalf("want exactly one breakpoint")
	}
}

/* ── the kill switch ─────────────────────────────────────────────────── */

// TestKillSwitchRestoresThePreCachingRequestBody is the negative half, and
// the one a rollback depends on.
//
// It runs the SAME turn twice against two services that differ in exactly
// one boolean, then compares. The requirement is not "caching is off": it is
// that the disabled body is the one this module sent before caching existed.
//
// The `content` shape is asserted rather than described, because that is
// what the promise says. A change that turned every `content` into a
// one-element block array would still be semantically identical and would
// still be a change to a verified external boundary.
func TestKillSwitchRestoresThePreCachingRequestBody(t *testing.T) {
	capture := func(opts ...envOption) ports.CompletionRequest {
		e := newEnv(t, opts...)
		s := seedCachingAgent(t, e, e.wsA)
		rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
			map[string]any{"content": "olá"})
		wantStatus(t, rec, http.StatusOK)
		return e.llm.lastRequest
	}

	on := capture()
	off := capture(withPromptCacheDisabled())

	if n := len(breakpointIndexes(off)); n != 0 {
		t.Fatalf("the kill switch left %d breakpoints in place", n)
	}
	if n := len(breakpointIndexes(on)); n != 1 {
		t.Fatalf("the enabled arm carried %d breakpoints, want 1", n)
	}

	// ── how this becomes a claim about BYTES ────────────────────────
	//
	// The serialisation is the adapter's contract and is proved there, on
	// the bytes: `CacheBreakpoint == false` marshals `content` as a bare
	// JSON string with no `cache_control` anywhere, asserted literally in
	// adapters/llm.TestUnmarkedContentIsAByteIdenticalString.
	//
	// `toWireMessages` is unexported, and exporting it so this file could
	// call it would add production surface to serve a test. So the proof is
	// a chain rather than one assertion, and this is its first link: the
	// service hands the adapter a message list with no marker on it. The
	// adapter test is the second link, and together they say the disabled
	// body is byte-identical to the pre-caching one.
	//
	// The assertion above (`len(breakpointIndexes(off)) == 0`) is that link.

	// ── and nothing else moved ──────────────────────────────────────
	//
	// Same messages, same order, same roles, same text, same tools, same
	// parameters. If the two differ anywhere other than the marker, the kill
	// switch is not a kill switch, it is a second code path.
	if len(on.Messages) != len(off.Messages) {
		t.Fatalf("message count differs: %d on, %d off", len(on.Messages), len(off.Messages))
	}
	for i := range on.Messages {
		a, b := on.Messages[i], off.Messages[i]
		if a.Role != b.Role || a.Content != b.Content || a.ToolCallID != b.ToolCallID {
			t.Fatalf("message %d differs beyond the marker:\n on: %+v\noff: %+v", i, a, b)
		}
	}
	if len(on.Tools) != len(off.Tools) {
		t.Fatalf("the tool declaration differs: %d on, %d off", len(on.Tools), len(off.Tools))
	}
	if on.Model != off.Model || on.MaxTokens != off.MaxTokens || on.Temperature != off.Temperature {
		t.Fatalf("a request parameter differs beyond the marker")
	}
}

// TestKillSwitchTurnRecordsNoBreakpoint: the report must say the turn asked
// for nothing, rather than saying nothing.
func TestKillSwitchTurnRecordsNoBreakpoint(t *testing.T) {
	e := newEnv(t, withPromptCacheDisabled())
	s := seedCachingAgent(t, e, e.wsA)

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "olá"})
	wantStatus(t, rec, http.StatusOK)

	msgs := e.messages(e.wsA, s.conversationID)
	turn := msgs[len(msgs)-1]
	if turn.ContextReport == nil {
		t.Fatalf("no context report")
	}
	if turn.ContextReport.CacheBreakpointAfter != "" {
		t.Fatalf("the report claims a breakpoint after %q with caching disabled",
			turn.ContextReport.CacheBreakpointAfter)
	}
}

/* ── an agent with nothing stable ────────────────────────────────────── */

// TestAnAgentWithNoInstructionsMarksNothing.
//
// An agent with no capabilities and no system prompt has no stable head to
// cache. Marking the first history message instead would put a breakpoint on
// the most volatile block in the request — the one that grows every turn —
// and every entry would be written once and never read.
func TestAnAgentWithNoInstructionsMarksNothing(t *testing.T) {
	e := newEnv(t)
	s := e.seedWith(e.wsA, map[string]any{"system_prompt": ""})

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "olá"})
	wantStatus(t, rec, http.StatusOK)

	req := e.llm.lastRequest
	if len(req.Tools) != 0 {
		t.Fatalf("this fixture expects an agent with no tools; it has %d", len(req.Tools))
	}
	if marks := breakpointIndexes(req); len(marks) != 0 {
		t.Fatalf("%d breakpoints with nothing stable to mark: %v", len(marks), marks)
	}
}

/* ── the turn loop ───────────────────────────────────────────────────── */

// TestEveryRoundOfATurnCarriesTheSameBreakpoint.
//
// A turn is a loop, and the marker has to survive it. Within one turn the
// prefix is not merely stable, it is untouched — which makes the second
// provider call the single most certain cache hit in the system, and the
// audit measured that 55,2% of a multi-round turn's input tokens are exactly
// that retransmission.
//
// If the marker were dropped after the first call, every tool round would go
// back to full price and the largest saving in the system would silently
// disappear.
func TestEveryRoundOfATurnCarriesTheSameBreakpoint(t *testing.T) {
	e := newEnv(t)
	s := seedCachingAgent(t, e, e.wsA)

	e.llm.rounds = [][]ports.StreamEvent{
		{{
			FinishReason: "tool_calls",
			ToolCalls: []domain.ToolCall{
				{ID: "call_1", Name: "system.echo", Arguments: `{"text":"oi"}`},
			},
			Usage: &ports.Usage{PromptTokens: 100, CompletionTokens: 10},
		}},
		{
			{Delta: "pronto"},
			{FinishReason: "stop", Usage: &ports.Usage{PromptTokens: 140, CompletionTokens: 5}},
		},
	}

	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "ecoe oi"})
	wantStatus(t, rec, http.StatusOK)

	if len(e.llm.requests) < 2 {
		t.Fatalf("the turn made %d provider calls, want at least 2", len(e.llm.requests))
	}
	for i, req := range e.llm.requests {
		marks := breakpointIndexes(req)
		if len(marks) != 1 {
			t.Fatalf("round %d carried %d breakpoints, want exactly one", i+1, len(marks))
		}
		if req.Messages[marks[0]].Content != cachePrompt {
			t.Fatalf("round %d marked the wrong message: %q", i+1, req.Messages[marks[0]].Content)
		}
	}
	// The later round must be the longer one — it carries the tool exchange —
	// while the marked prefix is identical. That is the shape a cache hit is
	// made of.
	if len(e.llm.requests[1].Messages) <= len(e.llm.requests[0].Messages) {
		t.Fatalf("round 2 did not grow; the tool exchange did not reach the model")
	}
}

/* ── what is, and is not, inside a cache entry ───────────────────────── */

// TestNoConversationContentIsInsideTheCachedPrefix is the security
// assertion of this release, and it is the one that bounds every claim
// about cache isolation.
//
// ── Why it matters ─────────────────────────────────────────────────────
// A provider-side cache entry is keyed on the exact byte prefix of a
// request. Everything BEFORE the breakpoint can end up in one; everything
// after it cannot. So the question "what could a cache entry contain?" has
// an exact answer, and it is decided entirely by where the marker sits.
//
// ── The answer ─────────────────────────────────────────────────────────
// Only the tool declaration, the platform grounding policy, and the agent's
// own system prompt. All three are operator-authored configuration. No user
// message, no assistant reply, no tool result, no hydrated entity state and
// no financial payload is ever inside the marked prefix, because every one
// of those blocks is composed after the instructions.
//
// This test drives a real conversation with real content and a real tool
// exchange, then checks that none of it appears at or before the marker.
func TestNoConversationContentIsInsideTheCachedPrefix(t *testing.T) {
	const secret = "SEGREDO-QUE-NAO-PODE-ENTRAR-NO-PREFIXO"

	e := newEnv(t)
	s := seedCachingAgent(t, e, e.wsA)

	e.llm.rounds = [][]ports.StreamEvent{
		{{
			FinishReason: "tool_calls",
			ToolCalls: []domain.ToolCall{
				{ID: "call_1", Name: "system.echo", Arguments: `{"text":"` + secret + `"}`},
			},
			Usage: &ports.Usage{PromptTokens: 100, CompletionTokens: 10},
		}},
		{
			{Delta: "pronto"},
			{FinishReason: "stop", Usage: &ports.Usage{PromptTokens: 140, CompletionTokens: 5}},
		},
	}

	// A first turn, so the second one replays real history.
	rec := e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "ecoe " + secret})
	wantStatus(t, rec, http.StatusOK)

	e.llm.rounds = nil
	rec = e.do("POST", "/chat/conversations/"+s.conversationID+"/messages", e.wsA,
		map[string]any{"content": "e agora?"})
	wantStatus(t, rec, http.StatusOK)

	for n, req := range e.llm.requests {
		marks := breakpointIndexes(req)
		if len(marks) != 1 {
			t.Fatalf("request %d carried %d breakpoints", n+1, len(marks))
		}
		// Everything at or before the marker is what a cache entry may hold.
		for i := 0; i <= marks[0]; i++ {
			m := req.Messages[i]
			if m.Role != "system" {
				t.Fatalf("request %d: message %d inside the cached prefix has role %q; "+
					"only system-level instructions may be there", n+1, i, m.Role)
			}
			if strings.Contains(m.Content, secret) {
				t.Fatalf("request %d: conversation content reached the cached prefix at "+
					"message %d", n+1, i)
			}
			if len(m.ToolCalls) > 0 || m.ToolCallID != "" {
				t.Fatalf("request %d: a tool exchange is inside the cached prefix at "+
					"message %d", n+1, i)
			}
		}
	}

	// And the content really was in the request, after the marker — otherwise
	// this test would pass against a turn that never carried it.
	last := e.llm.requests[len(e.llm.requests)-1]
	found := false
	for i := breakpointIndexes(last)[0] + 1; i < len(last.Messages); i++ {
		if strings.Contains(last.Messages[i].Content, secret) {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("the fixture's content never reached the request at all; this test " +
			"is asserting nothing")
	}
}
