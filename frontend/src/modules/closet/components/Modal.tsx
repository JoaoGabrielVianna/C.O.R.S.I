import { useEffect, type ReactNode } from "react";
import { AnimatePresence, motion } from "framer-motion";
import { X } from "lucide-react";

import { cn } from "@/lib/utils";

const ease = [0.16, 1, 0.3, 1] as const;

/**
 * The module's one modal shell.
 *
 * ── Why the Closet has its own instead of reusing Job Radar's ──────────
 * Because Job Radar's is not a shell — it is a form with a dialog wrapped
 * around it, and extracting it would be a refactor of another module in a
 * sprint that is not allowed to do that. This is the same markup, the same
 * animation and the same escape handling, factored so the three dialogs
 * here share one copy rather than three.
 *
 * ── Why closing does not reset anything ────────────────────────────────
 * The caller owns the state. A dialog that cleared its own fields on close
 * would mean a misclick on the backdrop destroys a half-entered garment —
 * and the caller is the only party that knows whether that is what closing
 * should mean.
 */
export function Modal({
  open,
  onClose,
  eyebrow,
  title,
  description,
  closeLabel,
  children,
  wide,
}: {
  open: boolean;
  onClose: () => void;
  eyebrow: string;
  title: string;
  description?: string;
  closeLabel: string;
  children: ReactNode;
  wide?: boolean;
}) {
  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  return (
    <AnimatePresence>
      {open ? (
        <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto p-4 sm:p-8">
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.15 }}
            onClick={onClose}
            className="fixed inset-0 bg-black/45 backdrop-blur-sm"
            aria-hidden
          />
          <motion.div
            role="dialog"
            aria-modal="true"
            aria-label={title}
            initial={{ opacity: 0, y: -12, scale: 0.98 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: -8, scale: 0.98 }}
            transition={{ duration: 0.2, ease }}
            className={cn(
              "relative w-full overflow-hidden rounded-2xl",
              "border border-(--color-border) bg-(--color-card) shadow-(--shadow-lift)",
              wide ? "max-w-2xl" : "max-w-lg",
            )}
          >
            <header className="flex items-start justify-between gap-3 border-b border-(--color-border) px-5 py-3">
              <div className="min-w-0">
                <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
                  {eyebrow}
                </p>
                <h2 className="mt-0.5 font-display text-[15px] font-semibold tracking-tight text-(--color-foreground)">
                  {title}
                </h2>
                {description ? (
                  <p className="text-[11.5px] text-(--color-muted-foreground)">{description}</p>
                ) : null}
              </div>
              <button
                type="button"
                onClick={onClose}
                aria-label={closeLabel}
                className="flex size-7 shrink-0 items-center justify-center rounded-md text-(--color-muted-foreground) transition-colors hover:bg-(--color-muted) hover:text-(--color-foreground)"
              >
                <X className="size-3.5" />
              </button>
            </header>
            {children}
          </motion.div>
        </div>
      ) : null}
    </AnimatePresence>
  );
}
