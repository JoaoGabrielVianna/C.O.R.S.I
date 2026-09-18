// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, expect, it } from "vitest";

import { useLibraryParams, resetPage, type LibraryParams } from "./useLibraryParams";

/**
 * The Library's state lives in the URL, and only in the URL.
 *
 * ── What these guard ───────────────────────────────────────────────────
 * Three things, each of which has a cheap wrong version:
 *
 *   1. A refresh, a back button and a pasted link show the same view.
 *      The wrong version is `useState`, which loses all three.
 *
 *   2. An unrecognised value in the address bar is IGNORED, not forwarded.
 *      The wrong version passes it to the API, which makes a bookmark an
 *      input to the backend.
 *
 *   3. Narrowing a filter returns to page one. The wrong version leaves
 *      the reader on page four of a result that now has one page, looking
 *      at an empty list and concluding their Palace is empty.
 */

afterEach(cleanup);

/**
 * The probe renders what it observes instead of assigning to an outer
 * variable.
 *
 * Writing to a module-level binding during render is a side effect in the
 * render phase, which React's own lint rule refuses and which would make
 * the reading depend on when a re-render happened to occur. Rendering the
 * values means the assertions read the same DOM a person would.
 */
function Probe({ apply }: { apply?: Partial<LibraryParams> }) {
  const { params, setParams } = useLibraryParams();
  const location = useLocation();
  return (
    <>
      <output data-testid="params">{JSON.stringify(params)}</output>
      <output data-testid="search">{location.search}</output>
      {apply ? (
        <button type="button" onClick={() => setParams(apply)}>
          apply
        </button>
      ) : null}
    </>
  );
}

function mount(initial: string, apply?: Partial<LibraryParams>) {
  const view = render(
    <MemoryRouter initialEntries={[initial]}>
      <Routes>
        <Route path="/library" element={<Probe apply={apply} />} />
      </Routes>
    </MemoryRouter>,
  );
  return {
    ...view,
    params: () => JSON.parse(screen.getByTestId("params").textContent ?? "{}") as LibraryParams,
    search: () => screen.getByTestId("search").textContent ?? "",
    apply: () => fireEvent.click(screen.getByText("apply")),
  };
}

it("defaults to the rooms tab, active, first page", () => {
  const v = mount("/library");
  expect(v.params().tab).toBe("rooms");
  expect(v.params().status).toBe("active");
  expect(v.params().offset).toBe(0);
  expect(v.params().q).toBe("");
});

it("reads a full view out of the address bar", () => {
  const v = mount(
    "/library?tab=artifacts&q=presente&status=archived&kind=list&room=none&offset=25",
  );
  const p = v.params();
  expect(p.tab).toBe("artifacts");
  expect(p.q).toBe("presente");
  expect(p.status).toBe("archived");
  expect(p.artifactKind).toBe("list");
  expect(p.room).toBe("none");
  expect(p.offset).toBe(25);
});

it("ignores values that are not in the closed vocabulary", () => {
  const v = mount("/library?tab=nonsense&status=whatever&kind=banana&importance=99&offset=-4");
  const p = v.params();
  expect(p.tab).toBe("rooms");
  expect(p.status).toBe("active");
  expect(p.artifactKind).toBeUndefined();
  expect(p.memoryKind).toBeUndefined();
  // Clamped into range rather than forwarded: the floor is 1..5.
  expect(p.minImportance).toBe(5);
  expect(p.offset).toBe(0);
});

it("writes only non-default values, so a plain view has a clean address", () => {
  const v = mount("/library?tab=artifacts&offset=50", { tab: "rooms", offset: 0 });
  v.apply();
  expect(v.search()).toBe("");
});

it("round-trips a change through the URL", () => {
  const v = mount("/library", { tab: "memories", q: "caneca", minImportance: 4 });
  v.apply();
  expect(v.search()).toContain("tab=memories");
  expect(v.search()).toContain("q=caneca");
  expect(v.search()).toContain("importance=4");
  expect(v.params().tab).toBe("memories");
  expect(v.params().minImportance).toBe(4);
});

it("drops filters that do not belong to the tab being written", () => {
  // Importance is a memories filter. Carried onto rooms it would be a key
  // the rooms listing has no use for and a reader cannot see or clear.
  const v = mount("/library?tab=memories&importance=3", { tab: "rooms" });
  v.apply();
  expect(v.search()).not.toContain("importance");
});

it("returns to the first page whenever a filter is narrowed", () => {
  const v = mount("/library?tab=artifacts&offset=75", resetPage({ q: "presente" }));
  expect(v.params().offset).toBe(75);
  v.apply();
  expect(v.params().offset).toBe(0);
  expect(v.search()).not.toContain("offset");
});

it("keeps the page when only the page changes", () => {
  const v = mount("/library?tab=artifacts", { offset: 25 });
  v.apply();
  expect(v.params().offset).toBe(25);
  expect(v.params().tab).toBe("artifacts");
});

it("carries no sensitivity key, whatever the address bar says", () => {
  const v = mount("/library?include_highly_sensitive=true&tab=artifacts", { q: "x" });
  v.apply();
  expect(v.search()).not.toContain("include_highly_sensitive");
  expect(Object.keys(v.params())).not.toContain("includeHighlySensitive");
});
