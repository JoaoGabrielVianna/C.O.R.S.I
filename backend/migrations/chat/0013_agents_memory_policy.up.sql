-- Memory Policy, per agent.
--
-- ── What it decides ────────────────────────────────────────────────────
-- Whether this agent may be asked to look at a conversation and propose
-- what is worth remembering, and what extra guidance the user wants that
-- proposal to follow. Nothing here proposes anything on its own: the mode
-- is a permission the user grants, not a schedule.
--
-- ── Why columns and not a table ────────────────────────────────────────
-- The same three reasons as the budget in 0011: the policy is 1:1 with its
-- agent, it is read by an operation that has already loaded the agent, and
-- it has no history of its own. A table would buy a join and a second row
-- to keep in step, in exchange for nothing.
--
-- ── Why two columns and not one JSONB ──────────────────────────────────
-- Two scalar facts, both of which the database can check. A JSONB document
-- would move `mode IN ('off','on_request')` out of the schema and into
-- whichever code path remembered to enforce it, and the first mode added
-- by a future version would be free to appear in rows nothing validates.
--
-- ── Why the default is on_request and not off ──────────────────────────
-- `on_request` costs nothing until somebody types the command: it is a
-- permission, and the operation it permits is one the user starts. An
-- agent defaulted to `off` would answer a deliberate request with a
-- refusal nobody configured, which reads as a broken feature rather than
-- as a policy. `off` is what a user opts into.
ALTER TABLE chat.agents
    -- 'off'        the agent may not be asked to propose memories at all
    -- 'on_request' it may, and only when the user asks for it
    --
    -- No automatic mode exists in this version, and none is listed here.
    -- A value the schema accepts and no code implements would be
    -- configuration describing behaviour that does not exist.
    ADD COLUMN memory_policy_mode TEXT NOT NULL DEFAULT 'on_request'
        CHECK (memory_policy_mode IN ('off', 'on_request')),

    -- The user's addendum to the policy, not the policy itself. The rules
    -- about what is worth remembering ship in code; this is where a person
    -- says "for this agent, also keep track of X".
    --
    -- NOT NULL DEFAULT '' rather than nullable, unlike the budget limits
    -- next door: there, NULL means "no limit" and 0 means "a limit of
    -- zero", two different facts. Here "no guidance" and "empty guidance"
    -- are the same fact, and a schema able to express both would invite
    -- them to disagree.
    ADD COLUMN memory_policy_notes TEXT NOT NULL DEFAULT ''
        CHECK (length(memory_policy_notes) <= 1000);

-- No index. Both columns are read only through the agent row itself, which
-- is already fetched by primary key on every path that needs them, and
-- nothing filters or sorts by either.
