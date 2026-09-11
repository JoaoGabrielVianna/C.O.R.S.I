-- Where an imported statement came from, as an entity the database names.
--
-- ── The defect this exists to close ────────────────────────────────────
-- external_source is half of the identity duplicate detection matches on.
-- It used to be a free-text `account_scope` supplied by the model, and a
-- live run produced two spellings of the same card in consecutive turns:
--
--     turn N      nubank-4242
--     turn N+1    nubank-cartao-4242
--
-- The same statement, carrying the same bank identifiers, imported twice.
-- Idempotence cannot rest on a language model reproducing a string
-- byte-for-byte across turns, because it demonstrably does not.
--
-- So the namespace becomes an id this table issues. The caller selects a
-- row; the backend derives `import-source:<uuid>` from it. There is no
-- argument through which a model can influence that string.
--
-- ── Why this is not finance.cards ──────────────────────────────────────
-- Because a statement is not always a card. A bank account has no last4
-- in the card sense, no limit, no closing day, and forcing one into
-- finance.cards would make Card mean "any account", which is exactly the
-- kind of quiet remodelling that leaves a table lying about itself.
--
-- Card stays what it is, and an import source MAY point at one. The link
-- is optional and carries no rule: it exists so "the Nubank statement" and
-- "the Nubank card" can be recognised as related on a screen, not so one
-- can stand in for the other.
--
-- ── Why kind, institution and label, and nothing more ──────────────────
-- kind         a card statement and a bank statement are read by a person
--              choosing between them; without it the list is ambiguous
-- institution  two accounts at different banks are otherwise identical
-- label        the operator's own words, and the only thing they type
-- last4        optional, and the single most effective disambiguator when
--              somebody holds three cards at one bank
--
-- No account number, no branch, no holder. None of them has a question in
-- the open, and each would be a piece of a person's banking identity kept
-- for no reason.
--
-- NOTHING in this table participates in the dedup key. The key is the id.
-- A renamed label, a corrected institution, a last4 filled in later: none
-- of them can make an already-imported transaction look new.

CREATE TYPE finance.import_source_kind AS ENUM ('card', 'bank_account', 'other');

CREATE TABLE finance.import_sources (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,

    kind        finance.import_source_kind NOT NULL,
    institution TEXT NOT NULL,
    label       TEXT NOT NULL,
    last4       TEXT,

    -- Optional, and deliberately weak: ON DELETE SET NULL, because a card
    -- being archived must never take an import identity with it. The
    -- transactions imported under this source keep their external_source
    -- whatever happens to the card.
    card_id UUID REFERENCES finance.cards (id) ON DELETE SET NULL,

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at TIMESTAMPTZ,

    CONSTRAINT import_sources_institution_chk CHECK (length(institution) BETWEEN 1 AND 80),
    CONSTRAINT import_sources_label_chk       CHECK (length(label) BETWEEN 1 AND 80),
    CONSTRAINT import_sources_last4_chk       CHECK (last4 IS NULL OR last4 ~ '^[0-9]{4}$')
);

-- One name per workspace among live rows, case-insensitively, matching how
-- categories, cards and persons already behave. Two sources called
-- "Nubank" would make the operator's own choice ambiguous, which is the
-- one thing this table exists to prevent.
CREATE UNIQUE INDEX import_sources_workspace_label_unique
    ON finance.import_sources (workspace_id, lower(label))
    WHERE deleted_at IS NULL;

CREATE INDEX import_sources_workspace_active_idx
    ON finance.import_sources (workspace_id)
    WHERE deleted_at IS NULL;

-- The batch now points at the source rather than carrying a string.
--
-- Nullable and unconstrained in the other direction on purpose: batches
-- created before this migration keep the account_scope they were staged
-- with, and nothing rewrites them. A history that recomputes itself is not
-- a history.
ALTER TABLE finance.import_batches
    ADD COLUMN import_source_id UUID REFERENCES finance.import_sources (id);

CREATE INDEX import_batches_source_idx
    ON finance.import_batches (workspace_id, import_source_id)
    WHERE import_source_id IS NOT NULL;
