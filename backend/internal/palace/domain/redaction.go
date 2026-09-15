package domain

// Redaction: what happens when something tries to render a Palace entity
// without saying which fields it means.
//
// ══════════════════════════════════════════════════════════════════════
//
//	EVERY CONTENT-BEARING ENTITY HERE REDACTS ITSELF BY DEFAULT
//
// ══════════════════════════════════════════════════════════════════════
//
// ── The leak this closes ───────────────────────────────────────────────
// The requirement on this context is that highly sensitive content never
// reaches an audit payload, a log, a trace or an error message. Three of
// those four are covered by rules: `ToolDefinition.Confidential` drops
// the audit payload, and errors.go forbids quoting content in a message.
// Logs and traces are covered by nothing, because they are covered by
// discipline: one `s.log.Info("saved", "memory", m)` written by somebody
// in a hurry puts a memory's full text into stdout, and from there into
// whatever collects it.
//
// Discipline does not survive contact with a repository this size. So the
// entities make the careless thing safe instead:
//
//	slog with a TextHandler  → formats with %v, which honours String()
//	slog with a JSONHandler  → marshals, which honours MarshalJSON()
//	fmt.Sprintf("%v", m)     → String()
//	json.Marshal(m)          → MarshalJSON()
//
// All four produce an identity and nothing else.
//
// ── Why the receivers are values ───────────────────────────────────────
// Because `json.Marshal(m)` and `json.Marshal(&m)` must both redact, and
// a method set on the pointer alone covers only the second. A value copy
// of a memory is still that memory's text.
//
// ── What this deliberately does NOT do ─────────────────────────────────
// It does not make the content unreachable. Every field stays exported,
// the repository scans into them and the tools read them to build the map
// they mean to return. The rule this enforces is narrow and is the one
// that matters: a surface that wants to expose a field has to NAME it.
// Nothing leaves this context by being handed to a generic encoder.
//
// ── Why Relation has none of this ──────────────────────────────────────
// It carries no free text. Two ids, two type words and a kind word, all
// of them vocabulary. There is nothing in a Relation to withhold, and
// adding a redacted encoding would cost the one thing a debug line is
// for without protecting anything.

import (
	"encoding/json"

	"github.com/google/uuid"
)

// redactedView is what every redacted encoding produces. One shape, so a
// reader that meets one of these recognises all of them.
type redactedView struct {
	Type string    `json:"type"`
	ID   uuid.UUID `json:"id"`
	// Redacted is stated rather than implied. A reader seeing an object
	// with only an id has to guess whether the content was withheld or
	// was never there, and those are different facts.
	Redacted bool `json:"redacted"`
}

func redact(kind string, id uuid.UUID) ([]byte, error) {
	return json.Marshal(redactedView{Type: kind, ID: id, Redacted: true})
}

func describe(kind string, id uuid.UUID) string {
	return kind + "(" + id.String() + ", redacted)"
}

const (
	typeRoom     = "palace.room"
	typeArtifact = "palace.artifact"
	typeItem     = "palace.artifact_item"
	typeMemory   = "palace.memory"
	typeSource   = "palace.source"
	typeSession  = "palace.session"
)

func (r Room) String() string               { return describe(typeRoom, r.ID) }
func (r Room) MarshalJSON() ([]byte, error) { return redact(typeRoom, r.ID) }

func (a Artifact) String() string               { return describe(typeArtifact, a.ID) }
func (a Artifact) MarshalJSON() ([]byte, error) { return redact(typeArtifact, a.ID) }

func (i ArtifactItem) String() string               { return describe(typeItem, i.ID) }
func (i ArtifactItem) MarshalJSON() ([]byte, error) { return redact(typeItem, i.ID) }

func (m Memory) String() string               { return describe(typeMemory, m.ID) }
func (m Memory) MarshalJSON() ([]byte, error) { return redact(typeMemory, m.ID) }

func (s Source) String() string               { return describe(typeSource, s.ID) }
func (s Source) MarshalJSON() ([]byte, error) { return redact(typeSource, s.ID) }

// A session carries one free-text field, its summary, and that summary is
// an account of a stretch of the operator's work. Same treatment.
func (s Session) String() string               { return describe(typeSession, s.ID) }
func (s Session) MarshalJSON() ([]byte, error) { return redact(typeSession, s.ID) }
