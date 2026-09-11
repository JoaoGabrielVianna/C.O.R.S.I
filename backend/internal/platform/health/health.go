// Package health exposes liveness and readiness probes.
//
// ── Why readiness carries a build fingerprint ──────────────────────────
// Because a healthy answer from the wrong binary is worse than no answer.
// This project has already lost time to it once: a process listening on
// :8080 reported `{"status":"ok"}` while executing a build made before the
// changes under test, so "the capability is missing" and "the capability is
// not in THAT binary" were indistinguishable from the outside.
//
// The fields below are the ones this project can actually establish. They
// are deliberately not a release-versioning system, and nothing here is
// invented when it is unknown — see BuildInfo.
//
// ── Why the build fingerprint was not enough ───────────────────────────
// It answers "which binary", and every field of it — a path, an mtime, a
// start time, a Go version — is something ANY Go service would report in
// the same shape. It could tell two C.O.R.S.I. builds apart and could not
// tell C.O.R.S.I. from a different product entirely. `Application` is the
// field that closes that gap, and it is separate from the build block
// because it is a fact about the PRODUCT rather than about this compilation
// of it.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Application is the stable, verifiable answer to "which product is this?".
//
// ── Why a port number is not an identity ───────────────────────────────
// It was assumed to be one, and the assumption cost a whole investigation.
// A frontend proxying to `localhost:8080` reached another project's API,
// which answered `200 OK` on `/health/live` with `{"status":"alive"}`, and
// every C.O.R.S.I. route under it returned a plausible-looking 404. The
// symptom read as "the feature is missing", which is the most expensive
// wrong diagnosis available: it sends someone looking for a bug in code
// that was never executed.
//
// It gets worse than "someone took the port", and the worse case is the
// one actually observed. `localhost` resolves to both 127.0.0.1 and ::1,
// so TWO different applications can hold the same port at the same time,
// one per address family, with NEITHER failing to bind:
//
//	127.0.0.1:8080  →  another product
//	[::1]:8080      →  C.O.R.S.I.
//
// Which one a client reaches then depends on its resolver's ordering, and
// curl, Node and a browser do not have to agree. No amount of "check the
// port is free" catches that. Only asking the service what it is does.
//
// ── Why the value is a constant and not configuration ──────────────────
// Because a configurable identity is not an identity. If this could be set
// by an environment variable, then the failure being guarded against —
// a misconfigured environment — is exactly the situation in which it would
// be wrong.
//
// ── What this is NOT ───────────────────────────────────────────────────
// It is not authentication and must never be used as any. It is an
// unauthenticated string served to anyone who asks, and anything can copy
// it. It answers "am I pointed at the right service" for an operator and a
// dev tool; it does not answer "is this service trustworthy" for anybody.
// See platform/preflight, which uses it for exactly the first question.
const Application = "corsi"

// version and commit are injected at link time, e.g.
//
//	go build -ldflags="-X github.com/corsi/backend/internal/platform/health.version=1.2.0"
//
// ── Why they are empty by default and stay empty ───────────────────────
// The working tree this runs from is not a git repository, and the build
// does not stamp anything. There is therefore no honest value to default
// to: a hard-coded "0.1.0" would be a number that never changes while the
// binary does, which is precisely the failure mode the fingerprint exists
// to catch. Empty means unknown, and unknown is omitted from the response
// rather than reported as a plausible string.
var (
	version = ""
	commit  = ""
)

// startedAt is when this process began serving. Two probes returning
// different values is proof the process restarted between them.
var startedAt = time.Now().UTC()

// BuildInfo identifies the running binary.
//
// ── What each field can and cannot tell you ────────────────────────────
// Binary and BinaryModifiedAt are the ones that resolve the stale-process
// question, and they are the only ones always available: they come from the
// executable itself, so they cannot disagree with what is actually running.
// A binary whose mtime predates an edit did not include that edit.
//
// Version and Commit are omitted unless something injected them. An absent
// field says "this build carries no version metadata", which is true and
// checkable; a fabricated one would say something false and reassuring.
type BuildInfo struct {
	Version string `json:"version,omitempty"`
	Commit  string `json:"commit,omitempty"`
	// Binary is the path of the running executable. It is what catches a
	// process started from /tmp while the source tree sits elsewhere.
	Binary string `json:"binary"`
	// BinaryModifiedAt is that file's mtime, UTC. This is the field to
	// compare against the time of a build.
	BinaryModifiedAt *time.Time `json:"binary_modified_at"`
	StartedAt        time.Time  `json:"started_at"`
	GoVersion        string     `json:"go_version"`
}

// currentBuild reads what can be read, and reports what cannot as absent.
//
// Errors are not propagated: a readiness probe that failed because it could
// not stat its own executable would turn a diagnostic aid into an outage.
// An unreadable path yields empty strings and a nil timestamp, which the
// JSON then shows as null — an honest "not known", not a wrong answer.
func currentBuild() BuildInfo {
	b := BuildInfo{
		Version:   version,
		Commit:    commit,
		StartedAt: startedAt,
		GoVersion: runtime.Version(),
	}
	path, err := os.Executable()
	if err != nil {
		return b
	}
	b.Binary = path
	if fi, err := os.Stat(path); err == nil {
		mod := fi.ModTime().UTC()
		b.BinaryModifiedAt = &mod
	}
	return b
}

func Handler(pool *pgxpool.Pool) http.Handler {
	r := chi.NewRouter()
	r.Get("/live", live)
	r.Get("/ready", ready(pool))
	return r
}

// live stays a bare status. Liveness answers "should this process be
// restarted", and a fingerprint on it would invite orchestrators to parse
// a field whose only consumer is a human debugging a deploy.
// The identity rides on liveness as well as readiness, and that is the
// point: liveness is the cheapest probe, it needs no database, and it is
// therefore the one a dev tool can call on every start-up. A guard that
// required a healthy database to answer "which product are you" would go
// silent in exactly the degraded moment somebody is trying to diagnose.
func live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":      "ok",
		"application": Application,
	})
}

func ready(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		// The build is reported on the failure path too. "Which binary is
		// failing" is the first question asked about a database outage, and
		// withholding it exactly when something is wrong would be the least
		// useful possible choice.
		build := currentBuild()
		if err := pool.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status":      "db_unavailable",
				"application": Application,
				"build":       build,
			})
			return
		}
		// `status` keeps its exact previous value and position. The
		// EasyPanel probe reads that field and nothing else, so adding a
		// sibling is additive: an old consumer sees no change.
		writeJSON(w, http.StatusOK, map[string]any{
			"status":      "ok",
			"application": Application,
			"build":       build,
		})
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
