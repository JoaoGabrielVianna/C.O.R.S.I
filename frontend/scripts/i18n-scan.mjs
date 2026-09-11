/**
 * Hardcoded user-facing copy scanner.
 *
 * ── Why this is not a grep ─────────────────────────────────────────────
 * A grep for quoted strings in a React codebase reports mostly noise:
 * Tailwind classes, route paths, query keys, `lucide-react` icon names,
 * ARIA roles, test ids, model identifiers. A gate that cries wolf on all of
 * those gets silenced within a week, and a silenced gate is worse than no
 * gate — it reads as coverage while providing none.
 *
 * So this looks in two places only, and filters hard:
 *
 *   1. JSX text nodes — the words actually rendered between tags.
 *   2. String literals assigned to props that carry copy by definition:
 *      placeholder, aria-label, title, alt, label, description, and the
 *      handful of named copy props this codebase uses.
 *
 * Everything that looks like an identifier, a path, a URL, a class list, a
 * number, or a lone technical token is dropped. It still is a heuristic,
 * and `BASELINE` below is the honest admission of that: it records what is
 * known-outstanding per file so the gate can ratchet — new hardcoded copy
 * fails, existing debt does not, and paying debt down tightens the gate.
 *
 * Run directly for a report:
 *   node scripts/i18n-scan.mjs            summary by file
 *   node scripts/i18n-scan.mjs --detail   with the strings themselves
 */
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const ROOT = join(fileURLToPath(new URL(".", import.meta.url)), "..", "src");

/**
 * Files the gate tracks but does not count as translatable debt, each with
 * the reason it is excluded. Every entry must name something a reader can
 * never reach — not something that is merely inconvenient to migrate.
 *
 * These are still scanned, and `scanAll()` still reports them, so the
 * ratchet keeps them from growing. They are simply not counted toward
 * "user-facing copy that should have been translated".
 */
export const INTENTIONAL_EXCLUSIONS = {
  "modules/agents/components/AgentsPanel.tsx":
    "LEGACY, unreachable. The agent registry as a table; since replaced by " +
    "per-agent Settings. Nothing imports it — kept only because this tree has no " +
    "version control, per the file's own header.",
  "modules/agents/components/AgentsSettingsView.tsx":
    "LEGACY, unreachable. The three-panel settings screen; its parts live on the " +
    "Providers page and each agent's Settings. Nothing imports it.",
  "modules/agents/components/PanePicker.tsx":
    "FROZEN, unreachable. Part of Comparison Mode, out of scope for Agents v1.0.0 " +
    "by owner decision D8, which says to preserve rather than delete. Nothing imports it.",
  "lib/api/BackendMismatchScreen.tsx":
    "Development-only diagnostic, rendered OUTSIDE the provider tree by " +
    "construction: main.tsx swaps it in for the entire app before <I18nProvider> " +
    "mounts, so `useT` is unavailable to it. It never renders in production, and " +
    "its reader is whoever is running `make dev`.",
  "modules/agents/components/AgentsNav.tsx":
    "LEGACY, unreachable. The Chat / Configuração switcher; the module navigates " +
    "by URL now. Nothing imports it.",
};

