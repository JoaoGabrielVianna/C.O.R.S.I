package tools

import (
	"context"
	"strings"
	"time"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/palace/app"
	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
)

/* ── shaping ─────────────────────────────────────────────────────────── */

func memorySummary(m *domain.Memory) map[string]any {
	out := map[string]any{
		"memory_id":   m.ID.String(),
		"kind":        string(m.Kind),
		"importance":  m.Importance,
		"confidence":  string(m.Confidence),
		"status":      string(m.Status),
		"sensitivity": string(m.Sensitivity),
		"occurred_at": stampPtr(m.OccurredAt),
		"room_id":     idPtr(m.RoomID),
		"artifact_id": idPtr(m.ArtifactID),
		"updated_at":  stamp(m.UpdatedAt),
	}
	// The summary when there is one, an excerpt of the content otherwise.
	// A listing has to be readable without carrying every sentence.
	if s := strings.TrimSpace(m.Summary); s != "" {
		out["summary"] = m.Summary
	} else {
		withExcerpt(out, "content", m.Content)
	}
	return out
}

func memoryDetail(m *domain.Memory) map[string]any {
	return map[string]any{
		"memory_id":   m.ID.String(),
		"kind":        string(m.Kind),
		"content":     m.Content,
		"summary":     m.Summary,
		"importance":  m.Importance,
		"confidence":  string(m.Confidence),
		"status":      string(m.Status),
		"sensitivity": string(m.Sensitivity),
		"occurred_at": stampPtr(m.OccurredAt),
		"room_id":     idPtr(m.RoomID),
		"artifact_id": idPtr(m.ArtifactID),
		"created_at":  stamp(m.CreatedAt),
		"updated_at":  stamp(m.UpdatedAt),
	}
}

/* ── the date ────────────────────────────────────────────────────────── */

const occurredAtLayout = "2006-01-02"

// parseOccurredAt reads the day something happened.
//
// ── Why a date and not an instant ──────────────────────────────────────
// Because the caller knows "isso foi em março", not the minute. A field
// that demanded an instant would be answered with a fabricated one.
//
// ── Why UTC midnight, said out loud ────────────────────────────────────
// Palace has no reporting timezone and no period contract: nothing here
// buckets on this field, sorts a month by it or totals across it, so the
// instant inside the day carries no meaning. Midnight UTC is therefore a
// representation choice rather than an answer to a question, and it is
// documented so that the day somebody DOES bucket on it, they find this
// note instead of a silent three-hour skew. Finance, which does bucket,
// resolves periods in FINANCE_TIMEZONE for exactly that reason.
func parseOccurredAt(raw string) (time.Time, error) {
	t, err := time.ParseInLocation(occurredAtLayout, strings.TrimSpace(raw), time.UTC)
	if err != nil {
		return time.Time{}, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			"occurred_at must be a date like 2026-03-14. Omit it when you do not know when it happened; "+
				"never guess a date")
	}
	return t, nil
}

var occurredAtProperty = chatdomain.ToolProperty{
	Type: chatdomain.TypeString,
	Description: "Optional. The day the thing this memory is ABOUT happened, as " +
		"2026-03-14. Not the day you are writing it down. Omit it when the user did " +
		"not say, and never guess: plenty of knowledge has no date.",
	MaxLength: 10,
}

/* ── palace.memory.list ──────────────────────────────────────────────── */

type memoryList struct{ base }

func (memoryList) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         MemoryListTool,
		Title:        "Palace · Listar conhecimento",
		Effect:       chatdomain.EffectRead,
		Confidential: true,
		Description: "Lists what the user knows and asked to keep: facts, preferences, " +
			"ideas, decisions, learnings and reflections. THIS IS THE FIRST THING TO CALL " +
			"when the user asks what they decided, what they prefer, or what they know " +
			"about something. Answering from the conversation alone would mean answering " +
			"from what happens to be in front of you rather than from what they actually " +
			"kept. Filter rather than listing everything.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"kind": {
					Type:        chatdomain.TypeString,
					Description: vocabulary("Optional. Only knowledge of this sort.", domain.MemoryKindNames()),
					MaxLength:   20,
				},
				"status": {
					Type:        chatdomain.TypeString,
					Description: vocabulary("Optional. Only records in this state.", domain.LifecycleNames()),
					MaxLength:   20,
				},
				"room_id": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Only what is filed in this area.",
					MaxLength:   36,
				},
				"min_importance": {
					Type: chatdomain.TypeInteger,
					Description: "Optional, 1 to 5. A floor, not a sort: the listing stays in " +
						"recency order and this narrows it to what matters at least this much.",
				},
				"search": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Matches the content or the summary.",
					MaxLength:   200,
				},
				"include_highly_sensitive": includeHighlySensitiveProperty,
				"limit":                    limitProperty,
			},
		},
	}
}

