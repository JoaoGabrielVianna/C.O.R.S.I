import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import * as api from "@/modules/job-radar/api";
import {
  EMPTY_FILTERS,
  type Company,
  type Filters,
  type JobRadarState,
  type Opportunity,
  type PipelineStage,
} from "./types";

/**
 * Job Radar store — backed by the Job Radar API.
 *
 * ── What changed, and what deliberately did not ────────────────────────
 * The previous version of this file kept the whole module in
 * `localStorage`, and its own header named the swap:
 *
 *   "Backend insertion point: when the collector + API exist, replace the
 *    `readStored` / `writeStored` pair with HTTP calls; the action surface
 *    below stays identical."
 *
 * That is what happened. Every action keeps its name and its arguments, so
 * the screens are untouched. The actions are now async — the callers all
 * ignored the return value already, and a write that pretends to be
 * synchronous when it crosses a network is a lie the UI eventually trips
 * over.
 *
 * ── Why the module needed a server at all ──────────────────────────────
 * Not for durability. For REACHABILITY: a pipeline that exists only inside
 * one browser tab cannot be read or changed by anything else — not another
 * device, and not an agent holding a `job_radar.*` grant. The tools call
 * the same application layer these routes call, which is what makes a move
 * made in conversation and a move made by dragging a card the same event.
 *
 *   agent → tool → jobradar/app ← HTTP ← this store
 *
 * ── The refresh strategy ───────────────────────────────────────────────
 * Every write refetches the list. It is the honest and cheap choice at this
 * size: a personal job search is tens of rows, the alternative is
 * hand-written cache patching that can disagree with the server, and the
 * one thing this migration must not introduce is a UI that shows a stage
 * the database does not have.
 */

const INITIAL_STATE: JobRadarState = {
  companies: [],
  opportunities: [],
  filters: EMPTY_FILTERS,
  savedFilters: [],
  modalId: null,
};

/* ── the legacy document ─────────────────────────────────────────────── */

/**
 * The key the browser-only version wrote to.
 *
 * It is read, never written, and never deleted by this file. The import is
 * a one-way migration into Postgres, and removing the source before the
 * user has seen the result would destroy the only copy of a real job search
 * if anything went wrong. Clearing it is left to a person who has looked.
 */
const LEGACY_STORAGE_KEY = "corsi.module.jobradar.v1";

/**
 * Marks that the migration has been offered and answered.
 *
 * ── What this flag is, and what it emphatically is not ─────────────────
 * It is a UX hint: "do not put this banner in front of me again". It is
 * NOT the record of whether the data was imported, and it never was fit to
 * be one — it is per ORIGIN, so the same document offered at :5173, :5174
 * and :5175 carried three independent answers, and the same pipeline could
 * be imported three times over with each browser believing it was the
 * first.
 *
 * The server is the authority now: every item carries its legacy id, and a
 * record already migrated comes back as `already_imported` rather than
 * being created again. Losing this flag therefore costs a redundant banner
 * and nothing else, which is the correct amount of damage for a client-side
 * preference to be able to do.
 */
const IMPORT_DONE_KEY = "corsi.module.jobradar.imported.v1";

interface LegacyDoc {
  companies?: { id: string; name: string; domain?: string }[];
  opportunities?: Opportunity[];
}

function readLegacy(): LegacyDoc | null {
  if (typeof window === "undefined") return null;
  try {
    const raw = window.localStorage.getItem(LEGACY_STORAGE_KEY);
    if (!raw) return null;
    const parsed = JSON.parse(raw) as LegacyDoc;
    if (!parsed.opportunities?.length) return null;
    return parsed;
  } catch {
    return null;
  }
}

function legacyAlreadyImported(): boolean {
  if (typeof window === "undefined") return true;
  return window.localStorage.getItem(IMPORT_DONE_KEY) === "true";
}

/**
 * Turns the browser document into import items.
 *
 * ── What this function is responsible for not losing ───────────────────
 * Three things were silently dropped here until they were audited, and all
 * three had a destination waiting for them on the server:
 *
 *   legacy id     the record's own id, which is what lets a repeated
 *                 import be recognised instead of duplicated
 *   its clock     createdAt/updatedAt. Letting the server stamp "now"
 *                 resets every "12 days in Applied" to zero on migration
 *                 day, which is the one number the board exists to show
 *   its history   tracking.history. Without it a record that went
 *                 saved → applied → interview arrives with a timeline that
 *                 claims it appeared in `interview` out of nowhere
 *
 * The company `domain` is the fourth, and it is carried for the same
 * reason: the column exists, so dropping it was a loss with no upside.
 */
