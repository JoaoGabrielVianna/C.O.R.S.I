// Package preflight answers one question before the server binds a port:
// is something already there, and if so, is it us?
//
// ── The failure this exists to prevent ─────────────────────────────────
// "Check the port is free" does not work, and the reason is specific
// enough to write down. `localhost` resolves to both 127.0.0.1 and ::1,
// and a listener may take one family without taking the other. Two
// different products can therefore hold :8080 SIMULTANEOUSLY, each binding
// successfully, neither logging anything wrong:
//
//	127.0.0.1:8080  →  another project's API
//	[::1]:8080      →  C.O.R.S.I.
//
// That is not hypothetical; it is the state this machine was found in. The
// frontend's dev proxy targets `localhost:8080`, so which backend it
// reached depended on the resolver, and the symptom was a scatter of 404s
// that read like missing features.
//
// So the check is not "can I bind". It is "who answers", asked on every
// address the name can resolve to, and answered by the service itself
// through the one field that cannot be mistaken for another product's:
// health.Application.
//
// ── What it deliberately does not do ───────────────────────────────────
// It never kills anything. A process this one did not start belongs to
// somebody else — possibly another developer's session, possibly another
// product — and terminating it to claim a port would be a far worse
// failure than refusing to start. It reports, and a person decides.
package preflight

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// SkipEnv disables the check entirely.
//
// It exists because a preflight that cannot be turned off becomes the thing
// standing between someone and a running process at the worst possible
// moment — a container with an odd network, a probe that hangs, a case
// nobody predicted. Turning it off is a deliberate act with an obvious
// name, which is the honest shape for an escape hatch.
const SkipEnv = "CORSI_SKIP_PORT_PREFLIGHT"

// probeTimeout bounds ONE probe. Short on purpose: everything being asked
// is on the loopback interface, where an answer takes microseconds and a
// non-answer is an immediate connection refused. This budget only matters
// for the pathological case of something accepting the connection and then
// saying nothing, and there the right outcome is to move on quickly rather
// than to hold up start-up.
const probeTimeout = 700 * time.Millisecond

// Occupant is what answered, if anything did.
type Occupant struct {
	// Address is the concrete host:port that answered, never the wildcard
	// form. "Something is on :8080" is not actionable; "something is on
	// 127.0.0.1:8080 while ::1 is free" is the whole diagnosis.
	Address string
	// Application is the value the service reported, empty when it answered
	// without identifying itself — which is itself the useful signal, since
	// every C.O.R.S.I. build since this package existed reports one.
	Application string
	// Body is a short excerpt, kept for the error message. An operator
	// staring at "something else is there" will immediately ask "what?", and
	// a few bytes of its answer usually names it.
	Body string
}

// IsCorsi reports whether this occupant identified itself as this product.
func (o Occupant) IsCorsi(application string) bool { return o.Application == application }

// Port inspects every address `addr` could bind and reports what it finds.
//
// `addr` is the server's listen address in Go's usual form: ":8080",
// "0.0.0.0:8080", "localhost:8080". A bare or wildcard host expands to both
// loopback families, because that is precisely the case a single probe
// misses.
//
// Returning an error means DO NOT START. A nil error with occupants means
// start, but say something first — see Guard.
func Port(ctx context.Context, addr, application string) ([]Occupant, error) {
	if os.Getenv(SkipEnv) != "" {
		return nil, nil
	}
	hosts, port, err := probeTargets(addr)
	if err != nil {
		// An address this package cannot parse is not a reason to refuse a
		// boot. The server itself is about to parse it and will fail with a
		// far better message than a preflight guessing.
		return nil, nil
	}

	var found []Occupant
	for _, host := range hosts {
		target := net.JoinHostPort(host, port)
		occ, ok := probe(ctx, target)
		if !ok {
			continue
		}
		occ.Address = target
		found = append(found, occ)
	}
	return found, nil
}

