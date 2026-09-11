import { useEffect, useRef, useState } from "react";
import { Brain } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { cn } from "@/lib/utils";
import { MAX_MEMORY_CHARS } from "@/modules/agents/api/memories";
import { useT } from "@/lib/i18n";

/**
 * The one form a memory is written in — new or edited, from the Memory page
 * or from a message in the chat.
 *
 * ── Why the text is always editable ────────────────────────────────────
 * When this opens from a message it arrives pre-filled with that message.
 * A whole paragraph is almost never a good memory: the distilling is the
 * point, and it happens here. Pre-filling saves the typing; the edit is
 * what makes the result worth storing.
 *
 * ── Why a failure never closes it ──────────────────────────────────────
 * The error lands inline and the text stays exactly where it was. Losing
 * what someone wrote because a request failed is the one outcome a form
 * like this must never produce.
 *
 * ── Why it takes no `open` prop ────────────────────────────────────────
 * Callers mount it when it should be open and unmount it when it should
 * not. That is what makes `initialContent` a real initial value: the draft
 * is seeded once, on mount, and belongs to the user from then on — no
 * effect re-seeding it, and no risk of a re-render throwing away what was
 * typed after a failed save.
 */
export function MemoryDialog({
  title,
  agentName,
  initialContent = "",
  confirmLabel,
  busy,
  error,
  onConfirm,
  onCancel,
}: {
  title: string;
  /** Named so it is obvious *which* agent is about to remember this. */
  agentName: string;
  initialContent?: string;
  confirmLabel?: string;
  busy?: boolean;
  error?: string | null;
  onConfirm: (content: string) => void;
  onCancel: () => void;
}) {
  const t = useT();
  const [value, setValue] = useState(initialContent);
  const ref = useRef<HTMLTextAreaElement>(null);

  // Focus and caret placement: a DOM concern, which is what an effect is
  // for. The cursor lands at the end, ready to edit rather than to replace.
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.focus();
    el.setSelectionRange(el.value.length, el.value.length);
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onCancel();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onCancel]);

  const text = value.trim();
  const over = value.length > MAX_MEMORY_CHARS;
  const canSave = text.length > 0 && !over && !busy;

  return (
    <div className="fixed inset-0 z-[60] flex items-center justify-center p-4">
      <div aria-hidden onClick={onCancel} className="absolute inset-0 bg-black/45 backdrop-blur-sm" />
      <div
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className="relative w-full max-w-lg rounded-2xl border border-(--color-border) bg-(--color-card) p-4 shadow-(--shadow-card)"
      >
        <div className="flex items-start gap-2.5">
          <span className="mt-0.5 flex size-8 shrink-0 items-center justify-center rounded-xl bg-(--color-brand-50) text-(--color-brand-700)">
            <Brain className="size-4" />
          </span>
          <div className="min-w-0">
            <h2 className="text-sm font-semibold text-(--color-foreground)">{title}</h2>
            <p className="mt-0.5 text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
              {t.app.modules.agents.interp.memoryDialogScope.replace("{agent}", agentName)}
            </p>
          </div>
        </div>

        <textarea
          ref={ref}
          rows={4}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => {
            // Enter breaks the line here: a memory is often two sentences,
            // and this is a form, not a composer.
            if (e.key === "Enter" && (e.metaKey || e.ctrlKey) && canSave) {
              e.preventDefault();
              onConfirm(text);
            }
          }}
          className={cn(
            "mt-3 max-h-64 w-full resize-y rounded-xl border bg-(--color-background) px-3 py-2",
            "text-[13px] leading-relaxed text-(--color-foreground) outline-none",
            "focus:ring-2 focus:ring-(--color-brand-500)/20",
            over ? "border-(--color-destructive)" : "border-(--color-border) focus:border-(--color-brand-500)",
          )}
        />

        <div className="mt-1.5 flex items-center justify-between gap-3">
          <span
            className={cn(
              "font-mono text-[10.5px]",
              over ? "text-(--color-destructive)" : "text-(--color-muted-foreground)",
            )}
          >
            {value.length} / {MAX_MEMORY_CHARS}
          </span>
          <span className="text-[10.5px] text-(--color-muted-foreground)">{t.app.modules.agents.memory.dialog.saveHint}</span>
        </div>

        {error ? (
          <p className="mt-2 rounded-xl border border-(--color-destructive)/30 bg-(--color-destructive)/5 px-3 py-2 text-[11.5px] leading-relaxed text-(--color-destructive)">
            {error}
          </p>
        ) : null}

        <div className="mt-3 flex items-center justify-end gap-2">
          <Button size="sm" variant="ghost" onClick={onCancel} disabled={busy}>
            {t.app.modules.agents.memory.dialog.cancel}
          </Button>
          <Button size="sm" onClick={() => onConfirm(text)} disabled={!canSave}>
            {busy ? "Salvando…" : confirmLabel}
          </Button>
        </div>
      </div>
    </div>
  );
}
