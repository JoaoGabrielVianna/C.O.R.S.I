import { AlertTriangle, Check, ShieldOff, X } from "lucide-react";

import { useT } from "@/lib/i18n";
import type { WriteReceipt } from "@/modules/agents/api/stream";

/**
 * What the system knows this turn changed.
 *
 * ── Why this component exists ──────────────────────────────────────────
 * A live financial agent answered "8 transações importadas" in a turn that
 * called nothing, against a ledger holding zero transactions. Everything
 * about that turn was correct except what the person read, because the
 * only thing on screen was the model's sentence.
 *
 * So the sentence stops being the only thing on screen. This renders the
 * receipt and nothing else: it never reads the message text, and there is
 * no path through it by which prose can produce a confirmation.
 *
 * ── Why a turn that changed nothing renders nothing ────────────────────
 * Because most turns change nothing, and a badge saying so on every
 * answer would be noise that teaches people to stop reading badges. The
 * asymmetry is deliberate: a confirmed change is always shown, and its
 * absence is the default state of the interface.
 *
 * The claim the incident produced is therefore contradicted by omission —
 * the user sees no confirmation beside it — and any surface that wants to
 * be louder about that reads `executed` itself rather than the text.
 */
export function WriteReceiptStrip({ receipt }: { receipt?: WriteReceipt }) {
  const t = useT();
  if (!receipt) return null;
  const { executed, failed, refused } = receipt;
  if (executed === 0 && failed === 0 && refused === 0) return null;

  const capabilities = (status: WriteReceipt["writes"][number]["status"]) =>
    receipt.writes.filter((w) => w.status === status).map((w) => w.capability);

  return (
    <div className="mt-2 flex flex-wrap items-center gap-2 text-xs">
      {executed > 0 && (
        <span
          title={capabilities("EXECUTED").join(", ")}
          className="inline-flex items-center gap-1 rounded-full border border-(--color-border) px-2 py-0.5 text-(--color-muted-foreground)"
        >
          <Check aria-hidden className="size-3" />
          {t.app.modules.agents.receipt.executed(executed)}
        </span>
      )}
      {failed > 0 && (
        <span
          title={capabilities("FAILED").join(", ")}
          className="inline-flex items-center gap-1 rounded-full border border-(--color-border) px-2 py-0.5 text-(--color-muted-foreground)"
        >
          <X aria-hidden className="size-3" />
          {t.app.modules.agents.receipt.failed(failed)}
        </span>
      )}
      {refused > 0 && (
        <span
          title={capabilities("NOT_EXECUTED").join(", ")}
          className="inline-flex items-center gap-1 rounded-full border border-(--color-border) px-2 py-0.5 text-(--color-muted-foreground)"
        >
          <ShieldOff aria-hidden className="size-3" />
          {t.app.modules.agents.receipt.refused(refused)}
        </span>
      )}
      {executed === 0 && (failed > 0 || refused > 0) && (
        <span className="inline-flex items-center gap-1 text-(--color-muted-foreground)">
          <AlertTriangle aria-hidden className="size-3" />
          {t.app.modules.agents.receipt.nothingChanged}
        </span>
      )}
    </div>
  );
}