// probeTargets expands a listen address into the concrete addresses a
// client could reach it on.
//
// A wildcard or empty host becomes BOTH loopback families. That expansion
// is the entire value of this package: probing only 127.0.0.1 is what let
// a foreign service and this one coexist unnoticed.
func probeTargets(addr string) ([]string, string, error) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return nil, "", err
	}
	if port == "" {
		return nil, "", fmt.Errorf("no port in %q", addr)
	}
	switch host {
	case "", "0.0.0.0", "::", "[::]", "localhost":
		return []string{"127.0.0.1", "::1"}, port, nil
	default:
		return []string{host}, port, nil
	}
}

// probe asks one address who it is.
//
// `/health/live` and not `/health/ready`: liveness needs no database, so a
// C.O.R.S.I. instance whose Postgres is down still identifies itself rather
// than being mistaken for a foreign service. Anything that answers HTTP at
// all counts as an occupant, identified or not.
func probe(ctx context.Context, target string) (Occupant, bool) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+target+"/health/live", nil)
	if err != nil {
		return Occupant{}, false
	}
	// A dedicated client with no keep-alives: this runs once, at start-up,
	// and a pooled connection to a service we are about to refuse to share a
	// port with has nothing to be reused for.
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	resp, err := client.Do(req)
	if err != nil {
		// Connection refused is the ordinary, healthy answer: nobody is
		// there. It is not distinguished from a timeout because the action
		// is the same either way — proceed.
		return Occupant{}, false
	}
	defer func() { _ = resp.Body.Close() }()

	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	body := strings.TrimSpace(string(buf[:n]))

	var payload struct {
		Application string `json:"application"`
	}
	_ = json.Unmarshal([]byte(body), &payload)

	return Occupant{Application: payload.Application, Body: truncate(body, 160)}, true
}

// Guard is the decision, made once, in the words an operator needs.
//
// Two occupants, two very different outcomes:
//
//	a foreign service   → refuse. Starting anyway is how a frontend ends up
//	                      proxying to another product for an afternoon.
//	another C.O.R.S.I.  → warn and continue. It is a legitimate thing to do
//	                      on purpose, and it is also how two dev runtimes
//	                      end up writing to one database without either
//	                      knowing — so it is said out loud, every time.
//
// Note what makes the refusal safe to be strict about: it fires only when
// something ANSWERED and was not us. A free port produces no occupants at
// all, which is the overwhelmingly common case and costs one refused
// connection per family.
func Guard(ctx context.Context, addr, application string, log *slog.Logger) error {
	occupants, err := Port(ctx, addr, application)
	if err != nil || len(occupants) == 0 {
		return nil
	}

	var foreign []Occupant
	for _, o := range occupants {
		if o.IsCorsi(application) {
			log.Warn("another C.O.R.S.I. runtime is already serving this address",
				"address", o.Address,
				"detail", "both instances will use the same database; "+
					"stop the other one unless you meant to run two")
			continue
		}
		foreign = append(foreign, o)
	}
	if len(foreign) == 0 {
		return nil
	}

	var b strings.Builder
	b.WriteString("refusing to start: ")
	b.WriteString(addr)
	b.WriteString(" is already served by something that is not C.O.R.S.I.\n")
	for _, o := range foreign {
		b.WriteString("  ")
		b.WriteString(o.Address)
		b.WriteString(" → ")
		if o.Application == "" {
			b.WriteString("no application identity")
		} else {
			b.WriteString("application=" + o.Application)
		}
		if o.Body != "" {
			b.WriteString(" " + o.Body)
		}
		b.WriteString("\n")
	}
	b.WriteString("\nBinding anyway would let the dev proxy reach that service instead of this one,\n")
	b.WriteString("and its 404s would read as missing C.O.R.S.I. features.\n")
	b.WriteString("Stop it yourself, choose another port with HTTP_ADDR, or set ")
	b.WriteString(SkipEnv)
	b.WriteString("=1 to proceed anyway.")
	return fmt.Errorf("%s", b.String())
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
