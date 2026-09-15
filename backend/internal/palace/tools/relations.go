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

func relationOut(r *domain.Relation) map[string]any {
	return map[string]any{
		"relation_id": r.ID.String(),
		"from_type":   string(r.FromType),
		"from_id":     r.FromID.String(),
		"kind":        string(r.Kind),
		"to_type":     string(r.ToType),
		"to_id":       r.ToID.String(),
		"created_at":  stamp(r.CreatedAt),
	}
}

// shapeGuide renders the closed matrix for the model, built from the
// matrix itself so a pairing added later reaches the schema the day it is
// added rather than whenever somebody remembers a string.
func shapeGuide() string {
	var b strings.Builder
	for i, kind := range domain.RelationKinds {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(string(kind))
		b.WriteString(": ")
		b.WriteString(domain.ShapeDescription(kind))
	}
	return b.String()
}

/* ── palace.relation.create ──────────────────────────────────────────── */

type relationCreate struct{ base }

func (relationCreate) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         RelationCreateTool,
		Title:        "Palace · Conectar",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Draws one controlled link between two things the user has kept. " +
			"Only these shapes exist, and anything else is refused: " + shapeGuide() + ". " +
			"`supersedes` reads NEW supersedes OLD: the first id is the thing that " +
			"replaced, the second is the thing replaced, and getting that backwards " +
			"records the abandoned option as the current one. `decision_for` may only " +
			"start at a record whose kind is `decision`. Both ends must exist and be " +
			"active: an archived thing refuses a new link.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"from_type": {
					Type: chatdomain.TypeString,
					Description: vocabulary("What the link starts at. For `supersedes` this is "+
						"the NEW one.", domain.EntityTypeNames()),
					MaxLength: 20,
				},
				"from_id": {
					Type:        chatdomain.TypeString,
					Description: "The id of the thing the link starts at.",
					MaxLength:   36,
				},
				"kind": {
					Type:        chatdomain.TypeString,
					Description: vocabulary("What the link says.", domain.RelationKindNames()),
					MaxLength:   20,
				},
				"to_type": {
					Type: chatdomain.TypeString,
					Description: vocabulary("What the link points at. For `supersedes` this is "+
						"the OLD one.", domain.EntityTypeNames()),
					MaxLength: 20,
				},
				"to_id": {
					Type:        chatdomain.TypeString,
					Description: "The id of the thing the link points at.",
					MaxLength:   36,
				},
			},
			Required: []string{"from_type", "from_id", "kind", "to_type", "to_id"},
		},
	}
}

func (t relationCreate) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}

	fromType, err := domain.ParseEntityType(argString(args, "from_type"))
	if err != nil {
		return nil, toolError(err)
	}
	toType, err := domain.ParseEntityType(argString(args, "to_type"))
	if err != nil {
		return nil, toolError(err)
	}
	kind, err := domain.ParseRelationKind(argString(args, "kind"))
	if err != nil {
		return nil, toolError(err)
	}
	fromID, err := parseID(argString(args, "from_id"), "from_id")
	if err != nil {
		return nil, err
	}
	toID, err := parseID(argString(args, "to_id"), "to_id")
	if err != nil {
		return nil, err
	}

	res, err := t.svc.CreateRelation(ctx, ws, app.CreateRelationInput{
		FromType: fromType, FromID: fromID, Kind: kind, ToType: toType, ToID: toID,
	})
	if err != nil {
		return nil, toolError(err)
	}

	out := map[string]any{
		"relation": relationOut(res.Relation),
		// "já estava registrado" is a different sentence from "registrei",
		// and a caller that could not tell them apart would report work it
		// did not do.
		"created":         res.Created,
		"already_existed": !res.Created,
	}
	return fit(out, "")
}

/* ── palace.relation.list ────────────────────────────────────────────── */

type relationList struct{ base }

