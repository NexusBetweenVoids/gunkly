-- Phase 1A schema. Portable SQL for SQLite and Postgres.
-- Conventions: TEXT UUIDv7 primary keys; BIGINT Unix-millisecond UTC
-- timestamps; *_lower columns for case-insensitive uniqueness; secrets stored
-- as SHA-256 hex hashes (*_hash) or key-versioned ciphertext (*_ciphertext).

CREATE TABLE users (
    id                     TEXT PRIMARY KEY,
    email                  TEXT,
    email_lower            TEXT UNIQUE,
    username               TEXT NOT NULL,
    username_lower         TEXT NOT NULL UNIQUE,
    display_name           TEXT NOT NULL,
    password_hash          TEXT NOT NULL,
    locale                 TEXT NOT NULL DEFAULT '',  -- '' = follow browser
    week_start             INTEGER CHECK (week_start BETWEEN 0 AND 6), -- NULL = locale default; 0 = Sunday
    is_admin               BOOLEAN NOT NULL DEFAULT FALSE,
    totp_secret_ciphertext TEXT,
    totp_enabled           BOOLEAN NOT NULL DEFAULT FALSE,
    email_verified         BOOLEAN NOT NULL DEFAULT FALSE,
    password_changed_at    BIGINT NOT NULL,
    created_at             BIGINT NOT NULL,
    last_active_at         BIGINT NOT NULL
);
CREATE INDEX users_last_active ON users (last_active_at);

CREATE TABLE recovery_codes (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash  TEXT NOT NULL,
    used_at    BIGINT,
    created_at BIGINT NOT NULL
);
CREATE INDEX recovery_codes_user ON recovery_codes (user_id);

-- Single-use emailed tokens: verification, password reset, email OTP.
CREATE TABLE user_tokens (
    token_hash TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose    TEXT NOT NULL CHECK (purpose IN ('verify_email', 'reset_password', 'email_otp')),
    attempts   INTEGER NOT NULL DEFAULT 0,
    created_at BIGINT NOT NULL,
    expires_at BIGINT NOT NULL,
    used_at    BIGINT
);
CREATE INDEX user_tokens_user ON user_tokens (user_id, purpose);
CREATE INDEX user_tokens_expires ON user_tokens (expires_at);

CREATE TABLE sessions (
    id           TEXT PRIMARY KEY,           -- public id for the device list
    token_hash   TEXT NOT NULL UNIQUE,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    user_agent   TEXT NOT NULL DEFAULT '',   -- short label, e.g. "Firefox on Android"
    created_at   BIGINT NOT NULL,
    last_seen_at BIGINT NOT NULL,
    expires_at   BIGINT NOT NULL,            -- absolute maximum
    step_up_at   BIGINT
);
CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_expires ON sessions (expires_at);

CREATE TABLE plans (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    limits     TEXT NOT NULL DEFAULT '{}',  -- JSON object of limit overrides
    created_at BIGINT NOT NULL
);

CREATE TABLE households (
    id                      TEXT PRIMARY KEY,
    name                    TEXT NOT NULL,
    timezone                TEXT NOT NULL,          -- IANA name
    quiet_start_min         INTEGER CHECK (quiet_start_min BETWEEN 0 AND 1439),
    quiet_end_min           INTEGER CHECK (quiet_end_min BETWEEN 0 AND 1439),
    default_reminder_preset TEXT NOT NULL DEFAULT '',
    plan_id                 TEXT REFERENCES plans (id) ON DELETE SET NULL,
    created_at              BIGINT NOT NULL
);

CREATE TABLE memberships (
    household_id TEXT NOT NULL REFERENCES households (id) ON DELETE CASCADE,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role         TEXT NOT NULL CHECK (role IN ('owner', 'member', 'guest')),
    joined_at    BIGINT NOT NULL,
    PRIMARY KEY (household_id, user_id)
);
CREATE INDEX memberships_user ON memberships (user_id);

CREATE TABLE invites (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households (id) ON DELETE CASCADE,
    token_hash   TEXT NOT NULL UNIQUE,
    role         TEXT NOT NULL CHECK (role IN ('member', 'guest')),
    created_by   TEXT REFERENCES users (id) ON DELETE SET NULL,
    created_at   BIGINT NOT NULL,
    expires_at   BIGINT NOT NULL,
    revoked_at   BIGINT
);
CREATE INDEX invites_household ON invites (household_id);
CREATE INDEX invites_expires ON invites (expires_at);

