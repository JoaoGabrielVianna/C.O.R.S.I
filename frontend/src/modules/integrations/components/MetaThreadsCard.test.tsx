// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { I18nFixture } from "@/lib/i18n";
import { MetaThreadsCard } from "./MetaThreadsCard";

/**
 * The Meta Threads card's four states, and the properties that outrank
 * them.
 *
 * ── The properties ─────────────────────────────────────────────────────
 *  1. No token, code or app secret ever reaches this component. The card
 *     renders a hint and nothing else, because that is all the backend
 *     returns.
 *  2. The authorization URL is the BACKEND'S. This file never builds one,
 *     never names a scope, and therefore cannot request a write permission.
 *  3. An unsolicited callback is refused. A code arriving with a `state`
 *     this browser did not issue is never redeemed.
 *  4. A deployment with no Meta app says so instead of offering a button
 *     that cannot work.
 *
 * ── Why fetch is stubbed and the hooks are not ─────────────────────────
 * Everything between the component and the network is real: the query
 * hooks, the cache invalidation, the API client and its error mapping. A
 * test that mocked `useMetaThreadsStatus` would prove the card renders what
 * it is handed, which is not in doubt.
 */

const AUTH_URL =
  "https://threads.net/oauth/authorize?client_id=123&scope=threads_basic%2Cthreads_manage_insights";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function wrapper() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  // The language is pinned rather than inherited: the subject of these
  // assertions is the card's behaviour, not which language jsdom reports.
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>
      <I18nFixture lang="pt">{children}</I18nFixture>
    </QueryClientProvider>
  );
}

const notConfigured = { connected: false, configured: false };
const configuredNotConnected = { connected: false, configured: true };

const connected = {
  connected: true,
  configured: true,
  connection: {
    id: "c1",
    token_hint: "…zzzz",
    // Comfortably in the future, so the healthy path is the default.
    token_expires_at: new Date(Date.now() + 45 * 86_400_000).toISOString(),
    scopes: ["threads_basic", "threads_manage_insights", "threads_keyword_search"],
    account_id: "17841400000000000",
    username: "joaocorsi",
    display_name: "João Corsi",
    created_at: "2026-08-23T00:00:00Z",
    updated_at: "2026-08-23T00:00:00Z",
    last_verified_at: "2026-08-23T00:00:00Z",
  },
};

const expiredConnection = {
  ...connected,
  connection: {
    ...connected.connection,
    token_expires_at: new Date(Date.now() - 86_400_000).toISOString(),
  },
};

function stubFetch(routes: Record<string, () => Response>) {
  const calls: { url: string; init?: RequestInit }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = typeof input === "string" ? input : input.toString();
      calls.push({ url, init });
      for (const [needle, respond] of Object.entries(routes)) {
        if (url.includes(needle)) return respond();
      }
      return new Response("not found", { status: 404 });
    }),
  );
  return calls;
}

/** Puts a callback in the address bar, as Meta's redirect would. */
function arriveWith(query: string) {
  window.history.replaceState({}, "", `/app/settings/integrations${query}`);
}


let assign: ReturnType<typeof vi.fn>;

const realLocation = window.location;

beforeEach(() => {
  window.history.replaceState({}, "", "/app/settings/integrations");
  sessionStorage.clear();

  // jsdom implements no navigation, so `assign` has to be replaced — and
  // it cannot be replaced in place: on a real `Location` it is a
  // non-configurable data property, which also makes a Proxy illegal (the
  // trap may not return a different value for it).
  //
  // `window.location` itself IS redefinable, so a plain stand-in with live
  // getters is installed instead. Live, not a snapshot: the card reads
  // `search` on mount, and a value frozen here would make every callback
  // test see the query string as it was BEFORE the test set it — which is
  // how the first version of this file passed for the wrong reason.
  assign = vi.fn();
  const stand_in = {
    get href() {
      return realLocation.href;
    },
    get origin() {
      return realLocation.origin;
    },
    get pathname() {
      return realLocation.pathname;
    },
    get search() {
      return realLocation.search;
    },
    assign,
  };
  Object.defineProperty(window, "location", { configurable: true, get: () => stand_in });
});

