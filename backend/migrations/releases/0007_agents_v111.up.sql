-- Agents v1.1.1, recorded and published.
--
-- A patch on the version the owner is about to start using daily. It fixes
-- two things a person hits in the first minutes of ordinary use, and adds
-- nothing.
--
-- ── Why PATCH and not MINOR ────────────────────────────────────────────
-- Nothing persisted changed: `turn_references` still stores one row per
-- capability, with the same shape, and rows written by v1.1.0 render
-- identically. No migration outside this timeline. No new domain feature.
-- The wire did gain an accepted value — `kind:"integration"` — but it is
-- additive and in service of correcting the granularity of a capability
-- v1.1.0 already shipped: `kind:"tool"` is still accepted and still tested.
--
-- What justifies a version at all is that the v1.1.0 snapshot describes a
-- selection whose unit has changed. A release history that goes on
-- describing something else is the one thing this context exists to prevent.
--
-- ── Every number below was counted, not quoted ─────────────────────────
-- 692  `grep -rhoE '^func Test[A-Za-z0-9_]*' --include='*_test.go' . | wc -l`
-- 236  `grep -hE '^func Test' internal/chat/*_integration_test.go | wc -l`
-- 215  frontend, `npx vitest run`
--  27  `npx vitest run …/Composer.reopen.test.tsx`
--  17  `ls migrations/chat/*.up.sql | wc -l` — unchanged by this hotfix
--   7  the GitHub tools in internal/integrations/github/tools/tools.go
-- Recounted for this row rather than carried over from v1.1.0.

INSERT INTO releases.releases (
    module_key, version, major, minor, patch,
    status, stability, released_at, published_at,
    summary,
    capabilities, evidence, limitations, decisions, technical_notes, doc_refs
) VALUES (
    'agents', '1.1.1', 1, 1, 1,
    'published', 'stable',
    TIMESTAMPTZ '2026-08-18 00:00:00+00',
    TIMESTAMPTZ '2026-08-18 00:00:00+00',

    'A patch to the composer. `@` now offers the integrations an agent can reach '
    || 'rather than one row per capability, and the `/` and `@` menus reopen after '
    || 'they have been dismissed. Nothing was added, no schema changed, and the '
    || 'capabilities themselves are untouched: a group is a way of naming a set of '
    || 'them, expanded against the grants before anything is exposed.',

    '[
      {"name": "Integration selection (@)", "note": "One row per provider — GitHub, Job Radar — instead of one per capability. The row stands for the capabilities of that provider this agent is already authorized to use."},
      {"name": "Namespace as identity",     "note": "A provider IS the namespace its capabilities are named under. Derived from the tool name on both sides, never a list: an integration that ships later groups correctly the first time it appears."},
      {"name": "Menus that reopen",         "note": "Escape, blur, choosing something, or deleting the token no longer leave `/` or `@` unable to open again. A dismissal belongs to the occurrence the user closed, not to the text they had typed."}
    ]'::jsonb,

    '[
      {"name": "Backend gate",          "note": "make -C backend ci green. 692 Go tests, 236 of them chat integration tests."},
      {"name": "Frontend gate",         "note": "215 tests, build clean, lint with no errors."},
      {"name": "Grouping",              "note": "10 backend integration tests over expansion, refusal, cross-provider isolation and the frozen record; 15 frontend tests over derivation, identity and search. 4 semantic mutations, 4 killed — including expanding from the registry instead of the grants, which would have been a selection that grants."},
      {"name": "Reopen",                "note": "27 tests, the shared cases run once per trigger because the state machine is shared. 6 mutations, 6 killed, one of which reintroduces the original defect."},
      {"name": "Live, integration scope","note": "An agent with 10 authorized capabilities across two providers: `@github` declared 7 and withheld 3, and the turn froze 7 capabilities, all in the github namespace, with nothing from the other provider."},
      {"name": "Live, legacy turn",     "note": "The same agent with no selection declared all 10 and stored no references, exactly as v1.1.0 did."},
      {"name": "Live, historical truth","note": "A grant revoked AFTER a turn left that turn''s frozen record unchanged: still 7 capabilities, still naming the revoked one."}
    ]'::jsonb,

    '[
      {"name": "A provider with no grants is not offered", "note": "It would be a row that cannot be chosen, in a menu whose only purpose is choosing. Authorization is a different screen, and that one shows every provider including the empty ones."},
      {"name": "Everything v1.1.0 listed",                 "note": "This patch changes nothing about autonomous investigation, model-dependent grounding, evidence cost, or the three-round ceiling. See the v1.1.0 snapshot; all of it still applies."}
    ]'::jsonb,

    '[
      {"name": "The wire carries the group, the record keeps the capabilities", "note": "Storing only \"GitHub\" would silently change its meaning the day an eighth GitHub tool shipped: an old turn would start looking as though it could have used a capability that did not exist when it ran. So the selection is expanded on the way in and the individual capabilities are frozen, and the interface groups them back for display."},
      {"name": "Expansion reads the grants, never the registry",               "note": "Expanding from the registry would turn a selection into a grant, which is the one thing a selection may never be. The registry is consulted only to tell \"you have authorized none of this provider\" apart from \"no such provider exists\"."},
      {"name": "A dismissal belongs to an occurrence",                         "note": "It was keyed on the token text, so dismissing `@` stored the empty query and every later `@` was born closed. The occurrence ends the moment the caret is in no token at all, which is what makes a re-typed trigger a new one."},
      {"name": "The grouping already existed",                                 "note": "The authorization screen had derived providers from the namespace since GitHub shipped. The `@` menu reuses that function rather than growing a second one, so there is one answer to \"which provider is this?\"."}
    ]'::jsonb,

    '[
      {"text": "No schema change. `chat.turn_references` keeps its shape, rows written by v1.1.0 render identically, and no migration was needed outside this release timeline."},
      {"text": "The wire accepts kind:\"integration\" in addition to kind:\"tool\". Additive: the legacy value is still accepted and still covered by tests."},
      {"text": "ToolName.Namespace is the canonical accessor, added because the namespace stopped being a naming convention and became an identity that travels on the wire and decides a turn''s scope.", "ref": "chat/domain/tool.go"},
      {"text": "The frontend derives the same way through toolGroups.namespaceOf, and the only place a provider is named in production code decides an icon and nothing else."},
      {"text": "Read/write gates are untouched: a write capability still declares that it changes data, the grounding policy still carves writes out of autonomous use, and a group selection is strictly narrower than the unscoped turn that already declared everything."}
    ]'::jsonb,

    '[
      {"label": "Evidence continuity", "path": "docs/implementation/AGENTS-V11-EVIDENCE-CONTINUITY.md"},
      {"label": "Capability menu",     "path": "docs/implementation/AGENTS-V11-BATCH-04.md"},
      {"label": "GitHub integration",  "path": "docs/implementation/GITHUB-INTEGRATION-V1.md"}
    ]'::jsonb
)
ON CONFLICT (module_key, version) DO NOTHING;
