DROP INDEX IF EXISTS finance.import_batches_source_idx;
ALTER TABLE finance.import_batches DROP COLUMN IF EXISTS import_source_id;
DROP TABLE IF EXISTS finance.import_sources;
DROP TYPE  IF EXISTS finance.import_source_kind;
