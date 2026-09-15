package tools

import (
	"context"

	chatdomain "github.com/corsi/backend/internal/chat/domain"
)

// Provenance: citing evidence for something already kept.
//
// ══════════════════════════════════════════════════════════════════════
//
//	THIS CAPABILITY CREATES NOTHING BUT THE LINK
//
// ══════════════════════════════════════════════════════════════════════
//
// It takes two ids that already exist and records that one rests on the
// other. It does not create a Source, does not create a Memory, does not
// edit either, and cannot be used to make one from the other.
//
// ── Why linking is its own capability rather than a field on create ────
// Because `palace.memory.create` with a `source_id` would be two writes
// behind one name: the record and the citation. The application layer has
// no transaction, so a failure between them would leave a memory that was
// supposed to rest on something and does not, with no operation able to
// finish the job. One id in, one link out, and the caller can see which
// of the two steps failed.
//
// ── Why there is no unlink ─────────────────────────────────────────────
// Provenance is append-only in Palace Core v1. That is a scope decision
// rather than an eternal invariant, and the case for an explicit
// correction is recorded in domain/provenance.go. It is not this
// capability's to invent, and it must never arrive as a flag on this one.

/* ── palace.memory.link_source ───────────────────────────────────────── */

type memoryLinkSource struct{ base }

func (memoryLinkSource) Definition() chatdomain.ToolDefinition {
	return chatdomain.ToolDefinition{
		Name:         MemoryLinkSourceTool,
		Title:        "Palace · Citar evidência",
		Effect:       chatdomain.EffectWrite,
		Confidential: true,
		Description: "Records that one kept record RESTS ON one piece of evidence. Both must " +
			"already exist: this creates neither and edits neither. Use it only when the " +
			"evidence is real material the user actually provided, and only after storing " +
			"that material with " + string(SourceCreateTool) + ". Do NOT invent a source in " +
			"order to give a record provenance: a record that rests on nothing is honest, " +
			"and a record citing something the user never said is a lie with a citation. " +
			"Linking the same evidence twice is safe and reports that it was already " +
			"recorded. A record cannot be less withheld than the evidence behind it, so " +
			"this may refuse and tell you to raise the record's sensitivity first. Links " +
			"cannot be removed.",
		Schema: chatdomain.ToolSchema{
			Properties: map[string]chatdomain.ToolProperty{
				"memory_id": idProperty("record", string(MemoryListTool)+" or "+string(MemoryCreateTool)),
				"source_id": idProperty("evidence", string(SourceCreateTool)+" or "+string(MemoryGetTool)),
			},
			Required: []string{"memory_id", "source_id"},
		},
	}
}

func (t memoryLinkSource) Execute(ctx context.Context, args map[string]any) (chatdomain.ToolOutput, error) {
	ws, err := workspaceOf(ctx)
	if err != nil {
		return nil, err
	}
	memoryID, err := parseID(argString(args, "memory_id"), "memory_id")
	if err != nil {
		return nil, err
	}
	sourceID, err := parseID(argString(args, "source_id"), "source_id")
	if err != nil {
		return nil, err
	}

	// Straight through to the operation that already exists. Both ends are
	// resolved in this workspace there, the sensitivity floor is applied
	// there, and the idempotency is the primary key's. Nothing is decided
	// in this file.
	res, err := t.svc.LinkSource(ctx, ws, memoryID, sourceID)
	if err != nil {
		return nil, toolError(err)
	}
	return fit(map[string]any{
		"memory_id": memoryID.String(),
		"source_id": sourceID.String(),
		// "já estava registrado" is a different sentence from "registrei",
		// and a caller that could not tell them apart would report work it
		// did not do.
		"created":         res.Created,
		"already_existed": !res.Created,
	}, "")
}
