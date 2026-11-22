-- Imported history gets its own completion source, so it can never pass for a
-- completion made in the app, by link or by token.
ALTER TABLE completions DROP CONSTRAINT completions_source_check;
ALTER TABLE completions ADD CONSTRAINT completions_source_check
    CHECK (source IN ('app', 'mark_done_link', 'api_token', 'import'));
