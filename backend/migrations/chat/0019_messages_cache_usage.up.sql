-- What the input of a turn was made of, when the provider said so.
--
-- ── Why nullable, and why that is the whole point ──────────────────────
-- NULL means the provider did not report the figure. It does NOT mean the
-- turn read nothing from cache. Every turn written before this migration is
-- NULL for that reason, and a cache hit rate that treated those as zeros
-- would be averaging in every call the system ever made without measuring
-- one of them.
--
-- The same rule the usage_source column already enforces for the two counts
-- beside it, extended to the counts that say what those two were made of.
--
-- ── Why these three and not the whole frame ────────────────────────────
-- These are the ones a cost question aggregates over. The rest of what a
-- gateway reports — the OpenAI-shaped restatement of the read count, the
-- provider's own total — is preserved per provider call inside
-- context_report.rounds[].usage, which is where a diagnosis is read and
-- where it costs nothing to keep.
ALTER TABLE chat.messages
    ADD COLUMN cache_read_tokens     integer,
    ADD COLUMN cache_creation_tokens integer,
    ADD COLUMN reasoning_tokens      integer;

-- Negative counts are a gateway bug, not a state this table may hold. The
-- application clamps before writing; this is the backstop that keeps a
-- clamp regression from becoming a permanently wrong row.
ALTER TABLE chat.messages
    ADD CONSTRAINT messages_cache_read_tokens_check
        CHECK (cache_read_tokens IS NULL OR cache_read_tokens >= 0),
    ADD CONSTRAINT messages_cache_creation_tokens_check
        CHECK (cache_creation_tokens IS NULL OR cache_creation_tokens >= 0),
    ADD CONSTRAINT messages_reasoning_tokens_check
        CHECK (reasoning_tokens IS NULL OR reasoning_tokens >= 0);
