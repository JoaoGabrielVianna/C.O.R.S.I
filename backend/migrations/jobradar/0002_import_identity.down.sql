DROP INDEX IF EXISTS jobradar.opportunities_import_identity_idx;

ALTER TABLE jobradar.opportunities
    DROP CONSTRAINT IF EXISTS opportunities_import_identity_complete,
    DROP CONSTRAINT IF EXISTS opportunities_import_external_id_check,
    DROP CONSTRAINT IF EXISTS opportunities_import_source_check;

ALTER TABLE jobradar.opportunities
    DROP COLUMN IF EXISTS import_external_id,
    DROP COLUMN IF EXISTS import_source;
