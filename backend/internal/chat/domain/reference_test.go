package domain

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The reference vocabulary, without a database.
//
// What is worth pinning here is not the field list — it is that the four
// refusals stay told apart, and that a reference cannot attach itself to a
// row it does not belong on.

func TestReferenceKindIsClosed(t *testing.T) {
	// The two families that exist. `tool` is what a record is written in;
	// `integration` is what the composer sends and the service expands.
	for _, k := range []ReferenceKind{ReferenceKindTool, ReferenceKindIntegration} {
		if !k.Valid() {
			t.Fatalf("%q must be a valid kind", k)
		}
	}
	// Every name a client might plausibly guess for a family nobody built.
	// Each one has to be refused as an unknown kind rather than silently
	// accepted and scoped against nothing.
	for _, k := range []ReferenceKind{"", "source", "memory", "agent", "Tool", "TOOL", "Integration"} {
		if k.Valid() {
			t.Fatalf("kind %q must not be valid: only families that exist may be selected", k)
		}
	}
}

func TestReferenceRefusalsAreToldApart(t *testing.T) {
	cases := []struct {
		name string
		ref  TurnReference
		code string
	}{
		{"unknown kind", TurnReference{Kind: "source", ID: "x"}, CodeReferenceKindUnknown},
		{"no id", TurnReference{Kind: ReferenceKindTool, ID: "  "}, CodeReferenceInvalid},
		{"label too long", TurnReference{
			Kind: ReferenceKindTool, ID: "system.echo", Label: strings.Repeat("a", 200),
		}, CodeReferenceInvalid},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.ref.Validate()
			var de *Error
			if !errors.As(err, &de) {
				t.Fatalf("err = %v, want a domain error", err)
			}
			if de.Kind != KindInvalid {
				// 400, always. A malformed selection is a bad request, never
				// an upstream failure and never a conflict with stored state.
				t.Fatalf("kind = %q, want %q", de.Kind, KindInvalid)
			}
			if de.Code != c.code {
				t.Fatalf("code = %q, want %q", de.Code, c.code)
			}
			if de.Message == "" {
				t.Fatalf("a refusal must say what is wrong")
			}
		})
	}
}

func TestTurnReferencesAreBounded(t *testing.T) {
	ok := make([]TurnReference, MaxTurnReferences)
	for i := range ok {
		ok[i] = TurnReference{Kind: ReferenceKindTool, ID: "system.echo"}
	}
	if err := ValidateTurnReferences(ok); err != nil {
		t.Fatalf("exactly the ceiling must be allowed: %v", err)
	}
	if err := ValidateTurnReferences(append(ok, ok[0])); err == nil {
		t.Fatalf("one past the ceiling must be refused")
	}
}

// An absent selection and an empty one are the same input, everywhere. The
// whole backward-compatibility story rests on them never being told apart.
func TestNoSelectionAndEmptySelectionAreTheSame(t *testing.T) {
	if err := ValidateTurnReferences(nil); err != nil {
		t.Fatalf("nil is a valid selection (there was none): %v", err)
	}
	if err := ValidateTurnReferences([]TurnReference{}); err != nil {
		t.Fatalf("empty is a valid selection (there was none): %v", err)
	}
}

// A selection is something a person did. An assistant row carrying one
// would read as the model having chosen its own capabilities, which is the
// one claim this feature must never be able to make.
func TestOnlyAUserTurnMayCarryReferences(t *testing.T) {
	base := func(role Role) *Message {
		return &Message{
			WorkspaceID:    uuid.New(),
			ConversationID: uuid.New(),
			Role:           role,
			Content:        "oi",
			References:     []TurnReference{{Kind: ReferenceKindTool, ID: "system.echo", Label: "Echo"}},
		}
	}
	if err := base(RoleUser).Validate(); err != nil {
		t.Fatalf("a user turn may carry references: %v", err)
	}
	if err := base(RoleAssistant).Validate(); err == nil {
		t.Fatalf("an assistant turn must not carry references")
	}
}
