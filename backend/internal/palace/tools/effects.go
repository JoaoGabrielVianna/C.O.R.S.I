package tools

import (
	chatdomain "github.com/corsi/backend/internal/chat/domain"
	chatports "github.com/corsi/backend/internal/chat/ports"
)

// Which entity each write touched, declared by the capability itself.
//
// ══════════════════════════════════════════════════════════════════════
//
//	RECEIPT PROVES EXECUTION
//	EFFECT REF IDENTIFIES THE EFFECT
//	READ PROVES STATE
//
// ══════════════════════════════════════════════════════════════════════
//
// ── Why this file exists ───────────────────────────────────────────────
// A turn created a Room and an Artifact, hit the round ceiling before it
// could add the items, and the continuation created a SECOND Room and a
// SECOND Artifact. The receipt was right — it said two creates had run —
// and useless for continuing, because every Palace capability is
// Confidential: the payloads are not kept and never replayed, so nothing
// could say WHICH room.
//
// ── Why each one is written out by hand ────────────────────────────────
// Because the alternative is a helper that reaches into any output and
// returns "the first thing that looks like an id", and that is exactly the
// inference this design refuses. A capability that returns three ids would
// silently report the wrong one, and nothing downstream could tell. Every
// implementation below names the ONE entity its call is about, and the ones
// that are about no single entity are absent from this file on purpose.
//
// ── Why only these ─────────────────────────────────────────────────────
// A ref exists to stop a continuation creating a second copy of something.
// So the capabilities that report one are the ones that CREATE: a
// continuation that knows `room:<uuid>` already exists will not make
// another.
//
// The updates are deliberately silent. They change a thing that already
// existed, so a resume repeating one is idempotent in the way that matters
// — it cannot produce a duplicate entity — and reporting a ref for them
// would widen the surface with no incident behind it. `relation.create` is
// silent for a different reason: its identity is the pair of endpoints it
// already carries, not a new uuid, and a ref for it would need this file to
// invent one. Widening any of that is a decision, not an oversight.
//
// ── The confidentiality boundary, restated ─────────────────────────────
// Every ref below is a type and a UUID that the domain layer generated.
// None is a title, a name, a body or anything a person typed. The domain
// refuses a ref whose id is not a UUID, so a mistake here fails closed:
// the ref is dropped, the resume reads instead, and nothing leaks.

// refFrom builds a ref from a nested object's id field.
//
// The nesting and the key are named by the CALLER — this only spares six
// identical nil checks. It never searches: given the wrong key it returns
// false, which is the safe answer.
func refFrom(out chatdomain.ToolOutput, object, idKey, entity string) (chatdomain.EffectRef, bool) {
	nested, ok := out[object].(map[string]any)
	if !ok {
		return chatdomain.EffectRef{}, false
	}
	id, ok := nested[idKey].(string)
	if !ok || id == "" {
		return chatdomain.EffectRef{}, false
	}
	ref := chatdomain.EffectRef{Type: entity, ID: id}
	if !ref.Valid() {
		return chatdomain.EffectRef{}, false
	}
	return ref, true
}

/* ── the capabilities that create ────────────────────────────────────── */

func (roomCreate) EffectRefOf(out chatdomain.ToolOutput) (chatdomain.EffectRef, bool) {
	return refFrom(out, "room", "room_id", "room")
}

func (artifactCreate) EffectRefOf(out chatdomain.ToolOutput) (chatdomain.EffectRef, bool) {
	return refFrom(out, "artifact", "artifact_id", "artifact")
}

func (memoryCreate) EffectRefOf(out chatdomain.ToolOutput) (chatdomain.EffectRef, bool) {
	return refFrom(out, "memory", "memory_id", "memory")
}

func (sourceCreate) EffectRefOf(out chatdomain.ToolOutput) (chatdomain.EffectRef, bool) {
	return refFrom(out, "source", "source_id", "source")
}

// itemAdd names the ITEM it created, not the list it created it on.
//
// The distinction is the whole point of a ref: a continuation that sees
// `item:<uuid>` knows that entry exists and does not add it twice. Reporting
// the artifact instead would say "something happened to this list", which is
// true and does not stop a duplicate.
func (itemAdd) EffectRefOf(out chatdomain.ToolOutput) (chatdomain.EffectRef, bool) {
	return refFrom(out, "item", "item_id", "item")
}

// Compile-time proof that each of these really satisfies the optional
// contract. Without it a renamed method would silently stop reporting, and
// the symptom would be a duplicate entity months later.
var (
	_ chatports.EffectReporter = roomCreate{}
	_ chatports.EffectReporter = artifactCreate{}
	_ chatports.EffectReporter = memoryCreate{}
	_ chatports.EffectReporter = sourceCreate{}
	_ chatports.EffectReporter = itemAdd{}
)
