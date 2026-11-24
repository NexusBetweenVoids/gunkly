-- Single sign-on identities (OpenID Connect, ADR 0018). The key is the
-- provider's issuer and the user's subject ("sub"), never an email address.
-- One identity per account and issuer; an identity belongs to one account.
CREATE TABLE sso_identities (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    issuer       TEXT NOT NULL,
    subject      TEXT NOT NULL,
    created_at   BIGINT NOT NULL,
    last_used_at BIGINT,
    UNIQUE (issuer, subject),
    UNIQUE (user_id, issuer)
);
