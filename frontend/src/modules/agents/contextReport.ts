/**
 * Reading a turn's context report.
 *
 * ── What this module may and may not do ────────────────────────────────
 * It labels, orders and measures **for display**. It does not decide
 * anything. Every fact the Inspector shows — which blocks existed, what was
 * excluded, why, and whether something failed to load — was decided by the
 * backend when the turn happened and is read from the report verbatim.
 *
 * In particular there is no budget arithmetic here. The client does not
 * know the ceilings, does not compare against them, and cannot conclude
 * that something "should have fitted". If it did, two implementations of
 * one rule would exist and the older turns would be the first to be
 * described wrongly.
 *
 * ── Why it is a plain module ───────────────────────────────────────────
 * Pure functions over the API shape, no React. It is the part of the
 * Inspector that could be tested first when the frontend gets a harness.
 */

import type {
  ApiContextBlock,
  ApiContextReport,
  ApiMessage,
  ContextBlockKind,
  ContextExclusionReason,
} from "@/modules/agents/api/conversations";

/* ── vocabulary ──────────────────────────────────────────────────────── */

/**
 * The order blocks are composed in, which is also the order they are shown.
 *
 * The report already arrives in this order; the constant exists so a block
 * the backend adds later still lands somewhere sensible instead of being
 * silently dropped by a lookup table.
 */
const BLOCK_ORDER: ContextBlockKind[] = [
  "instructions",
  "tools",
  "memory",
  "sources",
  "tool_evidence",
  "history",
  "current_message",
  "tool_results",
];

const BLOCK_LABEL: Record<ContextBlockKind, string> = {
  instructions: "Instruções",
  // The declaration of what the agent may run. Labelled as the declaration
  // and not as "Tools", because the block is the schema being sent, not the
  // tools being used — the using is `tool_results`.
  tools: "Ferramentas declaradas",
  memory: "Memória",
  sources: "Fontes",
  // Never "Memória". They are different blocks with different lifetimes,
  // and the module already paid once for confusing the two.
  // What earlier turns of this conversation observed. Named for what it is
  // rather than for where it came from: "Ferramentas anteriores" would read
  // as a list of tools, and the block is the observations, not the tools.
  tool_evidence: "Evidência anterior",
  history: "Conversa",
  current_message: "Mensagem atual",
  tool_results: "Resultado das ferramentas",
};

const REASON_LABEL: Record<ContextExclusionReason, string> = {
  empty_turn: "turno vazio",
  budget: "orçamento",
  too_large: "grande demais",
  unavailable: "indisponível",
  // Reads as a decision, not a shortage. "Excluída" or "cortada" would
  // describe the same row as a loss, and this one is a choice the person
  // made — the transcript shows what they chose, right above.
  not_selected: "não anexada",
  // "cortada" and not "excluída": the item is there, its tail is not, and a
  // reader who confused the two would look for something that is present.
  truncated: "cortada",
  // A recorded call that failed, so it was not replayed as observation.
  failed: "falhou",
  // Replaced by a later, identical call.
  superseded: "substituída",
};

export function blockLabel(kind: ContextBlockKind): string {
  return BLOCK_LABEL[kind] ?? kind;
}

export function reasonLabel(reason: ContextExclusionReason): string {
  return REASON_LABEL[reason] ?? reason;
}

/* ── derived views ───────────────────────────────────────────────────── */

export interface BlockRow {
  kind: ContextBlockKind;
  label: string;
  items: number;
  characters: number;
  estimatedTokens: number;
  /** Share of the turn's estimated tokens, 0..1. For the proportion bar. */
  share: number;
}

/** The blocks that contributed something, in composition order. */
export function blockRows(report: ApiContextReport): BlockRow[] {
  const total = report.total_estimated_tokens;
  return ordered(report.blocks)
    .filter((b) => b.characters > 0)
    .map((b) => ({
      kind: b.kind,
      label: blockLabel(b.kind),
      items: b.items,
      characters: b.characters,
      estimatedTokens: b.estimated_tokens,
      share: total > 0 ? b.estimated_tokens / total : 0,
    }));
}

export interface ExclusionRow {
  kind: ContextBlockKind;
  blockLabel: string;
  reason: ContextExclusionReason;
  reasonLabel: string;
  items: number;
  characters: number;
}

/**
 * Everything the turn left out, except the degradations.
 *
 * `unavailable` is filtered out here and surfaced by `warnings` instead: it
 * is not an item that lost a budget race, it is a block that could not be
 * read at all, and listing it as "1 excluded" would understate it.
 */
export function exclusionRows(report: ApiContextReport): ExclusionRow[] {
  const out: ExclusionRow[] = [];
  for (const block of ordered(report.blocks)) {
    for (const ex of block.exclusions ?? []) {
      if (ex.reason === "unavailable") continue;
      out.push({
        kind: block.kind,
        blockLabel: blockLabel(block.kind),
        reason: ex.reason,
        reasonLabel: reasonLabel(ex.reason),
        items: ex.items,
        characters: ex.characters,
      });
    }
  }
  return out;
}