CREATE TABLE areas (
    id           TEXT PRIMARY KEY,
    household_id TEXT NOT NULL REFERENCES households (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    name_lower   TEXT NOT NULL,
    created_at   BIGINT NOT NULL,
    UNIQUE (household_id, name_lower)
);

CREATE TABLE chores (
    id                  TEXT PRIMARY KEY,
    household_id        TEXT NOT NULL REFERENCES households (id) ON DELETE CASCADE,
    area_id             TEXT REFERENCES areas (id) ON DELETE SET NULL, -- same household: enforced by store
    name                TEXT NOT NULL,
    notes               TEXT NOT NULL DEFAULT '',
    link                TEXT NOT NULL DEFAULT '',
    kind                TEXT NOT NULL CHECK (kind IN ('recurring', 'oneoff')),
    schedule_type       TEXT NOT NULL CHECK (schedule_type IN ('interval', 'rrule')),
    interval_n          INTEGER CHECK (interval_n > 0),
    interval_unit       TEXT CHECK (interval_unit IN ('day', 'week', 'month')),
    rrule               TEXT NOT NULL DEFAULT '',
    anchor              TEXT NOT NULL CHECK (anchor IN ('from_completion', 'from_scheduled')),
    first_due           TEXT NOT NULL,        -- local date YYYY-MM-DD in household time zone
    due_date            TEXT NOT NULL,        -- local date of the current occurrence
    next_due            BIGINT,               -- UTC ms of the current occurrence; NULL when none
    paused_until        BIGINT,
    paused_indefinitely BOOLEAN NOT NULL DEFAULT FALSE,
    archived_at         BIGINT,
    reminder_preset     TEXT NOT NULL DEFAULT '', -- '' = household default; 'custom' = chore_reminders rows
    created_by          TEXT REFERENCES users (id) ON DELETE SET NULL,
    created_at          BIGINT NOT NULL,
    updated_at          BIGINT NOT NULL
);
CREATE INDEX chores_household_due ON chores (household_id, next_due);
CREATE INDEX chores_area ON chores (area_id);
-- Scheduler: active chores by due time across all households.
CREATE INDEX chores_active_due ON chores (next_due) WHERE archived_at IS NULL AND paused_indefinitely = FALSE;

CREATE TABLE chore_assignees (
    chore_id TEXT NOT NULL REFERENCES chores (id) ON DELETE CASCADE,
    user_id  TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    PRIMARY KEY (chore_id, user_id)
);
CREATE INDEX chore_assignees_user ON chore_assignees (user_id);

CREATE TABLE chore_reminders (
    id          TEXT PRIMARY KEY,
    chore_id    TEXT NOT NULL REFERENCES chores (id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK (kind IN ('before', 'on', 'overdue')),
    days_before INTEGER NOT NULL DEFAULT 0 CHECK (days_before >= 0),
    time_of_day INTEGER NOT NULL CHECK (time_of_day BETWEEN 0 AND 1439) -- minutes after local midnight
);
CREATE INDEX chore_reminders_chore ON chore_reminders (chore_id);

CREATE TABLE chore_mutes (
    user_id  TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    chore_id TEXT NOT NULL REFERENCES chores (id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, chore_id)
);
CREATE INDEX chore_mutes_chore ON chore_mutes (chore_id);

CREATE TABLE completions (
    id            TEXT PRIMARY KEY,
    chore_id      TEXT NOT NULL REFERENCES chores (id) ON DELETE CASCADE,
    user_id       TEXT REFERENCES users (id) ON DELETE SET NULL, -- NULL = deleted user / household channel
    completed_at  BIGINT NOT NULL,
    source        TEXT NOT NULL CHECK (source IN ('app', 'mark_done_link', 'api_token')),
    token_id      TEXT,                      -- API token id or mark-done channel id, for attribution
    note          TEXT NOT NULL DEFAULT '',
    reverted_at   BIGINT,
    prev_next_due BIGINT,                    -- chore state before this completion, for undo
    prev_due_date TEXT NOT NULL DEFAULT '',
    created_at    BIGINT NOT NULL
);
CREATE INDEX completions_chore ON completions (chore_id, completed_at);
CREATE INDEX completions_user ON completions (user_id);

CREATE TABLE notification_channels (
    id             TEXT PRIMARY KEY,
    scope          TEXT NOT NULL CHECK (scope IN ('user', 'household')),
    user_id        TEXT REFERENCES users (id) ON DELETE CASCADE,
    household_id   TEXT REFERENCES households (id) ON DELETE CASCADE,
    service        TEXT NOT NULL,            -- shoutrrr service kind, e.g. telegram
    url_ciphertext TEXT NOT NULL,
    label          TEXT NOT NULL DEFAULT '',
    created_at     BIGINT NOT NULL,
    updated_at     BIGINT NOT NULL,
    CHECK ((scope = 'user' AND user_id IS NOT NULL AND household_id IS NULL)
        OR (scope = 'household' AND household_id IS NOT NULL AND user_id IS NULL))
);
CREATE INDEX notification_channels_user ON notification_channels (user_id);
CREATE INDEX notification_channels_household ON notification_channels (household_id);

CREATE TABLE push_subscriptions (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    endpoint        TEXT NOT NULL UNIQUE,
    keys_ciphertext TEXT NOT NULL,
    user_agent      TEXT NOT NULL DEFAULT '',
    created_at      BIGINT NOT NULL,
    last_used_at    BIGINT
);
CREATE INDEX push_subscriptions_user ON push_subscriptions (user_id);

CREATE TABLE deliveries (
    id                   TEXT PRIMARY KEY,
    delivery_key         TEXT NOT NULL UNIQUE, -- idempotency: one send per reminder occurrence+target
    household_id         TEXT NOT NULL REFERENCES households (id) ON DELETE CASCADE,
    chore_id             TEXT REFERENCES chores (id) ON DELETE CASCADE,
    user_id              TEXT REFERENCES users (id) ON DELETE CASCADE, -- recipient, NULL for household channel
    channel_id           TEXT REFERENCES notification_channels (id) ON DELETE SET NULL,
    push_subscription_id TEXT REFERENCES push_subscriptions (id) ON DELETE SET NULL,
    status               TEXT NOT NULL CHECK (status IN ('pending', 'sent', 'failed')),
    attempts             INTEGER NOT NULL DEFAULT 0,
    next_attempt_at      BIGINT,
    last_error           TEXT NOT NULL DEFAULT '',
    created_at           BIGINT NOT NULL,
    sent_at              BIGINT
);
CREATE INDEX deliveries_pending ON deliveries (next_attempt_at) WHERE status = 'pending';
CREATE INDEX deliveries_household ON deliveries (household_id, created_at);
CREATE INDEX deliveries_user ON deliveries (user_id, created_at);
CREATE INDEX deliveries_created ON deliveries (created_at);

CREATE TABLE mark_done_tokens (
    token_hash     TEXT PRIMARY KEY,
    chore_id       TEXT NOT NULL REFERENCES chores (id) ON DELETE CASCADE,
    occurrence_due BIGINT NOT NULL,
    user_id        TEXT REFERENCES users (id) ON DELETE CASCADE,                 -- personal-channel recipient
    channel_id     TEXT REFERENCES notification_channels (id) ON DELETE CASCADE, -- household channel
    created_at     BIGINT NOT NULL,
    expires_at     BIGINT NOT NULL,
    used_at        BIGINT,
    CHECK (user_id IS NOT NULL OR channel_id IS NOT NULL)
);
CREATE INDEX mark_done_tokens_chore ON mark_done_tokens (chore_id);
CREATE INDEX mark_done_tokens_user ON mark_done_tokens (user_id);
CREATE INDEX mark_done_tokens_channel ON mark_done_tokens (channel_id);
CREATE INDEX mark_done_tokens_expires ON mark_done_tokens (expires_at);

CREATE TABLE undo_tokens (
    token_hash    TEXT PRIMARY KEY,
    completion_id TEXT NOT NULL REFERENCES completions (id) ON DELETE CASCADE,
    created_at    BIGINT NOT NULL,
    expires_at    BIGINT NOT NULL,
    used_at       BIGINT
);
CREATE INDEX undo_tokens_completion ON undo_tokens (completion_id);
CREATE INDEX undo_tokens_expires ON undo_tokens (expires_at);

CREATE TABLE calendar_feeds (
    id           TEXT PRIMARY KEY,
    token_hash   TEXT NOT NULL UNIQUE,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    household_id TEXT NOT NULL REFERENCES households (id) ON DELETE CASCADE,
    created_at   BIGINT NOT NULL,
    revoked_at   BIGINT
);
CREATE INDEX calendar_feeds_user ON calendar_feeds (user_id, household_id);

CREATE TABLE api_tokens (
    id           TEXT PRIMARY KEY,
    token_hash   TEXT NOT NULL UNIQUE,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         TEXT NOT NULL DEFAULT '',
    scopes       TEXT NOT NULL,              -- space-separated, e.g. "chores:read chores:complete"
    created_at   BIGINT NOT NULL,
    expires_at   BIGINT,
    last_used_at BIGINT,
    revoked_at   BIGINT
);
CREATE INDEX api_tokens_user ON api_tokens (user_id);

CREATE TABLE api_token_households (
    token_id     TEXT NOT NULL REFERENCES api_tokens (id) ON DELETE CASCADE,
    household_id TEXT NOT NULL REFERENCES households (id) ON DELETE CASCADE,
    PRIMARY KEY (token_id, household_id)
);
CREATE INDEX api_token_households_household ON api_token_households (household_id);

CREATE TABLE audit_log (
    id            TEXT PRIMARY KEY,
    actor_user_id TEXT REFERENCES users (id) ON DELETE SET NULL,
    household_id  TEXT REFERENCES households (id) ON DELETE SET NULL,
    event         TEXT NOT NULL,
    ip            TEXT NOT NULL DEFAULT '',
    meta          TEXT NOT NULL DEFAULT '{}', -- JSON; never secrets
    created_at    BIGINT NOT NULL
);
CREATE INDEX audit_log_created ON audit_log (created_at);
CREATE INDEX audit_log_actor ON audit_log (actor_user_id, created_at);
CREATE INDEX audit_log_household ON audit_log (household_id, created_at);

-- Instance settings changed at runtime from the admin panel.
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at BIGINT NOT NULL
);

-- Fixed-window rate-limit counters shared across instances.
CREATE TABLE rate_limits (
    key          TEXT PRIMARY KEY,           -- e.g. "login:ip:203.0.113.5"
    hits         INTEGER NOT NULL,
    window_start BIGINT NOT NULL,
    expires_at   BIGINT NOT NULL
);
CREATE INDEX rate_limits_expires ON rate_limits (expires_at);
