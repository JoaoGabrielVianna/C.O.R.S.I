import { useEffect, useState, type FormEvent, type KeyboardEvent } from "react";
import { AnimatePresence, motion } from "framer-motion";
import { X } from "lucide-react";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/Button";
import { Input } from "@/components/ui/Input";
import { Label } from "@/components/ui/Label";
import { useT } from "@/lib/i18n";
import type { NewOpportunityInput } from "./store";

const ease = [0.16, 1, 0.3, 1] as const;

type Props = {
  open: boolean;
  onClose: () => void;
  onSubmit: (input: NewOpportunityInput) => void;
};

/**
 * AddOpportunityModal — manual feed entry.
 *
 * In the new mental model, opportunities will eventually be ingested by the
 * collector → normalizer pipeline. Manual add is the "I found this elsewhere
 * — record it" path. Source defaults to "manual"; the user can override
 * (linkedin, lever, greenhouse, …) so future normalizers can route accordingly.
 */
export function AddOpportunityModal({ open, onClose, onSubmit }: Props) {
  const t = useT();
  const labels = t.app.modules.jobRadar.form;

  const [company, setCompany] = useState("");
  const [role, setRole] = useState("");
  const [salary, setSalary] = useState("");
  const [location, setLocation] = useState("");
  const [stack, setStack] = useState<string[]>([]);
  const [stackDraft, setStackDraft] = useState("");
  const [description, setDescription] = useState("");
  const [source, setSource] = useState("manual");
  const [sourceUrl, setSourceUrl] = useState("");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey as unknown as EventListener);
    return () => window.removeEventListener("keydown", onKey as unknown as EventListener);
  }, [open, onClose]);

  const reset = () => {
    setCompany("");
    setRole("");
    setSalary("");
    setLocation("");
    setStack([]);
    setStackDraft("");
    setDescription("");
    setSource("manual");
    setSourceUrl("");
    setError(null);
  };

  const onStackKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key !== "Enter" && e.key !== ",") return;
    e.preventDefault();
    const v = stackDraft.trim().replace(/,$/, "");
    if (!v) return;
    if (stack.includes(v)) {
      setStackDraft("");
      return;
    }
    setStack([...stack, v]);
    setStackDraft("");
  };

  const handleSubmit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    if (!company.trim()) return setError(labels.errors.companyRequired);
    if (!role.trim()) return setError(labels.errors.roleRequired);
    onSubmit({
      companyName: company,
      role,
      salary,
      location,
      stack,
      description,
      source,
      sourceUrl: sourceUrl.trim() || undefined,
    });
    reset();
    onClose();
  };

  return (
    <AnimatePresence>
      {open ? (
        <div className="fixed inset-0 z-[100] flex items-start justify-center px-4 pt-[10vh] sm:pt-[14vh]">
          <motion.div
            initial={{ opacity: 0 }}
            animate={{ opacity: 1 }}
            exit={{ opacity: 0 }}
            transition={{ duration: 0.15 }}
            onClick={onClose}
            className="absolute inset-0 bg-black/45 backdrop-blur-sm"
            aria-hidden
          />
          <motion.div
            role="dialog"
            aria-modal="true"
            aria-label={labels.title}
            initial={{ opacity: 0, y: -12, scale: 0.98 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: -8, scale: 0.98 }}
            transition={{ duration: 0.2, ease }}
            className={cn(
              "relative w-full max-w-lg overflow-hidden rounded-2xl",
              "border border-(--color-border) bg-(--color-card) shadow-(--shadow-lift)",
            )}
          >
            <header className="flex items-center justify-between border-b border-(--color-border) px-5 py-3">
              <div>
                <p className="font-mono text-[10px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
                  Job Radar
                </p>
                <h2 className="mt-0.5 font-display text-[15px] font-semibold tracking-tight text-(--color-foreground)">
                  {labels.title}
                </h2>
                <p className="text-[11.5px] text-(--color-muted-foreground)">{labels.subtitle}</p>
              </div>
              <button
                type="button"
                onClick={onClose}
                aria-label={labels.cancel}
                className="flex size-7 items-center justify-center rounded-md text-(--color-muted-foreground) transition-colors hover:bg-(--color-muted) hover:text-(--color-foreground)"
              >
                <X className="size-3.5" />
              </button>
            </header>

            <form onSubmit={handleSubmit} className="space-y-3 px-5 py-4">
              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <Field label={labels.fields.company}>
                  <Input
                    value={company}
                    onChange={(e) => setCompany(e.target.value)}
                    placeholder={labels.placeholders.company}
                    autoFocus
                  />
                </Field>
                <Field label={labels.fields.role}>
                  <Input
                    value={role}
                    onChange={(e) => setRole(e.target.value)}
                    placeholder={labels.placeholders.role}
                  />
                </Field>
                <Field label={labels.fields.salary}>
                  <Input
                    value={salary}
                    onChange={(e) => setSalary(e.target.value)}
                    placeholder={labels.placeholders.salary}
                  />
                </Field>
                <Field label={labels.fields.location}>
                  <Input
                    value={location}
                    onChange={(e) => setLocation(e.target.value)}
                    placeholder={labels.placeholders.location}
                  />
                </Field>
              </div>

              <Field label={labels.fields.stack}>
                <Input
                  value={stackDraft}
                  onChange={(e) => setStackDraft(e.target.value)}
                  onKeyDown={onStackKey}
                  placeholder={labels.placeholders.stack}
                />
                {stack.length > 0 ? (
                  <div className="mt-2 flex flex-wrap gap-1.5">
                    {stack.map((s) => (
                      <button
                        key={s}
                        type="button"
                        onClick={() => setStack(stack.filter((x) => x !== s))}
                        className="inline-flex items-center gap-1 rounded-full border border-(--color-border) bg-(--color-muted) px-2 py-0.5 font-mono text-[10px] uppercase tracking-wide text-(--color-foreground) hover:border-rose-300 hover:bg-rose-50 hover:text-rose-700 dark:hover:border-rose-700 dark:hover:bg-rose-500/10 dark:hover:text-rose-300"
                      >
                        {s}
                        <X className="size-2.5" />
                      </button>
                    ))}
                  </div>
                ) : null}
              </Field>

              <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <Field label={labels.fields.source}>
                  <Input
                    value={source}
                    onChange={(e) => setSource(e.target.value)}
                    placeholder={labels.placeholders.source}
                  />
                </Field>
                <Field label={labels.fields.sourceUrl}>
                  <Input
                    value={sourceUrl}
                    onChange={(e) => setSourceUrl(e.target.value)}
                    placeholder={labels.placeholders.sourceUrl}
                    inputMode="url"
                  />
                </Field>
              </div>

              <Field label={labels.fields.description}>
                <textarea
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  placeholder={labels.placeholders.description}
                  rows={3}
                  className={cn(
                    "w-full rounded-xl border border-(--color-border) bg-(--color-card) px-3 py-2 text-[13px]",
                    "text-(--color-foreground) placeholder:text-(--color-muted-foreground)",
                    "shadow-(--shadow-soft) outline-none resize-y",
                    "transition-[border-color,box-shadow] duration-[250ms]",
                    "focus:border-(--color-brand-500) focus:ring-2 focus:ring-(--color-brand-500)/20",
                  )}
                />
              </Field>

              {error ? (
                <p className="flex items-start gap-2 rounded-lg border border-rose-200 bg-rose-50 px-3 py-2 text-[12.5px] text-rose-700 dark:border-rose-500/30 dark:bg-rose-500/10 dark:text-rose-300">
                  {error}
                </p>
              ) : null}

              <div className="flex items-center justify-end gap-2 pt-1">
                <Button type="button" variant="ghost" onClick={onClose}>
                  {labels.cancel}
                </Button>
                <Button type="submit">{labels.submit}</Button>
              </div>
            </form>
          </motion.div>
        </div>
      ) : null}
    </AnimatePresence>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label className="text-[11.5px] text-(--color-muted-foreground)">{label}</Label>
      {children}
    </div>
  );
}
