-- Areas are an optional per-household feature, off by default (PRD "Areas").
-- Existing areas and chore-area links are kept either way.
ALTER TABLE households ADD COLUMN areas_enabled BOOLEAN NOT NULL DEFAULT FALSE;