func (t memoryList) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}

	filter := ports.MemoryFilter{
		MinImportance: argInt(args, "min_importance", 0),
		Search:        argString(args, "search"),
		Sensitive:     ports.Sensitive{IncludeHighlySensitive: argBool(args, "include_highly_sensitive")},
		Page:          ports.Page{Limit: argInt(args, "limit", 0)},
	}
	if raw := strings.TrimSpace(argString(args, "kind")); raw != "" {
		kind, err := domain.ParseMemoryKind(raw)
		if err != nil {
			return nil, toolError(err)
		}
		filter.Kind = &kind
	}
	if raw := strings.TrimSpace(argString(args, "status")); raw != "" {
		status, err := domain.ParseLifecycle(raw)
		if err != nil {
			return nil, toolError(err)
		}
		filter.Status = &status
	}
	if raw := strings.TrimSpace(argString(args, "room_id")); raw != "" {
		id, err := parseID(raw, "room_id")
		if err != nil {
			return nil, err
		}
		filter.RoomID = &id
	}

	memories, total, err := t.svc.ListMemories(ctx, ws, filter)
	if err != nil {
		return nil, toolError(err)
	}

	rows := make([]map[string]any, 0, len(memories))
	for _, m := range memories {
		rows = append(rows, memorySummary(m))
	}

	out := map[string]any{"memories": rows, "matching_total": total}
	if len(rows) == 0 {
		out["note"] = "nothing matched. Try a broader filter, or call this with no arguments. " +
			"An empty result means the user has not kept anything about this, which is a " +
			"real answer: say so rather than answering from the conversation."
	}
	return fit(out, "memories")
}

/* ── palace.memory.get ───────────────────────────────────────────────── */

type memoryGet struct{ base }

func (memoryGet) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         MemoryGetTool,
		Title:        "Palace · Ver conhecimento",
		Effect:       chatdomain.EffectRead,
		Confidential: true,
		Description: "Reads one record in full, together with the EVIDENCE it rests on: " +
			"which sources were cited for it, and when they were captured. This is how " +
			"you answer \"por que eu acho isso\" and how you check whether a belief has " +
			"anything behind it. A record with no evidence is an ordinary and honest " +
			"state; it does not mean the evidence is missing, it means none was cited.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"memory_id": idProperty("record", string(MemoryListTool)+" or "+string(MemoryCreateTool)),
			},
			Required: []string{"memory_id"},
		},
	}
}

func (t memoryGet) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(argString(args, "memory_id"), "memory_id")
	if err != nil {
		return nil, err
	}

	memory, err := t.svc.GetMemory(ctx, ws, id)
	if err != nil {
		return nil, toolError(err)
	}

	// ── Why provenance comes back from `get` ───────────────────────────
	// Because there is no provenance listing capability, and evidence
	// that could be recorded but never read would be a record nobody can
	// consult. `get` is where a record states itself in full, and what it
	// rests on is part of that.
	//
	// The sources arrive as identity plus an excerpt, not in full: the
	// whole text is one call away through palace.source.get, and carrying
	// every transcript into every read would put the material back in
	// front of the model on a question that only asked what is behind a
	// belief.
	sources, err := t.svc.SourcesFor(ctx, ws, id)
	if err != nil {
		return nil, toolError(err)
	}
	rows := make([]map[string]any, 0, len(sources))
	for _, s := range sources {
		row := map[string]any{
			"source_id":   s.ID.String(),
			"kind":        string(s.Kind),
			"captured_at": stamp(s.CapturedAt),
			"sensitivity": string(s.Sensitivity),
		}
		if ref := strings.TrimSpace(s.ExternalRef); ref != "" {
			row["external_ref"] = s.ExternalRef
		}
		withExcerpt(row, "content", s.Content)
		rows = append(rows, row)
	}

	out := map[string]any{
		"memory":   memoryDetail(memory),
		"evidence": rows,
		// Stated rather than implied. An absent key would leave the model
		// guessing whether nothing was cited or nothing was returned.
		"evidence_count": len(rows),
	}
	if len(rows) == 0 {
		out["evidence_note"] = "no evidence was ever cited for this record. That is an " +
			"ordinary state and not a gap to fill: do not invent a source for it."
	}
	return fit(out, "evidence")
}

/* ── palace.memory.create ────────────────────────────────────────────── */

