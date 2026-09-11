// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { I18nFixture } from "@/lib/i18n";
import { GitHubCard } from "./GitHubCard";

/**
 * The GitHub card's three states, and the one property that outranks them.
 *
 * ── The property ───────────────────────────────────────────────────────
 * A token entered here must not survive in the page. It is submitted and
 * cleared; nothing reads it back, because nothing can — the backend answers
 * with a four-character hint and never with the credential. The last test
 * in this file is the one that would fail if that ever changed.
 *
 * ── Why fetch is stubbed and the hooks are not ─────────────────────────
 * Everything between the component and the network is real: the query
 * hooks, the cache invalidation, the API client and its error mapping. A
 * test that mocked `useGitHubStatus` would prove the card renders whatever
 * it is handed, which is not in doubt.
 */

const TOKEN = "github_pat_11ABCDEFG_thisisnotarealtoken";

function jsonResponse(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function wrapper() {
  // Retries off so a deliberate failure resolves in one tick rather than
  // after the default backoff.
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  // The provider is not decoration here: the card renders copy and formats
  // a timestamp through i18n, and a component that does either without the
  // provider is one that would throw in the app too.
  //
  // The language is pinned rather than inherited. These assertions were
  // written against the Portuguese copy that used to sit in the JSX, and
  // their subject is the card's behaviour, not its language — so they say
  // `pt` out loud instead of depending on what jsdom reports for
  // `navigator.language`.
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>
      <I18nFixture lang="pt">{children}</I18nFixture>
    </QueryClientProvider>
  );
}

const disconnected = { connected: false, repositories: [], organizations: [] };

const connectedNoRepos = {
  connected: true,
  connection: {
    id: "c1",
    auth_kind: "pat",
    token_hint: "...oken",
    account_login: "joaocorsi",
    account_id: 4242,
    account_type: "User",
    account_name: "João Corsi",
    created_at: "2026-08-15T00:00:00Z",
    updated_at: "2026-08-15T00:00:00Z",
  },
  repositories: [],
  organizations: [],
};

const connectedWithRepos = {
  ...connectedNoRepos,
  repositories: [
    {
      id: "r1",
      github_id: 1,
      owner: "joaocorsi",
      name: "c.o.r.s.i",
      full_name: "joaocorsi/c.o.r.s.i",
      private: false,
      default_branch: "main",
      owner_type: "User",
      authorized_at: "2026-08-15T00:00:00Z",
    },
  ],
  organizations: [],
};

/** Routes a stubbed fetch by path, so one test can serve several endpoints. */
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

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

describe("GitHubCard", () => {
  it("offers a connect form when nothing is connected", async () => {
    stubFetch({ "/integrations/github/connection": () => jsonResponse(disconnected) });
    render(<GitHubCard />, { wrapper: wrapper() });

    expect(await screen.findByText("Não conectado")).toBeTruthy();
    expect(screen.getByLabelText(/personal access token/i)).toBeTruthy();
    // The instruction that matters: a read-only, repository-scoped token.
    expect(screen.getByText(/fine-grained token/i)).toBeTruthy();
  });

  it("says plainly that a connected account with no repositories can read nothing", async () => {
    // The state most likely to be misread as working. If the card were
    // quiet here, nobody would believe the two-layer model is enforced.
    stubFetch({ "/integrations/github/connection": () => jsonResponse(connectedNoRepos) });
    render(<GitHubCard />, { wrapper: wrapper() });

    expect(await screen.findByText("Sem repositórios")).toBeTruthy();
    expect(screen.getByText(/nenhum repositório foi autorizado/i)).toBeTruthy();
  });

  it("shows the account and the authorized set once there is one", async () => {
    stubFetch({
      "/integrations/github/connection": () => jsonResponse(connectedWithRepos),
      "/integrations/github/activity": () =>
        jsonResponse({ items: [], repositories_read: 5, per_repository: 5 }),
    });
    render(<GitHubCard />, { wrapper: wrapper() });

    expect(await screen.findByText("Conectado")).toBeTruthy();
    expect(screen.getByText("@joaocorsi")).toBeTruthy();
    expect(screen.getByText("joaocorsi/c.o.r.s.i")).toBeTruthy();
    // The hint, never the credential.
    expect(screen.getByText(/\.\.\.oken/)).toBeTruthy();
  });

  it("surfaces the backend's actionable sentence when the token stops working", async () => {
    // A revoked token must not render as a generic failure: the sentence
    // the backend wrote is the one that says what to do.
    stubFetch({
      "/integrations/github/connection": () =>
        jsonResponse(
          {
            error: {
              code: "github_unauthorized",
              message:
                "GitHub rejected the stored credential. Reconnect GitHub in Integrations with a valid token.",
            },
          },
          409,
        ),
    });
    render(<GitHubCard />, { wrapper: wrapper() });

    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.getByText(/Reconnect GitHub/i)).toBeTruthy();
  });

  it("submits the token once and keeps no copy of it in the page", async () => {
    // The property this whole file exists for.
    const calls = stubFetch({
      "/integrations/github/connection": () => jsonResponse(disconnected),
    });
    const user = userEvent.setup();
    render(<GitHubCard />, { wrapper: wrapper() });

    const field = (await screen.findByLabelText(/personal access token/i)) as HTMLInputElement;
    // A password field, so the value is not shoulder-readable.
    expect(field.type).toBe("password");

    await user.type(field, TOKEN);
    await user.click(screen.getByRole("button", { name: /conectar github/i }));

    // It reached the request...
    await waitFor(() => {
      const post = calls.find((c) => c.init?.method === "POST");
      expect(post).toBeTruthy();
      expect(String(post?.init?.body)).toContain(TOKEN);
    });

    // ...and it is gone from the field afterwards, whatever the outcome. A
    // token left in the DOM sits there until the tab is closed.
    await waitFor(() => {
      expect((screen.getByLabelText(/personal access token/i) as HTMLInputElement).value).toBe("");
    });
    // And it is nowhere else on the page either.
    expect(document.body.textContent ?? "").not.toContain(TOKEN);
  });
});
