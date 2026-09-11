package health

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The identity is the field a dev tool keys on, so it is asserted as a
// contract rather than left to whatever the handler happens to emit.

func TestLivenessNamesTheApplication(t *testing.T) {
	rec := httptest.NewRecorder()
	live(rec, httptest.NewRequest(http.MethodGet, "/health/live", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["application"] != Application {
		t.Errorf("application = %v, want %q", body["application"], Application)
	}
	// `status` keeps its exact previous value. The EasyPanel probe reads
	// that field and nothing else, so this addition has to be invisible to
	// it — a rename here would be an outage in a deploy, not a test failure.
	if body["status"] != "ok" {
		t.Errorf("status = %v, want \"ok\"; the probe contract changed", body["status"])
	}
}

// A liveness answer must not need a database. It is the probe a dev guard
// calls on every start-up, and one that went silent when Postgres was down
// would disappear in exactly the degraded moment somebody is diagnosing.
func TestLivenessNeedsNoDatabase(t *testing.T) {
	// live takes no pool at all; this compiles only while that stays true.
	var probe func(http.ResponseWriter, *http.Request) = live
	rec := httptest.NewRecorder()
	probe(rec, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

// The identity is a compile-time constant on purpose: a configurable one
// would be wrong in precisely the misconfigured environment it exists to
// detect.
func TestApplicationIsAStableConstant(t *testing.T) {
	if Application != "corsi" {
		t.Fatalf("Application = %q; dev tooling and preflight both key on this "+
			"exact string, so changing it is a coordinated change, not a rename",
			Application)
	}
}
