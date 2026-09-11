import { useMemo } from "react";
import {
  Bookmark,
  ExternalLink,
  Inbox,
  Plus,
  Send,
} from "lucide-react";
import { cn } from "@/lib/utils";
import { useT } from "@/lib/i18n";
import type { JobRadarStore } from "./store";
import type { Opportunity } from "./types";
import { activeFormat } from "@/lib/i18n";

type Props = {
  store: JobRadarStore;
  onAddManually: () => void;
};

/**
 * DiscoverFeed — feed of untracked opportunities.
 *
 * Cards expose: company · role · location · salary · source · open-original ·
 * save · apply manually. No match score, no reject button, no curated rails.
 * Empty state is honest — the collector isn't connected yet.
 */
export function DiscoverFeed({ store, onAddManually }: Props) {
  const t = useT();
  const labels = t.app.modules.jobRadar.discover;
  const { state, openModal, moveStage, companyById } = store;

  const filtered = useMemo(() => {
    const f = state.filters;
    const q = f.query.trim().toLowerCase();
    const stackSet = new Set(f.stack.map((s) => s.toLowerCase()));
    return state.opportunities
      .filter((o) => o.tracking === null)
      .filter((o) => {
        if (q) {
          const co = companyById.get(o.companyId)?.name?.toLowerCase() ?? "";
          const hay = `${o.role} ${co} ${o.location} ${o.stack.join(" ")}`.toLowerCase();
          if (!hay.includes(q)) return false;
        }
        if (stackSet.size > 0 && !o.stack.some((s) => stackSet.has(s.toLowerCase()))) return false;
        if (f.country && !o.location.toLowerCase().includes(f.country.toLowerCase())) return false;
        if (f.workMode !== "any") {
          const probe = f.workMode === "onsite" ? "on-site" : f.workMode;
          if (!o.location.toLowerCase().includes(probe)) return false;
        }
        return true;
      })
      .sort((a, b) => b.postedAt - a.postedAt);
  }, [state.opportunities, state.filters, companyById]);

  const totalDiscover = state.opportunities.filter((o) => o.tracking === null).length;

  if (totalDiscover === 0) {
    return (
      <EmptyDiscover onAddManually={onAddManually} />
    );
  }

  if (filtered.length === 0) {
    return (
      <div className="flex h-full min-h-0 flex-col gap-3">
        <Toolbar shown={filtered.length} total={totalDiscover} onAddManually={onAddManually} />
        <div className="flex min-h-0 flex-1 items-center justify-center">
          <div className="flex flex-col items-center gap-2 rounded-2xl border border-dashed border-(--color-border) bg-(--color-card)/40 px-6 py-10 text-center">
            <span className="flex size-9 items-center justify-center rounded-full border border-(--color-border) bg-(--color-card) text-(--color-muted-foreground)">
              <Inbox className="size-4" />
            </span>
            <p className="text-sm font-medium text-(--color-foreground)">{labels.emptyFiltered.title}</p>
            <p className="max-w-sm text-[12.5px] text-(--color-muted-foreground)">{labels.emptyFiltered.body}</p>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="flex h-full min-h-0 flex-col gap-3">
      <Toolbar shown={filtered.length} total={totalDiscover} onAddManually={onAddManually} />
      <div className="grid min-h-0 flex-1 gap-3 overflow-y-auto pb-2 grid-cols-[repeat(auto-fit,minmax(280px,1fr))]">
        {filtered.map((o) => (
          <DiscoverCard
            key={o.id}
            opportunity={o}
            companyName={companyById.get(o.companyId)?.name ?? "—"}
            onOpen={() => openModal(o.id)}
            onSave={() => moveStage(o.id, "saved")}
            onApply={() => moveStage(o.id, "applied")}
          />
        ))}
      </div>
    </div>
  );
}

function Toolbar({
  shown,
  total,
  onAddManually,
}: {
  shown: number;
  total: number;
  onAddManually: () => void;
}) {
  const t = useT();
  return (
    <div className="flex shrink-0 flex-wrap items-center justify-between gap-2">
      <p className="font-mono text-[10.5px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
        {shown} / {total}
      </p>
      <button
        type="button"
        onClick={onAddManually}
        className="inline-flex items-center gap-1.5 rounded-md border border-(--color-border) bg-(--color-card) px-2 py-1 text-[11.5px] font-medium text-(--color-foreground) transition-colors hover:bg-(--color-muted)"
      >
        <Plus className="size-3" />
        {t.app.modules.jobRadar.addManuallyShort}
      </button>
    </div>
  );
}

function EmptyDiscover({ onAddManually }: { onAddManually: () => void }) {
  const t = useT();
  const labels = t.app.modules.jobRadar.discover.empty;
  return (
    <div className="flex min-h-[280px] items-center justify-center">
      <div className="flex flex-col items-center gap-3 rounded-2xl border border-dashed border-(--color-border) bg-(--color-card)/40 px-6 py-12 text-center">
        <span className="flex size-10 items-center justify-center rounded-full border border-(--color-border) bg-(--color-card) text-(--color-muted-foreground)">
          <Inbox className="size-4" />
        </span>
        <p className="text-sm font-medium text-(--color-foreground)">{labels.title}</p>
        <p className="max-w-md text-[13px] text-(--color-muted-foreground)">{labels.body}</p>
        <button
          type="button"
          onClick={onAddManually}
          className="mt-1 inline-flex items-center gap-1.5 rounded-lg border border-(--color-border) bg-(--color-card) px-3 py-1.5 text-[12.5px] font-medium text-(--color-foreground) shadow-(--shadow-soft) transition-colors hover:bg-(--color-muted)"
        >
          <Plus className="size-3.5" />
          {labels.cta}
        </button>
      </div>
    </div>
  );
}

// ── Card ────────────────────────────────────────────────────────────────

type DiscoverCardProps = {
  opportunity: Opportunity;
  companyName: string;
  onOpen: () => void;
  onSave: () => void;
  onApply: () => void;
};

function DiscoverCard({ opportunity, companyName, onOpen, onSave, onApply }: DiscoverCardProps) {
  const t = useT();
  const labels = t.app.modules.jobRadar.discover.card;

  return (
    <article
      onClick={onOpen}
      className={cn(
        "group flex h-full cursor-pointer flex-col rounded-2xl border border-(--color-border) bg-(--color-card) p-3.5 shadow-(--shadow-soft)",
        "transition-[border-color,transform,box-shadow] duration-[250ms] [transition-timing-function:var(--ease-premium)]",
        "hover:-translate-y-px hover:border-(--color-brand-300) hover:shadow-(--shadow-card) dark:hover:border-(--color-brand-700)",
      )}
    >
      <header className="flex items-start justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <CompanyAvatar name={companyName} />
          <div className="min-w-0">
            <p className="truncate text-[12.5px] font-medium text-(--color-foreground)">{companyName}</p>
            <p className="font-mono text-[10px] uppercase tracking-[0.14em] text-(--color-muted-foreground)">
              {labels.posted} · {formatDate(opportunity.postedAt)}
            </p>
          </div>
        </div>
        <span
          className="shrink-0 rounded-full border border-(--color-border) bg-(--color-muted) px-2 py-0.5 font-mono text-[9.5px] uppercase tracking-wide text-(--color-muted-foreground)"
          title={labels.source}
        >
          {opportunity.source}
        </span>
      </header>

      <h3 className="mt-2.5 line-clamp-2 font-display text-[14.5px] font-semibold leading-snug tracking-tight text-(--color-foreground)">
        {opportunity.role || "—"}
      </h3>

      {opportunity.salary || opportunity.location ? (
        <p className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-0.5 font-mono text-[10.5px] text-(--color-muted-foreground)">
          {opportunity.salary ? <span>{opportunity.salary}</span> : null}
          {opportunity.salary && opportunity.location ? <span aria-hidden>·</span> : null}
          {opportunity.location ? <span>{opportunity.location}</span> : null}
        </p>
      ) : null}

      {opportunity.stack.length > 0 ? (
        <div className="mt-2 flex flex-wrap gap-1">
          {opportunity.stack.slice(0, 5).map((s) => (
            <span
              key={s}
              className="rounded-full border border-(--color-border) bg-(--color-muted)/60 px-1.5 py-px font-mono text-[9.5px] uppercase tracking-wide text-(--color-muted-foreground)"
            >
              {s}
            </span>
          ))}
          {opportunity.stack.length > 5 ? (
            <span className="font-mono text-[9.5px] text-(--color-muted-foreground)">
              +{opportunity.stack.length - 5}
            </span>
          ) : null}
        </div>
      ) : null}

      <footer className="mt-3 flex items-center justify-between gap-1.5 border-t border-(--color-border) pt-2.5">
        {opportunity.sourceUrl ? (
          <a
            href={opportunity.sourceUrl}
            target="_blank"
            rel="noreferrer noopener"
            onClick={(e) => e.stopPropagation()}
            className="inline-flex h-7 items-center gap-1.5 rounded-md border border-(--color-border) bg-(--color-card) px-2 text-[11px] font-medium text-(--color-muted-foreground) transition-colors hover:text-(--color-foreground)"
            title={labels.openOriginal}
          >
            <ExternalLink className="size-3" />
            <span className="hidden sm:inline">{labels.openOriginal}</span>
          </a>
        ) : (
          <span />
        )}
        <div className="flex items-center gap-1">
          <CardAction
            icon={Bookmark}
            label={labels.save}
            onClick={(e) => {
              e.stopPropagation();
              onSave();
            }}
            tone="ghost"
          />
          <CardAction
            icon={Send}
            label={labels.apply}
            onClick={(e) => {
              e.stopPropagation();
              onApply();
            }}
            tone="primary"
          />
        </div>
      </footer>
    </article>
  );
}

function CompanyAvatar({ name }: { name: string }) {
  const initial = name.trim().charAt(0).toUpperCase() || "?";
  return (
    <span className="flex size-7 shrink-0 items-center justify-center rounded-md border border-(--color-border) bg-(--color-muted) text-[11px] font-semibold text-(--color-foreground)">
      {initial}
    </span>
  );
}

type CardActionProps = {
  icon: React.ComponentType<React.SVGProps<SVGSVGElement>>;
  label: string;
  onClick: (e: React.MouseEvent) => void;
  tone: "primary" | "ghost";
};

function CardAction({ icon: Icon, label, onClick, tone }: CardActionProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={label}
      aria-label={label}
      className={cn(
        "inline-flex h-7 items-center gap-1 rounded-md px-2 text-[11px] font-medium transition-colors",
        tone === "primary"
          ? "bg-(--color-accent) text-(--color-accent-foreground) shadow-(--shadow-soft) hover:shadow-(--shadow-card)"
          : "border border-(--color-border) bg-(--color-card) text-(--color-muted-foreground) hover:bg-(--color-muted) hover:text-(--color-foreground)",
      )}
    >
      <Icon className="size-3" />
      <span className="hidden sm:inline">{label}</span>
    </button>
  );
}

function formatDate(ms: number): string {
  return activeFormat().date(ms, "long");
}
