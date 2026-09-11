/**
 * The Meta Threads integration's surface.
 *
 * ══════════════════════════════════════════════════════════════════════
 *  This is the EXTERNAL social network, not the Threads module
 * ══════════════════════════════════════════════════════════════════════
 *
 * C.O.R.S.I. Threads — the operator's own pipeline of ideas and drafts —
 * has no frontend at all, by decision. Nothing on this card reads or writes
 * it. What this card manages is one OAuth credential for meta.com's
 * network, and nothing else.
 *
 * ── The question this card answers ─────────────────────────────────────
 * "Is an account linked, what may that credential do, and how long will it
 * keep working?" It is deliberately not a Threads client: no post list, no
 * metrics, no composer. Reading published history is what an agent does,
 * inside a conversation, through tools — and a settings page that also did
 * it would be a second reader with its own idea of what a page of history
 * is.
 *
 * ── The OAuth round trip, and why it needs no new route ────────────────
 * Meta sends the browser back to a registered redirect URI with `?code=`.
 * That URI is THIS PAGE, so the flow leaves and returns to the same place
 * the operator started from, and no parallel page or callback route
 * exists. The card notices the code on mount, redeems it through the
 * backend, and scrubs the query string.
 *
 * ── The credential ─────────────────────────────────────────────────────
 * Nothing here ever holds a token. The authorization URL is built by the
 * backend — including the scope list, so this file cannot ask for a write
 * permission — and the code is handed straight back to the backend, which
 * performs both exchanges and seals the result. What returns is a
 * four-character hint.
 */

import { useEffect, useMemo, useRef, useState } from "react";
import { AlertTriangle, Check, Loader2, RefreshCw, Unplug } from "lucide-react";

import { Button } from "@/components/ui/Button";
import { useFormat, useT } from "@/lib/i18n";
import { getMetaThreadsAuthorizeURL, type MetaThreadsConnection } from "../api/metaThreads";
import {
  metaThreadsErrorMessage,
  useConnectMetaThreads,
  useDisconnectMetaThreads,
  useMetaThreadsStatus,
  useRefreshMetaThreads,
  type MetaThreadsFailure,
} from "../hooks/useMetaThreads";
import { IntegrationCard, IntegrationStatusPill } from "./IntegrationCard";
import { MetaThreadsGlyph } from "./integrationIcons";

/**
 * Where Meta sends the browser back. This page, exactly.
 *
 * Computed from the running origin rather than configured, so a developer
 * on :5173 and production on corsi.dev each produce their own correct
 * value — and the card can SHOW it, which is what the operator has to paste
 * into Meta's console. A constant here would be wrong on one of the two.
 */
function redirectURI(): string {
  return `${window.location.origin}${window.location.pathname}`;
}

/** Where the CSRF state lives between leaving and coming back. */
const STATE_KEY = "corsi.meta-threads.oauth-state";

/**
 * A one-shot, unguessable value for this authorization attempt.
 *
 * ── Why not `crypto.randomUUID()` ──────────────────────────────────────
 * It is absent outside a secure context and in some test environments, and
 * a `state` generator that can throw would turn "connect" into a dead
 * button on exactly the setups hardest to debug. `getRandomValues` is the
 * primitive underneath it and is available wherever this app runs.
 *
 * `Math.random` is deliberately not a fallback: a predictable state is the
 * same as no state at all, and a check that can be guessed is worse than an
 * honest absence because it reads as protection.
 */
