package config

import (
	"bufio"
	"io"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/corsi/backend/internal/platform/identity"
)

// ══════════════════════════════════════════════════════════════════════
//
//	`.env.example` IS EXECUTED, NOT READ
//
// ══════════════════════════════════════════════════════════════════════
//
// `make run` loads the env file with `set -a; source $(ENV); set +a`, which
// means every line in it is a line of BASH. A value that is valid as data
// and invalid as shell does not fail loudly — it fails as a mutilated
// value, or as a file the shell tried to open, and the consequence shows up
// somewhere else entirely.
//
// Both defects this pins were real and both were found by a session that
// could not start the backend:
//
//   - `AUTH_PASSWORD_HASH=$argon2id$v=19$m=...` unquoted. The shell expanded
//     `$argon2id`, `$v` and `$m` to nothing and the server received
//     `=19=65536,t=3,p=2`. An unparseable verifier fails the BOOT, so the
//     symptom was "identity is broken" rather than "the example was copied
//     verbatim".
//
//   - `META_THREADS_REDIRECT_URI=https://<seu-host>/...` unquoted. `<` is
//     input redirection, so `source` aborted with
//     `seu-host: No such file or directory` — a failure attributed to
//     whatever variable came after it.
//
// The test loads the file the way the Makefile does, through a real bash,
// so what it verifies is the path an operator actually takes and not a
// parser written here to agree with it.
func envExamplePath(t *testing.T) string {
	t.Helper()
	// internal/platform/config → internal/platform → internal → backend
	return filepath.Join("..", "..", "..", ".env.example")
}

// sourceEnvExample loads the example through bash and returns the resulting
// environment, exactly as `make run` would produce it.
func sourceEnvExample(t *testing.T) map[string]string {
	t.Helper()

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash not available, and the loading path under test is bash: %v", err)
	}

	// `env -0` so a value containing a newline cannot be read as two
	// variables. The script mirrors the Makefile's `run` target.
	cmd := exec.Command(bash, "-c", `set -a; . "$1"; set +a; env -0`, "bash", envExamplePath(t))
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()

	// ── Why stderr is checked, and not just the exit status ────────────
	// Because `source` does NOT abort on a failed line. The `<seu-host>`
	// redirection defect printed `No such file or directory`, kept going,
	// and exited 0 — so an exit-status-only assertion watched the defect
	// happen and called it a pass. That mistake was made here first and is
	// the reason this reads two channels.
	if s := strings.TrimSpace(stderr.String()); s != "" {
		t.Fatalf("loading .env.example produced shell errors — every line in it is a\n"+
			"line of bash, and a value the shell cannot read is a value the operator\n"+
			"never gets:\n%s\n\nQuote the offending value with single quotes.", s)
	}
	if err != nil {
		t.Fatalf("sourcing .env.example failed: %v", err)
	}

	env := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(stdout.String()))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	sc.Split(splitNUL)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if ok {
			env[k] = v
		}
	}
	return env
}

func splitNUL(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for i, b := range data {
		if b == 0 {
			return i + 1, data[:i], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// TestEnvExampleSurvivesShellLoading is the regression for both defects.
//
// The assertion that matters is the last one: the verifier the shell
// produced is handed to the real `identity.New`, which is the same call the
// composition root makes. A hash that survives as a string but not as a
// credential would pass a string comparison and fail the boot.
func TestEnvExampleSurvivesShellLoading(t *testing.T) {
	env := sourceEnvExample(t)

	hash := env["AUTH_PASSWORD_HASH"]
	if hash == "" {
		t.Fatal("AUTH_PASSWORD_HASH is absent from the loaded environment")
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("AUTH_PASSWORD_HASH lost its `$` segments to shell expansion.\n"+
			"  got:  %q\n"+
			"  want: a PHC string starting with `$argon2id$`\n"+
			"Quote the value with SINGLE quotes in .env.example; double quotes still expand.",
			hash)
	}

	email := env["AUTH_EMAIL"]
	if email == "" {
		t.Fatal("AUTH_EMAIL is absent from the loaded environment")
	}

	// The end-to-end check: what the shell produced must build the real
	// service. `New` parses the verifier and rejects a malformed one, which
	// is precisely the boot failure this test exists to prevent.
	if _, err := identity.New(
		identity.Config{Email: email, PasswordHash: hash, SessionTTL: time.Hour},
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
	); err != nil {
		t.Fatalf("the credential in .env.example does not survive `make run`: %v", err)
	}
}

// TestEnvExampleCredentialIsExplicitlyForDevelopment keeps the operator's
// real password out of version control by making the example's identity
// obviously fake. A file that is safe to copy is a file somebody eventually
// pastes their own credential into.
func TestEnvExampleCredentialIsExplicitlyForDevelopment(t *testing.T) {
	env := sourceEnvExample(t)

	if got := env["AUTH_EMAIL"]; !strings.HasSuffix(got, "@corsi.local") {
		t.Errorf("AUTH_EMAIL = %q — the example must name a local development "+
			"address, never a real one", got)
	}
	// Production's answer is the absence of this variable. The example sets
	// it because the example describes localhost over plain http.
	if got := env["AUTH_COOKIE_INSECURE"]; got != "true" {
		t.Errorf("AUTH_COOKIE_INSECURE = %q, want \"true\" — the example is the "+
			"development configuration, and production gets Secure by setting nothing", got)
	}
}
