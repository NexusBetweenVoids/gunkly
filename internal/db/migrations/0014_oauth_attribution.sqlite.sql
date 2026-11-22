-- OAuth completions get their own source (the token_id is the grant id), so
-- they are told apart from API token completions. SQLite cannot change a
-- CHECK constraint: rebuild the table. Dropping it cascades to undo_tokens
-- (foreign keys are on), so those rows are saved and put back.
CREATE TEMP TABLE undo_tokens_save AS SELECT * FROM undo_tokens;

CREATE TABLE completions_new (
    id            TEXT PRIMARY KEY,
    chore_id      TEXT NOT NULL REFERENCES chores (id) ON DELETE CASCADE,
    user_id       TEXT REFERENCES users (id) ON DELETE SET NULL, -- NULL = deleted user / household channel
    completed_at  BIGINT NOT NULL,
    source        TEXT NOT NULL CHECK (source IN ('app', 'mark_done_link', 'api_token', 'oauth', 'import')),
    token_id      TEXT,                      -- API token id or mark-done channel id, for attribution
    note          TEXT NOT NULL DEFAULT '',
    reverted_at   BIGINT,
    prev_next_due BIGINT,                    -- chore state before this completion, for undo
    prev_due_date TEXT NOT NULL DEFAULT '',
    created_at    BIGINT NOT NULL
);
INSERT INTO completions_new (id, chore_id, user_id, completed_at, source, token_id, note, reverted_at, prev_next_due, prev_due_date, created_at)
    SELECT id, chore_id, user_id, completed_at, source, token_id, note, reverted_at, prev_next_due, prev_due_date, created_at FROM completions;
DROP TABLE completions;
ALTER TABLE completions_new RENAME TO completions;
CREATE INDEX completions_chore ON completions (chore_id, completed_at);
CREATE INDEX completions_user ON completions (user_id);

INSERT INTO undo_tokens SELECT * FROM undo_tokens_save;
DROP TABLE undo_tokens_save;

-- The registering client key (an IP, IPv6 collapsed to its /64) bounds how
-- many never-used clients one address can keep; it is never logged.
ALTER TABLE oauth_clients ADD COLUMN reg_key TEXT NOT NULL DEFAULT '';
CREATE INDEX oauth_clients_reg ON oauth_clients (reg_key);