function toImportItems(doc: LegacyDoc): api.ImportItem[] {
  const companies = new Map<string, { name: string; domain?: string }>();
  for (const c of doc.companies ?? []) {
    companies.set(c.id, { name: c.name, domain: c.domain });
  }

  return (doc.opportunities ?? []).map((o) => {
    const company = companies.get(o.companyId);
    return {
      legacy_id: o.id,
      company: company?.name ?? "Desconhecida",
      company_domain: company?.domain,
      role: o.role,
      salary: o.salary,
      location: o.location,
      stack: o.stack,
      description: o.description,
      source: o.source || "manual",
      source_url: o.sourceUrl,
      match_percent: o.matchPercent ?? null,
      posted_at: o.postedAt,
      stage: o.tracking?.stage ?? "discover",
      tracked_at: o.tracking?.trackedAt,
      stage_entered_at: o.tracking?.stageEnteredAt,
      next_action: o.tracking?.nextAction,
      notes: o.notes,
      created_at: o.createdAt,
      updated_at: o.updatedAt,
      // Sent as-is. An entry naming a stage this system does not have —
      // the legacy document contains `recruiter` — fails that ITEM on the
      // server with a message naming the stage, rather than being mapped
      // to something plausible here. A guess made in the client would be
      // invisible in the result.
      history: o.tracking?.history?.map((h) => ({ stage: h.stage, at: h.at })),
    };
  });
}

export type NewOpportunityInput = {
  companyName: string;
  role: string;
  salary: string;
  location: string;
  stack: string[];
  description: string;
  source?: string;
  sourceUrl?: string;
};

