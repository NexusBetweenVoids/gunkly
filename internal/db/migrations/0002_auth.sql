-- TOTP replay protection: the last accepted time step per user. A code is
-- accepted only if its step is strictly greater (RFC 6238 section 5.2).
ALTER TABLE users ADD COLUMN totp_last_step BIGINT NOT NULL DEFAULT 0;
