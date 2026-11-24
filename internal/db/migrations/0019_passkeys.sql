-- Passkeys (WebAuthn, ADR 0018). A passkey is a sign-in method and a second
-- factor of one account. Only public material is stored: the credential ID,
-- the COSE public key, the signature counter, transports and the AAGUID
-- (attestation is "none"). Binary values are base64url text so both databases
-- store them alike.
CREATE TABLE passkeys (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    credential_id TEXT NOT NULL UNIQUE,
    public_key    TEXT NOT NULL,
    sign_count    BIGINT NOT NULL DEFAULT 0,
    transports    TEXT NOT NULL DEFAULT '',  -- comma separated
    aaguid        TEXT NOT NULL DEFAULT '',  -- hex
    flags         INTEGER NOT NULL DEFAULT 0, -- authenticator flags byte (backup eligible/state, UV)
    created_at    BIGINT NOT NULL,
    last_used_at  BIGINT
);
CREATE INDEX passkeys_user ON passkeys (user_id);

-- Ceremony state between "options" and "finish": single use, short lived, so
-- it also works behind several app instances. The row holds the library's
-- session data (challenge, allowed credentials, user verification rule).
CREATE TABLE passkey_challenges (
    id         TEXT PRIMARY KEY,
    purpose    TEXT NOT NULL,   -- register | login | second | stepup
    user_id    TEXT,            -- the account the ceremony is bound to (NULL for a passwordless sign-in)
    data       TEXT NOT NULL,
    expires_at BIGINT NOT NULL
);
CREATE INDEX passkey_challenges_expires ON passkey_challenges (expires_at);
