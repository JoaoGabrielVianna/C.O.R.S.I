package domain

import (
	"encoding/base64"
	"strings"

	"github.com/google/uuid"
)

/* ── what a button carries ───────────────────────────────────────────── */

// ══════════════════════════════════════════════════════════════════════
//
//	CALLBACK DATA IS A HINT, NEVER AN AUTHORIZATION
//
// ══════════════════════════════════════════════════════════════════════
//
// Telegram hands `callback_data` back verbatim from whatever the bot put
// on the button — and a client can send any bytes it likes to the Bot API,
// so what arrives is CLIENT-CONTROLLED. The rule this file exists to make
// structural:
//
//   - a callback never carries a workspace. The workspace comes from the
//     binding, which was written by an operator.
//   - a callback never carries a conversation. The conversation comes from
//     the binding's active agent, which is server state.
//   - an agent id in a callback is a PROPOSAL, checked against the agents
//     the bound workspace actually has before anything happens.
//
// So a forged callback can, at absolute most, name an agent the operator
// already owns — which is what the buttons offered anyway.
//
// ── Why base64 and not the UUID's own text ─────────────────────────────
// Telegram caps callback_data at 64 BYTES. A canonical UUID is 36, which
// fits once and not twice. Raw base64url of the 16-byte value is 22, which
// leaves room for a prefix and for a second field if one is ever needed.

// CallbackKind is the closed vocabulary of buttons this bot emits.
type CallbackKind string

const (
	// CallbackSelectAgent: "talk to this agent from now on".
	CallbackSelectAgent CallbackKind = "a"
	// CallbackResume: "continue the interrupted turn".
	//
	// It carries the interrupted MESSAGE id and nothing else. The
	// conversation is resolved server-side, and the Agents runtime refuses
	// any message that is not that conversation's last turn — so a forged
	// id cannot reach another thread even in principle. See
	// app.ResumeTurn/resumeTarget.
	CallbackResume CallbackKind = "r"
)

// Callback is a decoded button press.
type Callback struct {
	Kind CallbackKind
	ID   uuid.UUID
}

// EncodeCallback builds the `callback_data` for one button.
func EncodeCallback(kind CallbackKind, id uuid.UUID) string {
	return string(kind) + ":" + base64.RawURLEncoding.EncodeToString(id[:])
}

// DecodeCallback parses what came back, refusing anything it does not
// recognise.
//
// Deliberately strict and deliberately dumb: it decides whether the bytes
// are a well-formed button press, and nothing else. Whether the id names
// something the sender may touch is a question for the application layer,
// which is the only place that can see the binding.
func DecodeCallback(data string) (Callback, error) {
	kind, rest, ok := strings.Cut(data, ":")
	if !ok {
		return Callback{}, Invalid("unrecognised button")
	}
	k := CallbackKind(kind)
	switch k {
	case CallbackSelectAgent, CallbackResume:
	default:
		return Callback{}, Invalid("unrecognised button")
	}
	raw, err := base64.RawURLEncoding.DecodeString(rest)
	if err != nil || len(raw) != 16 {
		return Callback{}, Invalid("unrecognised button")
	}
	var id uuid.UUID
	copy(id[:], raw)
	return Callback{Kind: k, ID: id}, nil
}

// MaxCallbackBytes is Telegram's documented ceiling. Asserted in tests
// rather than trusted, because exceeding it is a silent failure: the Bot
// API rejects the whole keyboard and the message arrives with no buttons.
const MaxCallbackBytes = 64