export function useJobRadar() {
  const [state, setState] = useState<JobRadarState>(INITIAL_STATE);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  /** The legacy document waiting to be imported, if there is one. */
  const [pendingImport, setPendingImport] = useState<LegacyDoc | null>(null);
  const [importing, setImporting] = useState(false);
  const [importResult, setImportResult] = useState<api.ImportResult | null>(null);

  // Guards the effect against React 18 double-invocation in development,
  // which would otherwise fire two loads on mount.
  const loadedOnce = useRef(false);

  const refresh = useCallback(async () => {
    const [opportunities, companies] = await Promise.all([
      api.listOpportunities(),
      api.listCompanies(),
    ]);
    setState((prev) => ({
      ...prev,
      opportunities: opportunities.items,
      companies: companies.map((c) => ({ id: c.id, name: c.name, domain: c.domain })),
    }));
  }, []);

  /**
   * Runs a write and resynchronises.
   *
   * The error is surfaced rather than swallowed: the previous version could
   * not fail, so nothing in the UI was built to notice one, and a silently
   * dropped write would look exactly like a successful one until the page
   * was reloaded.
   */
  const mutate = useCallback(
    async (fn: () => Promise<unknown>) => {
      setError(null);
      try {
        await fn();
        await refresh();
      } catch (e) {
        setError(e instanceof Error ? e.message : "a operação falhou");
      }
    },
    [refresh],
  );

  useEffect(() => {
    if (loadedOnce.current) return;
    loadedOnce.current = true;

    void (async () => {
      try {
        await refresh();
        // Offered whenever there is a legacy document the user has not
        // already dismissed.
        //
        // ── Why this no longer needs to check the server ───────────────
        // The comment that used to sit here claimed it was "only offered
        // when the server has nothing yet", and the code never checked —
        // the two disagreed, and the code was what ran. It mattered because
        // the endpoint duplicated on a second run.
        //
        // It no longer does. Every item carries its legacy id and the server
        // recognises what it has already migrated, so offering the import
        // twice costs a banner rather than a duplicated pipeline. The claim
        // and the behaviour now agree because the behaviour is safe.
        const legacy = readLegacy();
        if (legacy && !legacyAlreadyImported()) setPendingImport(legacy);
      } catch (e) {
        setError(e instanceof Error ? e.message : "não foi possível carregar o Job Radar");
      } finally {
        setLoading(false);
      }
    })();
  }, [refresh]);

  /* ── the migration ─────────────────────────────────────────────────── */

  const runImport = useCallback(async () => {
    if (!pendingImport) return;
    setImporting(true);
    setError(null);
    try {
      const result = await api.importOpportunities(toImportItems(pendingImport));
      setImportResult(result);
      window.localStorage.setItem(IMPORT_DONE_KEY, "true");
      setPendingImport(null);
      await refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : "a importação falhou");
    } finally {
      setImporting(false);
    }
  }, [pendingImport, refresh]);

  /** Declines the migration without touching the stored document. */
  const dismissImport = useCallback(() => {
    window.localStorage.setItem(IMPORT_DONE_KEY, "true");
    setPendingImport(null);
  }, []);

  /* ── Opportunity actions ─────────────────────────────────────────── */

  const addOpportunity = useCallback(
    (input: NewOpportunityInput) =>
      mutate(() =>
        api.createOpportunity({
          company: input.companyName,
          role: input.role,
          salary: input.salary,
          location: input.location,
          stack: input.stack,
          description: input.description,
          source: input.source || "manual",
          source_url: input.sourceUrl,
        }),
      ),
    [mutate],
  );

  const updateOpportunity = useCallback(
    (id: string, patch: Partial<Omit<Opportunity, "id" | "createdAt">>) =>
      mutate(() =>
        api.updateOpportunity(id, {
          role: patch.role,
          salary: patch.salary,
          location: patch.location,
          stack: patch.stack,
          description: patch.description,
          source_url: patch.sourceUrl,
          notes: patch.notes,
        }),
      ),
    [mutate],
  );

  const updateNotes = useCallback(
    (id: string, patch: Partial<Opportunity["notes"]>) => {
      const current = state.opportunities.find((o) => o.id === id);
      if (!current) return Promise.resolve();
      // Notes are sent whole because the wire field is one object. Merging
      // against the loaded record keeps a single-channel edit from blanking
      // the other three.
      return mutate(() =>
        api.updateOpportunity(id, { notes: { ...current.notes, ...patch } }),
      );
    },
    [mutate, state.opportunities],
  );

  const moveStage = useCallback(
    (id: string, stage: PipelineStage) => mutate(() => api.moveOpportunity(id, stage)),
    [mutate],
  );

  const setNextAction = useCallback(
    (id: string, nextAction: string) =>
      mutate(() => api.updateOpportunity(id, { next_action: nextAction })),
    [mutate],
  );

  const untrack = useCallback(
    (id: string) => mutate(() => api.untrackOpportunity(id)),
    [mutate],
  );

  const removeOpportunity = useCallback(
    (id: string) =>
      mutate(async () => {
        await api.deleteOpportunity(id);
        setState((prev) => ({
          ...prev,
          modalId: prev.modalId === id ? null : prev.modalId,
        }));
      }),
    [mutate],
  );

  /* ── modal ─────────────────────────────────────────────────────────── */

  /**
   * Opening the detail also fetches that record's stage history.
   *
   * The listing leaves `history` empty — the server returns transitions
   * only from the by-id read, because a board of forty cards has no use for
   * their four hundred events. The timeline in the modal is the one place
   * that does.
   */
  const openModal = useCallback((id: string | null) => {
    setState((prev) => ({ ...prev, modalId: id }));
    if (!id) return;
    void (async () => {
      try {
        const { history } = await api.getOpportunity(id);
        setState((prev) => ({
          ...prev,
          opportunities: prev.opportunities.map((o) =>
            o.id === id && o.tracking ? { ...o, tracking: { ...o.tracking, history } } : o,
          ),
        }));
      } catch {
        // A timeline that could not be loaded renders empty. It is
        // decoration on a modal whose substance is already on screen, and
        // failing the whole panel over it would be the wrong trade.
      }
    })();
  }, []);

  const closeModal = useCallback(
    () => setState((prev) => ({ ...prev, modalId: null })),
    [],
  );

  /* ── Filter actions ──────────────────────────────────────────────── */

  const setFilters = useCallback((patch: Partial<Filters>) => {
    setState((prev) => ({ ...prev, filters: { ...prev.filters, ...patch } }));
  }, []);

  const clearFilters = useCallback(() => {
    setState((prev) => ({ ...prev, filters: EMPTY_FILTERS }));
  }, []);

  /* ── Derived selectors ───────────────────────────────────────────── */

  const companyById = useMemo(() => {
    const map = new Map<string, Company>();
    for (const c of state.companies) map.set(c.id, c);
    return map;
  }, [state.companies]);

  const modalOpportunity = useMemo(
    () =>
      state.modalId
        ? state.opportunities.find((o) => o.id === state.modalId) ?? null
        : null,
    [state.modalId, state.opportunities],
  );

  const counts = useMemo(() => {
    let discover = 0;
    let pipeline = 0;
    for (const o of state.opportunities) {
      if (o.tracking) pipeline++;
      else discover++;
    }
    return { discover, pipeline };
  }, [state.opportunities]);

  return {
    state,
    modalOpportunity,
    companyById,
    counts,

    loading,
    error,

    pendingImportCount: pendingImport?.opportunities?.length ?? 0,
    importing,
    importResult,
    runImport,
    dismissImport,

    addOpportunity,
    updateOpportunity,
    updateNotes,
    moveStage,
    setNextAction,
    untrack,
    removeOpportunity,

    openModal,
    closeModal,

    setFilters,
    clearFilters,
  };
}

export type JobRadarStore = ReturnType<typeof useJobRadar>;
