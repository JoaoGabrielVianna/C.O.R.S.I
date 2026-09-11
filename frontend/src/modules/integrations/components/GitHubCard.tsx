/**
 * The GitHub integration's surface.
 *
 * ── The question this card answers ─────────────────────────────────────
 * "What can C.O.R.S.I. reach through GitHub, and what has it been allowed
 * to use?" It is deliberately not a GitHub client: there is no file
 * browser, no issue list, no diff viewer. Anything a person would go to
 * github.com for belongs on github.com.
 *
 * ── The two layers, kept visible ───────────────────────────────────────
 * Connecting an account and authorizing a repository are separate acts,
 * and the card shows them as separate: a freshly connected account reads
 * "0 repositories authorized" rather than quietly working. That is the
 * whole security model made legible — if the interface hid it, nobody
 * would believe the backend enforced it.
 *
 * ── The credential ─────────────────────────────────────────────────────
 * The token is held in a component state variable for as long as it takes
 * to submit, and cleared on success. Nothing reads it back: the backend
 * returns a four-character hint, and that is what the connected state
 * renders.
 */

import { useMemo, useState } from "react";
import {
  AlertTriangle,
  Check,
  GitCommitHorizontal,
  Loader2,
  Lock,
  RefreshCw,
  Unplug,
} from "lucide-react";

import { GithubIcon } from "@/components/ui/BrandIcons";
import { Button } from "@/components/ui/Button";
import { Input } from "@/components/ui/Input";
import { cn } from "@/lib/utils";
import type { GitHubAvailableRepository, GitHubStatus } from "../api/github";
import {
  githubErrorMessage,
  type GitHubFailure,
  useConnectGitHub,
  useDisconnectGitHub,
  useGitHubActivity,
  useGitHubRepositories,
  useGitHubStatus,
  useSetAuthorizedRepositories,
} from "../hooks/useGitHub";
import { IntegrationCard, IntegrationStatusPill } from "./IntegrationCard";
import { useFormat, useT } from "@/lib/i18n";

const TAGLINE =
  "Deixe um agente consultar commits, código e pull requests dos repositórios que você autorizar. Somente leitura.";

export function GitHubCard() {
  const t = useT();
  const status = useGitHubStatus();

  if (status.isLoading) {
    return (
      <IntegrationCard
        icon={GithubIcon}
        title="GitHub"
        tagline={TAGLINE}
        status={<IntegrationStatusPill tone="muted">···</IntegrationStatusPill>}
      >
        <div className="h-16 animate-pulse rounded-lg bg-(--color-muted)/40" />
      </IntegrationCard>
    );
  }

  if (status.isError) {
    return (
      <IntegrationCard
        icon={GithubIcon}
        title="GitHub"
        tagline={TAGLINE}
        status={<IntegrationStatusPill tone="warning">{t.app.settings.github.error}</IntegrationStatusPill>}
      >
        <ErrorNote failure={githubErrorMessage(status.error, t)} fallback={t.app.settings.github.errors.readStateFailed} />
      </IntegrationCard>
    );
  }

  if (!status.data?.connected) return <DisconnectedCard />;
  return <ConnectedCard status={status.data} />;
}

/* ── disconnected ────────────────────────────────────────────────────── */

function DisconnectedCard() {
  const t = useT();
  const [token, setToken] = useState("");
  const connect = useConnectGitHub();

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const value = token.trim();
    if (!value) return;
    connect.mutate(value, {
      // Cleared whatever happens. A failed attempt leaving the token in the
      // field is a token sitting in the DOM until the tab is closed.
      onSettled: () => setToken(""),
    });
  };

  return (
    <IntegrationCard
      icon={GithubIcon}
      title="GitHub"
      tagline={TAGLINE}
      status={<IntegrationStatusPill tone="muted">{t.app.settings.github.notConnected}</IntegrationStatusPill>}
    >
      <form className="space-y-3" onSubmit={submit}>
        <div className="space-y-1.5">
          <label
            htmlFor="github-token"
            className="text-[12.5px] font-medium text-(--color-foreground)"
          >
            {t.app.settings.github.patLabel}
          </label>
          <Input
            id="github-token"
            // A password field so the value is not shoulder-readable and is
            // not offered to a password manager as a username.
            type="password"
            autoComplete="off"
            spellCheck={false}
            placeholder={t.app.settings.github.patPlaceholder}
            value={token}
            onChange={(e) => setToken(e.target.value)}
          />
          <p className="text-[12px] leading-relaxed text-(--color-muted-foreground)">
            {t.app.settings.github.helpLead} <strong>{t.app.settings.github.helpLink}</strong> {t.app.settings.github.helpScopes}{" "}
            (<code>Contents: read</code>, <code>Metadata: read</code> {t.app.settings.github.helpMiddle}{" "}
            <code>Pull requests: read</code>{t.app.settings.github.helpTail}
          </p>
        </div>

        {connect.isError ? (
          <ErrorNote failure={githubErrorMessage(connect.error, t)} fallback={t.app.settings.github.errors.connectFailed} />
        ) : null}

        <div className="flex justify-end">
          <Button size="sm" variant="primary" type="submit" disabled={connect.isPending || !token.trim()}>
            {connect.isPending ? (
              <Loader2 className="size-3.5 animate-spin" />
            ) : (
              <GithubIcon className="size-3.5" />
            )}
            Conectar GitHub
          </Button>
        </div>
      </form>
    </IntegrationCard>
  );
}

