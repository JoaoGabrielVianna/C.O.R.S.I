package config

import (
	"testing"
)

// The safety of the internal-tools switch is entirely in its default.
//
// Everything else about it is enforced by the registry, but the registry
// only ever sees the value this package produced — so a deployment that
// sets nothing must get `false`, and an `envDefault` typo would hand
// production a catalogue of diagnostics with no other test noticing.
//
// It reads the real struct tag through Load(), not a constant, so the thing
// under test is the thing that runs.
func TestInternalToolsAreOffUnlessAskedFor(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://ignored/ignored")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Modules.ChatInternalTools {
		t.Fatal("CHAT_ENABLE_INTERNAL_TOOLS defaults to true; a deployment that " +
			"configures nothing would offer diagnostic tools as capabilities")
	}
}

// And it is a real switch, not a constant somebody wired to false.
func TestInternalToolsCanBeTurnedOn(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://ignored/ignored")
	t.Setenv("CHAT_ENABLE_INTERNAL_TOOLS", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Modules.ChatInternalTools {
		t.Fatal("the env var was set and did not reach the config")
	}
}

// The other production-critical default, checked here for the same reason:
// a deployment that forgets it falls back to the dev sentinel, which is
// SEC-003. The value is false by design (zero-config local dev) and
// production is required to set it — this test exists so the default is a
// decision somebody can see rather than an accident.
func TestWorkspaceHeaderDefaultIsTheDocumentedOne(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://ignored/ignored")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Workspace.RequireHeader {
		t.Fatal("WORKSPACE_REQUIRE_HEADER now defaults to true; the docs and " +
			".env.example say it defaults to false and that production must set it")
	}
}

// Prompt caching is the first switch in this file whose safe default is ON.
//
// ── Why the polarity is inverted, and why it is tested ─────────────────
// Every other switch here defaults to the conservative value because the
// unsafe one costs correctness. This one is the opposite: caching changes
// how a provider BILLS a prefix and not what the model reads — proved on
// the bytes in adapters/llm/cache_control_test.go — so the conservative
// value is the CHEAP one, and a deployment that configures nothing should
// get it.
//
// The variable is therefore an opt-OUT, and its name says so. An
// `envDefault` typo here would make every deployment quietly pay full
// price for a prefix it never had to, which is exactly the kind of silent
// regression no other test would notice.
func TestPromptCachingIsOnUnlessTurnedOff(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://ignored/ignored")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Modules.ChatDisablePromptCache {
		t.Fatal("CHAT_DISABLE_PROMPT_CACHE defaults to true; a deployment that " +
			"configures nothing would pay full price for every stable prefix")
	}
}

// And the kill switch is a real switch. It exists because caching changes
// the shape of the body sent to a gateway this project does not control: if
// that gateway regresses, an operator has to be one environment variable
// away from the previous body, not one deploy.
func TestPromptCachingCanBeTurnedOff(t *testing.T) {
	t.Setenv("POSTGRES_DSN", "postgres://ignored/ignored")
	t.Setenv("CHAT_DISABLE_PROMPT_CACHE", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Modules.ChatDisablePromptCache {
		t.Fatal("the env var was set and did not reach the config")
	}
}
