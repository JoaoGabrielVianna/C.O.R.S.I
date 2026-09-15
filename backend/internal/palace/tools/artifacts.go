package tools

import (
	"context"
	"strings"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/palace/app"
	"github.com/corsi/backend/internal/palace/domain"
	"github.com/corsi/backend/internal/palace/ports"
)

/* ── shaping ─────────────────────────────────────────────────────────── */

func artifactSummary(a *domain.Artifact) map[string]any {
	m := map[string]any{
		"artifact_id": a.ID.String(),
		"kind":        string(a.Kind),
		"title":       a.Title,
		"status":      string(a.Status),
		"sensitivity": string(a.Sensitivity),
		"room_id":     idPtr(a.RoomID),
		"updated_at":  stamp(a.UpdatedAt),
	}
	withExcerpt(m, "body", a.Body)
	return m
}

// artifactDetail is the whole record, which is the right answer for
// `get`: it is called when the model has decided WHICH artifact it is
// working on, and the next thing it does is rewrite the text. Handing
// back an abbreviated version would produce an edit that silently
// truncates the user's work.
func artifactDetail(a *domain.Artifact) map[string]any {
	return map[string]any{
		"artifact_id": a.ID.String(),
		"kind":        string(a.Kind),
		"title":       a.Title,
		"body":        a.Body,
		"status":      string(a.Status),
		"sensitivity": string(a.Sensitivity),
		"room_id":     idPtr(a.RoomID),
		"created_at":  stamp(a.CreatedAt),
		"updated_at":  stamp(a.UpdatedAt),
	}
}

func itemOut(i *domain.ArtifactItem) map[string]any {
	return map[string]any{
		"item_id":  i.ID.String(),
		"position": i.Position,
		"text":     i.Text,
		"done":     i.Done,
	}
}

/* ── palace.artifact.list ────────────────────────────────────────────── */

type artifactList struct{ base }

func (artifactList) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         ArtifactListTool,
		Title:        "Palace · Listar objetos",
		Effect:       chatdomain.EffectRead,
		Confidential: true,
		Description: "Lists the living objects of the palace: projects, lists, plans and " +
			"notes. Call it when the user refers to one by name rather than by id, and to " +
			"answer \"no que eu estou trabalhando\", \"quais listas eu tenho\". It is how " +
			"you find the id every other artifact capability needs. Entries of a list are " +
			"NOT returned here; read them with " + string(ArtifactGetTool) + ".",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"kind": {
					Type:        chatdomain.TypeString,
					Description: vocabulary("Optional. Only objects of this kind.", domain.ArtifactKindNames()),
					MaxLength:   20,
				},
				"status": {
					Type:        chatdomain.TypeString,
					Description: vocabulary("Optional. Only objects in this state.", domain.LifecycleNames()),
					MaxLength:   20,
				},
				"room_id": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Only objects filed in this area.",
					MaxLength:   36,
				},
				"search": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Matches the title or the body.",
					MaxLength:   200,
				},
				"include_highly_sensitive": includeHighlySensitiveProperty,
				"limit":                    limitProperty,
			},
		},
	}
}

func (t artifactList) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}

	filter := ports.ArtifactFilter{
		Search:    argString(args, "search"),
		Sensitive: ports.Sensitive{IncludeHighlySensitive: argBool(args, "include_highly_sensitive")},
		Page:      ports.Page{Limit: argInt(args, "limit", 0)},
	}
	if raw := strings.TrimSpace(argString(args, "kind")); raw != "" {
		kind, err := domain.ParseArtifactKind(raw)
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

	artifacts, total, err := t.svc.ListArtifacts(ctx, ws, filter)
	if err != nil {
		return nil, toolError(err)
	}

	rows := make([]map[string]any, 0, len(artifacts))
	for _, a := range artifacts {
		rows = append(rows, artifactSummary(a))
	}

	out := map[string]any{"artifacts": rows, "matching_total": total}
	if len(rows) == 0 {
		out["note"] = "no object matched. Try a broader filter, or call this with no arguments."
	}
	return fit(out, "artifacts")
}

/* ── palace.artifact.get ─────────────────────────────────────────────── */

type artifactGet struct{ base }

func (artifactGet) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         ArtifactGetTool,
		Title:        "Palace · Ver objeto",
		Effect:       chatdomain.EffectRead,
		Confidential: true,
		Description: "Reads one object in full: its title, its body, and the ENTRIES it " +
			"carries when it is a project, a list or a plan. This is the only way to see " +
			"a list's entries and the ids they are edited by. Call it before rewriting " +
			"anything or ticking anything off: the stored record is the current state, " +
			"and working from a summary or from what was said several turns ago would " +
			"discard changes.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"artifact_id": idProperty("object", string(ArtifactListTool)+" or "+string(ArtifactCreateTool)),
			},
			Required: []string{"artifact_id"},
		},
	}
}

func (t artifactGet) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(argString(args, "artifact_id"), "artifact_id")
	if err != nil {
		return nil, err
	}

	artifact, err := t.svc.GetArtifact(ctx, ws, id)
	if err != nil {
		return nil, toolError(err)
	}

	out := map[string]any{"artifact": artifactDetail(artifact)}

	// ── Why the entries come back from `get` and not their own listing ─
	// Because there is no `artifact.item.list` capability, by decision,
	// and entries that could be added but never read would make a list
	// write-only. `get` is where an object states itself in full, and its
	// entries are what a list IS.
	//
	// Asked only for the kinds that carry them, so a note does not pay a
	// query to be told it has none.
	if artifact.Kind.AcceptsItems() {
		items, total, err := t.svc.ListItems(ctx, ws, id, ports.Page{Limit: 100})
		if err != nil {
			return nil, toolError(err)
		}
		rows := make([]map[string]any, 0, len(items))
		for _, i := range items {
			rows = append(rows, itemOut(i))
		}
		out["items"] = rows
		out["items_total"] = total
	}
	return fit(out, "items")
}

