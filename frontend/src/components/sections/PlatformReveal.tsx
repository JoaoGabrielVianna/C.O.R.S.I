import { motion } from "framer-motion";
import { BrowserFrame } from "@/components/mocks/BrowserFrame";
import { DashboardMockup } from "@/components/mocks/DashboardMockup";
import { useT } from "@/lib/i18n";

const ease = [0.16, 1, 0.3, 1] as const;

export function PlatformReveal() {
  const t = useT();
  return (
    <section
      id="platform"
      className="relative scroll-mt-24 overflow-hidden py-24 md:py-28"
    >
      <div className="pointer-events-none absolute left-1/2 top-32 -z-10 size-[640px] -translate-x-1/2 rounded-full bg-(--color-brand-200)/40 blur-3xl dark:bg-(--color-brand-700)/15" />

      <div className="container-page">
        <div className="mx-auto max-w-2xl text-center">
          <span className="eyebrow">{t.platform.eyebrow}</span>
          <h2 className="mt-4 font-display text-4xl font-bold tracking-tight text-(--color-foreground) md:text-5xl">
            {t.platform.title}
            <br />
            <span className="text-(--color-muted-foreground)">{t.platform.titleTail}</span>
          </h2>
          <p className="mt-5 text-lg leading-relaxed text-(--color-muted-foreground)">
            {t.platform.body}
          </p>
        </div>

        <motion.div
          initial={{ opacity: 0, y: 30 }}
          whileInView={{ opacity: 1, y: 0 }}
          viewport={{ once: true, margin: "-80px" }}
          transition={{ duration: 0.8, ease }}
          className="mt-14 mx-auto max-w-6xl"
        >
          <BrowserFrame url="app.corsi.dev/lab">
            <DashboardMockup />
          </BrowserFrame>
        </motion.div>
      </div>
    </section>
  );
}
