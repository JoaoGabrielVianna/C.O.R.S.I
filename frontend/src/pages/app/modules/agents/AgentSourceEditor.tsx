import { useFormat, useT } from "@/lib/i18n";
import { useState } from "react";
import { Link, useNavigate, useOutletContext, useParams } from "react-router-dom";
import { AlertTriangle, ArrowLeft } from "lucide-react";
import { Button } from "@/components/ui/Button";
import { ApiError } from "@/lib/api/client";
import { cn } from "@/lib/utils";
import {
  countCharacters,
  estimateTokens,
  MAX_SOURCE_CONTENT,
  MAX_SOURCE_DESCRIPTION,
  MAX_SOURCE_TITLE,
} from "@/modules/agents/api/sources";
import type { AgentContext } from "@/modules/agents/components/AgentShell";
import { formatTokens } from "@/modules/agents/format";
import {
  useCreateSource,
  useSource,
  useUpdateSource,
} from "@/modules/agents/hooks/useSources";

/**
 * The source editor: one document, on its own page.
 *
 * ── Why a page and not a dialog ────────────────────────────────────────
 * A source runs to twenty thousand characters. The module already made this
 * mistake once, with `system_prompt` in a five-line textarea inside a
 * table, and the audit classified it CONFUSING. A modal would be the same
 * mistake with rounded corners: the text needs the height of the window.
 *
 * It also makes each source addressable, which is the rule the module
 * settled on — a thing worth opening is worth linking to.
 *
 * ── One component, two states ──────────────────────────────────────────
 * `/sources/new` and `/sources/:sourceId` are the same screen. The only
 * difference is whether there is a document to load first, which is exactly
 * what `useSource` keys off.
 */
export function AgentSourceEditorPage() {
  const t = useT();
  const { agent } = useOutletContext<AgentContext>();
  const { sourceId } = useParams<{ sourceId: string }>();
  const existing = useSource(agent.id, sourceId);
  const base = `/app/modules/agents/${agent.id}/sources`;

  if (sourceId && existing.isLoading) {
    return (
      <Shell base={base}>
        <div className="min-h-0 flex-1 animate-pulse rounded-2xl border border-(--color-border) bg-(--color-muted)/40" />
      </Shell>
    );
  }

  // A source id that resolves to nothing: deleted, or owned by a workspace
  // this one is not. The server answers both the same way and so does this.
  if (sourceId && existing.isError) {
    return (
      <Shell base={base}>
        <div className="mx-auto max-w-lg rounded-2xl border border-dashed border-(--color-border) px-6 py-10 text-center">
          <p className="text-sm font-medium text-(--color-foreground)">{t.app.modules.agents.sources.notFound}</p>
          <p className="mt-2 text-[12px] leading-relaxed text-(--color-muted-foreground)">
            {t.app.modules.agents.sources.notFoundBody}
          </p>
          <Button size="sm" variant="outline" className="mt-4" asChild>
            <Link to={base}>{t.app.modules.agents.sources.backToSources}</Link>
          </Button>
        </div>
      </Shell>
    );
  }

  return (
    <Shell base={base}>
      {/* The form mounts only once there is a document to put in it, and is
          keyed on the source. That is what makes the fields plain initial
          state: no effect re-seeds them, so a background refetch can never
          overwrite what is being typed. */}
      <SourceForm
        key={sourceId ?? "new"}
        agentId={agent.id}
        sourceId={sourceId}
        initialTitle={existing.data?.title ?? ""}
        initialDescription={existing.data?.description ?? ""}
        initialContent={existing.data?.content ?? ""}
      />
    </Shell>
  );
}