/** Props whose string value is, by definition, shown to a person. */
const COPY_PROPS =
  /\b(placeholder|aria-label|title|alt|label|description|tooltip|hint|eyebrow|subtitle|caption|emptyMessage|confirmLabel|cancelLabel|submitLabel|errorMessage|helperText)\s*=\s*(["'])([^"'`]*?)\2/g;

/* ── Filters ─────────────────────────────────────────────────────────── */

/** Shapes that are code, not copy. */
const TECHNICAL = [
  /^[a-z0-9]+([-_./:][a-z0-9]+)+$/i,          // kebab / snake / dotted / path ids
  /^[a-z]+([A-Z][a-z0-9]*)+$/,                 // camelCase identifier
  /^[A-Z][A-Z0-9_]{2,}$/,                      // CONST_CASE
  /^(https?:|\/|#|@|\$|mailto:|data:)/,        // urls, routes, handles
  /^[\d\s.,%+\-x/()]+$/,                       // pure numbers and units
  /^[─-╿\s─│┌┐└┘├┤┬┴┼]+$/,          // box drawing (comment rules)
];

/** Single tokens that are never a sentence. */
const NOT_COPY = new Set([
  "px","rem","em","vh","vw","auto","none","flex","grid","block","hidden","true","false",
  "null","undefined","div","span","button","input","form","svg","img","utc","id","url",
  "api","json","html","css","sql","pt","en","ok",
]);

/** Code fragments that prove a captured span is not a JSX text node. */
const CODE_MARKERS = [
  "=>", "const ", "let ", "var ", "return ", "??", "&&", "||", "===", "!==",
  "function ", "await ", "typeof ", "props.", "useState", "useMemo", "useEffect",
  "?.", "...", "=", ";", "{", "}", "`", "__T",
  // Fragments of a ternary that spans JSX branches. Copy can contain "?"
  // and ":" — "Apagar esta conversa?" is copy — so the marker has to be
  // the punctuation *pair* that only appears in code.
  ") :", "? (", ": (", ") ?", "] :",
];

function looksLikeCopy(raw) {
  const v = raw.trim();
  if (v.length < 3) return false;
  if (!/[a-zA-ZÀ-ÿ]{2,}/.test(v)) return false;
  if (NOT_COPY.has(v.toLowerCase())) return false;
  if (TECHNICAL.some((re) => re.test(v))) return false;
  // A Tailwind-shaped class list: lowercase, hyphens, no sentence punctuation.
  if (/^[a-z0-9:\[\]()#%.\-/ ]+$/.test(v) &&
      /\b(text|bg|border|flex|grid|gap|px|py|mt|mb|ml|mr|w|h|rounded|font|items|justify|size|min|max)-/.test(v)) {
    return false;
  }
  // Needs either a space (a phrase) or four letters (a real word).
  return /\s/.test(v) || /[a-zA-ZÀ-ÿ]{4,}/.test(v);
}

function stripComments(src) {
  return src.replace(/\/\*[\s\S]*?\*\//g, " ").replace(/^[ \t]*\/\/.*$/gm, " ");
}

/**
 * Remove TypeScript type arguments before looking for JSX text.
 * `useState<Conversation | null>(null)` otherwise reads as a text node
 * containing "Conversation | null", which is the single largest source of
 * false positives in a typed React codebase.
 */
function stripTypeArgs(src) {
  let out = src;
  for (let i = 0; i < 4; i++) {
    // The content must look like a type argument, not a JSX tag: no
    // leading slash, and only the characters type syntax actually uses.
    const next = out.replace(
      /\b([A-Za-z_$][\w$]*)<([A-Za-z_$][\w$.,|&\s[\]"']*)>/g,
      "$1__T",
    );
    if (next === out) break;
    out = next;
  }
  return out;
}

export function scanFile(absPath) {
  const src = readFileSync(absPath, "utf8");
  const clean = stripTypeArgs(stripComments(src));
  const hits = new Set();

  for (const m of clean.matchAll(/>([^<>]+)</g)) {
    const text = m[1].trim();
    if (!text || CODE_MARKERS.some((c) => text.includes(c))) continue;
    if (looksLikeCopy(text)) hits.add(text);
  }
  for (const m of clean.matchAll(COPY_PROPS)) {
    if (looksLikeCopy(m[3])) hits.add(`${m[1]}="${m[3]}"`);
  }
  return [...hits];
}

function walk(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    const p = join(dir, entry);
    if (statSync(p).isDirectory()) walk(p, out);
    else if (/\.tsx?$/.test(entry) && !/\.(test|spec)\.tsx?$/.test(entry)) out.push(p);
  }
  return out;
}

/** `{ "relative/path.tsx": count }` for every file with outstanding copy. */
export function scanAll() {
  const report = {};
  for (const file of walk(ROOT)) {
    const hits = scanFile(file);
    if (hits.length) report[relative(ROOT, file)] = hits;
  }
  return report;
}

if (process.argv[1] === fileURLToPath(import.meta.url)) {
  const report = scanAll();
  const rows = Object.entries(report).sort((a, b) => b[1].length - a[1].length);
  let counted = 0;
  let excluded = 0;
  for (const [file, hits] of rows) {
    const skip = file in INTENTIONAL_EXCLUSIONS;
    if (skip) excluded += hits.length;
    else counted += hits.length;
    console.log(`${String(hits.length).padStart(4)}${skip ? "  [excluded]" : "           "}  ${file}`);
    if (process.argv.includes("--detail")) hits.forEach((h) => console.log(`        ${h}`));
  }
  console.log(`\n${counted} translatable across ${rows.length - Object.keys(INTENTIONAL_EXCLUSIONS).filter((f) => f in report).length} files`);
  console.log(`${excluded} excluded (unreachable code, see INTENTIONAL_EXCLUSIONS)`);
}
