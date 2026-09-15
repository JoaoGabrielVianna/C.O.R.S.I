package tools

import (
	"context"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/palace/app"
	"github.com/corsi/backend/internal/palace/domain"
)

// Artifact entries.
//
// ══════════════════════════════════════════════════════════════════════
//
//	ONE ENTRY PER CALL, AND THAT IS NOT A WORKAROUND
//
// ══════════════════════════════════════════════════════════════════════
//
// A tool schema here is a flat object of scalars: no arrays, no nesting.
// So a capability that added five entries at once would have to smuggle
// them through as a JSON string inside a string property, and the schema
// the model is shown would then describe a contract the validator does
// not enforce. A declared shape we do not check is a contract we do not
// have.
//
// The entries are therefore unitary, and the cost is stated plainly
// rather than hidden: building a five-line checklist takes five calls,
// and a turn is allowed three rounds of tools. A list longer than that is
// finished across turns, which is visible to the user and slower than it
// should be. See agent.go, which says so to the model, and the
// round-limit evidence in the integration suite.

/* ── palace.artifact.item.add ────────────────────────────────────────── */

type itemAdd struct{ base }

func (itemAdd) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         ItemAddTool,
		Title:        "Palace · Adicionar entrada",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Adds ONE entry to a list, a plan or a project. Notes carry no " +
			"entries and will refuse. Each call adds a single entry, so several entries " +
			"mean several calls; if the user asked for more than a few, add what you can " +
			"and say plainly which ones are still missing rather than claiming the whole " +
			"list is there. Omit `position` to append at the end, which is almost always " +
			"what is meant.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"artifact_id": idProperty("object", string(ArtifactListTool)),
				"text": {
					Type:        chatdomain.TypeString,
					Description: "The entry, in the user's own words where possible.",
					MaxLength:   domain.MaxItemText,
				},
				"position": {
					Type: chatdomain.TypeInteger,
					Description: "Optional. Where to place it, counting from 0. " +
						"Omit it to append after the last entry.",
				},
				"done": {
					Type:        chatdomain.TypeBoolean,
					Description: "Optional, defaults to false. Only for something already finished.",
				},
			},
			Required: []string{"artifact_id", "text"},
		},
	}
}

func (t itemAdd) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	artifactID, err := parseID(argString(args, "artifact_id"), "artifact_id")
	if err != nil {
		return nil, err
	}

	item, err := t.svc.AddItem(ctx, ws, artifactID, app.AddItemInput{
		Text:     argString(args, "text"),
		Position: argIntPtr(args, "position"),
		Done:     argBool(args, "done"),
	})
	if err != nil {
		return nil, toolError(err)
	}
	return fit(map[string]any{
		"artifact_id": artifactID.String(),
		"item":        itemOut(item),
		"created":     true,
	}, "")
}

/* ── palace.artifact.item.update ─────────────────────────────────────── */

type itemUpdate struct{ base }

func (itemUpdate) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         ItemUpdateTool,
		Title:        "Palace · Atualizar entrada",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Edits ONE entry: its text, whether it is done, or where it sits. " +
			"Both the object and the entry must be named, and an entry id from a " +
			"different object will not be found. Read the object with " +
			string(ArtifactGetTool) + " to get entry ids. Send only what you are " +
			"changing.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"artifact_id": idProperty("object", string(ArtifactListTool)),
				"item_id":     idProperty("entry", string(ArtifactGetTool)),
				"text": {
					Type:        chatdomain.TypeString,
					Description: "Optional. The new text.",
					MaxLength:   domain.MaxItemText,
				},
				"done": {
					Type:        chatdomain.TypeBoolean,
					Description: "Optional. Tick or untick the entry.",
				},
				"position": {
					Type:        chatdomain.TypeInteger,
					Description: "Optional. Move it, counting from 0.",
				},
			},
			Required: []string{"artifact_id", "item_id"},
		},
	}
}

func (t itemUpdate) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	artifactID, err := parseID(argString(args, "artifact_id"), "artifact_id")
	if err != nil {
		return nil, err
	}
	itemID, err := parseID(argString(args, "item_id"), "item_id")
	if err != nil {
		return nil, err
	}

	res, err := t.svc.UpdateItem(ctx, ws, artifactID, itemID, domain.ItemChange{
		Text:     argStringPtr(args, "text"),
		Done:     argBoolPtr(args, "done"),
		Position: argIntPtr(args, "position"),
	})
	if err != nil {
		return nil, toolError(err)
	}

	out := map[string]any{
		"artifact_id": artifactID.String(),
		"item":        itemOut(res.Item),
		"changed":     !res.Unchanged(),
		"unchanged":   res.Unchanged(),
	}
	if res.DoneChanged {
		// "marquei como feito" and "já estava feito" are different
		// answers, and only the first one is work.
		out["done_was"] = res.PreviousDone
	}
	return fit(out, "")
}

/* ── palace.artifact.item.remove ─────────────────────────────────────── */

type itemRemove struct{ base }

func (itemRemove) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         ItemRemoveTool,
		Title:        "Palace · Remover entrada",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Takes ONE entry off a list. Both the object and the entry must be " +
			"named. Removing an entry that is already gone reports that it was not found, " +
			"which is how you tell \"I removed it\" from \"it was already gone\". This " +
			"removes an entry, never the object: archive the object with " +
			string(ArtifactUpdateTool) + " if that is what was meant.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"artifact_id": idProperty("object", string(ArtifactListTool)),
				"item_id":     idProperty("entry", string(ArtifactGetTool)),
			},
			Required: []string{"artifact_id", "item_id"},
		},
	}
}

func (t itemRemove) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	artifactID, err := parseID(argString(args, "artifact_id"), "artifact_id")
	if err != nil {
		return nil, err
	}
	itemID, err := parseID(argString(args, "item_id"), "item_id")
	if err != nil {
		return nil, err
	}

	if err := t.svc.RemoveItem(ctx, ws, artifactID, itemID); err != nil {
		return nil, toolError(err)
	}
	return fit(map[string]any{
		"artifact_id": artifactID.String(),
		"item_id":     itemID.String(),
		"removed":     true,
	}, "")
}
