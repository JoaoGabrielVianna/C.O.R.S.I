import { cn } from "@/lib/utils";
import {
  contextReferenceKey,
  type ApiContextReference,
} from "@/modules/agents/context-references/contract";
import { presentationFor } from "@/modules/agents/context-references/registry";
import { useT } from "@/lib/i18n";

/**
 * The Web presentation of a context reference.
 *
 * ── This component is presentation and nothing else ────────────────────
 * It reads `{ type, id, label, subtitle, unavailable }` and draws them. It
 * does not fetch, does not resolve, and does not know what a Job Radar
 * opportunity is — the provider's glyph and name come from the registry,
 * and the words come from the backend. A different channel drawing the same
 * data as one line of text loses nothing but pixels.
 *
 * ── Why it is deliberately small ───────────────────────────────────────
 * The card exists so a person recognises the entity at a glance: which
 * system, which thing, roughly what state. It is not the opportunity page.
 * Reproducing the detail view inside the transcript would mean the chat
 * showing a copy of data that the tools are supposed to read fresh — the
 * snapshot problem, wearing a nicer layout.
 */

export function ContextReferenceCard({
  reference,
  className,
}: {
  reference: ApiContextReference;
  className?: string;
}) {
  const t = useT();
  const { providerLabel, icon: Icon } = presentationFor(reference.type);
  const unavailable = reference.unavailable === true;

  return (
    <div
      className={cn(
        "flex items-start gap-2.5 rounded-lg border border-(--color-border) bg-(--color-card) px-3 py-2",
        unavailable && "opacity-60",
        className,
      )}
      // The type and id are in the DOM because they are the semantics; a
      // test that asserted on the label alone would pass for a card showing
      // the right words about the wrong row.
      data-reference-type={reference.type}
      data-reference-id={reference.id}
    >
      <Icon className="mt-0.5 size-3.5 shrink-0 text-(--color-muted-foreground)" aria-hidden />
      <div className="min-w-0">
        <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
          {providerLabel}
        </p>
        <p className="truncate text-[12.5px] font-medium text-(--color-foreground)">
          {reference.label}
        </p>
        {unavailable ? (
          // Said plainly rather than by hiding the row. A conversation that
          // silently dropped its subject would leave the transcript talking
          // about something it no longer names.
          <p className="text-[11.5px] text-(--color-muted-foreground)">
            {t.app.modules.agents.chat.referenceGone}
          </p>
        ) : reference.subtitle ? (
          <p className="truncate text-[11.5px] text-(--color-muted-foreground)">
            {reference.subtitle}
          </p>
        ) : null}
      </div>
    </div>
  );
}

/**
 * The subjects of a conversation, drawn as a row of cards.
 *
 * Renders nothing at all when there are none, so a thread started from the
 * composer looks exactly as it did before this feature existed.
 */
export function ContextReferenceList({
  references,
  className,
}: {
  references: readonly ApiContextReference[] | undefined;
  className?: string;
}) {
  if (!references || references.length === 0) return null;
  return (
    <div className={cn("flex flex-wrap gap-2", className)}>
      {references.map((r) => (
        <ContextReferenceCard key={contextReferenceKey(r)} reference={r} />
      ))}
    </div>
  );
}
