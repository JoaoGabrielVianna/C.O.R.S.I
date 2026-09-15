-- Reverting means every agent must carry a number again, so the ones that
-- expressed no preference are given the old platform default back.
--
-- That is lossy and deliberately so: "no preference" has nowhere to live in
-- the reverted schema, and the only alternative would be refusing to revert
-- at all. 0.7 is restored rather than 1 because 0.7 is what the column said
-- before, and a down migration's job is to restore the prior shape, not to
-- improve on it.
--
-- Note what it re-creates: the rows filled in here are exactly the ones
-- that will fail on claude-opus-* again. Forward-only in a populated
-- database is the rule for this reason.

UPDATE chat.agents SET temperature = 0.7 WHERE temperature IS NULL;
ALTER TABLE chat.agents ALTER COLUMN temperature SET DEFAULT 0.7;
ALTER TABLE chat.agents ALTER COLUMN temperature SET NOT NULL;
