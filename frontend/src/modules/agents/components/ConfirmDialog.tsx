import { useEffect, useRef } from "react";
import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { useT } from "@/lib/i18n";

/**
 * The one modal in this module: a destructive act asking to be confirmed.
 *
 * The module resolves every other overlapping surface by replacing content
 * in place, and that stays true — a confirmation is the exception because
 * it must interrupt. Deleting a thread used to fire straight off a hover-
 * only trash icon, which is the cheapest possible way to lose work.
 *
 * `subject` is rendered verbatim so the dialog names the exact row it will
 * remove. "Apagar esta conversa?" is not a confirmation; it is a coin toss
 * about which row the pointer was over.
 *
 * Cancelling does nothing, including on Escape and on the backdrop. Only
 * the confirm button acts.
 */
export function ConfirmDialog({
  open,
  title,
  subject,
  description,
  confirmLabel,
  busy,
  error,
  onConfirm,
  onCancel,
}: {
  open: boolean;
  title: string;
  /** The exact thing being removed, shown quoted. */
  subject?: string;
  description?: string;
  /** Defaults to the dictionary's delete label when omitted. */
  confirmLabel?: string;
  busy?: boolean;
  error?: string | null;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  const t = useT();
  const cancelRef = useRef<HTMLButtonElement>(null);

  // Escape cancels, and focus lands on the safe button — a confirmation
  // whose destructive action is one Enter away is a trap.
  useEffect(() => {
    if (!open) return;
    cancelRef.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onCancel]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-[60] flex items-center justify-center p-4">
      <div
        aria-hidden
        onClick={onCancel}
        className="absolute inset-0 bg-black/50 backdrop-blur-sm"
      />
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="confirm-title"
        className="relative w-full max-w-sm rounded-2xl border border-(--color-border) bg-(--color-card) p-5 shadow-(--shadow-card)"
      >
        <div className="flex items-start gap-3">
          <span className="flex size-9 shrink-0 items-center justify-center rounded-xl bg-(--color-destructive)/10 text-(--color-destructive)">
            <AlertTriangle className="size-4" />
          </span>
          <div className="min-w-0">
            <h2 id="confirm-title" className="text-sm font-semibold text-(--color-foreground)">
              {title}
            </h2>
            {subject ? (
              <p className="mt-1 break-words text-[13px] font-medium text-(--color-foreground)">
                “{subject}”
              </p>
            ) : null}
            {description ? (
              <p className="mt-1.5 text-[12px] leading-relaxed text-(--color-muted-foreground)">
                {description}
              </p>
            ) : null}
          </div>
        </div>

        {error ? (
          <p className="mt-3 text-[12px] leading-relaxed text-(--color-destructive)">{error}</p>
        ) : null}

        <div className="mt-4 flex items-center justify-end gap-2">
          <Button ref={cancelRef} size="sm" variant="ghost" onClick={onCancel} disabled={busy}>
            {t.app.modules.agents.confirmDialog.cancel}
          </Button>
          <Button size="sm" variant="destructive" onClick={onConfirm} disabled={busy}>
            {busy
              ? t.app.modules.agents.confirmDialog.busy
              : (confirmLabel ?? t.app.modules.agents.confirmDialog.defaultConfirm)}
          </Button>
        </div>
      </div>
    </div>
  );
}
