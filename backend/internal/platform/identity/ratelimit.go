package identity

import (
	"sync"
	"time"
)

// ── What this is for, and what it is not ───────────────────────────────
//
// It bounds ONLINE guessing against the login endpoint. It is not a defence
// against an attacker who already has AUTH_PASSWORD_HASH — that is what the
// argon2id cost parameters are for — and it is not a general API rate
// limiter, which this product does not have and does not need with one
// user.
//
// In memory, and deliberately so. A shared counter would need Redis or a
// table, and a restart clearing the window is an acceptable weakness for a
// single-replica deployment: the attacker's gain is one extra burst per
// restart, and restarts are not attacker-triggerable from outside.

const (
	// maxAttempts before a key is refused.
	maxAttempts = 5
	// attemptWindow is how long a key's failures are remembered.
	attemptWindow = 15 * time.Minute
	// maxTrackedKeys bounds memory. A flood from spoofed sources must not
	// turn a rate limiter into the thing that exhausts the host, so past
	// this size the oldest windows are dropped — which degrades toward
	// allowing, not toward denying, because denying everyone is the
	// attacker's goal and not ours.
	maxTrackedKeys = 4096
)

type attemptWindowState struct {
	count int
	first time.Time
}

type rateLimiter struct {
	mu   sync.Mutex
	keys map[string]attemptWindowState
	now  func() time.Time
}

func newRateLimiter(now func() time.Time) *rateLimiter {
	if now == nil {
		now = time.Now
	}
	return &rateLimiter{keys: make(map[string]attemptWindowState), now: now}
}

// allow reports whether `key` may attempt a login right now. It does not
// count the attempt; see fail.
func (r *rateLimiter) allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.keys[key]
	if !ok {
		return true
	}
	if r.now().Sub(st.first) >= attemptWindow {
		delete(r.keys, key)
		return true
	}
	return st.count < maxAttempts
}

// fail records a failed attempt.
//
// Only failures count. A correct login costs nothing, so the operator
// logging in from a new device twenty times in a row is never locked out by
// their own success.
func (r *rateLimiter) fail(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	st, ok := r.keys[key]
	if !ok || now.Sub(st.first) >= attemptWindow {
		if len(r.keys) >= maxTrackedKeys {
			r.evictStaleLocked(now)
		}
		r.keys[key] = attemptWindowState{count: 1, first: now}
		return
	}
	st.count++
	r.keys[key] = st
}

// succeed clears a key's window, so a successful login ends any penalty the
// operator's own typos accumulated.
func (r *rateLimiter) succeed(key string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.keys, key)
}

func (r *rateLimiter) evictStaleLocked(now time.Time) {
	for k, st := range r.keys {
		if now.Sub(st.first) >= attemptWindow {
			delete(r.keys, k)
		}
	}
	// Still full of live windows: drop arbitrary entries rather than grow.
	// Map iteration order is unspecified, which is fine — there is no
	// fairness property to preserve among attackers.
	for k := range r.keys {
		if len(r.keys) < maxTrackedKeys {
			break
		}
		delete(r.keys, k)
	}
}
