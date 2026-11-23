-- Outgoing email outbox (ADR 0008): transactional mail is queued here and sent
-- by the scheduler with retries instead of from a fire-and-forget goroutine.
-- The recipient, subject and body (which can hold a sign-in code or a link
-- token) are sealed together in payload; the row is deleted once the message
-- is sent or given up. expires_at stops retries of a message that is no longer
-- useful (an expired code).
CREATE TABLE mail_outbox (
    id              TEXT PRIMARY KEY,
    kind            TEXT NOT NULL,   -- i18n kind stem (verify, reset, otp, ...); safe to log
    user_id         TEXT,            -- recipient account, for audit and account deletion
    payload         TEXT NOT NULL,   -- sealed {to, subject, body}
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at BIGINT NOT NULL, -- also the claim lease while a send is in flight
    expires_at      BIGINT NOT NULL,
    created_at      BIGINT NOT NULL
);
CREATE INDEX mail_outbox_due ON mail_outbox (next_attempt_at);
CREATE INDEX mail_outbox_user ON mail_outbox (user_id);
