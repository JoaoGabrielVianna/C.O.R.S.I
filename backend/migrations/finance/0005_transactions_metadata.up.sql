-- Closes the load-bearing semantic gaps for the frontend Transaction model:
--   * status           — paid/pending/scheduled (drives every dashboard total)
--   * payment_method   — debit/credit/pix/cash/transfer (Overview "by method")
--   * source           — manual/whatsapp/import/ai (origin badge)
--   * notes            — free-form text up to 1000 chars
--
-- Scope discipline: this does NOT introduce transfers, installments,
-- splitAmong, planId, persons or account FKs. Those remain v0.3+.

CREATE TYPE finance.transaction_status AS ENUM ('paid', 'pending', 'scheduled');
CREATE TYPE finance.payment_method     AS ENUM ('debit', 'credit', 'pix', 'cash', 'transfer');
CREATE TYPE finance.transaction_source AS ENUM ('manual', 'whatsapp', 'import', 'ai');

ALTER TABLE finance.transactions
    ADD COLUMN status         finance.transaction_status NOT NULL DEFAULT 'paid',
    ADD COLUMN payment_method finance.payment_method     NOT NULL DEFAULT 'pix',
    ADD COLUMN source         finance.transaction_source NOT NULL DEFAULT 'manual',
    ADD COLUMN notes          TEXT                       NOT NULL DEFAULT ''
        CHECK (length(notes) <= 1000);

-- Server-side status filtering is load-bearing: every dashboard surface
-- partitions paid vs scheduled. Partial index keeps the working set tight
-- because soft-deleted rows never participate.
CREATE INDEX transactions_workspace_status_idx
    ON finance.transactions (workspace_id, status)
    WHERE deleted_at IS NULL;