type memoryCreate struct{ base }

func (memoryCreate) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         MemoryCreateTool,
		Title:        "Palace · Guardar conhecimento",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Keeps one thing the user knows: a fact, a preference, an idea, a " +
			"decision, a learning or a reflection. Use it when they ASK you to remember " +
			"something (\"anota isso\", \"guarda que eu decidi X\") or when what they said " +
			"plainly is an instruction to keep it. Do NOT write a record because a " +
			"conversation touched on something you found interesting: this is the user's " +
			"own record of their life, and filling it with things they never chose to " +
			"keep makes the things they did choose worth less. Write the content in their " +
			"words, not as a summary of your own.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"kind": {
					Type:        chatdomain.TypeString,
					Description: vocabulary("What sort of knowledge this is.", domain.MemoryKindNames()),
					MaxLength:   20,
				},
				"content": {
					Type: chatdomain.TypeString,
					Description: "What is being kept, in the user's own words where possible. " +
						"This IS the record; everything else describes it.",
					MaxLength: domain.MaxMemoryContent,
				},
				"summary": {
					Type:        chatdomain.TypeString,
					Description: "Optional. One line for listings, when the content is long.",
					MaxLength:   domain.MaxMemorySummary,
				},
				"importance": {
					Type:        chatdomain.TypeInteger,
					Description: "Optional, 1 to 5, defaults to 3. How much this matters.",
				},
				"confidence": {
					Type: chatdomain.TypeString,
					Description: vocabulary("Optional, defaults to medium. How sure the user is.",
						domain.ConfidenceNames()),
					MaxLength: 10,
				},
				"occurred_at": occurredAtProperty,
				"room_id": {
					Type:        chatdomain.TypeString,
					Description: "Optional. File it in this area.",
					MaxLength:   36,
				},
				"artifact_id": {
					Type: chatdomain.TypeString,
					Description: "Optional. Say which object this is ABOUT. It does not make the " +
						"record as withheld as that object; sensitivity is stated here.",
					MaxLength: 36,
				},
				"sensitivity": {
					Type: chatdomain.TypeString,
					Description: vocabulary("Optional, defaults to normal. Use private or "+
						"highly_sensitive when the user signals that this is not casual material.",
						domain.SensitivityNames()),
					MaxLength: 20,
				},
			},
			Required: []string{"kind", "content"},
		},
	}
}

func (t memoryCreate) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}

	kind, err := domain.ParseMemoryKind(argString(args, "kind"))
	if err != nil {
		return nil, toolError(err)
	}

	in := app.CreateMemoryInput{
		Kind:       kind,
		Content:    argString(args, "content"),
		Summary:    argString(args, "summary"),
		Importance: argIntPtr(args, "importance"),
	}
	if raw := strings.TrimSpace(argString(args, "confidence")); raw != "" {
		c, err := domain.ParseConfidence(raw)
		if err != nil {
			return nil, toolError(err)
		}
		in.Confidence = &c
	}
	if raw := strings.TrimSpace(argString(args, "occurred_at")); raw != "" {
		when, err := parseOccurredAt(raw)
		if err != nil {
			return nil, err
		}
		in.OccurredAt = &when
	}
	if raw := strings.TrimSpace(argString(args, "room_id")); raw != "" {
		id, err := parseID(raw, "room_id")
		if err != nil {
			return nil, err
		}
		in.RoomID = &id
	}
	if raw := strings.TrimSpace(argString(args, "artifact_id")); raw != "" {
		id, err := parseID(raw, "artifact_id")
		if err != nil {
			return nil, err
		}
		in.ArtifactID = &id
	}
	if raw := strings.TrimSpace(argString(args, "sensitivity")); raw != "" {
		level, err := domain.ParseSensitivity(raw)
		if err != nil {
			return nil, toolError(err)
		}
		in.Sensitivity = &level
	}

	memory, err := t.svc.CreateMemory(ctx, ws, in)
	if err != nil {
		return nil, toolError(err)
	}
	return fit(map[string]any{"memory": memoryDetail(memory), "created": true}, "")
}

/* ── palace.memory.update ────────────────────────────────────────────── */

type memoryUpdate struct{ base }

