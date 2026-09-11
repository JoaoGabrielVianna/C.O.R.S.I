import { AlertTriangle, Globe, Unplug } from "lucide-react";

import { useT } from "@/lib/i18n";
import type { ReadReceipt } from "@/modules/agents/api/stream";

/**
 * Whether this turn actually read anything outside the product.
 *
 * ── Why this component exists ──────────────────────────────────────────
 * A live content agent answered "1.535 seguidores, 40.055 views" in a turn
 * that called nothing. The real figures were 163 and 224. Every layer
 * behaved — the gate allowed a call that was never made, the tool worked,
 * the audit recorded that nothing ran — and the person read invented
 * numbers because the only thing on screen was the model's sentence.
 *
 * So the sentence stops being the only thing on screen. This renders the
 * receipt and nothing else: it never reads the message text, and there is
 * no path through it by which prose can produce a verification.
 *
 * ── Why the asymmetry is INVERTED here ─────────────────────────────────
 * `WriteReceiptStrip` shows a confirmed change and stays silent otherwise,
 * because a badge on every unchanged turn would be noise.
 *
 * Reads are the opposite case, and copying that rule would have reproduced
 * the bug. The dangerous state is the ABSENCE of a read, not its presence:
 * a turn that says "you have 163 followers" having read nothing looks
 * exactly like one that read. So silence is what must be labelled, and the
 * label is shown whenever this agent COULD have read externally.
 *
 * ── Why `available` gates it ───────────────────────────────────────────
 * An agent with no external capability produces NO_EXTERNAL_READ on every
 * turn, correctly and uselessly. Showing it there would train people to
 * ignore the badge in the one place it matters.
 */
export function ExternalReadStrip({ receipt }: { receipt?: ReadReceipt }) {
  const t = useT();
  const copy = t.app.modules.agents.readReceipt;
  if (!receipt) return null;

  // Nothing to say for an agent that cannot reach outside at all — unless
  // it somehow did, which is worth showing regardless.
  if (!receipt.available && receipt.reads.length === 0) return null;

  const sources = [...new Set(receipt.reads.map((r) => r.source))].join(", ");
  const capabilities = receipt.reads.map((r) => r.capability).join(", ");

  if (receipt.status === "VERIFIED_EXTERNAL_READ") {
    return (
      <Badge title={capabilities} tone="verified">
        <Globe aria-hidden className="size-3" />
        {copy.verified(receipt.verified, sources)}
        {receipt.failed > 0 ? ` · ${copy.alsoFailed(receipt.failed)}` : ""}
      </Badge>
    );
  }

  if (receipt.status === "FAILED_EXTERNAL_READ") {
    return (
      <Badge title={capabilities} tone="warning">
        <Unplug aria-hidden className="size-3" />
        {copy.failed(receipt.failed)}
      </Badge>
    );
  }

  // NO_EXTERNAL_READ, on an agent that could have read. THE case.
  return (
    <Badge tone="warning">
      <AlertTriangle aria-hidden className="size-3" />
      {copy.none}
    </Badge>
  );
}

function Badge({
  children,
  title,
  tone,
}: {
  children: React.ReactNode;
  title?: string;
  tone: "verified" | "warning";
}) {
  return (
    <div className="mt-2 flex flex-wrap items-center gap-2 text-xs">
      <span
        title={title}
        data-testid="external-read-badge"
        data-tone={tone}
        className={
          tone === "verified"
            ? "inline-flex items-center gap-1 rounded-full border border-(--color-border) px-2 py-0.5 text-(--color-muted-foreground)"
            : "inline-flex items-center gap-1 rounded-full border border-amber-400/40 bg-amber-400/10 px-2 py-0.5 text-(--color-foreground)"
        }
      >
        {children}
      </span>
    </div>
  );
}
