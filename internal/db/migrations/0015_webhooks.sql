-- Outgoing webhooks (ADR 0017). Unused unless the admin turns the
-- webhooks_enabled instance setting on.
--
-- A webhook belongs to one household. Its URL and signing secret are sealed
-- (auth.Keyring; associated data "webhook-url:<id>" and "webhook:<id>"), so
-- neither leaves the store except for the scheduler's claim. A delivery row is
-- the outbox: it is written in the transaction of the event that caused it
-- and carries the finished JSON payload (no secrets, no email addresses).
CREATE TABLE webhooks (
    id                   TEXT PRIMARY KEY,
    household_id         TEXT NOT NULL REFERENCES households (id) ON DELETE CASCADE,
    description          TEXT NOT NULL DEFAULT '',
    url_ciphertext       TEXT NOT NULL,
    url_hint             TEXT NOT NULL,              -- host only, for display
    secret_ciphertext    TEXT NOT NULL,
    events               TEXT NOT NULL,              -- JSON array of event types
    active               BOOLEAN NOT NULL DEFAULT TRUE,
    failures             INTEGER NOT NULL DEFAULT 0, -- consecutive failed attempts
    auto_disabled_at     BIGINT,                     -- set when 'failures' switched it off
    last_success_at      BIGINT,
    created_by           TEXT REFERENCES users (id) ON DELETE SET NULL,
    created_at           BIGINT NOT NULL,
    updated_at           BIGINT NOT NULL
);
CREATE INDEX webhooks_household ON webhooks (household_id);

CREATE TABLE webhook_deliveries (
    id              TEXT PRIMARY KEY,
    delivery_key    TEXT NOT NULL UNIQUE,            -- webhook id + event id: one delivery per event
    webhook_id      TEXT NOT NULL REFERENCES webhooks (id) ON DELETE CASCADE,
    household_id    TEXT NOT NULL REFERENCES households (id) ON DELETE CASCADE,
    event_id        TEXT NOT NULL,
    event_type      TEXT NOT NULL,
    payload         TEXT NOT NULL,
    status          TEXT NOT NULL,                   -- pending | sent | failed
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at BIGINT,                          -- NULL once sent or failed for good
    http_status     INTEGER NOT NULL DEFAULT 0,      -- of the last attempt; 0 = none received
    duration_ms     INTEGER NOT NULL DEFAULT 0,      -- of the last attempt
    last_error      TEXT NOT NULL DEFAULT '',        -- redacted
    created_at      BIGINT NOT NULL,
    finished_at     BIGINT
);
CREATE INDEX webhook_deliveries_webhook ON webhook_deliveries (webhook_id, created_at);
CREATE INDEX webhook_deliveries_due ON webhook_deliveries (status, next_attempt_at);
CREATE INDEX webhook_deliveries_household ON webhook_deliveries (household_id);
