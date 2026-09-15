//go:build integration

// Temperature — S8B, part B.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THE PLATFORM DOES NOT CHOOSE A TEMPERATURE
//
// ══════════════════════════════════════════════════════════════════════
//
// ── The defect ─────────────────────────────────────────────────────────
// `chat.agents.temperature` defaulted to 0.7, so an agent created without
// an explicit choice carried one. Measured against the real gateway:
//
//	claude-opus-4-7             0.0  400 litellm.UnsupportedParamsError
//	                            0.5  400 litellm.UnsupportedParamsError
//	                            0.7  400 litellm.UnsupportedParamsError
//	                            1.0  ok
//	claude-haiku-4-5-20251001   0.0  ok
//	                            0.5  ok
//	                            0.7  ok
//	                            1.0  ok
//
// So creating an agent on that family through the ordinary path produced a
// 502 on its FIRST turn. Nothing was misconfigured by the operator: the
// platform chose the value, and the value was impossible.
//
// ── Why the fix is an absence rather than a better number ──────────────
// The two families above disagree about the same value, and the opus
// constraint is not a range but a single permitted number. There is no
// constant this system could pick that is right everywhere, and the one
// that would satisfy opus — 1 — would impose the loosest sampling there is
// on every other model to do it. Absence is the only answer that is correct
// for models nobody has measured yet.
package chat

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// agentTemperature reads the stored preference the way a client would:
// present with a number, or null.
func (e *env) agentTemperature(ws uuid.UUID, agentID string) (float32, bool) {
	e.t.Helper()
	rec := e.do("GET", "/chat/agents/"+agentID, ws, nil)
	wantStatus(e.t, rec, http.StatusOK)
	var body struct {
		Temperature *float32 `json:"temperature"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		e.t.Fatalf("decode agent: %v", err)
	}
	if body.Temperature == nil {
		return 0, false
	}
	return *body.Temperature, true
}

/* ── A · the default path no longer chooses ──────────────────────────── */

// The exact path that produced the 502: create an agent, say nothing about
// temperature, send a turn.
func TestAnAgentCreatedWithoutATemperatureSendsNone(t *testing.T) {
	e := newEnv(t)
	s := e.seedWith(e.wsA, map[string]any{"model": "claude-opus-4-7"})

	if _, set := e.agentTemperature(e.wsA, s.agentID); set {
		t.Fatal("the platform chose a temperature nobody asked for; on " +
			"claude-opus-* that choice is a guaranteed 502 on turn one")
	}

	e.scriptNext(answer("ok", 20, 4))
	e.send(e.wsA, s.conversationID, "oi")
	if got := e.llm.lastRequest.Temperature; got != nil {
		t.Fatalf("the turn asked for temperature=%v; the operator expressed "+
			"no preference and the provider must apply its own", *got)
	}
}

// An explicit choice survives creation untouched, including one this
// model will refuse. Refusing it is correct: it was asked for.
func TestAnExplicitTemperatureSurvivesCreation(t *testing.T) {
	e := newEnv(t)
	s := e.seedWith(e.wsA, map[string]any{"temperature": 0.5})

	got, set := e.agentTemperature(e.wsA, s.agentID)
	if !set || got != 0.5 {
		t.Fatalf("temperature = %v (set=%v), want 0.5", got, set)
	}
	e.scriptNext(answer("ok", 20, 4))
	e.send(e.wsA, s.conversationID, "oi")
	if v := e.llm.lastRequest.Temperature; v == nil || *v != 0.5 {
		t.Fatalf("the turn asked for %v, want the configured 0.5", v)
	}
}

// Zero is a preference, not an absence. A float32 with `omitempty` would
// eat it silently, and it is the most deterministic setting there is.
func TestZeroIsAPreferenceAndNotAnAbsence(t *testing.T) {
	e := newEnv(t)
	s := e.seedWith(e.wsA, map[string]any{"temperature": 0})

	got, set := e.agentTemperature(e.wsA, s.agentID)
	if !set || got != 0 {
		t.Fatalf("temperature = %v (set=%v), want a deliberate 0", got, set)
	}
	e.scriptNext(answer("ok", 20, 4))
	e.send(e.wsA, s.conversationID, "oi")
	if v := e.llm.lastRequest.Temperature; v == nil || *v != 0 {
		t.Fatalf("the turn asked for %v, want a deliberate 0", v)
	}
}

/* ── B · the three states of a PATCH ─────────────────────────────────── */

// absent → leave it alone; null → clear; number → set.
//
// The third state is what makes the fix usable rather than one-way: without
// it an agent given a temperature could never be returned to the state that
// works on every model.
func TestPatchTellsAbsentFromNullFromAValue(t *testing.T) {
	e := newEnv(t)
	s := e.seedWith(e.wsA, map[string]any{"temperature": 0.7})

	// A PATCH that does not mention it leaves it exactly as it was. This is
	// the assertion that keeps the new `null` meaning from leaking into
	// every other edit a client makes.
	rec := e.do("PATCH", "/chat/agents/"+s.agentID, e.wsA,
		map[string]any{"description": "sem falar de temperatura"})
	wantStatus(t, rec, http.StatusOK)
	if got, set := e.agentTemperature(e.wsA, s.agentID); !set || got != 0.7 {
		t.Fatalf("an unrelated edit changed temperature to %v (set=%v)", got, set)
	}

	// A number sets it.
	rec = e.do("PATCH", "/chat/agents/"+s.agentID, e.wsA,
		map[string]any{"temperature": 1})
	wantStatus(t, rec, http.StatusOK)
	if got, set := e.agentTemperature(e.wsA, s.agentID); !set || got != 1 {
		t.Fatalf("temperature = %v (set=%v), want 1", got, set)
	}

	// null clears it back to no preference.
	rec = e.do("PATCH", "/chat/agents/"+s.agentID, e.wsA,
		map[string]any{"temperature": nil})
	wantStatus(t, rec, http.StatusOK)
	if _, set := e.agentTemperature(e.wsA, s.agentID); set {
		t.Fatal("null did not clear the preference; an agent that has been " +
			"given a temperature can never get back to the provider's default")
	}

	// And the cleared agent really sends nothing.
	e.scriptNext(answer("ok", 20, 4))
	e.send(e.wsA, s.conversationID, "oi")
	if v := e.llm.lastRequest.Temperature; v != nil {
		t.Fatalf("a cleared agent still asked for temperature=%v", *v)
	}
}

// A malformed value is refused rather than read as a clear. "temperature":
// "quente" must not silently become "no preference".
func TestAMalformedTemperatureIsRefused(t *testing.T) {
	e := newEnv(t)
	s := e.seedWith(e.wsA, map[string]any{"temperature": 0.7})

	rec := e.do("PATCH", "/chat/agents/"+s.agentID, e.wsA,
		map[string]any{"temperature": "quente"})
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want the request refused", rec.Code)
	}
	if got, set := e.agentTemperature(e.wsA, s.agentID); !set || got != 0.7 {
		t.Fatalf("a refused request still changed the value to %v (set=%v)", got, set)
	}
}

// The range check still bounds an explicit value, and is still not a
// compatibility claim: 0.7 is inside it and claude-opus-4-7 refuses it.
func TestTheRangeCheckStillBoundsAnExplicitValue(t *testing.T) {
	e := newEnv(t)
	s := e.seedWith(e.wsA, map[string]any{"temperature": 1})

	rec := e.do("PATCH", "/chat/agents/"+s.agentID, e.wsA,
		map[string]any{"temperature": 5})
	if rec.Code == http.StatusOK {
		t.Fatal("temperature 5 was accepted")
	}
	if got, _ := e.agentTemperature(e.wsA, s.agentID); got != 1 {
		t.Fatalf("the refused request changed the value to %v", got)
	}
}

/* ── C · the auxiliary call follows the agent ────────────────────────── */

// Consolidation used to impose its own temperature, which is the same
// defect with a different author. An agent that expressed no preference
// must produce an auxiliary call that expresses none either.
func TestConsolidationSendsNoTemperatureWhenTheAgentChoseNone(t *testing.T) {
	e := newEnv(t)
	s := e.seedWith(e.wsA, map[string]any{"model": "claude-opus-4-7"})
	e.exchange(e.wsA, s.conversationID, "pergunta", "resposta")
	if e.llm.lastRequest.Temperature != nil {
		t.Fatal("fixture drifted: the agent should carry no preference")
	}

	e.proposes(`[]`, 100, 5)
	e.consolidate(e.wsA, s.conversationID, 0)

	if got := e.llm.lastRequest.Temperature; got != nil {
		t.Fatalf("consolidation asked for temperature=%v on an agent that "+
			"chose none; /lembrar would 502 on a model that chats perfectly", *got)
	}
}
