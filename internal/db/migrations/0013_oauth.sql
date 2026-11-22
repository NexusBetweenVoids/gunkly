-- OAuth 2.1 sign-in for MCP clients (ADR 0016). Everything here is unused
-- unless the admin turns the oauth_enabled instance setting on.
--
-- A client is registered dynamically (RFC 7591) and is public (no secret). A
-- grant is one user's approval of one client for some households and scopes;
-- codes and tokens hang off it, so revoking the grant ends them all. Secrets
-- (codes, access and refresh tokens) are stored only as SHA-256 hashes.
CREATE TABLE oauth_clients (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    redirect_uris TEXT NOT NULL,              -- JSON array of exact redirect URIs
    created_at    BIGINT NOT NULL,
    last_used_at  BIGINT                      -- last authorization request; NULL = never used
);

CREATE TABLE oauth_grants (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client_id    TEXT NOT NULL REFERENCES oauth_clients (id) ON DELETE CASCADE,
    scopes       TEXT NOT NULL,               -- space-separated, as api_tokens
    created_at   BIGINT NOT NULL,
    expires_at   BIGINT NOT NULL,             -- absolute lifetime
    activated_at BIGINT,                      -- first token issued; NULL = the code was never redeemed
    last_used_at BIGINT,
    revoked_at   BIGINT
);
CREATE INDEX oauth_grants_user ON oauth_grants (user_id);
CREATE INDEX oauth_grants_client ON oauth_grants (client_id);

CREATE TABLE oauth_grant_households (
    grant_id     TEXT NOT NULL REFERENCES oauth_grants (id) ON DELETE CASCADE,
    household_id TEXT NOT NULL REFERENCES households (id) ON DELETE CASCADE,
    PRIMARY KEY (grant_id, household_id)
);
CREATE INDEX oauth_grant_households_household ON oauth_grant_households (household_id);

CREATE TABLE oauth_codes (
    code_hash    TEXT PRIMARY KEY,
    grant_id     TEXT NOT NULL REFERENCES oauth_grants (id) ON DELETE CASCADE,
    redirect_uri TEXT NOT NULL,
    challenge    TEXT NOT NULL,               -- PKCE S256 code challenge
    expires_at   BIGINT NOT NULL,
    used_at      BIGINT
);
CREATE INDEX oauth_codes_grant ON oauth_codes (grant_id);

CREATE TABLE oauth_tokens (
    token_hash TEXT PRIMARY KEY,
    kind       TEXT NOT NULL CHECK (kind IN ('access', 'refresh')),
    grant_id   TEXT NOT NULL REFERENCES oauth_grants (id) ON DELETE CASCADE,
    created_at BIGINT NOT NULL,
    expires_at BIGINT NOT NULL,
    used_at    BIGINT                         -- refresh tokens: rotated away (presenting one again revokes the grant)
);
CREATE INDEX oauth_tokens_grant ON oauth_tokens (grant_id);
CREATE INDEX oauth_tokens_expires ON oauth_tokens (expires_at);
