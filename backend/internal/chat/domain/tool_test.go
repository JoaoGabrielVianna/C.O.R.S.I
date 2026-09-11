package domain

import (
	"errors"
	"strings"
	"testing"
)

/* ── names ───────────────────────────────────────────────────────────── */

// The convention is normative and machine-checked, because the name is what
// `chat.agent_tools` stores, what the audit trail records, and what the
// provider is told. A name that varies by case or by separator is two names
// to a database and one to a person.
func TestToolNameConvention(t *testing.T) {
	valid := []string{
		"system.echo",
		"github.repository.read",
		"finance.transaction.create",
		"github.pull_request.comment.create",
		"a.b",
	}
	for _, n := range valid {
		if !ValidToolName(n) {
			t.Errorf("ValidToolName(%q) = false, want true", n)
		}
	}

	invalid := map[string]string{
		"":                             "empty",
		"echo":                         "a single segment names no owner",
		"a.b.c.d.e":                    "five segments is a path, not a name",
		"System.Echo":                  "uppercase",
		"system.echo.":                 "trailing separator leaves an empty segment",
		".system.echo":                 "leading separator",
		"system..echo":                 "empty middle segment",
		"system.1echo":                 "a segment starting with a digit",
		"system.ec-ho":                 "a dash is not in the character set",
		"system.ec ho":                 "a space is not in the character set",
		"system.ec__ho":                "a double underscore collides with the wire encoding",
		"sys__tem.echo":                "same, in the first segment",
		strings.Repeat("a.", 40) + "b": "longer than the protocol's 64-character ceiling",
	}
	for n, why := range invalid {
		if ValidToolName(n) {
			t.Errorf("ValidToolName(%q) = true, want false (%s)", n, why)
		}
	}
}

// The double-underscore rule is not style. The provider wire encoding maps
// `.` to `__`, and a name containing `__` would decode back to something
// else — which is how a call for one tool ends up executing another.
func TestDoubleUnderscoreWouldBreakTheWireEncoding(t *testing.T) {
	const ambiguous = "system.ec__ho"
	if ValidToolName(ambiguous) {
		t.Fatalf("%q must be rejected: encoding it gives system__ec__ho, "+
			"which decodes to system.ec.ho — a different tool", ambiguous)
	}
}

/* ── definitions ─────────────────────────────────────────────────────── */

func validDefinition() ToolDefinition {
	return ToolDefinition{
		Name:        "system.echo",
		Title:       "Echo",
		Description: "Returns the text it was given.",
		Effect:      EffectRead,
		Schema: ToolSchema{
			Properties: map[string]ToolProperty{
				"text": {Type: TypeString, MaxLength: 100},
			},
			Required: []string{"text"},
		},
	}
}

func TestToolDefinitionValidation(t *testing.T) {
	if err := validDefinition().Validate(); err != nil {
		t.Fatalf("a well-formed definition was rejected: %v", err)
	}

	cases := map[string]func(*ToolDefinition){
		"a bad name":            func(d *ToolDefinition) { d.Name = "Echo" },
		"no title":              func(d *ToolDefinition) { d.Title = "  " },
		"no description":        func(d *ToolDefinition) { d.Description = "" },
		"no effect":             func(d *ToolDefinition) { d.Effect = "" },
		"an unknown effect":     func(d *ToolDefinition) { d.Effect = "maybe" },
		"no properties":         func(d *ToolDefinition) { d.Schema.Properties = nil },
		"an unsupported type":   func(d *ToolDefinition) { d.Schema.Properties["text"] = ToolProperty{Type: "array"} },
		"required not declared": func(d *ToolDefinition) { d.Schema.Required = []string{"missing"} },
	}
	for name, mutate := range cases {
		d := validDefinition()
		mutate(&d)
		if err := d.Validate(); err == nil {
			t.Errorf("a definition with %s was accepted", name)
		}
	}
}

// The description is what the model reads to decide whether to call the
// tool. Without one it is called at random, so it is required rather than
// merely recommended.
func TestDescriptionIsRequiredBecauseTheModelReadsIt(t *testing.T) {
	d := validDefinition()
	d.Description = ""
	err := d.Validate()
	if err == nil {
		t.Fatal("a tool with no description was accepted")
	}
	if !strings.Contains(err.Error(), "description") {
		t.Fatalf("the error should name what is missing, got %q", err)
	}
}

/* ── argument validation ─────────────────────────────────────────────── */

func schema() ToolSchema {
	return ToolSchema{
		Properties: map[string]ToolProperty{
			"text":  {Type: TypeString, MaxLength: 10},
			"count": {Type: TypeInteger},
			"ratio": {Type: TypeNumber},
			"flag":  {Type: TypeBoolean},
		},
		Required: []string{"text"},
	}
}

