import { motion, useReducedMotion } from "framer-motion";
import { ArrowRight, Layers } from "lucide-react";
import { Link } from "react-router-dom";
import { Button } from "@/components/ui/Button";
import { useMouseParallax } from "@/lib/useMouseParallax";
import { BrowserFrame } from "@/components/mocks/BrowserFrame";
import { DashboardMockup } from "@/components/mocks/DashboardMockup";
import { useT } from "@/lib/i18n";

const ease = [0.16, 1, 0.3, 1] as const;

const wordVariants = {
  hidden: { opacity: 0, y: 18 },
  show: { opacity: 1, y: 0, transition: { duration: 0.55, ease } },
};

export function Hero() {
  const t = useT();
  const reduced = useReducedMotion();
  const mockup = useMouseParallax({ intensity: 8, smoothing: 0.08 });

  return (
    <section
      id="top"
      className="relative isolate overflow-hidden bg-(--color-slate-950) pt-28 pb-24 text-white lg:pt-36 lg:pb-32"
    >
      {/* Ambient blobs */}
      <div className="pointer-events-none absolute -left-32 top-20 size-[480px] rounded-full bg-(--color-brand-600)/40 blur-3xl animate-blob" />
      <div className="pointer-events-none absolute right-[-8%] top-[28%] size-[420px] rounded-full bg-sky-500/12 blur-3xl animate-blob" />
      <div className="pointer-events-none absolute bottom-[-20%] left-1/3 size-[520px] rounded-full bg-(--color-brand-800)/30 blur-3xl animate-blob" />

      {/* Dot grid + top hairline */}
      <div className="pointer-events-none absolute inset-0 dot-grid-dark opacity-40" />
      <div className="pointer-events-none absolute inset-x-0 top-0 h-px bg-gradient-to-r from-transparent via-white/15 to-transparent" />

      <div className="container-page relative">
        <div className="grid items-center gap-12 lg:grid-cols-[1.05fr_1fr] lg:gap-16">
          {/* Copy */}
          <motion.div
            initial="hidden"
            animate="show"
            variants={{ hidden: {}, show: { transition: { staggerChildren: 0.05 } } }}
            className="max-w-xl"
          >
            <motion.div variants={wordVariants}>
              <span className="inline-flex items-center gap-2 rounded-full border border-white/10 bg-white/[0.05] px-3 py-1 font-mono text-[11px] uppercase tracking-[0.16em] text-white/75 backdrop-blur">
                <span className="relative inline-flex size-1.5">
                  <span className="absolute inset-0 animate-pulse-soft rounded-full bg-(--color-brand-400)" />
                  <span className="relative inline-flex size-1.5 rounded-full bg-(--color-brand-500)" />
                </span>
                {t.hero.badge}
              </span>
            </motion.div>

            <h1 className="mt-7 font-display text-5xl font-bold leading-[1.04] tracking-tight text-white md:text-6xl lg:text-7xl">
              <motion.span
                variants={wordVariants}
                className="inline-block whitespace-pre text-brand-gradient"
              >
                {t.hero.title}
              </motion.span>
            </h1>

            <motion.p
              variants={{
                hidden: { opacity: 0, y: 12 },
                show: { opacity: 1, y: 0, transition: { duration: 0.6, ease, delay: 0.45 } },
              }}
              className="mt-6 max-w-lg font-display text-xl font-semibold leading-snug tracking-tight text-white md:text-2xl"
            >
              {t.hero.subtitle}
            </motion.p>

            <motion.p
              variants={{
                hidden: { opacity: 0, y: 12 },
                show: { opacity: 1, y: 0, transition: { duration: 0.6, ease, delay: 0.55 } },
              }}
              className="mt-5 max-w-lg text-base leading-relaxed text-white/65 md:text-lg"
            >
              {t.hero.body}
            </motion.p>

            <motion.div
              variants={{
                hidden: { opacity: 0, y: 12 },
                show: { opacity: 1, y: 0, transition: { duration: 0.6, ease, delay: 0.7 } },
              }}
              className="mt-9 flex flex-wrap items-center gap-3"
            >
              <Button asChild size="lg" className="shadow-(--shadow-glow) shine-on-hover">
                <Link to="/login">
                  {t.hero.ctaPrimary} <ArrowRight />
                </Link>
              </Button>
              <Button
                asChild
                size="lg"
                variant="outline"
                className="border-white/15 bg-white/5 text-white hover:bg-white/10 hover:text-white"
              >
                <a href="#modules">
                  <Layers className="size-4" /> {t.hero.ctaSecondary}
                </a>
              </Button>
            </motion.div>
          </motion.div>

          {/* Mockup */}
          <motion.div
            initial={{ opacity: 0, y: 30, scale: 0.96 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            transition={{ duration: 0.9, delay: 0.2, ease }}
            className="relative"
          >
            <motion.div
              style={reduced ? undefined : { x: mockup.x, y: mockup.y }}
              className="relative mx-auto w-full max-w-[720px]"
            >
              <div
                style={{
                  transform: "perspective(2000px) rotateY(-6deg) rotateX(4deg)",
                  transformStyle: "preserve-3d",
                }}
              >
                <BrowserFrame url="app.corsi.dev/lab" className="ring-1 ring-white/10">
                  <DashboardMockup />
                </BrowserFrame>
                <div className="pointer-events-none absolute -bottom-10 left-1/2 -z-10 h-24 w-3/4 -translate-x-1/2 rounded-full bg-(--color-brand-500)/20 blur-3xl" />
              </div>
            </motion.div>
          </motion.div>
        </div>

        {/* Stats band — personal metrics */}
        <motion.div
          initial={{ opacity: 0, y: 24 }}
          whileInView={{ opacity: 1, y: 0 }}
          viewport={{ once: true, margin: "-40px" }}
          transition={{ duration: 0.7, ease, delay: 0.05 }}
          className="mt-20 grid grid-cols-2 gap-px overflow-hidden rounded-2xl bg-white/10 ring-1 ring-white/10 sm:grid-cols-4"
        >
          {t.hero.stats.map((s) => (
            <div
              key={s.l}
              className="bg-(--color-slate-950)/80 px-6 py-5 backdrop-blur"
            >
              <p className="font-display text-3xl font-bold tracking-tight text-white tabular md:text-[2rem]">
                {s.v}
              </p>
              <p className="mt-1 text-xs uppercase tracking-wider text-white/55">
                {s.l}
              </p>
            </div>
          ))}
        </motion.div>
      </div>

      {/* Gradient fade to next section */}
      <div className="pointer-events-none absolute inset-x-0 bottom-0 h-24 bg-gradient-to-b from-transparent to-(--color-background)" />
    </section>
  );
}
