-- Email features (ADR 0008): email-OTP as a second factor, pending email
-- changes, the emailed-code hash and used sign-up proof-of-work challenges.
ALTER TABLE users ADD COLUMN email_otp_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- user_tokens: verify_email and reset_password rows key on the token hash;
-- email_otp rows key on the random challenge ID and carry the code's hash.
ALTER TABLE user_tokens ADD COLUMN code_hash TEXT;
ALTER TABLE user_tokens ADD COLUMN new_email TEXT;

-- Solved sign-up challenges, kept until they would have expired anyway so a
-- solution can be used once.
CREATE TABLE pow_used (
    challenge_hash TEXT PRIMARY KEY,
    expires_at     BIGINT NOT NULL
);
CREATE INDEX pow_used_expires ON pow_used (expires_at);
