import type { SVGProps } from "react";
import { LogoLockup } from "@/components/ui/Logo";
import { LanguageSwitcher } from "@/components/ui/LanguageSwitcher";
import { ThemeToggle } from "@/components/ui/ThemeToggle";
import { GithubIcon } from "@/components/ui/BrandIcons";
import { useT } from "@/lib/i18n";

const LinkedinIcon = (props: SVGProps<SVGSVGElement>) => (
  <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden {...props}>
    <path d="M20.45 20.45h-3.55v-5.57c0-1.33-.02-3.04-1.85-3.04-1.86 0-2.14 1.45-2.14 2.95v5.66H9.36V9h3.41v1.56h.05c.47-.9 1.64-1.85 3.37-1.85 3.6 0 4.27 2.37 4.27 5.46v6.28zM5.34 7.43a2.06 2.06 0 1 1 0-4.13 2.06 2.06 0 0 1 0 4.13zM7.12 20.45H3.56V9h3.56v11.45zM22.22 0H1.77C.79 0 0 .77 0 1.72v20.56C0 23.23.79 24 1.77 24h20.45c.98 0 1.78-.77 1.78-1.72V1.72C24 .77 23.2 0 22.22 0z" />
  </svg>
);

const TwitterIcon = (props: SVGProps<SVGSVGElement>) => (
  <svg viewBox="0 0 24 24" fill="currentColor" aria-hidden {...props}>
    <path d="M18.244 2.25h3.308l-7.227 8.26 8.502 11.24H16.17l-5.214-6.817L4.99 21.75H1.68l7.73-8.835L1.254 2.25H8.08l4.713 6.231zm-1.161 17.52h1.833L7.084 4.126H5.117L17.083 19.77z" />
  </svg>
);

export function Footer() {
  const t = useT();

  type FooterItem = { label: string; href: string; external?: boolean };
  const columns: Array<{ title: string; items: FooterItem[] }> = [
    {
      title: t.footer.columns.lab,
      items: [
        { label: t.footer.links.manifesto, href: "#manifesto" },
        { label: t.footer.links.modules, href: "#modules" },
        { label: t.footer.links.labNotes, href: "#lab-notes" },
      ],
    },
    {
      title: t.footer.columns.build,
      items: [
        { label: t.footer.links.github,    href: "https://github.com/joaocorsi", external: true },
        { label: t.footer.links.roadmap,   href: "https://github.com/joaocorsi", external: true },
        { label: t.footer.links.changelog, href: "#lab-notes" },
      ],
    },
    {
      title: t.footer.columns.author,
      items: [
        { label: t.footer.links.linkedin, href: "https://linkedin.com", external: true },
        { label: t.footer.links.twitter,  href: "https://x.com",        external: true },
        { label: t.footer.links.email,    href: "mailto:joao@corsi.dev" },
      ],
    },
  ];

  return (
    <footer className="border-t border-(--color-border) bg-(--color-card)">
      <div className="container-page py-16">
        <div className="grid gap-10 lg:grid-cols-[1.4fr_1fr_1fr_1fr]">
          <div>
            <LogoLockup />
            <p className="mt-4 max-w-xs text-sm leading-relaxed text-(--color-muted-foreground)">
              {t.footer.tagline}
            </p>
            <div className="mt-6 flex flex-wrap gap-1.5">
              {t.footer.seals.map((s) => (
                <span
                  key={s}
                  className="rounded-full border border-(--color-border) bg-(--color-background) px-2.5 py-0.5 font-mono text-[10px] text-(--color-muted-foreground)"
                >
                  {s}
                </span>
              ))}
            </div>
            <div className="mt-6 flex items-center gap-2 sm:hidden">
              <ThemeToggle />
              <LanguageSwitcher />
            </div>
          </div>

          {columns.map((c) => (
            <div key={c.title}>
              <p className="text-xs font-semibold uppercase tracking-[0.18em] text-(--color-muted-foreground)">
                {c.title}
              </p>
              <ul className="mt-4 space-y-2.5">
                {c.items.map((i) => (
                  <li key={i.label}>
                    <a
                      href={i.href}
                      target={i.external ? "_blank" : undefined}
                      rel={i.external ? "noreferrer noopener" : undefined}
                      className="text-sm text-(--color-foreground)/85 transition-colors hover:text-(--color-brand-600) dark:hover:text-(--color-brand-400)"
                    >
                      {i.label}
                    </a>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>

        <div className="mt-14 flex flex-col items-start justify-between gap-4 border-t border-(--color-border) pt-6 text-sm text-(--color-muted-foreground) md:flex-row md:items-center">
          <div>
            <p>© {new Date().getFullYear()} João Corsi · São Paulo</p>
            <p className="mt-1 font-mono text-xs">
              <a href="#" className="hover:text-(--color-brand-600) dark:hover:text-(--color-brand-400)">{t.footer.legal.terms}</a>{" · "}
              <a href="#" className="hover:text-(--color-brand-600) dark:hover:text-(--color-brand-400)">{t.footer.legal.privacy}</a>{" · "}
              <a href="#" className="hover:text-(--color-brand-600) dark:hover:text-(--color-brand-400)">{t.footer.legal.lgpd}</a>
            </p>
          </div>
          <div className="flex items-center gap-2">
            {[
              { Icon: LinkedinIcon, href: "https://linkedin.com",         label: t.footer.links.linkedin },
              { Icon: GithubIcon,   href: "https://github.com/joaocorsi", label: t.footer.links.github },
              { Icon: TwitterIcon,  href: "https://x.com",                label: t.footer.links.twitter },
            ].map(({ Icon, href, label }) => (
              <a
                key={label}
                href={href}
                target="_blank"
                rel="noreferrer noopener"
                aria-label={label}
                className="flex size-9 items-center justify-center rounded-lg border border-(--color-border) bg-(--color-background) text-(--color-muted-foreground) transition-colors hover:border-(--color-brand-200) hover:bg-(--color-brand-50) hover:text-(--color-brand-600) dark:hover:border-(--color-brand-700) dark:hover:bg-(--color-brand-500)/10 dark:hover:text-(--color-brand-400)"
              >
                <Icon className="size-4" />
              </a>
            ))}
          </div>
        </div>
      </div>
    </footer>
  );
}
