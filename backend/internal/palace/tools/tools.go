// Package tools exposes Palace as capabilities an agent can be
// authorized to use.
//
// ── Why this package imports the Agents module's ports ─────────────────
// It implements `chat/ports.Tool`, an interface Agents DECLARES and this
// package SATISFIES. That is dependency inversion, and it is the same
// arrangement GitHub, Job Radar, Threads and Finance already use. Nothing
// here can observe which agent is calling, which conversation it belongs
// to, or whether the caller is an agent at all; and nothing in Agents can
// name Palace. There is no `if agent.name == "Palace"` in this repository
// and there must never be one: capability comes from a grant, and a grant
// is a row an operator can see and revoke.
//
// ── Why it calls the application layer and not the repository ──────────
// Because the rules a write must pass live in `app`: both ends of a
// relation are resolved there, the sensitivity floor is applied there,
// `AcceptsItems` is checked there, and an entry is addressed by workspace
// AND artifact there. A tool holding a repository would be a second
// writer with its own idea of what each of those means, and the first
// thing it would get wrong is the one thing no foreign key can catch,
// because `palace.relations` has none. There is no SQL in this package
// and no way to reach any from it.
//
// ══════════════════════════════════════════════════════════════════════
//
//	EVERY CAPABILITY HERE IS Confidential. NONE IS External
//
// ══════════════════════════════════════════════════════════════════════
//
// Confidential, because this context holds the operator's own record: the
// decisions, the reflections, the transcripts behind them. The flag drops
// `arguments` and `result` from `chat.tool_calls`, which is the same
// posture Finance takes over money, applied to something at least as
// private.
//
// The cost is real and worth naming: a redacted result cannot feed
// evidence continuity on later turns, so an agent re-reads through a
// capability instead of reusing what it saw. For a context whose whole
// job is recall that is a genuine expense, and it is the right side of
// the trade.
//
// Not External, because every one of these reads OUR database. A Palace
// read can never make a turn VERIFIED_EXTERNAL_READ, and that is the
// point: the receipt exists to say when a claim rests on a system this
// product does not own, and none of these do.
//
// ── Presentation is not decided here ───────────────────────────────────
// Every output below is a flat map of semantic fields: an id, a word, a
// count, a timestamp. There is no markdown, no card shape, no colour and
// no component name. Web draws a panel from this, a text channel draws a
// line, and neither is in the contract.
//
// ── Entities are never serialised ──────────────────────────────────────
// Nothing here hands a Room, an Artifact, a Memory, a Source or a Session
// to an encoder. Every output is built field by field, which is exactly
// what the entities' redaction is for: see domain/redaction.go. A surface
// that wants to expose a field has to name it, and this package is the
// surface.
package tools

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	chatports "github.com/corsi/backend/internal/chat/ports"
	"github.com/corsi/backend/internal/palace/app"
	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/platform/workspace"
)

// New builds every Palace tool, ready for chat.Deps.Tools.
//
// Returned as the port type rather than as concrete values: the
// composition root's only job is to hand them over, and a caller that
// could reach a concrete method could reach past the contract.
func New(svc *app.Service) []chatports.Tool {
	b := base{svc: svc}
	return []chatports.Tool{
		roomList{b}, roomCreate{b}, roomUpdate{b},
		artifactList{b}, artifactGet{b}, artifactCreate{b}, artifactUpdate{b},
		itemAdd{b}, itemUpdate{b}, itemRemove{b},
		memoryList{b}, memoryGet{b}, memoryCreate{b}, memoryUpdate{b},
		sourceCreate{b}, sourceGet{b}, memoryLinkSource{b},
		relationCreate{b}, relationList{b}, relationRemove{b},
		sessionGet{b}, sessionStart{b}, sessionFocus{b}, sessionClose{b},
	}
}

type base struct{ svc *app.Service }

/* ── the tool names ──────────────────────────────────────────────────── */

