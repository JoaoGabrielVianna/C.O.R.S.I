package preflight

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// serveOn starts a real HTTP server on one loopback family and returns its
// port. Real sockets rather than a fake: the whole subject of this package
// is what happens at the network layer, and a stub would test the parsing
// and skip the part that was actually wrong.
func serveOn(t *testing.T, host string, body map[string]string) string {
	t.Helper()
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Skipf("cannot listen on %s: %v", host, err)
	}
	srv := &httptest.Server{
		Listener: ln,
		Config: &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(body)
		})},
	}
	srv.Start()
	t.Cleanup(srv.Close)
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	return port
}

/* ── the address-family trap ─────────────────────────────────────────── */

// THE case this package exists for. A foreign service on 127.0.0.1 and a
// free ::1 is a port that "can be bound" and must still refuse: whichever
// address the dev proxy's resolver picks decides which product it talks to.
func TestAForeignServiceOnOneFamilyIsFound(t *testing.T) {
	port := serveOn(t, "127.0.0.1", map[string]string{"status": "alive"})

	occ, err := Port(context.Background(), ":"+port, "corsi")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if len(occ) != 1 {
		t.Fatalf("occupants = %+v, want exactly the IPv4 one", occ)
	}
	if occ[0].IsCorsi("corsi") {
		t.Errorf("a service answering %q was taken for C.O.R.S.I.", "alive")
	}
	if !strings.Contains(occ[0].Address, "127.0.0.1") {
		t.Errorf("address = %q, want the concrete family that answered", occ[0].Address)
	}

	err = Guard(context.Background(), ":"+port, "corsi", quietLog())
	if err == nil {
		t.Fatal("Guard allowed a boot onto a port held by another product")
	}
	for _, want := range []string{"refusing to start", "127.0.0.1", "HTTP_ADDR"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q:\n%s", want, err)
		}
	}
}

// A wildcard address must be expanded to BOTH families. Probing only
// 127.0.0.1 is exactly the shortcut that let two products share :8080.
func TestAWildcardAddressProbesBothFamilies(t *testing.T) {
	hosts, port, err := probeTargets(":8080")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if port != "8080" {
		t.Errorf("port = %q", port)
	}
	if len(hosts) != 2 || hosts[0] != "127.0.0.1" || hosts[1] != "::1" {
		t.Fatalf("hosts = %v, want both loopback families", hosts)
	}
	for _, addr := range []string{"0.0.0.0:8080", "::0", "localhost:8080"} {
		if addr == "::0" {
			continue // not a host:port form; covered by the explicit list below
		}
		h, _, err := probeTargets(addr)
		if err != nil || len(h) != 2 {
			t.Errorf("%s expanded to %v (err %v), want both families", addr, h, err)
		}
	}
}

// An explicit host is taken at its word: someone who wrote 127.0.0.1 meant
// that one, and probing ::1 as well would refuse boots over a service the
// server was never going to reach.
func TestAnExplicitHostIsNotExpanded(t *testing.T) {
	hosts, _, err := probeTargets("127.0.0.1:8080")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(hosts) != 1 || hosts[0] != "127.0.0.1" {
		t.Fatalf("hosts = %v, want only the address given", hosts)
	}
}

/* ── the two outcomes ────────────────────────────────────────────────── */

// Another C.O.R.S.I. is a legitimate thing to be doing on purpose, so it
// warns and proceeds. It is also how two dev runtimes end up sharing one
// database without either knowing, which is why it is never silent.
func TestAnotherCorsiWarnsButDoesNotRefuse(t *testing.T) {
	port := serveOn(t, "127.0.0.1", map[string]string{"status": "ok", "application": "corsi"})

	occ, _ := Port(context.Background(), ":"+port, "corsi")
	if len(occ) != 1 || !occ[0].IsCorsi("corsi") {
		t.Fatalf("occupants = %+v, want one recognised C.O.R.S.I.", occ)
	}
	if err := Guard(context.Background(), ":"+port, "corsi", quietLog()); err != nil {
		t.Fatalf("Guard refused a boot beside another C.O.R.S.I.: %v", err)
	}
}

// A service that answers without identifying itself is foreign until it
// says otherwise. Deny by default: every C.O.R.S.I. build that has this
// package also reports the field, so silence means something else.
func TestAnUnidentifiedServiceIsTreatedAsForeign(t *testing.T) {
	port := serveOn(t, "127.0.0.1", map[string]string{"status": "ok"})
	err := Guard(context.Background(), ":"+port, "corsi", quietLog())
	if err == nil {
		t.Fatal("a service with no application identity was allowed")
	}
	if !strings.Contains(err.Error(), "no application identity") {
		t.Errorf("the refusal does not say what was missing:\n%s", err)
	}
}

// A free port is the common case and must cost nothing but a refused
// connection per family.
func TestAFreePortHasNoOccupants(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close() // now certainly free

	occ, err := Port(context.Background(), ":"+port, "corsi")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if len(occ) != 0 {
		t.Fatalf("occupants = %+v on a closed port", occ)
	}
	if err := Guard(context.Background(), ":"+port, "corsi", quietLog()); err != nil {
		t.Fatalf("Guard refused a free port: %v", err)
	}
}

// The escape hatch works, and is the only way past a foreign occupant.
func TestTheSkipEnvDisablesTheCheck(t *testing.T) {
	port := serveOn(t, "127.0.0.1", map[string]string{"status": "alive"})
	t.Setenv(SkipEnv, "1")

	if err := Guard(context.Background(), ":"+port, "corsi", quietLog()); err != nil {
		t.Fatalf("%s did not disable the check: %v", SkipEnv, err)
	}
}

// An address this package cannot parse is not its business to reject: the
// server is about to parse it and will say something better.
func TestAnUnparseableAddressDoesNotRefuseTheBoot(t *testing.T) {
	if err := Guard(context.Background(), "not-an-address", "corsi", quietLog()); err != nil {
		t.Fatalf("Guard refused over an address it could not parse: %v", err)
	}
}
