-- Channel URLs are never shown after saving; url_hint is the masked form the
-- UI may show ("...abcd"), computed when the URL is saved.
ALTER TABLE notification_channels ADD COLUMN url_hint TEXT NOT NULL DEFAULT '';

-- Test messages to a personal channel belong to no household, so a delivery's
-- household is optional. SQLite cannot drop NOT NULL: rebuild the table.
CREATE TABLE deliveries_new (
    id                   TEXT PRIMARY KEY,
    delivery_key         TEXT NOT NULL UNIQUE,
    household_id         TEXT REFERENCES households (id) ON DELETE CASCADE,
    chore_id             TEXT REFERENCES chores (id) ON DELETE CASCADE,
    user_id              TEXT REFERENCES users (id) ON DELETE CASCADE,
    channel_id           TEXT REFERENCES notification_channels (id) ON DELETE SET NULL,
    push_subscription_id TEXT REFERENCES push_subscriptions (id) ON DELETE SET NULL,
    status               TEXT NOT NULL CHECK (status IN ('pending', 'sent', 'failed')),
    attempts             INTEGER NOT NULL DEFAULT 0,
    next_attempt_at      BIGINT,
    last_error           TEXT NOT NULL DEFAULT '',
    created_at           BIGINT NOT NULL,
    sent_at              BIGINT,
    kind                 INTEGER NOT NULL DEFAULT 0,
    due_date             TEXT NOT NULL DEFAULT '',
    fire_at              BIGINT
);
INSERT INTO deliveries_new (id, delivery_key, household_id, chore_id, user_id, channel_id, push_subscription_id, status,
        attempts, next_attempt_at, last_error, created_at, sent_at, kind, due_date, fire_at)
    SELECT id, delivery_key, household_id, chore_id, user_id, channel_id, push_subscription_id, status,
        attempts, next_attempt_at, last_error, created_at, sent_at, kind, due_date, fire_at FROM deliveries;
DROP TABLE deliveries;
ALTER TABLE deliveries_new RENAME TO deliveries;
CREATE INDEX deliveries_pending ON deliveries (next_attempt_at) WHERE status = 'pending';
CREATE INDEX deliveries_household ON deliveries (household_id, created_at);
CREATE INDEX deliveries_user ON deliveries (user_id, created_at);
CREATE INDEX deliveries_created ON deliveries (created_at);