afterEach(() => {
  Object.defineProperty(window, "location", { configurable: true, get: () => realLocation });
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("MetaThreadsCard", () => {
  it("explains what is missing when the deployment has no Meta app", async () => {
    stubFetch({ "/integrations/meta-threads/connection": () => jsonResponse(notConfigured) });
    render(<MetaThreadsCard />, { wrapper: wrapper() });

    expect(await screen.findByText(/não tem um app da Meta/i)).toBeTruthy();
    // The three variables an operator has to set are named, and so is the
    // redirect URI they have to register — the card is the instruction.
    expect(screen.getByText(/META_THREADS_APP_ID/)).toBeTruthy();
    expect(screen.getByText(`${window.location.origin}/app/settings/integrations`)).toBeTruthy();
    // And no button that cannot work.
    expect(screen.queryByRole("button", { name: /conectar meta threads/i })).toBeNull();
  });

  it("sends the operator to the URL the BACKEND built, with a state it remembers", async () => {
    stubFetch({
      "/integrations/meta-threads/authorize-url": () =>
        jsonResponse({ authorization_url: AUTH_URL }),
      "/integrations/meta-threads/connection": () => jsonResponse(configuredNotConnected),
    });
    render(<MetaThreadsCard />, { wrapper: wrapper() });

    const connect = await screen.findByRole("button", { name: /conectar meta threads/i });
    await userEvent.click(connect);

    await waitFor(() => {
      expect(assign).toHaveBeenCalledWith(AUTH_URL);
    });
    // The state that left is the state this browser can check on return.
    expect(sessionStorage.getItem("corsi.meta-threads.oauth-state")).toBeTruthy();
  });

  it("never builds an authorization URL itself, so it cannot ask for a write scope", async () => {
    const calls = stubFetch({
      "/integrations/meta-threads/authorize-url": () =>
        jsonResponse({ authorization_url: AUTH_URL }),
      "/integrations/meta-threads/connection": () => jsonResponse(configuredNotConnected),
    });
    render(<MetaThreadsCard />, { wrapper: wrapper() });
    await userEvent.click(await screen.findByRole("button", { name: /conectar meta threads/i }));

    await waitFor(() => expect(assign).toHaveBeenCalled());
    // Every request this card made went to our own backend. The scope list
    // is decided there and is not a value this file can name.
    for (const call of calls) {
      expect(call.url).not.toContain("threads.net/oauth");
    }
    const authCall = calls.find((c) => c.url.includes("authorize-url"));
    expect(authCall).toBeTruthy();
    expect(authCall?.url).not.toContain("scope");
  });

  it("redeems a callback code through the backend and never keeps it", async () => {
    sessionStorage.setItem("corsi.meta-threads.oauth-state", "state-abc");
    arriveWith("?code=AUTH_CODE_123&state=state-abc");

    let statusBody: unknown = configuredNotConnected;
    const calls = stubFetch({
      "/integrations/meta-threads/connection": () => {
        const body = statusBody;
        statusBody = connected;
        return jsonResponse(body);
      },
    });
    render(<MetaThreadsCard />, { wrapper: wrapper() });

    await waitFor(() => {
      const post = calls.find((c) => c.init?.method === "POST");
      expect(post).toBeTruthy();
      expect(String(post?.init?.body)).toContain("AUTH_CODE_123");
    });
    // The code is single-use and short-lived: leaving it in the address bar
    // puts it in history, in a bookmark and in the next screenshot.
    await waitFor(() => expect(window.location.search).not.toContain("AUTH_CODE_123"));
    expect(document.body.textContent).not.toContain("AUTH_CODE_123");
    // The state is consumed, so a replayed callback cannot reuse it.
    expect(sessionStorage.getItem("corsi.meta-threads.oauth-state")).toBeNull();
  });

  it("refuses a callback this browser never asked for", async () => {
    // No state stored: the flow did not start here.
    arriveWith("?code=SOMEBODY_ELSES_CODE&state=forged");
    const calls = stubFetch({
      "/integrations/meta-threads/connection": () => jsonResponse(configuredNotConnected),
    });
    render(<MetaThreadsCard />, { wrapper: wrapper() });

    expect(await screen.findByText(/não corresponde ao pedido/i)).toBeTruthy();
    // The decisive assertion: nothing was redeemed.
    expect(calls.find((c) => c.init?.method === "POST")).toBeUndefined();
    expect(document.body.textContent).not.toContain("SOMEBODY_ELSES_CODE");
  });

  it("says so plainly when the operator cancelled on Meta", async () => {
    arriveWith("?error=access_denied&error_reason=user_denied");
    const calls = stubFetch({
      "/integrations/meta-threads/connection": () => jsonResponse(configuredNotConnected),
    });
    render(<MetaThreadsCard />, { wrapper: wrapper() });

    expect(await screen.findByText(/cancelou a autorização/i)).toBeTruthy();
    expect(calls.find((c) => c.init?.method === "POST")).toBeUndefined();
  });

  it("shows the account, what the credential may do, and how long it lasts", async () => {
    stubFetch({ "/integrations/meta-threads/connection": () => jsonResponse(connected) });
    render(<MetaThreadsCard />, { wrapper: wrapper() });

    expect(await screen.findByText("@joaocorsi")).toBeTruthy();
    expect(screen.getByText("João Corsi")).toBeTruthy();
    // Scopes are rendered as what they MEAN, with the identifier kept
    // beside them so the technical fact survives translation.
    expect(screen.getByText(/Métricas de post e de conta/)).toBeTruthy();
    expect(screen.getByText("threads_manage_insights")).toBeTruthy();
    expect(screen.getByText(/Expira em 45 dias/)).toBeTruthy();
    // And the coverage limit is stated on the card, not only in the docs.
    expect(screen.getByText(/App Review/)).toBeTruthy();
  });

  it("renders an unknown scope rather than silently dropping it", async () => {
    stubFetch({
      "/integrations/meta-threads/connection": () =>
        jsonResponse({
          ...connected,
          connection: { ...connected.connection, scopes: ["threads_basic", "threads_something_new"] },
        }),
    });
    render(<MetaThreadsCard />, { wrapper: wrapper() });

    // A list that dropped it would tell the operator their credential is
    // narrower than it is.
    expect(await screen.findByText("threads_something_new")).toBeTruthy();
    expect(screen.getByText(/não reconhecida/i)).toBeTruthy();
  });

  it("treats an expired token as unhealthy and offers reconnect, not refresh", async () => {
    stubFetch({ "/integrations/meta-threads/connection": () => jsonResponse(expiredConnection) });
    render(<MetaThreadsCard />, { wrapper: wrapper() });

    // Twice on purpose: the pill and the token panel both say it, because
    // an operator scanning the page and one reading it must both find out.
    expect(await screen.findAllByText(/token expirado/i)).toHaveLength(2);
    expect(screen.getByRole("button", { name: /reconectar/i })).toBeTruthy();
    // Refreshing an expired token is refused by Meta and by our backend, so
    // the card does not offer it as if it might work.
    expect(screen.queryByRole("button", { name: /renovar token/i })).toBeNull();
  });

  it("shows the server's own refusal when a refresh is too early", async () => {
    stubFetch({
      "/integrations/meta-threads/connection/refresh": () =>
        jsonResponse(
          {
            error: {
              code: "meta_threads_invalid",
              message:
                "a Meta Threads token can only be refreshed once it is 24 hours old; " +
                "this one may be refreshed after 2026-08-24T12:00:00Z",
            },
          },
          400,
        ),
      "/integrations/meta-threads/connection": () => jsonResponse(connected),
    });
    render(<MetaThreadsCard />, { wrapper: wrapper() });

    await userEvent.click(await screen.findByRole("button", { name: /renovar token/i }));

    // The exact moment comes from the server. No local rule restates it,
    // because a second copy would be free to disagree.
    expect(await screen.findByText(/2026-08-24T12:00:00Z/)).toBeTruthy();
  });

  it("never renders a token, a code or an app secret", async () => {
    stubFetch({ "/integrations/meta-threads/connection": () => jsonResponse(connected) });
    render(<MetaThreadsCard />, { wrapper: wrapper() });

    await screen.findByText("@joaocorsi");
    const rendered = document.body.textContent ?? "";
    // The hint is the ONLY thing about the credential that is shown.
    expect(rendered).toContain("…zzzz");
    for (const secret of ["access_token", "client_secret", "LONG_LIVED", "token_cipher"]) {
      expect(rendered).not.toContain(secret);
    }
  });
});
