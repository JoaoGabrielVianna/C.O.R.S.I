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

// roomOut is one room, field by field.
//
// Built rather than serialised: a Room redacts itself when handed to an
// encoder, and that net is exactly why a surface has to NAME what it
// exposes. This is the surface. See domain/redaction.go.
func roomOut(r *domain.Room) map[string]any {
	m := map[string]any{
		"room_id":     r.ID.String(),
		"name":        r.Name,
		"status":      string(r.Status),
		"sensitivity": string(r.Sensitivity),
		"updated_at":  stamp(r.UpdatedAt),
	}
	withExcerpt(m, "description", r.Description)
	return m
}

/* ── palace.room.list ────────────────────────────────────────────────── */

type roomList struct{ base }

func (roomList) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         RoomListTool,
		Title:        "Palace · Listar salas",
		Effect:       chatdomain.EffectRead,
		Confidential: true,
		Description: "Lists the thematic areas of the user's palace: the contexts their " +
			"projects, notes and knowledge are filed under. Call it first when the user " +
			"refers to an area by name rather than by id, and to answer \"onde eu guardo " +
			"isso\". It is how you find the id every other room-taking capability needs. " +
			"If more than one room plausibly matches what the user meant, ask which one; " +
			"do not guess.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"status": {
					Type:        chatdomain.TypeString,
					Description: vocabulary("Optional. Only rooms in this state.", domain.LifecycleNames()),
					MaxLength:   20,
				},
				"search": {
					Type:        chatdomain.TypeString,
					Description: "Optional. Matches the name or the description.",
					MaxLength:   200,
				},
				"include_highly_sensitive": includeHighlySensitiveProperty,
				"limit":                    limitProperty,
			},
		},
	}
}

func (t roomList) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}

	filter := ports.RoomFilter{
		Search:    argString(args, "search"),
		Sensitive: ports.Sensitive{IncludeHighlySensitive: argBool(args, "include_highly_sensitive")},
		Page:      ports.Page{Limit: argInt(args, "limit", 0)},
	}
	if raw := strings.TrimSpace(argString(args, "status")); raw != "" {
		status, err := domain.ParseLifecycle(raw)
		if err != nil {
			return nil, toolError(err)
		}
		filter.Status = &status
	}

	rooms, total, err := t.svc.ListRooms(ctx, ws, filter)
	if err != nil {
		return nil, toolError(err)
	}

	rows := make([]map[string]any, 0, len(rooms))
	for _, r := range rooms {
		rows = append(rows, roomOut(r))
	}

	out := map[string]any{
		"rooms": rows,
		// The unbounded count of what the same filter matches, returned
		// even when it equals the number of rows: a model that has to
		// infer completeness from a length will eventually infer wrong.
		"matching_total": total,
	}
	if len(rows) == 0 {
		out["note"] = "no room matched. Try a broader filter, or call this with no " +
			"arguments to see every room."
	}
	return fit(out, "rooms")
}

/* ── palace.room.create ──────────────────────────────────────────────── */

type roomCreate struct{ base }

func (roomCreate) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         RoomCreateTool,
		Title:        "Palace · Criar sala",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Creates a thematic area in the user's palace. Use it when the user " +
			"asks to organise something into a new context (\"cria uma área para X\"), " +
			"not every time a new topic comes up in conversation. List first: an area " +
			"that already exists should be reused, not duplicated under a different name.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"name": {
					Type:        chatdomain.TypeString,
					Description: "A short handle for the area, as the user would say it.",
					MaxLength:   domain.MaxRoomName,
				},
				"description": {
					Type:        chatdomain.TypeString,
					Description: "Optional. One paragraph about what belongs here.",
					MaxLength:   domain.MaxRoomDescription,
				},
				"sensitivity": {
					Type: chatdomain.TypeString,
					Description: vocabulary("Optional, defaults to normal. How withheld this "+
						"area is; a room's NAME alone can disclose what it holds.",
						domain.SensitivityNames()),
					MaxLength: 20,
				},
			},
			Required: []string{"name"},
		},
	}
}

func (t roomCreate) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}

	in := app.CreateRoomInput{
		Name:        argString(args, "name"),
		Description: argString(args, "description"),
	}
	if raw := strings.TrimSpace(argString(args, "sensitivity")); raw != "" {
		level, err := domain.ParseSensitivity(raw)
		if err != nil {
			return nil, toolError(err)
		}
		in.Sensitivity = &level
	}

	room, err := t.svc.CreateRoom(ctx, ws, in)
	if err != nil {
		return nil, toolError(err)
	}
	return fit(map[string]any{"room": roomOut(room), "created": true}, "")
}

/* ── palace.room.update ──────────────────────────────────────────────── */

type roomUpdate struct{ base }

func (roomUpdate) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         RoomUpdateTool,
		Title:        "Palace · Atualizar sala",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Renames an area, rewrites its description, changes how withheld it " +
			"is, or archives it. Archiving is how an area is retired: it stays readable " +
			"and nothing inside it is deleted. Send only the fields you are changing; " +
			"anything you omit is left exactly as it is.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"room_id": idProperty("room", string(RoomListTool)+" or "+string(RoomCreateTool)),
				"name": {
					Type:        chatdomain.TypeString,
					Description: "Optional. The new name.",
					MaxLength:   domain.MaxRoomName,
				},
				"description": {
					Type:        chatdomain.TypeString,
					Description: "Optional. The new description; send an empty string to clear it.",
					MaxLength:   domain.MaxRoomDescription,
				},
				"status": {
					Type:        chatdomain.TypeString,
					Description: vocabulary("Optional. Archive or restore the area.", domain.LifecycleNames()),
					MaxLength:   20,
				},
				"sensitivity": {
					Type:        chatdomain.TypeString,
					Description: vocabulary("Optional. How withheld this area is.", domain.SensitivityNames()),
					MaxLength:   20,
				},
			},
			Required: []string{"room_id"},
		},
	}
}

func (t roomUpdate) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(argString(args, "room_id"), "room_id")
	if err != nil {
		return nil, err
	}

	change := domain.RoomChange{
		Name:        argStringPtr(args, "name"),
		Description: argStringPtr(args, "description"),
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

	res, err := t.svc.UpdateRoom(ctx, ws, id, change)
	if err != nil {
		return nil, toolError(err)
	}

	out := map[string]any{
		"room": roomOut(res.Room),
		// What actually moved, so a confirmation can be checked rather
		// than believed. "unchanged" is the honest answer to a request
		// that asked for what was already true.
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
