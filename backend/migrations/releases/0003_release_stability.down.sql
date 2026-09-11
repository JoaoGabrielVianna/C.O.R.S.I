-- Dropping the column loses the maturity of every recorded release. There
-- is no way to recover it: 'stable' and 'rc' are decisions, not anything
-- derivable from the rest of the row.
ALTER TABLE releases.releases DROP COLUMN IF EXISTS stability;
