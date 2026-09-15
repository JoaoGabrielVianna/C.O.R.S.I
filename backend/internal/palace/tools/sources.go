package tools

import (
	"context"
	"strings"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
	"github.com/corsi/backend/internal/palace/app"
	"github.com/corsi/backend/internal/palace/domain"
)

// Sources: evidence.
//
// ══════════════════════════════════════════════════════════════════════
//
//	A SOURCE IS EVIDENCE THAT EXISTS, NOT A NOTE WITH A DIFFERENT NAME
//
// ══════════════════════════════════════════════════════════════════════
//
// The failure this guards against is specific and easy to fall into: a
// model that has learned "memories should have provenance" starts
// manufacturing a source for every memory, writing its own paraphrase of
// the conversation into the content field. The record then says the
// belief rests on something the user said, in words the user never said,
// and it is indistinguishable afterwards from a real transcript.
//
// That is worse than having no provenance at all. A memory with none is
// honest about resting on nothing; a memory with a fabricated source is a
// lie with a citation.
//
// So the rule the description states, and that agent.go repeats to the
// model: a source is created ONLY when real material exists to put in it,
// and its content is that material verbatim. Nothing in this product
// turns a chat message into a source automatically, and no capability
// here does it on the model's initiative.

/* ── palace.source.create ────────────────────────────────────────────── */

type sourceCreate struct{ base }

func (sourceCreate) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         SourceCreateTool,
		Title:        "Palace · Guardar evidência",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Stores a piece of EVIDENCE: material that actually exists, kept " +
			"verbatim so a conclusion drawn from it can be checked later. A pasted " +
			"message, a transcript of something the user recorded, the text of a document " +
			"they gave you. NEVER write your own paraphrase here and never compose text " +
			"as though the user had said it: a fabricated source is worse than no source, " +
			"because it is a lie that looks like a citation. If you have no real material, " +
			"do not call this. Evidence CANNOT be edited or deleted afterwards.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"kind": {
					Type:        chatdomain.TypeString,
					Description: vocabulary("Where this material came from.", domain.SourceKindNames()),
					MaxLength:   30,
				},
				"content": {
					Type: chatdomain.TypeString,
					Description: "The material itself, verbatim. Not a summary of it and not " +
						"your own words.",
					MaxLength: domain.MaxSourceContent,
				},
				"external_ref": {
					Type: chatdomain.TypeString,
					Description: "Where it came from when that is outside this product: a URL, " +
						"a message id, an event name. REQUIRED for kind `external`.",
					MaxLength: domain.MaxSourceExternalRef,
				},
				"captured_at": {
					Type: chatdomain.TypeString,
					Description: "Optional. The day the material was PRODUCED, as 2026-03-14, " +
						"which is not the day you are storing it. Omit it for something " +
						"produced now.",
					MaxLength: 10,
				},
				"sensitivity": {
					Type: chatdomain.TypeString,
					Description: vocabulary("Optional, defaults to normal. This becomes a FLOOR: "+
						"any record citing this evidence must be at least this withheld, and "+
						"cannot be lowered below it afterwards.", domain.SensitivityNames()),
					MaxLength: 20,
				},
			},
			Required: []string{"kind", "content"},
		},
	}
}

func (t sourceCreate) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}

	kind, err := domain.ParseSourceKind(argString(args, "kind"))
	if err != nil {
		return nil, toolError(err)
	}

	in := app.CreateSourceInput{
		Kind:        kind,
		Content:     argString(args, "content"),
		ExternalRef: argString(args, "external_ref"),
	}
	if raw := strings.TrimSpace(argString(args, "captured_at")); raw != "" {
		// The same date shape, and the same UTC note, as a memory's
		// occurred_at. See parseOccurredAt.
		when, err := parseOccurredAt(raw)
		if err != nil {
			return nil, chatdomain.ToolError(chatdomain.ToolErrInvalidArguments,
				"captured_at must be a date like 2026-03-14. Omit it for material produced now")
		}
		in.CapturedAt = &when
	}
	if raw := strings.TrimSpace(argString(args, "sensitivity")); raw != "" {
		level, err := domain.ParseSensitivity(raw)
		if err != nil {
			return nil, toolError(err)
		}
		in.Sensitivity = &level
	}

	source, err := t.svc.CreateSource(ctx, ws, in)
	if err != nil {
		return nil, toolError(err)
	}
	return fit(map[string]any{
		"source": map[string]any{
			"source_id":   source.ID.String(),
			"kind":        string(source.Kind),
			"captured_at": stamp(source.CapturedAt),
			"sensitivity": string(source.Sensitivity),
		},
		"created": true,
	}, "")
}

/* ── palace.source.get ───────────────────────────────────────────────── */

type sourceGet struct{ base }

func (sourceGet) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         SourceGetTool,
		Title:        "Palace · Ver evidência",
		Effect:       chatdomain.EffectRead,
		Confidential: true,
		Description: "Reads one piece of evidence in full. Use it when the user asks what " +
			"something was actually based on and the excerpt from " + string(MemoryGetTool) +
			" is not enough. What comes back is the material as it was stored: quote it " +
			"as evidence, not as something the user is saying now.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"source_id": idProperty("evidence", string(MemoryGetTool)+" or "+string(SourceCreateTool)),
			},
			Required: []string{"source_id"},
		},
	}
}

func (t sourceGet) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	id, err := parseID(argString(args, "source_id"), "source_id")
	if err != nil {
		return nil, err
	}

	source, err := t.svc.GetSource(ctx, ws, id)
	if err != nil {
		return nil, toolError(err)
	}
	out := map[string]any{
		"source": map[string]any{
			"source_id":    source.ID.String(),
			"kind":         string(source.Kind),
			"content":      source.Content,
			"external_ref": source.ExternalRef,
			"captured_at":  stamp(source.CapturedAt),
			"sensitivity":  string(source.Sensitivity),
			"created_at":   stamp(source.CreatedAt),
		},
	}
	return fit(out, "")
}