/* ── palace.artifact.create ──────────────────────────────────────────── */

type artifactCreate struct{ base }

func (artifactCreate) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         ArtifactCreateTool,
		Title:        "Palace · Criar objeto",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Creates a living object: a project, a list, a plan or a note. Use it " +
			"when the user asks for one (\"cria uma lista de X\", \"abre um projeto para " +
			"Y\"), not because a conversation touched a subject. The KIND CANNOT BE " +
			"CHANGED afterwards, so choose it deliberately: `note` is prose and carries no " +
			"entries; `list`, `plan` and `project` carry entries. Re-filing later means " +
			"creating the right object and archiving the wrong one.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"kind": {
					Type: chatdomain.TypeString,
					Description: vocabulary("What this object IS. Cannot be changed later.",
						domain.ArtifactKindNames()),
					MaxLength: 20,
				},
				"title": {
					Type:        chatdomain.TypeString,
					Description: "A short handle, as the user would say it.",
					MaxLength:   domain.MaxArtifactTitle,
				},
				"body": {
					Type:        chatdomain.TypeString,
					Description: "Optional. The prose of the object: what it is for, or what it says.",
					MaxLength:   domain.MaxArtifactBody,
				},
				"room_id": {
					Type:        chatdomain.TypeString,
					Description: "Optional. File it in this area. Omit it when the user has not said where it belongs.",
					MaxLength:   36,
				},
				"sensitivity": {
					Type:        chatdomain.TypeString,
					Description: vocabulary("Optional, defaults to normal.", domain.SensitivityNames()),
					MaxLength:   20,
				},
			},
			Required: []string{"kind", "title"},
		},
	}
}

func (t artifactCreate) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}

	kind, err := domain.ParseArtifactKind(argString(args, "kind"))
	if err != nil {
		return nil, toolError(err)
	}

	in := app.CreateArtifactInput{
		Kind:  kind,
		Title: argString(args, "title"),
		Body:  argString(args, "body"),
	}
	if raw := strings.TrimSpace(argString(args, "room_id")); raw != "" {
		id, err := parseID(raw, "room_id")
		if err != nil {
			return nil, err
		}
		in.RoomID = &id
	}
	if raw := strings.TrimSpace(argString(args, "sensitivity")); raw != "" {
		level, err := domain.ParseSensitivity(raw)
		if err != nil {
			return nil, toolError(err)
		}
		in.Sensitivity = &level
	}

	artifact, err := t.svc.CreateArtifact(ctx, ws, in)
	if err != nil {
		return nil, toolError(err)
	}
	out := map[string]any{"artifact": artifactDetail(artifact), "created": true}
	if kind.AcceptsItems() {
		out["note"] = "this object carries entries; add them one at a time with " +
			string(ItemAddTool) + "."
	}
	return fit(out, "")
}

/* ── palace.artifact.update ──────────────────────────────────────────── */

type artifactUpdate struct{ base }

func (artifactUpdate) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         ArtifactUpdateTool,
		Title:        "Palace · Atualizar objeto",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Retitles an object, rewrites its body, moves it between areas, " +
			"changes how withheld it is, or archives it. It CANNOT change the kind. Send " +
			"only the fields you are changing; anything you omit is left exactly as it " +
			"is. Read the object first if you are rewriting its body: editing from what " +
			"was said several turns ago would discard work.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"artifact_id": idProperty("object", string(ArtifactListTool)),
				"title": {
					Type:        chatdomain.TypeString,
					Description: "Optional. The new title.",
					MaxLength:   domain.MaxArtifactTitle,
				},
				"body": {
					Type:        chatdomain.TypeString,
					Description: "Optional. The new body; send an empty string to clear it.",
					MaxLength:   domain.MaxArtifactBody,
				},
				"status": {
					Type:        chatdomain.TypeString,
					Description: vocabulary("Optional. Archive or restore the object.", domain.LifecycleNames()),
					MaxLength:   20,
				},
				"sensitivity": {
					Type:        chatdomain.TypeString,
					Description: vocabulary("Optional. How withheld this object is.", domain.SensitivityNames()),
					MaxLength:   20,
				},
				"room_id": {
					Type:        chatdomain.TypeString,
					Description: "Optional. File it in this area.",
					MaxLength:   36,
				},
				"detach_room": detachProperty("area"),
			},
			Required: []string{"artifact_id"},
		},
	}
}

func (t artifactUpdate) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(argString(args, "artifact_id"), "artifact_id")
	if err != nil {
		return nil, err
	}

	room, err := optionalRefArg(args, "room_id", "detach_room")
	if err != nil {
		return nil, err
	}

	change := domain.ArtifactChange{
		Title: argStringPtr(args, "title"),
		Body:  argStringPtr(args, "body"),
		Room:  room,
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

	res, err := t.svc.UpdateArtifact(ctx, ws, id, change)
	if err != nil {
		return nil, toolError(err)
	}

	out := map[string]any{
		"artifact":  artifactSummary(res.Artifact),
		"changed":   !res.Unchanged(),
		"unchanged": res.Unchanged(),
	}
	if res.StatusChanged {
		out["status_was"] = string(res.PreviousStatus)
	}
	if res.SensitivityChanged {
		out["sensitivity_was"] = string(res.PreviousSensitivity)
	}
	return fit(out, "")
}
