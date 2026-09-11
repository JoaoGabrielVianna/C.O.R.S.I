-- Statement import: a staging area, and the reason it has to exist.
--
-- ── Why staging is not an optimisation ─────────────────────────────────
-- A tool schema in this system is a CLOSED SUBSET: a flat object of scalar
-- properties, no arrays and no nested objects (chat/domain/tool.go). A
-- statement is a list. There is therefore no way for a model to hand 150
-- normalised rows to a tool, and no way for a tool to hand 150 rows back
-- inside MaxToolResultBytes.
--
-- So the rows have to live somewhere between "we parsed them" and "the
-- operator said import". That somewhere is here. It is NOT the ledger:
-- nothing in finance.transactions moves, no total changes, and no read the
-- Ledger can perform returns any of it.
--
-- ── Why the batch survives the tool call ───────────────────────────────
-- All finance capabilities are Confidential, which means the recorder
-- drops their arguments and results. A reconciliation that lived only in a
-- tool result would be erased from the audit at the moment it was written.
-- The counts below are the durable record that 150 lines arrived and where
-- each one went.
--
-- ── What is deliberately NOT stored ────────────────────────────────────
-- The raw file. content_sha256 is operational evidence — "this is the same
-- text you previewed" — and never an identity for a transaction: the same
-- content may legitimately be imported again after a delete, and different
-- content may carry the same transactions.

CREATE TYPE finance.import_format AS ENUM ('csv');

CREATE TYPE finance.import_batch_status AS ENUM (
    'prepared',   -- parsed and classified; nothing in the ledger
    'committed',  -- the eligible rows were materialised
    'discarded'
);

-- The classification of one parsed line.
--
-- Six states, each earned by a case the design audit found. There is no
-- state here for a card that could not be resolved: a statement does not
-- say which card it belongs to, transactions.account_id is closed to new
-- construction, and a state nobody could ever resolve would be decoration.
CREATE TYPE finance.import_row_state AS ENUM (
    'ready',
    'duplicate',            -- STRONG identity already present. Provable.
    'ambiguous_duplicate',  -- heuristic match only. NEVER dropped silently.
    'unresolved_category',
    'ambiguous_internal',   -- may be a transfer between the operator's own
                            -- accounts; a statement shows one leg only.
    'invalid'
);

-- How a row claims to be identifiable.
--
-- ── Why this is a stored column and not a convention in a string ───────
-- STRONG is an identifier the institution issued and stands behind: an OFX
-- FITID. HEURISTIC is a hash WE derived from the fields we happened to
-- get. They are not the same kind of claim, and the difference decides
-- what the system is allowed to do without asking.
--
-- Two legitimate purchases can share account, date, amount and description
-- — the same coffee, twice, same afternoon. No fingerprint can tell that
-- apart from one purchase exported twice. So a heuristic match is a
-- QUESTION, and only a strong match is an answer.
CREATE TYPE finance.import_identity_strategy AS ENUM ('strong', 'heuristic');

