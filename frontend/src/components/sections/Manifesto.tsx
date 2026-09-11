import { motion } from "framer-motion";
import { useT } from "@/lib/i18n";

const ease = [0.16, 1, 0.3, 1] as const;

export function Manifesto() {
  const t = useT();
  return (
    <section
      id="manifesto"
      className="relative scroll-mt-24 py-28 md:py-36"
    >
      <div className="container-page">
        <div className="mx-auto max-w-2xl text-center">
          <motion.span
            initial={{ opacity: 0, y: 10 }}
            whileInView={{ opacity: 1, y: 0 }}
            viewport={{ once: true, margin: "-60px" }}
            transition={{ duration: 0.5, ease }}
            className="eyebrow"
          >
            {t.manifesto.eyebrow}
          </motion.span>

          <motion.h2
            initial={{ opacity: 0, y: 14 }}
            whileInView={{ opacity: 1, y: 0 }}
            viewport={{ once: true, margin: "-60px" }}
            transition={{ duration: 0.6, ease, delay: 0.06 }}
            className="mt-4 font-display text-4xl font-bold leading-[1.05] tracking-tight text-(--color-foreground) md:text-5xl lg:text-[3.25rem]"
          >
            {t.manifesto.title}{" "}
            <span className="text-(--color-muted-foreground)">
              {t.manifesto.titleTail}
            </span>
          </motion.h2>
        </div>

        <div className="mx-auto mt-12 max-w-2xl space-y-6 text-[16.5px] leading-relaxed text-(--color-muted-foreground) md:text-[17.5px]">
          {t.manifesto.paragraphs.map((p, i) => (
            <motion.p
              key={i}
              initial={{ opacity: 0, y: 12 }}
              whileInView={{ opacity: 1, y: 0 }}
              viewport={{ once: true, margin: "-60px" }}
              transition={{ duration: 0.55, ease, delay: 0.15 + i * 0.08 }}
            >
              {p}
            </motion.p>
          ))}
        </div>

        <motion.div
          initial={{ opacity: 0 }}
          whileInView={{ opacity: 1 }}
          viewport={{ once: true }}
          transition={{ duration: 0.6, ease, delay: 0.4 }}
          className="mx-auto mt-12 flex max-w-2xl items-center gap-3 text-(--color-muted-foreground)"
        >
          <span className="h-px flex-1 bg-(--color-border)" />
          <span className="font-mono text-[11px] uppercase tracking-[0.18em]">
            {t.manifesto.signoff}
          </span>
          <span className="h-px flex-1 bg-(--color-border)" />
        </motion.div>
      </div>
    </section>
  );
}