// The twenty-four capabilities, named once.
//
// Exported and constant because more than one thing depends on each
// string being exactly this: the tool's own Definition, the agent
// blueprint in agent.go, and the tests. Two literals would be a rename
// that goes half-done.
const (
	RoomListTool   chatdomain.ToolName = "palace.room.list"
	RoomCreateTool chatdomain.ToolName = "palace.room.create"
	RoomUpdateTool chatdomain.ToolName = "palace.room.update"

	ArtifactListTool   chatdomain.ToolName = "palace.artifact.list"
	ArtifactGetTool    chatdomain.ToolName = "palace.artifact.get"
	ArtifactCreateTool chatdomain.ToolName = "palace.artifact.create"
	ArtifactUpdateTool chatdomain.ToolName = "palace.artifact.update"

	ItemAddTool    chatdomain.ToolName = "palace.artifact.item.add"
	ItemUpdateTool chatdomain.ToolName = "palace.artifact.item.update"
	ItemRemoveTool chatdomain.ToolName = "palace.artifact.item.remove"

	MemoryListTool   chatdomain.ToolName = "palace.memory.list"
	MemoryGetTool    chatdomain.ToolName = "palace.memory.get"
	MemoryCreateTool chatdomain.ToolName = "palace.memory.create"
	MemoryUpdateTool chatdomain.ToolName = "palace.memory.update"

	SourceCreateTool chatdomain.ToolName = "palace.source.create"
	SourceGetTool    chatdomain.ToolName = "palace.source.get"

	// MemoryLinkSourceTool cites evidence for a record that already
	// exists. Named under `memory` rather than under `source` because the
	// thing it changes is what a MEMORY rests on; the source is untouched
	// by it, and a name is an ownership statement.
	MemoryLinkSourceTool chatdomain.ToolName = "palace.memory.link_source"

	RelationCreateTool chatdomain.ToolName = "palace.relation.create"
	RelationListTool   chatdomain.ToolName = "palace.relation.list"
	RelationRemoveTool chatdomain.ToolName = "palace.relation.remove"

	SessionGetTool   chatdomain.ToolName = "palace.session.get"
	SessionStartTool chatdomain.ToolName = "palace.session.start"
	SessionFocusTool chatdomain.ToolName = "palace.session.focus"
	SessionCloseTool chatdomain.ToolName = "palace.session.close"
)

/* ── the workspace ───────────────────────────────────────────────────── */

// workspaceOf reads the workspace this call belongs to.
//
// ══════════════════════════════════════════════════════════════════════
//
//	NO TOOL IN THIS PACKAGE TAKES A WORKSPACE ARGUMENT
//
// ══════════════════════════════════════════════════════════════════════
//
// Not an optional one, not one that defaults. There is no property named
// workspace in any schema below, so the model has no way to express one
// and no way to be wrong about it. The value comes from the context the
// platform middleware stamped, unbroken from the request through the SSE
// handler and the turn loop into this executor.
//
// A missing one is a refusal, never a default. Guessing would mean
// reading, or rewriting, another workspace's record, and there is no
// fallback value that is not somebody's real life.
func workspaceOf(ctx context.Context) (uuid.UUID, error) {
	ws, ok := workspace.FromContext(ctx)
	if !ok || ws == uuid.Nil {
		return uuid.Nil, chatdomain.ToolError(chatdomain.ToolErrExecutionFailed,
			"this call arrived without a workspace, so no Palace data could be resolved")
	}
	return ws, nil
}

/* ── error translation ───────────────────────────────────────────────── */

// toolError turns a domain error into one the model can act on.
//
// The mapping is the whole reason the domain has a Kind: a missing room
// must reach the model as something it can respond to by listing and
// retrying, not as `internal_error`, which teaches it only that the tool
// is broken.
//
// The domain's own message is carried through, and that is safe by
// construction rather than by luck: every message this context produces
// names a field, a limit, a closed vocabulary or an id, and never quotes
// stored content. See domain/errors.go, which states the rule, and
// domain/privacy_test.go, which enforces it.
func toolError(err error) error {
	var de *domain.Error
	if !errAs(err, &de) {
		return chatdomain.ToolError(chatdomain.ToolErrExecutionFailed, err.Error())
	}
	switch de.Kind {
	case domain.KindInvalid, domain.KindNotFound:
		// Both are facts about the request the model can fix by itself:
		// correct the kind, or look the id up again.
		return chatdomain.ToolError(chatdomain.ToolErrInvalidArguments, de.Message)
	default:
		return chatdomain.ToolError(chatdomain.ToolErrExecutionFailed, de.Message)
	}
}