func (relationList) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         RelationListTool,
		Title:        "Palace · Listar conexões",
		Effect:       chatdomain.EffectRead,
		Confidential: true,
		Description: "Lists the links that TOUCH one thing: what it points at and what " +
			"points at it. It returns one step and never follows the chain, so reaching " +
			"something two links away means calling it again with the id you found. Use " +
			"it to answer \"o que isso substituiu\" (direction `from`, kind `supersedes`) " +
			"and \"o que substituiu isso\" (direction `to`). Ids come back without their " +
			"titles; read the thing itself if you need to name it.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"entity_type": {
					Type: chatdomain.TypeString,
					Description: vocabulary("Optional. What sort of thing to anchor at; send it "+
						"together with entity_id.", domain.EntityTypeNames()),
					MaxLength: 20,
				},
				"entity_id": {
					Type: chatdomain.TypeString,
					Description: "Optional. The thing to anchor at; send it together with " +
						"entity_type. Omit both to see every link.",
					MaxLength: 36,
				},
				"direction": {
					Type: chatdomain.TypeString,
					Description: "Optional. `from` for links that start at the anchor, `to` for " +
						"links that point at it. Omit it for both.",
					MaxLength: 10,
				},
				"kind": {
					Type:        chatdomain.TypeString,
					Description: vocabulary("Optional. Only links of this sort.", domain.RelationKindNames()),
					MaxLength:   20,
				},
				"limit": limitProperty,
			},
		},
	}
}

func (t relationList) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}

	filter := ports.RelationFilter{
		Direction: ports.RelationDirection(strings.ToLower(strings.TrimSpace(argString(args, "direction")))),
		Page:      ports.Page{Limit: argInt(args, "limit", 0)},
	}
	if raw := strings.TrimSpace(argString(args, "kind")); raw != "" {
		kind, err := domain.ParseRelationKind(raw)
		if err != nil {
			return nil, toolError(err)
		}
		filter.Kind = &kind
	}

	// The anchor is a pair, and half a pair is a request nobody can
	// answer: a uuid alone cannot say whether it names a record or an
	// object, and the two tables can legitimately carry the same value.
	rawType := strings.TrimSpace(argString(args, "entity_type"))
	rawID := strings.TrimSpace(argString(args, "entity_id"))
	switch {
	case rawType != "" && rawID != "":
		entity, err := domain.ParseEntityType(rawType)
		if err != nil {
			return nil, toolError(err)
		}
		id, err := parseID(rawID, "entity_id")
		if err != nil {
			return nil, err
		}
		filter.Anchor = &ports.RelationAnchor{Type: entity, ID: id}
	case rawType != "" || rawID != "":
		return nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
			"entity_type and entity_id go together: an id on its own cannot say what kind "+
				"of thing it names")
	}

	relations, total, err := t.svc.ListRelations(ctx, ws, filter)
	if err != nil {
		return nil, toolError(err)
	}

	rows := make([]map[string]any, 0, len(relations))
	for _, r := range relations {
		rows = append(rows, relationOut(r))
	}

	out := map[string]any{"relations": rows, "matching_total": total}
	if len(rows) == 0 {
		out["note"] = "nothing is linked to that. This listing returns one step only and " +
			"never follows the chain."
	}
	return fit(out, "relations")
}

/* ── palace.relation.remove ──────────────────────────────────────────── */

type relationRemove struct{ base }

func (relationRemove) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         RelationRemoveTool,
		Title:        "Palace · Remover conexão",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Withdraws one link. It removes the LINK and touches neither of the " +
			"things it connected: nothing is archived, deleted or edited by this. It is " +
			"also the first of the two steps needed to re-classify a record that governs " +
			"something as a decision: remove the `decision_for` link, then change the " +
			"kind. Removing a link that is already gone reports that it was not found.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"relation_id": idProperty("link", string(RelationListTool)+" or "+string(RelationCreateTool)),
			},
			Required: []string{"relation_id"},
		},
	}
}

func (t relationRemove) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(argString(args, "relation_id"), "relation_id")
	if err != nil {
		return nil, err
	}
	if err := t.svc.RemoveRelation(ctx, ws, id); err != nil {
		return nil, toolError(err)
	}
	return fit(map[string]any{"relation_id": id.String(), "removed": true}, "")
}