function SourceForm({
  agentId,
  sourceId,
  initialTitle,
  initialDescription,
  initialContent,
}: {
  agentId: string;
  sourceId?: string;
  initialTitle: string;
  initialDescription: string;
  initialContent: string;
}) {
  const t = useT();
  const fmt = useFormat();
  const navigate = useNavigate();
  const createSource = useCreateSource(agentId);
  const updateSource = useUpdateSource(agentId);

  const [title, setTitle] = useState(initialTitle);
  const [description, setDescription] = useState(initialDescription);
  const [content, setContent] = useState(initialContent);
  const [error, setError] = useState<string | null>(null);

  const base = `/app/modules/agents/${agentId}/sources`;

  const characters = countCharacters(content);
  const tokens = estimateTokens(characters);
  const overContent = characters > MAX_SOURCE_CONTENT;
  const overTitle = countCharacters(title) > MAX_SOURCE_TITLE;
  const busy = createSource.isPending || updateSource.isPending;
  const canSave = title.trim() !== "" && content.trim() !== "" && !overContent && !overTitle && !busy;

  const save = () => {
    setError(null);
    const body = { title: title.trim(), description: description.trim(), content };
    const onError = (err: unknown) => setError(messageOf(err));

    if (sourceId) {
      updateSource.mutate(
        { id: sourceId, body },
        { onSuccess: () => navigate(base), onError },
      );
      return;
    }
    createSource.mutate(body, { onSuccess: () => navigate(base), onError });
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-2.5">
      <div className="grid shrink-0 grid-cols-1 gap-2.5 sm:grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)]">
        <Field
          label={t.app.modules.agents.sources.titleField}
          hint={`${countCharacters(title)}/${MAX_SOURCE_TITLE}`}
          invalid={overTitle}
        >
          <input
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder={t.app.modules.agents.sources.titlePlaceholder}
            className={inputClass(overTitle)}
          />
        </Field>
        <Field
          label={t.app.modules.agents.sources.descriptionField}
          hint={t.app.modules.agents.sources.descriptionHint}
          invalid={countCharacters(description) > MAX_SOURCE_DESCRIPTION}
        >
          <input
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            placeholder={t.app.modules.agents.sources.descriptionPlaceholder}
            className={inputClass(countCharacters(description) > MAX_SOURCE_DESCRIPTION)}
          />
        </Field>
      </div>

      <div className="flex min-h-0 flex-1 flex-col">
        <div className="mb-1 flex shrink-0 items-center justify-between gap-3 px-0.5">
          <label htmlFor="source-content" className="text-[11px] font-medium text-(--color-foreground)">
            {t.app.modules.agents.sources.content}
          </label>
          <span
            className={cn(
              "font-mono text-[10.5px]",
              overContent ? "text-(--color-destructive)" : "text-(--color-muted-foreground)",
            )}
          >
            {fmt.number(characters)}/{fmt.number(MAX_SOURCE_CONTENT)} car.
            · ~{formatTokens(tokens)} tok
          </span>
        </div>
        <textarea
          id="source-content"
          value={content}
          onChange={(e) => setContent(e.target.value)}
          placeholder={t.app.modules.agents.sources.contentPlaceholder}
          className={cn(
            "min-h-0 flex-1 resize-none rounded-2xl border bg-(--color-card) px-3.5 py-3",
            "font-mono text-[12.5px] leading-[1.7] text-(--color-foreground)",
            "placeholder:text-(--color-muted-foreground) outline-none",
            "focus:ring-2 focus:ring-(--color-brand-500)/20",
            overContent
              ? "border-(--color-destructive)"
              : "border-(--color-border) focus:border-(--color-brand-500)",
          )}
        />
      </div>

      {error ? (
        <p className="flex shrink-0 items-start gap-2 rounded-xl border border-(--color-destructive)/30 bg-(--color-destructive)/5 px-3 py-2 text-[11.5px] leading-relaxed text-(--color-destructive)">
          <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
          {error}
        </p>
      ) : null}

      <div className="flex shrink-0 items-center justify-end gap-2">
        <Button size="sm" variant="ghost" asChild>
          <Link to={base}>{t.app.modules.agents.sources.cancel}</Link>
        </Button>
        <Button size="sm" onClick={save} disabled={!canSave}>
          {busy ? "Salvando…" : "Salvar"}
        </Button>
      </div>
    </div>
  );
}

function Shell({ base, children }: { base: string; children: React.ReactNode }) {
  const t = useT();
  return (
    <section className="flex min-h-0 flex-1 flex-col gap-3">
      <header className="flex shrink-0 items-center gap-2">
        <Link
          to={base}
          aria-label={t.app.modules.agents.sources.backLabel}
          className="rounded-md p-0.5 text-(--color-muted-foreground) transition-colors hover:text-(--color-foreground)"
        >
          <ArrowLeft className="size-4" />
        </Link>
        <h2 className="text-sm font-semibold text-(--color-foreground)">{t.app.modules.agents.sources.singular}</h2>
      </header>
      {children}
    </section>
  );
}

function Field({
  label,
  hint,
  invalid,
  children,
}: {
  label: string;
  hint: string;
  invalid?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div>
      <div className="mb-1 flex items-center justify-between gap-2 px-0.5">
        <span className="text-[11px] font-medium text-(--color-foreground)">{label}</span>
        <span
          className={cn(
            "font-mono text-[10.5px]",
            invalid ? "text-(--color-destructive)" : "text-(--color-muted-foreground)",
          )}
        >
          {hint}
        </span>
      </div>
      {children}
    </div>
  );
}

function inputClass(invalid?: boolean): string {
  return cn(
    "h-9 w-full rounded-xl border bg-(--color-card) px-3 text-[13px]",
    "text-(--color-foreground) placeholder:text-(--color-muted-foreground)",
    "outline-none focus:ring-2 focus:ring-(--color-brand-500)/20",
    invalid ? "border-(--color-destructive)" : "border-(--color-border) focus:border-(--color-brand-500)",
  );
}

function messageOf(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  if (err instanceof Error) return err.message;
  return String(err);
}