func (memoryUpdate) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         MemoryUpdateTool,
		Title:        "Palace · Atualizar conhecimento",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Corrects or re-files one record: its text, its kind, how much it " +
			"matters, how sure the user is, where it is filed, how withheld it is, or " +
			"whether it is archived. Send only what you are changing. Two rules will " +
			"refuse you, and neither is a malfunction: a record that is the origin of a " +
			"`decision_for` relation must stay a decision until that relation is removed, " +
			"and a record cannot be made less withheld than the evidence cited for it.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"memory_id": idProperty("record", string(MemoryListTool)),
				"kind": {
					Type:        chatdomain.TypeString,
					Description: vocabulary("Optional. Re-classify it.", domain.MemoryKindNames()),
					MaxLength:   20,
				},
				"content": {
					Type:        chatdomain.TypeString,
					Description: "Optional. The corrected text.",
					MaxLength:   domain.MaxMemoryContent,
				},
				"summary": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Send an empty string to clear it.",
					MaxLength:   domain.MaxMemorySummary,
				},
				"importance": {
					Type:        chatdomain.TypeInteger,
					Description: "Optional, 1 to 5.",
				},
				"confidence": {
					Type:        chatdomain.TypeString,
					Description: vocabulary("Optional.", domain.ConfidenceNames()),
					MaxLength:   10,
				},
				"occurred_at":       occurredAtProperty,
				"clear_occurred_at": detachProperty("date"),
				"status": {
					Type:        chatdomain.TypeString,
					Description: vocabulary("Optional. Archive or restore the record.", domain.LifecycleNames()),
					MaxLength:   20,
				},
				"sensitivity": {
					Type:        chatdomain.TypeString,
					Description: vocabulary("Optional.", domain.SensitivityNames()),
					MaxLength:   20,
				},
				"room_id": {
					Type:        chatdomain.TypeString,
					Description: "Optional. File it in this area.",
					MaxLength:   36,
				},
				"detach_room": detachProperty("area"),
				"artifact_id": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Say which object this is about.",
					MaxLength:   36,
				},
				"detach_artifact": detachProperty("object"),
			},
			Required: []string{"memory_id"},
		},
	}
}

func (t memoryUpdate) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(argString(args, "memory_id"), "memory_id")
	if err != nil {
		return nil, err
	}

	room, err := optionalRefArg(args, "room_id", "detach_room")
	if err != nil {
		return nil, err
	}
	artifact, err := optionalRefArg(args, "artifact_id", "detach_artifact")
	if err != nil {
		return nil, err
	}

	change := domain.MemoryChange{
		Content:    argStringPtr(args, "content"),
		Summary:    argStringPtr(args, "summary"),
		Importance: argIntPtr(args, "importance"),
		Room:       room,
		Artifact:   artifact,
	}
	if raw := argStringPtr(args, "kind"); raw != nil {
		kind, err := domain.ParseMemoryKind(*raw)
		if err != nil {
			return nil, toolError(err)
		}
		change.Kind = &kind
	}
	if raw := argStringPtr(args, "confidence"); raw != nil {
		c, err := domain.ParseConfidence(*raw)
		if err != nil {
			return nil, toolError(err)
		}
		change.Confidence = &c
	}
	if raw := argStringPtr(args, "status"); raw != nil {
		status, err := domain.ParseLifecycle(*raw)
		if err != nil {
			return nil, toolError(err)
		}
		change.Status = &status
	}
	if raw := argStringPtr(args, "sensitivity"); raw != nil {
		level, err := domain.ParseSensitivity(*raw)
		if err != nil {
			return nil, toolError(err)
		}
		change.Sensitivity = &level
	}

	// The date is the same three-state edit as a reference, on the same
	// reasoning: "do not touch it" and "it has no date after all" are
	// different requests, and a flat schema needs two properties to say
	// three things. The contradiction is refused here.
	occurred := argStringPtr(args, "occurred_at")
	clearOccurred := argBool(args, "clear_occurred_at")
	switch {
	case occurred != nil && clearOccurred:
		return nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			"send either occurred_at or clear_occurred_at, not both")
	case clearOccurred:
		change.OccurredAt = domain.ClearTime()
	case occurred != nil:
		when, err := parseOccurredAt(*occurred)
		if err != nil {
			return nil, err
		}
		change.OccurredAt = domain.SetTime(when)
	}

	res, err := t.svc.UpdateMemory(ctx, ws, id, change)
	if err != nil {
		return nil, toolError(err)
	}

	out := map[string]any{
		"memory":    memorySummary(res.Memory),
		"changed":   !res.Unchanged(),
		"unchanged": res.Unchanged(),
	}
	if res.KindChanged {
		out["kind_was"] = string(res.PreviousKind)
	}
	if res.StatusChanged {
		out["status_was"] = string(res.PreviousStatus)
	}
	if res.SensitivityChanged {
		out["sensitivity_was"] = string(res.PreviousSensitivity)
	}
	return fit(out, "")
}