func TestValidArgumentsDecode(t *testing.T) {
	args, err := schema().ValidateArguments(`{"text":"olá","count":3,"ratio":1.5,"flag":true}`)
	if err != nil {
		t.Fatalf("valid arguments were rejected: %v", err)
	}
	if args["text"] != "olá" {
		t.Errorf("text = %v", args["text"])
	}
	if args["count"] != int64(3) {
		t.Errorf("count = %#v, want int64(3)", args["count"])
	}
	if args["ratio"] != 1.5 {
		t.Errorf("ratio = %v", args["ratio"])
	}
	if args["flag"] != true {
		t.Errorf("flag = %v", args["flag"])
	}
}

// A no-argument call arrives as "" from some gateways and as "{}" from
// others. They are the same request.
func TestEmptyArgumentsAreAnEmptyObject(t *testing.T) {
	s := ToolSchema{Properties: map[string]ToolProperty{"text": {Type: TypeString}}}
	for _, raw := range []string{"", "  ", "{}"} {
		if _, err := s.ValidateArguments(raw); err != nil {
			t.Errorf("ValidateArguments(%q) = %v, want nil", raw, err)
		}
	}
}

func TestInvalidArgumentsAreRefusedWithAReason(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"not JSON at all", `{"text":`, "not a JSON object"},
		{"a JSON array", `["text"]`, "not a JSON object"},
		{"a JSON string", `"text"`, "not a JSON object"},
		{"a missing required field", `{"count":1}`, "text is required"},
		{"an unknown property", `{"text":"a","path":"/etc"}`, "unknown argument path"},
		{"a string where a number goes", `{"text":"a","count":"3"}`, "count must be a integer"},
		{"a float where an integer goes", `{"text":"a","count":3.5}`, "count must be a integer"},
		{"a number where a string goes", `{"text":42}`, "text must be a string"},
		{"a string where a boolean goes", `{"text":"a","flag":"yes"}`, "flag must be a boolean"},
		{"a string over its own limit", `{"text":"onze caracteres e mais"}`, "longer than 10 characters"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := schema().ValidateArguments(c.raw)
			if err == nil {
				t.Fatalf("ValidateArguments(%s) accepted it", c.raw)
			}
			var f *ToolFailure
			if !errors.As(err, &f) {
				t.Fatalf("error is %T, want *ToolFailure", err)
			}
			if f.Code != ToolErrInvalidArguments {
				t.Errorf("code = %q, want %q", f.Code, ToolErrInvalidArguments)
			}
			if !strings.Contains(f.Message, c.want) {
				t.Errorf("message = %q, want it to contain %q", f.Message, c.want)
			}
		})
	}
}

// Unknown properties are refused, not dropped. A model that invents `path`
// where the schema says `text` has misunderstood the contract, and running
// the tool on what remains would answer the wrong question and report
// success.
func TestUnknownPropertiesAreRefusedRatherThanIgnored(t *testing.T) {
	_, err := schema().ValidateArguments(`{"text":"a","path":"/etc/passwd"}`)
	if err == nil {
		t.Fatal("an unknown property was silently accepted")
	}
}

// The payload ceiling is checked BEFORE the JSON is parsed, so a hostile or
// runaway generation is refused without being decoded.
func TestOversizedArgumentsAreRefusedBeforeParsing(t *testing.T) {
	// Deliberately not valid JSON: if it were parsed, the error would say so.
	huge := strings.Repeat("x", MaxToolArgumentsBytes+1)
	_, err := schema().ValidateArguments(huge)
	if err == nil {
		t.Fatal("an oversized payload was accepted")
	}
	var f *ToolFailure
	if !errors.As(err, &f) || !strings.Contains(f.Message, "byte limit") {
		t.Fatalf("error = %v, want the size limit named", err)
	}
}

/* ── error semantics ─────────────────────────────────────────────────── */

// Five of the six failures go back to the model so it can correct itself or
// explain. The round limit does not: handing it back would invite exactly
// the loop it exists to stop.
func TestOnlyTheRoundLimitIsUnrecoverable(t *testing.T) {
	recoverable := []ToolErrorCode{
		ToolErrNotFound, ToolErrNotAuthorized, ToolErrInvalidArguments,
		ToolErrExecutionFailed, ToolErrTimeout,
	}
	for _, c := range recoverable {
		if !c.Recoverable() {
			t.Errorf("%s should be recoverable: the model can act on it", c)
		}
	}
	if ToolErrRoundLimit.Recoverable() {
		t.Error("tool_round_limit must be fatal to the turn")
	}
}
