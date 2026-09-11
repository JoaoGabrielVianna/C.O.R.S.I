import { motion } from "framer-motion";
import { useT } from "@/lib/i18n";

const ease = [0.16, 1, 0.3, 1] as const;

export function LabNotes() {
  const t = useT();
  return (
    <section
      id="lab-notes"
      className="relative scroll-mt-24 bg-(--color-card) py-24 md:py-28 border-y border-(--color-border)"
    >
      <div className="container-page">
        <div className="mx-auto max-w-2xl">
          <span className="eyebrow">{t.labNotes.eyebrow}</span>
          <h2 className="mt-4 font-display text-4xl font-bold tracking-tight text-(--color-foreground) md:text-5xl">
            {t.labNotes.title}
          </h2>
          <p className="mt-5 text-lg leading-relaxed text-(--color-muted-foreground)">
            {t.labNotes.body}
          </p>
        </div>

        <ol className="mx-auto mt-14 max-w-3xl">
          {t.labNotes.entries.map((entry, i) => (
            <motion.li
              key={entry.date + entry.text.slice(0, 20)}
              initial={{ opacity: 0, x: -8 }}
              whileInView={{ opacity: 1, x: 0 }}
              viewport={{ once: true, margin: "-40px" }}
              transition={{ duration: 0.45, ease, delay: i * 0.05 }}
              className="relative grid grid-cols-[88px_1fr] items-start gap-4 border-l border-(--color-border) pl-6 pb-8 last:pb-0"
            >
              <span
                aria-hidden
                className="absolute -left-1.5 top-1.5 size-3 rounded-full border-2 border-(--color-background) bg-(--color-brand-500)"
              />
              <div>
                <p className="font-mono text-[11px] uppercase tracking-[0.18em] text-(--color-muted-foreground)">
                  {entry.date}
                </p>
                <p className="mt-1 text-[11px] font-semibold uppercase tracking-[0.12em] text-(--color-brand-600)">
                  {entry.module}
                </p>
              </div>
              <p className="text-[15px] leading-relaxed text-(--color-foreground)/90">
                {entry.text}
              </p>
            </motion.li>
          ))}
        </ol>
      </div>
    </section>
  );
}