/* ── connected ───────────────────────────────────────────────────────── */

function ConnectedCard({ status }: { status: GitHubStatus }) {
  const t = useT();
  const fmt = useFormat();
  const [managing, setManaging] = useState(false);
  const disconnect = useDisconnectGitHub();
  const conn = status.connection;
  const authorizedCount = status.repositories.length;

  return (
    <IntegrationCard
      icon={GithubIcon}
      title="GitHub"
      tagline={TAGLINE}
      status={
        // Connected with nothing authorized is NOT the healthy state, and
        // the pill says so. It is the difference between "working" and
        // "connected and unable to read anything".
        <IntegrationStatusPill tone={authorizedCount > 0 ? "active" : "warning"}>
          {authorizedCount > 0 ? "Conectado" : "Sem repositórios"}
        </IntegrationStatusPill>
      }
      actions={
        <>
          <Button
            size="sm"
            variant="outline"
            onClick={() => setManaging((v) => !v)}
          >
            {managing ? "Fechar" : "Gerenciar repositórios"}
          </Button>
          <Button
            size="sm"
            variant="destructive"
            onClick={() => disconnect.mutate()}
            disabled={disconnect.isPending}
          >
            {disconnect.isPending ? (
              <Loader2 className="size-3.5 animate-spin" />
            ) : (
              <Unplug className="size-3.5" />
            )}
            Desconectar
          </Button>
        </>
      }
    >
      <div className="space-y-5">
        <AccountRow status={status} />

        {authorizedCount === 0 ? (
          <p className="rounded-lg border border-amber-400/30 bg-amber-400/10 px-3.5 py-3 text-[12.5px] text-(--color-foreground)">
            {t.app.settings.github.noReposLead} <strong>{t.app.settings.github.noReposEmphasis}</strong>
            {t.app.settings.github.noReposTail}
          </p>
        ) : (
          <AuthorizedList status={status} />
        )}

        {managing ? <RepositoryPicker status={status} /> : null}

        {authorizedCount > 0 ? <ActivityFeed /> : null}
      </div>

      {conn ? (
        <p className="mt-4 text-[11.5px] text-(--color-muted-foreground)">
          {t.app.settings.github.tokenLine.replace("{hint}", conn.token_hint)}{" "}
          {conn.last_verified_at
            ? t.app.settings.github.lastVerified.replace(
                "{when}",
                fmt.date(conn.last_verified_at, "dateTime"),
              )
            : t.app.settings.github.neverVerified}
        </p>
      ) : null}
    </IntegrationCard>
  );
}

function AccountRow({ status }: { status: GitHubStatus }) {
  const conn = status.connection;
  if (!conn) return null;
  return (
    <div className="flex items-center gap-3">
      {conn.account_avatar_url ? (
        <img
          src={conn.account_avatar_url}
          alt=""
          className="size-9 rounded-full border border-(--color-border)"
        />
      ) : (
        <span className="flex size-9 items-center justify-center rounded-full border border-(--color-border) bg-(--color-muted)">
          <GithubIcon className="size-4" />
        </span>
      )}
      <div className="min-w-0">
        <p className="truncate text-[13.5px] font-medium text-(--color-foreground)">
          {conn.account_name || conn.account_login}
        </p>
        <p className="truncate text-[12px] text-(--color-muted-foreground)">
          @{conn.account_login}
          {status.organizations.length > 0
            ? ` · organizações: ${status.organizations.join(", ")}`
            : ""}
        </p>
      </div>
    </div>
  );
}