func errAs(err error, target **domain.Error) bool {
	for err != nil {
		if de, ok := err.(*domain.Error); ok {
			*target = de
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

/* ── shared shaping ──────────────────────────────────────────────────── */

const timeFormat = "2006-01-02T15:04:05Z"

func stamp(t time.Time) string { return t.UTC().Format(timeFormat) }

func stampPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return stamp(*t)
}

func idPtr(id *uuid.UUID) any {
	if id == nil {
		return nil
	}
	return id.String()
}

// excerptRunes bounds the preview a listing carries per row.
//
// ── Why a listing carries any content at all ───────────────────────────
// Because disambiguation is the job: three memories whose titles would be
// identical are told apart by their first sentence, and a model resolving
// "aquela decisão sobre reunião" has to see the difference before it asks.
//
// ── Why it is 160 and not the whole thing ──────────────────────────────
// Every character is a prompt token on the next provider call, times the
// number of rows. 160 is about a sentence, and a model that needs the
// text is one call away from `get`.
const excerptRunes = 160

// excerpt returns the opening of some text, collapsed to one line, and
// whether it had to cut.
func excerpt(text string) (string, bool) {
	flat := strings.Join(strings.Fields(text), " ")
	if flat == "" {
		return "", false
	}
	runes := []rune(flat)
	if len(runes) <= excerptRunes {
		return flat, false
	}
	return strings.TrimSpace(string(runes[:excerptRunes])) + "…", true
}

// withExcerpt adds a bounded preview under `key`, saying when it cut.
//
// An excerpt that does not declare itself is indistinguishable from a
// very short record, and a model that mistook one for the other would
// rewrite a long memory from its first sentence.
func withExcerpt(m map[string]any, key, text string) {
	if ex, truncated := excerpt(text); ex != "" {
		m[key] = ex
		m[key+"_truncated"] = truncated
	}
}

// maxToolResultBytes bounds what these tools hand back.
//
// Below the Agents ceiling on purpose, and for the reason the GitHub and
// Threads tools give: Agents refuses an oversized result as an EXECUTION
// FAILURE, which the model reads as "the tool is broken" and answers by
// apologising. A tool that bounds itself returns a smaller honest answer
// instead.
const maxToolResultBytes = 24 << 10

// fit shrinks a listing until it fits, and says so when it had to.
func fit(out map[string]any, listKey string) (chatdomain.ToolOutput, error) {
	if size(out) <= maxToolResultBytes {
		return out, nil
	}
	items, _ := out[listKey].([]map[string]any)
	if listKey == "" || items == nil {
		return nil, chatdomain.ToolError(chatdomain.ToolErrExecutionFailed,
			"the result was larger than one tool call may carry; ask for something narrower")
	}
	kept := len(items)
	for kept > 0 {
		kept /= 2
		out[listKey] = items[:kept]
		out["truncated"] = true
		out["truncated_note"] = "the full result did not fit in one tool call; only the " +
			"first entries are shown. Narrow the request and try again."
		if size(out) <= maxToolResultBytes {
			return out, nil
		}
	}
	return nil, chatdomain.ToolError(chatdomain.ToolErrExecutionFailed,
		"even a single result was larger than one tool call may carry")
}

func size(v any) int {
	raw, err := json.Marshal(v)
	if err != nil {
		return maxToolResultBytes + 1
	}
	return len(raw)
}

/* ── shared schema pieces ────────────────────────────────────────────── */

// vocabulary renders a closed list into a property description, so the
// model is told the same words the parser enforces. Written from the enum
// rather than by hand: a value added later reaches the schema the day it
// is added.
func vocabulary(prefix string, names []string) string {
	return prefix + " One of: " + strings.Join(names, ", ") + "."
}

// idProperty is the shape every id argument shares.
//
// ── Why the model is told where the id comes from ──────────────────────
// Because the alternative is a model that invents a plausible uuid.
// Saying it must come from a listing turns "I do not have one" into "call
// list first", which is a recoverable state, rather than into a
// fabricated argument that resolves to nothing.
func idProperty(what, from string) chatdomain.ToolProperty {
	return chatdomain.ToolProperty{
		Type: chatdomain.TypeString,
		Description: "The " + what + "'s id, exactly as returned by " + from +
			". Never invent one: if you do not have an id, list first.",
		MaxLength: 36,
	}
}

var includeHighlySensitiveProperty = chatdomain.ToolProperty{
	Type: chatdomain.TypeBoolean,
	Description: "Optional, defaults to false. Listings show normal and private " +
		"records; set this to true ONLY when the user explicitly asked for the " +
		"most sensitive ones. Do not set it routinely.",
}

var limitProperty = chatdomain.ToolProperty{
	Type:        chatdomain.TypeInteger,
	Description: "How many to return, 1 to 100. Defaults to 25.",
}

/* ── argument readers ────────────────────────────────────────────────── */

// The comma-ok form is kept even though the schema already validated the
// types: "safe because somewhere else checked" is exactly the assumption
// that stops being true after a refactor.
func argString(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

// argStringPtr distinguishes an argument that was SENT from one that was
// not, which is the whole grammar of every update below.
func argStringPtr(args map[string]any, key string) *string {
	raw, present := args[key]
	if !present {
		return nil
	}
	v, ok := raw.(string)
	if !ok {
		return nil
	}
	return &v
}

func argBool(args map[string]any, key string) bool {
	v, _ := args[key].(bool)
	return v
}

func argBoolPtr(args map[string]any, key string) *bool {
	raw, present := args[key]
	if !present {
		return nil
	}
	v, ok := raw.(bool)
	if !ok {
		return nil
	}
	return &v
}

func argInt(args map[string]any, key string, fallback int) int {
	switch n := args[key].(type) {
	case int64:
		return int(n)
	case int:
		return n
	case float64:
		return int(n)
	default:
		return fallback
	}
}

func argIntPtr(args map[string]any, key string) *int {
	if _, present := args[key]; !present {
		return nil
	}
	v := argInt(args, key, 0)
	return &v
}

// parseID turns the model's string into a uuid.
//
// A malformed id is an invalid argument rather than a not-found, because
// they are different mistakes: the first means the model sent something
// that is not an id at all, and telling it "not found" would send it
// looking for a record instead of fixing its argument.
func parseID(raw, field string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return uuid.Nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			field+" is not a valid id; use an id returned by a Palace listing")
	}
	return id, nil
}

/* ── the three-state reference argument ──────────────────────────────── */

// optionalRefArg turns a flat pair of arguments into the domain's
// three-state edit.
//
// ══════════════════════════════════════════════════════════════════════
//
//	absent        leave it alone
//	<field>       point it at this
//	detach_<x>    let go of it
//
// ══════════════════════════════════════════════════════════════════════
//
// ── Why the wire shape is a pair when the domain refuses one ───────────
// OptionalRef exists precisely because a pointer beside a boolean admits
// a fourth, meaningless state: both set. The domain makes that
// unrepresentable, and a tool schema cannot: it is a flat object of
// scalars, so three states have to arrive as two properties.
//
// The contradiction is therefore refused HERE, at the boundary, and what
// travels inward is a legal OptionalRef that cannot express it. That is
// the translation a boundary is for, and it is the opposite of smuggling
// a nested object through as a string.
func optionalRefArg(args map[string]any, field, detachField string) (domain.OptionalRef, error) {
	raw := argStringPtr(args, field)
	detach := argBool(args, detachField)

	switch {
	case raw != nil && detach:
		return domain.KeepRef(), chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			"send either "+field+" or "+detachField+", not both: one points at something and "+
				"the other lets go of it")
	case detach:
		return domain.ClearRef(), nil
	case raw != nil:
		id, err := parseID(*raw, field)
		if err != nil {
			return domain.KeepRef(), err
		}
		return domain.SetRef(id), nil
	default:
		return domain.KeepRef(), nil
	}
}

func detachProperty(what string) chatdomain.ToolProperty {
	return chatdomain.ToolProperty{
		Type: chatdomain.TypeBoolean,
		Description: "Optional. Set to true to detach the " + what +
			" entirely. Do not send it together with the id.",
	}
}
