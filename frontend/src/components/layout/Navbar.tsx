import { useEffect, useState } from "react";
import { motion } from "framer-motion";
import { Menu, X } from "lucide-react";
import { Link } from "react-router-dom";
import { Button } from "@/components/ui/Button";
import { LogoLockup } from "@/components/ui/Logo";
import { LanguageSwitcher } from "@/components/ui/LanguageSwitcher";
import { ThemeToggle } from "@/components/ui/ThemeToggle";
import { useT } from "@/lib/i18n";
import { cn } from "@/lib/utils";

const ease = [0.16, 1, 0.3, 1] as const;

type Item = { href: string; id: string; label: string };

function useActiveSection(ids: string[], rootMargin: number) {
  const [activeId, setActiveId] = useState<string | null>(null);
  useEffect(() => {
    if (typeof window === "undefined") return;
    const targets = ids
      .map((id) => document.getElementById(id))
      .filter((el): el is HTMLElement => !!el);
    if (targets.length === 0) return;
    const io = new IntersectionObserver(
      (entries) => {
        const visible = entries
          .filter((e) => e.isIntersecting)
          .sort((a, b) => b.intersectionRatio - a.intersectionRatio);
        if (visible[0]) setActiveId(visible[0].target.id);
      },
      { rootMargin: `-${rootMargin}px 0px -60% 0px`, threshold: [0, 0.25, 0.5, 0.75, 1] },
    );
    targets.forEach((t) => io.observe(t));
    return () => io.disconnect();
  }, [ids, rootMargin]);
  return activeId;
}

export function Navbar() {
  const t = useT();
  const [scrolled, setScrolled] = useState(false);
  const [open, setOpen] = useState(false);

  const menu: Item[] = [
    { href: "#manifesto", id: "manifesto", label: t.nav.manifesto },
    { href: "#modules",   id: "modules",   label: t.nav.modules },
    { href: "#lab-notes", id: "lab-notes", label: t.nav.labNotes },
  ];

  const activeId = useActiveSection(menu.map((m) => m.id), 120);

  useEffect(() => {
    const onScroll = () => setScrolled(window.scrollY > 80);
    onScroll();
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, []);

  return (
    <header
      className={cn(
        "fixed inset-x-0 top-0 z-50",
        "transition-[background-color,border-color,backdrop-filter] duration-300 [transition-timing-function:var(--ease-premium)]",
        scrolled
          ? "border-b border-(--color-border) bg-(--color-background)/80 backdrop-blur-xl"
          : "border-b border-transparent",
      )}
    >
      <div
        className={cn(
          "container-page flex items-center justify-between",
          "transition-[height] duration-300 [transition-timing-function:var(--ease-premium)]",
          scrolled ? "h-14" : "h-16",
        )}
      >
        <a href="#top" className="flex items-center gap-2.5" aria-label="C.O.R.S.I">
          <LogoLockup />
          <span
            className={cn(
              "hidden items-center gap-1.5 rounded-full px-2 py-0.5 font-mono text-[10px] uppercase tracking-[0.18em] sm:inline-flex",
              scrolled
                ? "border border-(--color-brand-300) bg-(--color-brand-50) text-(--color-brand-700) dark:bg-(--color-brand-500)/15 dark:text-(--color-brand-300)"
                : "border border-white/15 bg-white/5 text-white/75",
            )}
          >
            <span className="size-1 rounded-full bg-(--color-brand-500)" />
            {t.meta.status} · {t.meta.version}
          </span>
        </a>

        <nav className="hidden items-center gap-1 md:flex">
          {menu.map((m) => {
            const isActive = activeId === m.id;
            return (
              <a
                key={m.href}
                href={m.href}
                className={cn(
                  "relative rounded-lg px-3 py-1.5 text-sm font-medium transition-colors",
                  scrolled
                    ? isActive
                      ? "text-(--color-foreground)"
                      : "text-(--color-muted-foreground) hover:text-(--color-foreground)"
                    : isActive
                      ? "text-white"
                      : "text-white/75 hover:text-white",
                )}
              >
                {isActive ? (
                  <motion.span
                    layoutId="navbar-pill"
                    aria-hidden
                    className={cn(
                      "absolute inset-0 -z-10 rounded-lg",
                      scrolled ? "bg-(--color-muted)" : "bg-white/10",
                    )}
                    transition={{ duration: 0.4, ease }}
                  />
                ) : null}
                <span className="relative">{m.label}</span>
              </a>
            );
          })}
        </nav>

        <div className="flex items-center gap-2">
          <ThemeToggle
            className={cn(
              "hidden sm:inline-flex",
              !scrolled && "border-white/15 bg-white/5",
            )}
          />
          <LanguageSwitcher
            className={cn(
              "hidden sm:inline-flex",
              !scrolled && "border-white/15 bg-white/5",
            )}
          />
          <Button asChild size="sm">
            <Link to="/login">{t.nav.enter}</Link>
          </Button>
          <button
            type="button"
            aria-label={t.nav.manifesto}
            onClick={() => setOpen((s) => !s)}
            className={cn(
              "inline-flex size-9 items-center justify-center rounded-lg md:hidden",
              scrolled
                ? "text-(--color-muted-foreground) hover:bg-(--color-muted)"
                : "text-white/80 hover:bg-white/5",
            )}
          >
            {open ? <X className="size-5" /> : <Menu className="size-5" />}
          </button>
        </div>
      </div>

      {open ? (
        <motion.div
          initial={{ opacity: 0, y: -8 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.4, ease }}
          className="border-t border-(--color-border) bg-(--color-background) px-4 py-3 md:hidden"
        >
          <nav className="flex flex-col">
            {menu.map((m) => (
              <a
                key={m.href}
                href={m.href}
                onClick={() => setOpen(false)}
                className={cn(
                  "rounded-lg px-3 py-2.5 text-sm font-medium transition-colors",
                  activeId === m.id
                    ? "bg-(--color-muted) text-(--color-foreground)"
                    : "text-(--color-muted-foreground) hover:bg-(--color-muted) hover:text-(--color-foreground)",
                )}
              >
                {m.label}
              </a>
            ))}
            <Link
              to="/login"
              onClick={() => setOpen(false)}
              className="rounded-lg px-3 py-2.5 text-sm font-medium text-(--color-muted-foreground) hover:bg-(--color-muted)"
            >
              {t.nav.enter}
            </Link>
            <div className="mt-3 flex items-center gap-2 px-3">
              <ThemeToggle />
              <LanguageSwitcher />
            </div>
          </nav>
        </motion.div>
      ) : null}
    </header>
  );
}