function AuthorizedList({ status }: { status: GitHubStatus }) {
  const t = useT();
  return (
    <div className="space-y-2">
      <SectionLabel>
        Repositórios autorizados ({status.repositories.length})
      </SectionLabel>
      <ul className="divide-y divide-(--color-border) overflow-hidden rounded-lg border border-(--color-border)">
        {status.repositories.map((r) => (
          <li key={r.id} className="flex items-center gap-2 px-3 py-2">
            <span className="min-w-0 flex-1 truncate text-[12.5px] text-(--color-foreground)">
              {r.full_name}
            </span>
            {r.private ? (
              <Lock className="size-3 shrink-0 text-(--color-muted-foreground)" aria-label={t.app.settings.github.private} />
            ) : null}
            <span className="shrink-0 font-mono text-[11px] text-(--color-muted-foreground)">
              {r.default_branch}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

/* ── the picker ──────────────────────────────────────────────────────── */

/**
 * Chooses the authorized set.
 *
 * The whole set is submitted, never a delta — matching the API, and for
 * the same reason: an add and a revoke arriving as two requests is a window
 * in which the stored set is neither the old one nor the new one.
 */
function RepositoryPicker({ status }: { status: GitHubStatus }) {
  const t = useT();
  const available = useGitHubRepositories(true);
  const save = useSetAuthorizedRepositories();
  const [filter, setFilter] = useState("");
  const [draft, setDraft] = useState<Set<string> | null>(null);

  const selected = useMemo(() => {
    if (draft) return draft;
    return new Set(status.repositories.map((r) => r.full_name.toLowerCase()));
  }, [draft, status.repositories]);

  const toggle = (full: string) => {
    const next = new Set(selected);
    const key = full.toLowerCase();
    if (next.has(key)) next.delete(key);
    else next.add(key);
    setDraft(next);
  };

  const items: GitHubAvailableRepository[] = useMemo(() => {
    const all = available.data?.items ?? [];
    const q = filter.trim().toLowerCase();
    if (!q) return all;
    return all.filter(
      (r) =>
        r.full_name.toLowerCase().includes(q) ||
        (r.description ?? "").toLowerCase().includes(q),
    );
  }, [available.data, filter]);

  if (available.isLoading) {
    return <div className="h-24 animate-pulse rounded-lg bg-(--color-muted)/40" />;
  }
  if (available.isError) {
    return <ErrorNote failure={githubErrorMessage(available.error, t)} fallback={t.app.settings.github.errors.listFailed} />;
  }

  const dirty = draft !== null;

  return (
    <div className="space-y-3 rounded-lg border border-(--color-border) p-3">
      <SectionLabel>{t.app.settings.github.reachable}</SectionLabel>
      <Input
        placeholder={t.app.settings.github.filterPlaceholder}
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
      />

      {available.data?.truncated ? (
        <p className="text-[11.5px] text-(--color-muted-foreground)">
          {t.app.settings.github.repoLimit.replace("{count}", String(available.data.limit))}
        </p>
      ) : null}

      <ul className="max-h-72 space-y-1 overflow-y-auto">
        {items.map((r) => {
          const on = selected.has(r.full_name.toLowerCase());
          return (
            <li key={r.id}>
              <button
                type="button"
                onClick={() => toggle(r.full_name)}
                aria-pressed={on}
                className={cn(
                  "flex w-full items-center gap-2 rounded-lg border px-3 py-2 text-left transition-colors",
                  on
                    ? "border-(--color-brand-500)/40 bg-(--color-brand-500)/10"
                    : "border-(--color-border) hover:bg-(--color-muted)",
                )}
              >
                <span
                  className={cn(
                    "flex size-4 shrink-0 items-center justify-center rounded border",
                    on
                      ? "border-(--color-brand-500) bg-(--color-brand-500) text-(--color-accent-foreground)"
                      : "border-(--color-border)",
                  )}
                >
                  {on ? <Check className="size-3" /> : null}
                </span>
                <span className="min-w-0 flex-1 truncate text-[12.5px]">{r.full_name}</span>
                {r.private ? (
                  <Lock className="size-3 shrink-0 text-(--color-muted-foreground)" aria-label={t.app.settings.github.private} />
                ) : null}
              </button>
            </li>
          );
        })}
        {items.length === 0 ? (
          <li className="px-3 py-4 text-center text-[12.5px] text-(--color-muted-foreground)">
            {t.app.settings.github.noneFound}
          </li>
        ) : null}
      </ul>

      {save.isError ? (
        <ErrorNote failure={githubErrorMessage(save.error, t)} fallback={t.app.settings.github.errors.saveFailed} />
      ) : null}

      <div className="flex items-center justify-between gap-2">
        <span className="text-[11.5px] text-(--color-muted-foreground)">
          {selected.size} selecionado{selected.size === 1 ? "" : "s"}
        </span>
        <div className="flex gap-2">
          <Button
            size="sm"
            variant="ghost"
            disabled={!dirty || save.isPending}
            onClick={() => setDraft(null)}
          >
            {t.app.settings.github.undo}
          </Button>
          <Button
            size="sm"
            variant="primary"
            disabled={!dirty || save.isPending}
            onClick={() =>
              save.mutate([...selected], { onSuccess: () => setDraft(null) })
            }
          >
            {save.isPending ? <Loader2 className="size-3.5 animate-spin" /> : null}
            Salvar autorização
          </Button>
        </div>
      </div>
    </div>
  );
}

/* ── activity ────────────────────────────────────────────────────────── */

function ActivityFeed() {
  const t = useT();
  const activity = useGitHubActivity(true);

  if (activity.isLoading) {
    return <div className="h-20 animate-pulse rounded-lg bg-(--color-muted)/40" />;
  }
  if (activity.isError) {
    return <ErrorNote failure={githubErrorMessage(activity.error, t)} fallback={t.app.settings.github.errors.activityFailed} />;
  }
  const items = activity.data?.items ?? [];
  if (items.length === 0) return null;

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <SectionLabel>{t.app.settings.github.recentCommits}</SectionLabel>
        <button
          type="button"
          onClick={() => void activity.refetch()}
          className="inline-flex items-center gap-1 text-[11.5px] text-(--color-muted-foreground) hover:text-(--color-foreground)"
        >
          <RefreshCw className="size-3" />
          {t.app.settings.github.refresh}
        </button>
      </div>
      <ul className="space-y-1.5">
        {items.slice(0, 10).map((c) => (
          <li key={c.repository + c.sha} className="flex items-start gap-2">
            <GitCommitHorizontal className="mt-0.5 size-3.5 shrink-0 text-(--color-muted-foreground)" />
            <div className="min-w-0">
              <p className="truncate text-[12.5px] text-(--color-foreground)">
                {c.message.split("\n")[0]}
              </p>
              <p className="truncate font-mono text-[11px] text-(--color-muted-foreground)">
                {c.repository} · {c.sha.slice(0, 7)}
                {c.author_login ? ` · @${c.author_login}` : ""}
              </p>
            </div>
          </li>
        ))}
      </ul>
      {/* The ceilings the server applied, stated rather than implied: a
          short feed must not read as a quiet repository. */}
      <p className="text-[11px] text-(--color-muted-foreground)">
        Até {activity.data?.per_repository} commits de {activity.data?.repositories_read}{" "}
        repositórios.
      </p>
    </div>
  );
}

/* ── bits ────────────────────────────────────────────────────────────── */

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <p className="font-mono text-[10.5px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
      {children}
    </p>
  );
}

/**
 * A failure, in the reader's language, with the server's own wording kept
 * beneath it whenever the two differ.
 *
 * Translating an error must not cost the ability to investigate it later,
 * so the code and the server's sentence stay on screen in monospace rather
 * than being replaced by the friendlier text.
 */
function ErrorNote({
  failure,
  fallback,
}: {
  failure: GitHubFailure | null;
  fallback: string;
}) {
  return (
    <div
      role="alert"
      className="flex items-start gap-2 rounded-lg border border-(--color-destructive)/30 bg-(--color-destructive)/10 px-3 py-2.5"
    >
      <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-(--color-destructive)" />
      <div className="min-w-0">
        <p className="text-[12.5px] text-(--color-foreground)">{failure?.text ?? fallback}</p>
        {failure?.technical ? (
          <p className="mt-1 break-words font-mono text-[10.5px] text-(--color-muted-foreground)">
            {failure.code}: {failure.technical}
          </p>
        ) : null}
      </div>
    </div>
  );
}