/**
 * The blocks whose source could not be read for this turn.
 *
 * This is the answer to "was this generated with everything it should have
 * had?", and it comes from the report rather than from any inference about
 * empty blocks — a block can be legitimately empty, and that is not a
 * warning.
 */
export function warnings(report: ApiContextReport): ContextBlockKind[] {
  return ordered(report.blocks)
    .filter((b) => (b.exclusions ?? []).some((ex) => ex.reason === "unavailable"))
    .map((b) => b.kind);
}

function ordered(blocks: ApiContextBlock[]): ApiContextBlock[] {
  return [...blocks].sort((a, b) => rank(a.kind) - rank(b.kind));
}

function rank(kind: ContextBlockKind): number {
  const i = BLOCK_ORDER.indexOf(kind);
  // An unknown kind sorts last rather than to the front, so a block this
  // client has not been taught about still appears instead of hiding.
  return i === -1 ? BLOCK_ORDER.length : i;
}

/* ── usage ───────────────────────────────────────────────────────────── */

/**
 * What a finished turn consumed, in the three states that actually occur.
 *
 * `estimated` is what the builder predicted before the call; `actual` is
 * what the provider reported after it. They are separate fields because
 * they are separate claims, and collapsing them would throw away the only
 * way to tell how good the heuristic is.
 */
export interface TurnUsage {
  estimatedInput: number | null;
  actualInput: number | null;
  output: number | null;
  /** Present only when both input figures are. */
  difference: number | null;
  /** True when the provider sent a usage frame. */
  measured: boolean;
}

export function turnUsage(message: ApiMessage): TurnUsage {
  const measured = message.usage_source === "provider";
  const estimatedInput =
    message.estimated_prompt_tokens ?? message.context_report?.total_estimated_tokens ?? null;
  // Counts only mean something when the source says they do. "unknown"
  // means the zeros are placeholders, and rendering them as measurements
  // would report a turn that consumed nothing.
  const actualInput = measured ? message.prompt_tokens : null;
  const output = message.usage_source && message.usage_source !== "unknown"
    ? message.completion_tokens
    : null;

  return {
    estimatedInput,
    actualInput,
    output,
    difference: estimatedInput !== null && actualInput !== null ? actualInput - estimatedInput : null,
    measured,
  };
}

/**
 * Whether this turn's cost is known.
 *
 * Mirrors the rule established for reports: absent is unknown, and
 * unknown is never zero. Kept as an explicit function so no caller is
 * tempted to write `cost ?? 0`.
 */
export function turnCost(message: ApiMessage): number | null {
  return message.cost ?? null;
}

/* ── tool rounds ─────────────────────────────────────────────────────── */

/**
 * One provider call of a turn, ready to render.
 *
 * The Inspector shows these instead of pretending the first call's snapshot
 * described the whole turn. It is a summary and stays one: counts, a cost,
 * and which tools ran. The arguments and results live in the audit trail,
 * which is a separate read and a separate record.
 */
export interface RoundRow {
  round: number;
  addedTokens: number;
  promptTokens: number;
  completionTokens: number;
  /** True when the provider measured this call rather than us estimating it. */
  measured: boolean;
  /** Null when this call could not be priced. Never render a null as zero. */
  cost: number | null;
  tools: RoundToolRow[];
}

export interface RoundToolRow {
  name: string;
  ok: boolean;
  /** Empty on success. The backend's code, rendered as the reason. */
  errorCode: string;
  durationMS: number;
}

/**
 * The turn's provider calls, or an empty list.
 *
 * Empty means one call, which is every turn that did not use a tool — the
 * backend omits the array rather than sending one of length one, so an
 * ordinary turn is byte-identical to what it was before tools existed.
 */
export function roundRows(report: ApiContextReport): RoundRow[] {
  return (report.rounds ?? []).map((r) => ({
    round: r.round,
    addedTokens: r.added_estimated_tokens,
    promptTokens: r.prompt_tokens,
    completionTokens: r.completion_tokens,
    measured: r.usage_source === "provider",
    cost: r.cost ?? null,
    tools: (r.tools ?? []).map((t) => ({
      name: t.name,
      ok: t.status === "ok",
      errorCode: t.error_code ?? "",
      durationMS: t.duration_ms,
    })),
  }));
}

/** Every tool this turn ran, across all of its rounds, in order. */
export function turnToolCalls(report: ApiContextReport | undefined | null): RoundToolRow[] {
  if (!report) return [];
  return roundRows(report).flatMap((r) => r.tools);
}

const TOOL_ERROR_LABEL: Record<string, string> = {
  tool_not_found: "ferramenta inexistente",
  tool_not_authorized: "não autorizada",
  tool_invalid_arguments: "argumentos inválidos",
  tool_execution_failed: "falhou",
  tool_timeout: "tempo esgotado",
  tool_round_limit: "limite de rodadas",
};

/**
 * The reason a tool call failed, in words.
 *
 * Falls back to the raw code rather than to a generic "erro": a code this
 * client has not been taught about is still more informative than a word
 * that says nothing, and it is the string somebody will search for.
 */
export function toolErrorLabel(code: string): string {
  return TOOL_ERROR_LABEL[code] ?? code;
}
