import { SectionCard } from "@/components/workspace";
import { useT } from "@/lib/i18n";
import {
  ApiGlyph,
  ComingSoonCard,
  GitHubCard,
  MetaThreadsCard,
  GmailGlyph,
  TelegramCard,
  WhatsappGlyph,
} from "@/modules/integrations";

export function IntegrationsSettingsPage() {
  const t = useT();
  const i = t.app.settings.integrations;

  return (
    <div className="space-y-8">
      {/* No page header: the settings shell draws one for the whole area.
          This page used to draw a second one under the same eyebrow, so
          the word "Settings" appeared twice above a single card list. */}
      <header className="space-y-1">
        <h2 className="font-display text-[15px] font-semibold tracking-tight text-(--color-foreground)">
          {i.title}
        </h2>
        <p className="text-[13px] text-(--color-muted-foreground)">{i.description}</p>
      </header>

      {/*
        GitHub is the first integration on this page that is real: a stored,
        encrypted credential and an authorization the backend enforces. It
        gets its own section rather than sitting beside the Telegram
        prototype, because a page that presented a working connector and a
        simulated one under the same heading would be describing itself
        dishonestly — which is the one thing this codebase refuses
        everywhere else.
      */}
      <section className="space-y-3">
        <SectionLabel>{t.app.settings.github.connected}</SectionLabel>
        <GitHubCard />
      </section>

      {/*
        Meta Threads sits beside GitHub rather than under "active" for the
        same reason GitHub does: it is a real connector with a real,
        encrypted credential and a backend that enforces it. The Telegram
        card below is a prototype, and a page that presented the three under
        one heading would be describing itself dishonestly.

        It is the EXTERNAL social network. The operator's internal content
        pipeline — C.O.R.S.I. Threads — has no screen at all, by decision,
        and nothing on this page reads or writes it.
      */}
      <section className="space-y-3">
        <SectionLabel>{i.sectionExternal}</SectionLabel>
        <MetaThreadsCard />
      </section>

      <section className="space-y-3">
        <SectionLabel>{i.sectionActive}</SectionLabel>
        <TelegramCard />
      </section>

      <section className="space-y-3">
        <SectionLabel>{i.sectionSoon}</SectionLabel>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <ComingSoonCard
            icon={WhatsappGlyph}
            title={i.whatsapp.title}
            tagline={i.whatsapp.tagline}
            label={i.comingSoon}
          />
          <ComingSoonCard
            icon={GmailGlyph}
            title={i.gmail.title}
            tagline={i.gmail.tagline}
            label={i.comingSoon}
          />
          <ComingSoonCard
            icon={ApiGlyph}
            title={i.apikeys.title}
            tagline={i.apikeys.tagline}
            label={i.future}
          />
        </div>
      </section>

      {/*
        Single-workspace MVP — every Finance API call carries the dev
        sentinel `X-Workspace-Id` (see `src/lib/api/workspace.ts`).
        Multi-workspace ships when external auth lands and there is a
        real workspace selector in the auth context.
      */}
      <SectionCard title={t.app.settings.workspace.title} description={t.app.settings.workspace.description}>
        <div className="flex items-start gap-3 rounded-lg border border-(--color-border) bg-(--color-muted)/40 px-3.5 py-3">
          <span
            aria-hidden
            className="mt-0.5 inline-flex size-2 shrink-0 rounded-full bg-amber-500"
          />
          <div className="min-w-0 space-y-1">
            <p className="text-[13px] font-medium text-(--color-foreground)">
              {t.app.settings.workspace.disabledLabel}
            </p>
            <p className="text-[12.5px] text-(--color-muted-foreground)">
              {t.app.settings.workspace.disabledBody}
            </p>
          </div>
        </div>
      </SectionCard>
    </div>
  );
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <p className="font-mono text-[10.5px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
      {children}
    </p>
  );
}