function randomState(): string {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

export function MetaThreadsCard() {
  const t = useT();
  const i = t.app.settings.metaThreads;
  const status = useMetaThreadsStatus();

  const shell = (children: React.ReactNode, pill: React.ReactNode) => (
    <IntegrationCard
      icon={MetaThreadsGlyph}
      title={i.title}
      tagline={i.tagline}
      status={pill}
    >
      {children}
    </IntegrationCard>
  );

  // The callback runs regardless of the status query, because a code in the
  // URL must not be lost while a read is in flight.
  const callback = useOAuthCallback();

  if (status.isLoading) {
    return shell(
      <div className="h-16 animate-pulse rounded-lg bg-(--color-muted)/40" />,
      <IntegrationStatusPill tone="muted">{i.loading}</IntegrationStatusPill>,
    );
  }
  if (status.isError) {
    return shell(
      <ErrorNote
        failure={metaThreadsErrorMessage(status.error, t)}
        fallback={i.errors.readStateFailed}
      />,
      <IntegrationStatusPill tone="warning">{i.error}</IntegrationStatusPill>,
    );
  }

  const data = status.data;
  if (data?.connected && data.connection) {
    return <ConnectedCard connection={data.connection} callback={callback} />;
  }
  return <DisconnectedCard configured={Boolean(data?.configured)} callback={callback} />;
}

/* ── the OAuth round trip ────────────────────────────────────────────── */

interface CallbackState {
  /** A redemption is in flight. */
  pending: boolean;
  failure: MetaThreadsFailure | null;
  /** Meta refused or the operator cancelled — not a server error. */
  notice: string | null;
}

/**
 * Notices Meta's answer in the URL and completes the connection.
 *
 * ── Why the state parameter is checked here ────────────────────────────
 * The backend hands `state` through untouched and stores nothing, because
 * it has no session to store it in. So the browser that started the flow is
 * the only party that can tell its own callback from somebody else's link,
 * and this is where that check belongs. A mismatch refuses without
 * redeeming — an authorization code that arrives unsolicited is one this
 * browser never asked for.
 *
 * ── Why the query string is scrubbed ───────────────────────────────────
 * An authorization code is single-use and short-lived, and leaving it in
 * the address bar puts it in history, in a bookmark, and in the next
 * screenshot. It is removed as soon as it has been read, whether or not the
 * redemption succeeded.
 */
function useOAuthCallback(): CallbackState {
  const t = useT();
  const i = t.app.settings.metaThreads;
  const connect = useConnectMetaThreads();

  // Read ONCE, during the first render, before anything scrubs the URL.
  //
  // The reading is pure — it inspects the query string and the stored state
  // and changes neither — so React re-running this initializer under strict
  // mode costs nothing. Consuming the state and scrubbing the address bar
  // are effects, and they live in the effect below.
  const [arrival] = useState<Arrival>(readArrival);
  const handled = useRef(false);

  useEffect(() => {
    if (arrival.kind === "none" || handled.current) return;
    // A code is single-use: strict mode's double invocation would turn a
    // working connection into a confusing failure without this.
    handled.current = true;

    sessionStorage.removeItem(STATE_KEY);
    // Scrubbed whatever the outcome. An authorization code left in the
    // address bar is one in history, in a bookmark and in the next
    // screenshot.
    window.history.replaceState({}, "", window.location.pathname);

    if (arrival.kind === "code") {
      connect.mutate({ code: arrival.code, redirectUri: redirectURI() });
    }
    // One shot, on mount. `arrival` is frozen by useState and the mutation
    // is stable for the life of the page.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const notice =
    arrival.kind === "cancelled" ? i.cancelled : arrival.kind === "mismatch" ? i.stateMismatch : null;

  return {
    pending: connect.isPending,
    failure: connect.isError ? metaThreadsErrorMessage(connect.error, t) : null,
    notice,
  };
}

/** What Meta put in the address bar, decided once and never re-read. */
type Arrival =
  | { kind: "none" }
  | { kind: "cancelled" }
  | { kind: "mismatch" }
  | { kind: "code"; code: string };

/**
 * Classifies the callback.
 *
 * ── Why the state check lives in the browser ───────────────────────────
 * The backend hands `state` through untouched and stores nothing, because
 * it has no session to store it in. So the browser that started the flow is
 * the only party that can tell its own callback from somebody else's link.
 * A mismatch is refused BEFORE redemption: an authorization code arriving
 * unsolicited is one this browser never asked for.
 */
function readArrival(): Arrival {
  const params = new URLSearchParams(window.location.search);
  const code = params.get("code");
  const error = params.get("error");
  if (!code && !error) return { kind: "none" };
  if (error) return { kind: "cancelled" };

  const expected = sessionStorage.getItem(STATE_KEY);
  if (!expected || expected !== params.get("state")) return { kind: "mismatch" };
  return { kind: "code", code: code as string };
}

/** Sends the browser to Meta, after remembering what it asked for. */
function useBeginAuthorization() {
  const [starting, setStarting] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const t = useT();

  const begin = async () => {
    setStarting(true);
    setFailure(null);
    try {
      const state = randomState();
      sessionStorage.setItem(STATE_KEY, state);
      const { authorization_url } = await getMetaThreadsAuthorizeURL(state);
      // A full navigation rather than a popup: the operator ends up back
      // on this page carrying a code, which is the whole flow, and a popup
      // adds a blocker to defeat and a window to lose.
      window.location.assign(authorization_url);
    } catch (err) {
      sessionStorage.removeItem(STATE_KEY);
      const failure = metaThreadsErrorMessage(err, t);
      setFailure(failure?.text ?? t.app.settings.metaThreads.errors.authorizeFailed);
      setStarting(false);
    }
  };

  return { begin, starting, failure };
}

/* ── disconnected ────────────────────────────────────────────────────── */

function DisconnectedCard({
  configured,
  callback,
}: {
  configured: boolean;
  callback: CallbackState;
}) {
  const t = useT();
  const i = t.app.settings.metaThreads;
  const auth = useBeginAuthorization();

  return (
    <IntegrationCard
      icon={MetaThreadsGlyph}
      title={i.title}
      tagline={i.tagline}
      status={<IntegrationStatusPill tone="muted">{i.notConnected}</IntegrationStatusPill>}
    >
      <div className="space-y-3">
        {callback.notice ? <Notice>{callback.notice}</Notice> : null}
        {callback.failure ? (
          <ErrorNote failure={callback.failure} fallback={i.errors.connectFailed} />
        ) : null}

        {configured ? (
          <>
            <p className="text-[12px] leading-relaxed text-(--color-muted-foreground)">
              {i.discoveryNote}
            </p>
            {auth.failure ? (
              <ErrorNote
                failure={{ text: auth.failure, technical: null, code: null }}
                fallback={i.errors.authorizeFailed}
              />
            ) : null}
            <div className="flex justify-end">
              <Button
                size="sm"
                variant="primary"
                onClick={() => void auth.begin()}
                disabled={auth.starting || callback.pending}
              >
                {auth.starting || callback.pending ? (
                  <Loader2 className="size-3.5 animate-spin" />
                ) : (
                  <MetaThreadsGlyph className="size-3.5" />
                )}
                {callback.pending ? i.connecting : i.connect}
              </Button>
            </div>
          </>
        ) : (
          // The deployment cannot link an account at all. Showing a Connect
          // button here would send the operator into a loop that cannot
          // terminate, so the card explains what is missing and who has to
          // fix it instead.
          <NotConfiguredNote />
        )}
      </div>
    </IntegrationCard>
  );
}

function NotConfiguredNote() {
  const i = useT().app.settings.metaThreads;
  return (
    <div className="space-y-2 rounded-lg border border-amber-400/30 bg-amber-400/10 px-3.5 py-3">
      <p className="text-[12.5px] font-medium text-(--color-foreground)">
        {i.notConfiguredTitle}
      </p>
      <p className="text-[12px] leading-relaxed text-(--color-muted-foreground)">
        {i.notConfiguredBody}
      </p>
      <p className="break-words font-mono text-[10.5px] text-(--color-muted-foreground)">
        {i.notConfiguredVars}
      </p>
      <p className="text-[12px] leading-relaxed text-(--color-muted-foreground)">
        {i.redirectHelp}
      </p>
      <p className="break-all font-mono text-[10.5px] text-(--color-foreground)">
        {redirectURI()}
      </p>
    </div>
  );
}

/* ── connected ───────────────────────────────────────────────────────── */

function ConnectedCard({
  connection,
  callback,
}: {
  connection: MetaThreadsConnection;
  callback: CallbackState;
}) {
  const t = useT();
  const fmt = useFormat();
  const i = t.app.settings.metaThreads;
  const disconnect = useDisconnectMetaThreads();
  const refresh = useRefreshMetaThreads();
  const auth = useBeginAuthorization();

  const life = useTokenLife(connection.token_expires_at);

  return (
    <IntegrationCard
      icon={MetaThreadsGlyph}
      title={i.title}
      tagline={i.tagline}
      status={
        // An expired token is NOT the healthy state, and the pill says so.
        // It is the difference between "connected" and "connected and
        // unable to read anything".
        <IntegrationStatusPill tone={life.expired ? "warning" : "active"}>
          {life.expired ? i.expired : i.connected}
        </IntegrationStatusPill>
      }
      actions={
        <>
          {life.expired ? (
            <Button
              size="sm"
              variant="primary"
              onClick={() => void auth.begin()}
              disabled={auth.starting}
            >
              {auth.starting ? <Loader2 className="size-3.5 animate-spin" /> : null}
              {i.reconnect}
            </Button>
          ) : (
            <Button
              size="sm"
              variant="outline"
              onClick={() => refresh.mutate()}
              disabled={refresh.isPending}
            >
              {refresh.isPending ? (
                <Loader2 className="size-3.5 animate-spin" />
              ) : (
                <RefreshCw className="size-3.5" />
              )}
              {i.refresh}
            </Button>
          )}
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
            {i.disconnect}
          </Button>
        </>
      }
    >
      <div className="space-y-5">
        {callback.notice ? <Notice>{callback.notice}</Notice> : null}

        <AccountRow connection={connection} />

        <div className="space-y-2">
          <SectionLabel>{i.capabilities}</SectionLabel>
          <ScopeList scopes={connection.scopes} />
          <p className="text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
            {i.discoveryNote}
          </p>
        </div>

        <div
          className={
            life.expired
              ? "rounded-lg border border-amber-400/30 bg-amber-400/10 px-3.5 py-3"
              : "rounded-lg border border-(--color-border) px-3.5 py-3"
          }
        >
          <p className="text-[12.5px] text-(--color-foreground)">{life.label(i, fmt)}</p>
          <p className="mt-1 text-[11.5px] leading-relaxed text-(--color-muted-foreground)">
            {life.expired ? i.expiredHelp : i.renewSoon}
          </p>
        </div>

        {/* The server's refusal is shown as it came. It carries the exact
            moment a refresh becomes possible, which no local rule could
            restate without becoming a second copy free to disagree. */}
        {refresh.isError ? (
          <ErrorNote
            failure={metaThreadsErrorMessage(refresh.error, t)}
            fallback={i.errors.refreshFailed}
          />
        ) : null}
        {auth.failure ? (
          <ErrorNote
            failure={{ text: auth.failure, technical: null, code: null }}
            fallback={i.errors.authorizeFailed}
          />
        ) : null}
      </div>

      <p className="mt-4 text-[11.5px] text-(--color-muted-foreground)">
        {i.tokenLine.replace("{hint}", connection.token_hint)} ·{" "}
        {connection.last_verified_at
          ? i.lastVerified.replace("{when}", fmt.date(connection.last_verified_at, "dateTime"))
          : i.neverVerified}
      </p>
    </IntegrationCard>
  );
}

function AccountRow({ connection }: { connection: MetaThreadsConnection }) {
  const i = useT().app.settings.metaThreads;
  return (
    <div className="space-y-2">
      <SectionLabel>{i.account}</SectionLabel>
      <div className="flex items-center gap-3">
        {connection.profile_picture_url ? (
          <img
            src={connection.profile_picture_url}
            alt=""
            className="size-9 rounded-full border border-(--color-border)"
          />
        ) : (
          <span className="flex size-9 items-center justify-center rounded-full border border-(--color-border) bg-(--color-muted)">
            <MetaThreadsGlyph className="size-4" />
          </span>
        )}
        <div className="min-w-0">
          <p className="truncate text-[13.5px] font-medium text-(--color-foreground)">
            {connection.display_name || connection.username}
          </p>
          <p className="truncate text-[12px] text-(--color-muted-foreground)">
            @{connection.username}
          </p>
        </div>
      </div>
    </div>
  );
}

/**
 * What the credential may do, named in the reader's language.
 *
 * ── Why an unknown scope still renders ─────────────────────────────────
 * Meta can grant a permission this build has never heard of, and a list
 * that dropped it would tell the operator their credential is narrower
 * than it is. The identifier is shown as it came, labelled as unrecognised
 * — the same fallback rule the reference registry follows for a type the
 * frontend was never taught.
 */
function ScopeList({ scopes }: { scopes: string[] }) {
  const i = useT().app.settings.metaThreads;

  // ── Why the lookup is explicit and not `names[scope] ?? fallback` ────
  // Because the dictionary answers a MISSING key with the key's own path
  // rather than with undefined, so `??` never fires and the operator is
  // shown `app.settings.metaThreads.scopeNames.threads_whatever` where a
  // sentence belongs. Listing the known ids here means an id that is not
  // one of them cannot reach the dictionary at all.
  const label = (scope: string): string => {
    switch (scope) {
      case "threads_basic":
        return i.scopeNames.threads_basic;
      case "threads_manage_insights":
        return i.scopeNames.threads_manage_insights;
      case "threads_keyword_search":
        return i.scopeNames.threads_keyword_search;
      default:
        return i.scopeUnknown;
    }
  };

  if (scopes.length === 0) return null;
  return (
    <ul className="divide-y divide-(--color-border) overflow-hidden rounded-lg border border-(--color-border)">
      {scopes.map((scope) => (
        <li key={scope} className="flex items-center gap-2 px-3 py-2">
          <Check className="size-3 shrink-0 text-(--color-brand-500)" />
          <span className="min-w-0 flex-1 truncate text-[12.5px] text-(--color-foreground)">
            {label(scope)}
          </span>
          <span className="shrink-0 font-mono text-[10.5px] text-(--color-muted-foreground)">
            {scope}
          </span>
        </li>
      ))}
    </ul>
  );
}

/**
 * How long the stored token has left.
 *
 * ── Why the UI computes days and not refreshability ────────────────────
 * Days remaining is arithmetic on a timestamp the backend already sent.
 * Whether a REFRESH is allowed is a rule — Meta requires the token to be at
 * least 24 hours old — and restating a rule here would create a second copy
 * free to disagree with the server's. So the button is always offered and
 * the server's own refusal, which names the exact moment, is what the
 * operator reads.
 */
function useTokenLife(expiresAt: string) {
  // The clock is read ONCE, in a lazy initializer, and then held.
  //
  // Reading Date.now() in the render body would make the component impure —
  // two renders in the same commit could disagree about whether the token
  // is alive — and reading it in an effect would set state on mount for a
  // value that was already knowable. A token's remaining life does not need
  // to tick: the card is re-mounted far more often than a day passes.
  const [now] = useState(() => Date.now());

  return useMemo(() => {
    const end = new Date(expiresAt).getTime();
    const expired = !Number.isFinite(end) || end <= now;
    const days = Math.max(0, Math.ceil((end - now) / 86_400_000));
    return {
      expired,
      days,
      label: (i: TokenLifeCopy, fmt?: TokenLifeFormat) => {
        if (expired) return i.expired;
        if (days === 0 || !fmt) return i.expiresToday;
        return fmt.plural(days, i.expiresIn);
      },
    };
  }, [expiresAt, now]);
}

interface TokenLifeCopy {
  expired: string;
  expiresToday: string;
  expiresIn: { one: string; other: string };
}

interface TokenLifeFormat {
  plural: (n: number, forms: { one: string; other: string }) => string;
}

/* ── bits ────────────────────────────────────────────────────────────── */

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <p className="font-mono text-[10.5px] uppercase tracking-[0.16em] text-(--color-muted-foreground)">
      {children}
    </p>
  );
}

/** A neutral outcome — cancelled, mismatched — which is not an error. */
function Notice({ children }: { children: React.ReactNode }) {
  return (
    <p
      role="status"
      className="rounded-lg border border-(--color-border) bg-(--color-muted)/40 px-3.5 py-2.5 text-[12.5px] text-(--color-foreground)"
    >
      {children}
    </p>
  );
}

function ErrorNote({
  failure,
  fallback,
}: {
  failure: MetaThreadsFailure | null;
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