CREATE TABLE finance.import_batches (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL,

    -- The operator's own words for where this came from. Never a path from
    -- their filesystem.
    source_label TEXT NOT NULL,

    -- The STABLE identity of the account, which becomes external_source on
    -- every row this batch creates. It must not vary between exports of the
    -- same account: if it carried the month or the filename, the same
    -- transaction re-exported next month would look new.
    account_scope TEXT NOT NULL,

    format         finance.import_format       NOT NULL,
    status         finance.import_batch_status NOT NULL DEFAULT 'prepared',
    content_sha256 TEXT NOT NULL,

    -- Reconciliation. row_count is the number of lines the parser saw, and
    -- the six state counts must always sum to it — no line may vanish.
    row_count                 INTEGER NOT NULL DEFAULT 0,
    created_count             INTEGER NOT NULL DEFAULT 0,
    skipped_count             INTEGER NOT NULL DEFAULT 0,

    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    committed_at TIMESTAMPTZ,

    CONSTRAINT import_batches_source_label_chk  CHECK (length(source_label)  BETWEEN 1 AND 120),
    CONSTRAINT import_batches_account_scope_chk CHECK (length(account_scope) BETWEEN 1 AND 60),
    CONSTRAINT import_batches_sha_chk           CHECK (content_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT import_batches_counts_chk        CHECK (
        row_count >= 0 AND created_count >= 0 AND skipped_count >= 0
    ),
    CONSTRAINT import_batches_committed_chk     CHECK (
        (status = 'committed' AND committed_at IS NOT NULL)
     OR (status <> 'committed' AND committed_at IS NULL)
    )
);

CREATE INDEX import_batches_workspace_idx
    ON finance.import_batches (workspace_id, created_at DESC);

CREATE TABLE finance.import_rows (
    id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    batch_id UUID NOT NULL REFERENCES finance.import_batches (id) ON DELETE CASCADE,
    -- Denormalised so every predicate can be workspace-scoped without a
    -- join. The batch owns it; this is a copy the query planner can use.
    workspace_id UUID NOT NULL,

    -- Position in the file, 1-based. Kept so a reported problem can be
    -- pointed at: "line 47" is actionable, "a line" is not.
    line_number INTEGER NOT NULL,

    -- ── The normalised line ────────────────────────────────────────────
    -- occurred_at is a timestamptz because that is what a Transaction
    -- carries. amount_cents is int64, exactly as the ledger stores it: the
    -- decimal text from the file is converted ONCE, here, by integer
    -- arithmetic, and never by a float.
    occurred_at  TIMESTAMPTZ,
    amount_cents BIGINT,
    description  TEXT NOT NULL DEFAULT '',
    direction    finance.entry_type,

    -- group_key is the normalised description. It is what the operator
    -- assigns a category to: 150 lines resolve through ~9 groups, and a
    -- tool schema of flat scalars can carry one group key per call.
    group_key TEXT NOT NULL DEFAULT '',

    state finance.import_row_state NOT NULL,
    -- Why this row is in that state, for a human. Never a stack trace.
    state_detail TEXT NOT NULL DEFAULT '',

    identity_strategy finance.import_identity_strategy,
    -- The value that will land in transactions.external_id. For a strong
    -- row it is the institution's id; for a heuristic row it is our hash.
    identity_value TEXT,

    -- Set by resolve_group, or by the parser when the description matches
    -- exactly one existing category by name. Never invented.
    category_id UUID REFERENCES finance.categories (id),

    -- The transaction this row became, once committed. NULL until then,
    -- and the proof that a row did or did not enter the ledger.
    transaction_id UUID REFERENCES finance.transactions (id),

    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT import_rows_line_chk        CHECK (line_number >= 1),
    CONSTRAINT import_rows_amount_chk      CHECK (amount_cents IS NULL OR amount_cents > 0),
    CONSTRAINT import_rows_description_chk CHECK (length(description) <= 280),
    CONSTRAINT import_rows_group_key_chk   CHECK (length(group_key) <= 280),
    CONSTRAINT import_rows_identity_chk    CHECK (
        (identity_strategy IS NULL     AND identity_value IS NULL)
     OR (identity_strategy IS NOT NULL AND identity_value IS NOT NULL
         AND length(identity_value) BETWEEN 1 AND 200)
    ),
    -- An INVALID row has nothing usable; every other state must carry the
    -- three fields a Transaction cannot be built without. This is what
    -- makes "commit never imports an invalid row" a property of the schema
    -- rather than a promise of the code.
    CONSTRAINT import_rows_parsed_chk CHECK (
        state = 'invalid'
     OR (occurred_at IS NOT NULL AND amount_cents IS NOT NULL AND direction IS NOT NULL)
    ),
    CONSTRAINT import_rows_committed_chk CHECK (
        transaction_id IS NULL OR state = 'ready'
    ),
    CONSTRAINT import_rows_line_unique UNIQUE (batch_id, line_number)
);

CREATE INDEX import_rows_batch_state_idx ON finance.import_rows (batch_id, state);
CREATE INDEX import_rows_batch_group_idx ON finance.import_rows (batch_id, group_key)
    WHERE state = 'unresolved_category';
CREATE INDEX import_rows_workspace_idx   ON finance.import_rows (workspace_id, batch_id);
