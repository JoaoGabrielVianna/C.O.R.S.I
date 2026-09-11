/**
 * Context references — the entities a conversation is about.
 *
 * ── Why this is not `composer/references.ts` ───────────────────────────
 * The word collides and the concepts do not. Read them together once:
 *
 *   composer/references   a CAPABILITY attached to one turn. Its id is a
 *                         tool name, and attaching it narrows what that
 *                         turn may do. That is what `@` offers.
 *   this file             an ENTITY the conversation is about. Its id is a
 *                         row in another module, and attaching it changes
 *                         no capability at all.
 *
 * The backend keeps them in two columns for the same reason (see
 * `chat/domain/context_reference.go`). Merging them here would mean a chip
 * whose meaning depends on which list it came from, and a menu that could
 * offer "GitHub · Buscar código" beside "Acme · Backend Engineer" as if
 * choosing either did the same thing.
 *
 * ── This module is the cross-channel contract ──────────────────────────
 * Everything below is plain data: three strings and two booleans. There is
 * no React in this file and nothing that assumes a screen. A WhatsApp or
 * Telegram channel consuming the same API would receive exactly these
 * fields and render a line of text from them, while the Web renders a card
 * — same semantics, different presentation, no shared component.
 *
 *   { type, id, label, subtitle?, unavailable? }
 *          │    │       │
 *          │    │       └── how it reads to a person
 *          │    └── identity inside the provider
 *          └── which provider owns it: job_radar.opportunity
 *
 * The renderer registry that turns one of these into a card lives beside
 * this file and imports it; nothing here imports the renderer.
 */

/**
 * The identity of an entity type, as `provider.entity`.
 *
 * Deliberately a plain string rather than a union of known values. A union
 * would be a catalogue of providers in the frontend — the second source of
 * truth `composer/references.ts` refuses to keep — and would make the
 * arrival of `github.repository` a type error in a file that should not
 * have to care.
 */
export type ContextReferenceType = string;

export interface ApiContextReference {
  type: ContextReferenceType;
  /** Identity inside the provider. Opaque here; never parsed. */
  id: string;
  /**
   * How the entity reads to a person: "Acme · Backend Engineer".
   *
   * Written by the backend from the owning module's own data. The client
   * never authors it — sending one is rejected — because this text also
   * reaches the model's context, and a label a client could write would be
   * a label a client could use to mislead the agent.
   */
  label: string;
  /**
   * One optional line of extra recognition, e.g. "Remote (BR)".
   *
   * STABLE facts only. It is written once, when the entity is attached, and
   * the backend deliberately keeps mutable domain state out of it: a stage,
   * a balance or a due date frozen here would keep asserting itself long
   * after the entity moved. Anything that changes is read fresh by the
   * backend on the turn it is needed and never travels in this field.
   *
   * So a card built from this is recognition text, not a status display.
   */
  subtitle?: string;
  /**
   * The entity could not be resolved on the last read: deleted, archived,
   * or no longer visible to this workspace.
   *
   * Recomputed by the backend on every read rather than stored, so an
   * entity that comes back is simply available again. A conversation must
   * still open and still be readable when this is true — the reference is
   * part of the record of what was discussed, and removing it would rewrite
   * the past.
   */
  unavailable?: boolean;
}

/**
 * What the client may send. Identity only.
 *
 * The label is absent by design: the backend writes it. The decoder rejects
 * unknown fields, so adding one here would be a 400 rather than a value
 * silently discarded.
 */
export interface ContextReferenceInput {
  type: ContextReferenceType;
  id: string;
}

/** The provider that owns the entity: everything before the first dot. */
export function providerOf(type: ContextReferenceType): string {
  const dot = type.indexOf(".");
  return dot === -1 ? type : type.slice(0, dot);
}

/** A stable key for dedupe and for React lists. */
export function contextReferenceKey(
  r: Pick<ApiContextReference, "type" | "id">,
): string {
  return `${r.type}:${r.id}`;
}

/** Identity only, for sending. */
export function toInput(r: Pick<ApiContextReference, "type" | "id">): ContextReferenceInput {
  return { type: r.type, id: r.id };
}
