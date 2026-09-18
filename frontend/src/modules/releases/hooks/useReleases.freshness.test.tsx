// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

import {
  useRelease,
  useReleaseModule,
  useReleaseModules,
  usePublishRelease,
} from "./useReleases";

/**
 * A published release snapshot is IMMUTABLE BY DATABASE TRIGGER.
 *
 * That is the whole argument for `staleTime: Infinity` here, and it is why
 * this is the one place in the product that gets it. Re-reading a published
 * release cannot return a different answer, so a refetch is not a
 * correctness measure — it is a request spent confirming that history is
 * still history.
 *
 * ── The three things that could quietly go wrong ───────────────────────
 *   1. `gcTime` left at the default five minutes throws the snapshot away
 *      while the session is still open, and the next visit re-fetches it.
 *      Infinite staleness with a five-minute cache is not caching.
 *
 *   2. A DRAFT is not immutable. Treating the whole route as history would
 *      pin a draft to whatever it looked like when first opened.
 *
 *   3. Publishing changes `current` and `release_count`, which are derived
 *      over the whole set. If `Infinity` also disabled invalidation, the
 *      page would keep describing the release it just superseded — which
 *      is why the value is `Infinity` and not `'static'`.
 */

const listModules = vi.fn();
const getModule = vi.fn();
const getRelease = vi.fn();
const publishRelease = vi.fn();

vi.mock("@/modules/releases/api/releases", () => ({
  listModules: (...a: unknown[]) => listModules(...a),
  getModule: (...a: unknown[]) => getModule(...a),
  getRelease: (...a: unknown[]) => getRelease(...a),
  publishRelease: (...a: unknown[]) => publishRelease(...a),
}));

function release(status: "draft" | "published") {
  return { id: "r1", module_key: "palace", version: "0.0.2", status };
}

let client: QueryClient;

beforeEach(() => {
  vi.clearAllMocks();
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

function mount(ui: React.ReactNode) {
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

function Probe({ use }: { use: () => { isPending: boolean } }) {
  const q = use();
  return <output data-testid="state">{q.isPending ? "pending" : "ready"}</output>;
}

it("does not re-read a published release on remount, hours later", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  getRelease.mockResolvedValue(release("published"));

  const first = mount(<Probe use={() => useRelease("palace", "0.0.2")} />);
  expect(await screen.findByText("ready")).toBeTruthy();
  expect(getRelease).toHaveBeenCalledTimes(1);
  first.unmount();

  // Past the default five-minute gcTime as well: this proves the entry was
  // KEPT, not merely considered fresh.
  await vi.advanceTimersByTimeAsync(2 * 60 * 60 * 1000);

  mount(<Probe use={() => useRelease("palace", "0.0.2")} />);
  expect(screen.getByTestId("state").textContent).toBe("ready");
  expect(getRelease).toHaveBeenCalledTimes(1);
});

it("does re-read a draft, because a draft is not immutable", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  getRelease.mockResolvedValue(release("draft"));

  const first = mount(<Probe use={() => useRelease("palace", "0.0.3")} />);
  expect(await screen.findByText("ready")).toBeTruthy();
  expect(getRelease).toHaveBeenCalledTimes(1);
  first.unmount();

  await vi.advanceTimersByTimeAsync(2 * 60 * 60 * 1000);

  mount(<Probe use={() => useRelease("palace", "0.0.3")} />);
  expect(await screen.findByText("ready")).toBeTruthy();
  expect(getRelease).toHaveBeenCalledTimes(2);
});

it("keeps the module index and a module timeline across remounts", async () => {
  vi.useFakeTimers({ shouldAdvanceTime: true });
  listModules.mockResolvedValue([]);
  getModule.mockResolvedValue({ key: "palace", releases: [] });

  const first = mount(
    <>
      <Probe use={() => useReleaseModules()} />
      <Probe use={() => useReleaseModule("palace")} />
    </>,
  );
  expect(await screen.findAllByText("ready")).toHaveLength(2);
  first.unmount();

  await vi.advanceTimersByTimeAsync(2 * 60 * 60 * 1000);

  mount(
    <>
      <Probe use={() => useReleaseModules()} />
      <Probe use={() => useReleaseModule("palace")} />
    </>,
  );
  expect(listModules).toHaveBeenCalledTimes(1);
  expect(getModule).toHaveBeenCalledTimes(1);
});

it("still invalidates the index when a release is published", async () => {
  const user = userEvent.setup();
  listModules.mockResolvedValue([]);
  publishRelease.mockResolvedValue(release("published"));

  function Publisher() {
    const modules = useReleaseModules();
    const publish = usePublishRelease("palace");
    return (
      <>
        <output data-testid="state">{modules.isPending ? "pending" : "ready"}</output>
        <button type="button" onClick={() => publish.mutate("0.0.2")}>
          publish
        </button>
      </>
    );
  }

  mount(<Publisher />);
  expect(await screen.findByText("ready")).toBeTruthy();
  expect(listModules).toHaveBeenCalledTimes(1);

  await user.click(screen.getByText("publish"));

  // Infinity is a statement about immutable rows, not a way of switching
  // the cache off. An index that survived its own mutation would be the
  // page lying about what is current.
  await vi.waitFor(() => expect(listModules).toHaveBeenCalledTimes(2));
});
