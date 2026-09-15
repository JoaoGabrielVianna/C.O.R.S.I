package llm

import (
	"testing"

	"github.com/corsi/backend/internal/chat/ports"
)

// Temperature on the wire — S8B, part B.
//
// ══════════════════════════════════════════════════════════════════════
//
//	A PARAMETER THE OPERATOR DID NOT CHOOSE IS NOT SENT
//
// ══════════════════════════════════════════════════════════════════════
//
// ── What was measured, against the real gateway ────────────────────────
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
// "claude-opus-4-7 does not support temperature=0.7. Only temperature=1 is
// supported."
//
// The constraint is per-model and VALUE-EXACT, not a range, and the two
// families disagree about the same number. So there is no value this
// package could send on the operator's behalf that is right everywhere —
// which is why, when they chose nothing, it sends nothing.

// The regression this file exists for.
//
// The platform used to default the column to 0.7, so an agent created
// without an explicit choice on claude-opus-* produced a 502 on its FIRST
// turn, before it had said anything. Put any default back and the key
// reappears here.
func TestNoTemperatureKeyWhenTheOperatorChoseNone(t *testing.T) {
	body := marshalRequest(t, ports.CompletionRequest{
		Model: "claude-opus-4-7", MaxTokens: 16,
	})
	if _, present := body["temperature"]; present {
		t.Fatalf("the request carries temperature=%v for an agent that chose "+
			"none; that exact body is what the gateway answers 400 to",
			body["temperature"])
	}
}

// An explicit preference is sent exactly as given, including one the model
// will refuse. Refusing it is correct: it is what was asked for, and the
// gateway names the problem precisely.
func TestAnExplicitTemperatureIsSentUnchanged(t *testing.T) {
	for _, want := range []float32{0, 0.5, 0.7, 1, 2} {
		body := marshalRequest(t, ports.CompletionRequest{
			Model: "claude-haiku-4-5-20251001", MaxTokens: 16,
			Temperature: floatPtr(want),
		})
		got, present := body["temperature"]
		if !present {
			t.Fatalf("temperature %v was dropped; the operator chose it", want)
		}
		// Compared as float32, which is the width the field actually has.
		// Widening the expectation instead would compare 0.7 against
		// 0.6999999880790710 and fail on the test's own arithmetic.
		if float32(got.(float64)) != want {
			t.Fatalf("temperature = %v, want %v: nothing here may normalise "+
				"what the operator configured", got, want)
		}
	}
}

// Zero is the case a naive `omitempty` on a float32 would silently eat, and
// it is a temperature somebody may genuinely want: haiku accepts it and it
// is the most deterministic setting there is.
//
// It is a separate test from the loop above because the failure it guards
// is a one-word change — `*float32` back to `float32` — that the other
// assertions would still pass.
func TestADeliberateZeroIsNotMistakenForAnAbsence(t *testing.T) {
	body := marshalRequest(t, ports.CompletionRequest{
		Model: "claude-haiku-4-5-20251001", MaxTokens: 16,
		Temperature: floatPtr(0),
	})
	got, present := body["temperature"]
	if !present {
		t.Fatal("a deliberate temperature=0 was dropped as though it were absent")
	}
	if got.(float64) != 0 {
		t.Fatalf("temperature = %v, want 0", got)
	}
}
