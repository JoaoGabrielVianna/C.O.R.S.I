package domain

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestCallbackDataFitsTelegramsLimit.
//
// Exceeding 64 bytes is a SILENT failure at the product level: the Bot API
// rejects the whole `reply_markup`, so the message arrives with no buttons
// at all and nothing in the logs says why the agent list stopped being
// tappable. Asserting the ceiling is how that stays impossible rather than
// unlikely.
func TestCallbackDataFitsTelegramsLimit(t *testing.T) {
	for _, kind := range []CallbackKind{CallbackSelectAgent, CallbackResume} {
		for i := 0; i < 200; i++ {
			data := EncodeCallback(kind, uuid.New())
			if len(data) > MaxCallbackBytes {
				t.Fatalf("callback %q is %d bytes, over telegram's %d",
					data, len(data), MaxCallbackBytes)
			}
		}
	}
	// The canonical UUID text is what a naive encoding would use. Stated
	// here so the reason for base64 survives somebody "simplifying" it:
	// two of those plus separators would not fit, and one barely does.
	if len("r:"+uuid.New().String()+":"+uuid.New().String()) <= MaxCallbackBytes {
		t.Fatal("this assertion is stale; canonical uuids now fit and the comment is wrong")
	}
}

func TestCallbackRoundTrips(t *testing.T) {
	for _, kind := range []CallbackKind{CallbackSelectAgent, CallbackResume} {
		id := uuid.New()
		got, err := DecodeCallback(EncodeCallback(kind, id))
		if err != nil {
			t.Fatalf("DecodeCallback: %v", err)
		}
		if got.Kind != kind || got.ID != id {
			t.Fatalf("round trip = %+v, want kind %q id %s", got, kind, id)
		}
	}
}

// TestDecodeRefusesEverythingItDoesNotRecognise.
//
// `callback_data` is CLIENT-CONTROLLED — a Bot API client can send any
// bytes it likes. The decoder is the first thing those bytes meet, so it
// refuses rather than tolerates. Note what none of these can do even in
// principle: name a workspace or a conversation. Neither has a field.
func TestDecodeRefusesEverythingItDoesNotRecognise(t *testing.T) {
	id := uuid.New()
	valid := EncodeCallback(CallbackSelectAgent, id)

	bad := map[string]string{
		"empty":                 "",
		"no separator":          "aXYZ",
		"unknown kind":          "z:" + strings.SplitN(valid, ":", 2)[1],
		"kind only":             "a:",
		"not base64":            "a:!!!!!!!!!!!!!!!!!!!!!!",
		"right length wrong id": "a:" + strings.Repeat("A", 21),
		"too many bytes":        "a:" + strings.Repeat("A", 44),
		"sql-ish":               "a:'; DROP TABLE telegram.bindings; --",
		"path traversal":        "a:../../etc/passwd",
		"a whole uuid":          "a:" + id.String(),
	}
	for name, data := range bad {
		if _, err := DecodeCallback(data); err == nil {
			t.Errorf("%s: DecodeCallback(%q) was accepted; it must be refused", name, data)
		}
	}
}

// TestDecodeRefusalSaysNothing.
//
// The refusal message is identical for every malformed input. A decoder
// that explained which part was wrong would be a probing oracle for
// whoever was sending the bytes.
func TestDecodeRefusalSaysNothing(t *testing.T) {
	_, a := DecodeCallback("zzz")
	_, b := DecodeCallback("a:!!!!")
	if a == nil || b == nil {
		t.Fatal("both inputs must be refused")
	}
	if a.Error() != b.Error() {
		t.Fatalf("refusals differ (%q vs %q); that is an oracle", a, b)
	}
}
