// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  usePalaceArtifacts,
  usePalaceMemories,
  usePalaceOverview,
  usePalaceRoom,
  usePalaceRooms,
} from "./usePalace";

/**
 * Leaving a Palace screen and coming back is not new information.
 *
 * ── What was measured ──────────────────────────────────────────────────
 * Every hook here had no `staleTime`, which is zero: the Map, a Room and
 * the Library each re-ran their read on every remount — going back from a
 * Room, switching modules and returning, reopening the Library. At 120ms
 * RTT that is a full round trip spent to receive the bytes already on
 * screen, on every single return.
 *
 * ── What these tests pin, and what they deliberately do not ────────────
 * They pin the WINDOW: one read inside 30 seconds, a second read after it.
 * They do not pin 30 as a number that makes Palace correct — nothing makes
 * a cache correct against a writer it cannot hear. The only writer is an
 * agent working through chat, this frontend has no mutation, and so the
 * staleness is real and bounded rather than prevented. A test that claimed
 * otherwise would be describing a mechanism that does not exist.
 */

const listRooms = vi.fn();
const getRoom = vi.fn();
const getOverview = vi.fn();
const listArtifacts = vi.fn();
const listMemories = vi.fn();

vi.mock("../api/palace", () => ({
  listRooms: (...a: unknown[]) => listRooms(...a),
  getRoom: (...a: unknown[]) => getRoom(...a),
  getOverview: (...a: unknown[]) => getOverview(...a),
  listArtifacts: (...a: unknown[]) => listArtifacts(...a),
  listMemories: (...a: unknown[]) => listMemories(...a),
  getArtifact: vi.fn(),
  getMemory: vi.fn(),
}));

const ROOM_ID = "11111111-1111-1111-1111-111111111111";

/** Each surface: the hook under test and the call it must not repeat. */
const SURFACES = [
  {
    name: "the Palace Map (overview)",
    spy: getOverview,
    reply: { room_total: 3 },
    use: () => usePalaceOverview(),
  },
  {
    name: "a Room",
    spy: getRoom,
    reply: { id: ROOM_ID, name: "Study" },
    use: () => usePalaceRoom(ROOM_ID),
  },
  {
    name: "the room list",
    spy: listRooms,
    reply: { items: [], total: 0 },
    use: () => usePalaceRooms({}),
  },
  {
    name: "the artifact list",
    spy: listArtifacts,
    reply: { items: [], total: 0 },
    use: () => usePalaceArtifacts({}),
  },
  {
    name: "the memory list",
    spy: listMemories,
    reply: { items: [], total: 0 },
    use: () => usePalaceMemories({}),
  },
] as const;

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

function mount(use: () => { data?: unknown; isPending: boolean }) {
  function Probe() {
    const q = use();
    return <output data-testid="state">{q.isPending ? "pending" : "ready"}</output>;
  }
  return render(
    <QueryClientProvider client={client}>
      <Probe />
    </QueryClientProvider>,
  );
}

describe.each(SURFACES)("$name", ({ spy, reply, use }) => {
  it("reads once when the screen is left and returned to inside 30s", async () => {
    spy.mockResolvedValue(reply);

    const first = mount(use);
    expect(await screen.findByText("ready")).toBeTruthy();
    expect(spy).toHaveBeenCalledTimes(1);

    // The remount is the whole point: React Router unmounts the route
    // subtree, and the previous behaviour turned that into a request.
    first.unmount();
    mount(use);
    expect(await screen.findByText("ready")).toBeTruthy();

    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("reads again once the window has passed", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    spy.mockResolvedValue(reply);

    const first = mount(use);
    expect(await screen.findByText("ready")).toBeTruthy();
    expect(spy).toHaveBeenCalledTimes(1);
    first.unmount();

    await vi.advanceTimersByTimeAsync(31_000);

    mount(use);
    expect(await screen.findByText("ready")).toBeTruthy();
    // Caching is a window, not a freeze. Past it the agent's writes are
    // picked up the next time the screen is opened.
    expect(spy).toHaveBeenCalledTimes(2);
  });
});

it("serves the return from cache without blanking the screen", async () => {
  getOverview.mockResolvedValue({ room_total: 3 });

  const first = mount(() => usePalaceOverview());
  expect(await screen.findByText("ready")).toBeTruthy();
  first.unmount();

  // Not "one request instead of two" — the screen never goes through
  // `isPending` again, so there is no skeleton to flash. Content that is
  // already known does not disappear to be re-learned.
  mount(() => usePalaceOverview());
  expect(screen.getByTestId("state").textContent).toBe("ready");
});
