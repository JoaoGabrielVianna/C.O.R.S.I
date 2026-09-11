/**
 * Known-outstanding hardcoded user-facing copy, per file.
 *
 * ── What this file is ──────────────────────────────────────────────────
 * A ratchet, not a target. The test beside this file fails when a file
 * gains hardcoded copy, or when a new file appears — so the debt can shrink
 * and cannot grow.
 *
 * ── How to change it ───────────────────────────────────────────────────
 * Migrating a file: re-run the scanner, then lower or delete its entry.
 * Lowering is the only edit here that should ever be routine.
 *
 * Raising a number, or adding a key, means shipping new untranslated copy.
 * That is a Definition-of-Done failure rather than a baseline update.
 *
 * Files named in `INTENTIONAL_EXCLUSIONS` (scripts/i18n-scan.mjs) appear
 * here too, so the ratchet still holds them, but they are not counted as
 * translatable debt: nothing a reader can reach renders them.
 *
 * Regenerate with: node scripts/i18n-scan.mjs
 */
export const HARDCODED_COPY_BASELINE = {
  "lib/api/BackendMismatchScreen.tsx": 3,
  "modules/agents/components/AgentsPanel.tsx": 33,
  "modules/agents/components/AgentsSettingsView.tsx": 6,
  "modules/agents/components/PanePicker.tsx": 7,
  "modules/agents/components/ProvidersPanel.tsx": 1,
  "modules/integrations/components/GitHubCard.tsx": 4,
  "pages/app/modules/finance/CardsInvoices.tsx": 2,
  "pages/app/modules/finance/Overview.tsx": 1,
  "pages/app/modules/finance/People.tsx": 2,
  "pages/app/modules/finance/TransactionModal.tsx": 1,
  "pages/app/modules/job-radar/AddOpportunityModal.tsx": 1
} as Record<string, number>;

/** Sum of the above, including the intentionally excluded files. */
export const BASELINE_TOTAL = 61;

/** The part of the total that is still real, translatable debt. */
export const TRANSLATABLE_TOTAL = 12;
