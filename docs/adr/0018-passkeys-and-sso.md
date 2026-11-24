# 0018. Passkeys and single sign-on

Status: Accepted

## Context
Passwords plus TOTP or emailed codes (ADR 0008, 0013) leave two gaps for 0.5: people want to sign in without typing a password (passkeys), and self-hosters who already run an identity provider (Authelia, Authentik, Keycloak, Kanidm, Zitadel, Pocket ID, Google) want one sign-in for everything (SSO). Both are optional, both touch the most security-sensitive code in the app, and the hosted service must stay as strict as before.

Two dependencies were approved for this work: `github.com/go-webauthn/webauthn` (WebAuthn ceremonies, attestation and assertion verification, COSE keys) and `github.com/coreos/go-oidc/v3` (OpenID Connect discovery, JWKS caching and ID token verification). Both are small, widely used and the part of the problem where hand-written code is most likely to be wrong. `golang.org/x/oauth2` (already in the module graph through the MCP SDK) becomes a direct dependency for the authorization-code exchange with PKCE. The Go dependency budget (10) was not raised: the app has 9 direct requirements.

## Decision: passkeys

**What a passkey is here.** A WebAuthn credential of one account, stored as public material only: credential ID, COSE public key, signature counter, transports, AAGUID and the authenticator flags (migration 0019, `passkeys`). The private key never leaves the user's device. Attestation is `none`: Gunkly never asks which device made a key, so it needs no certificate store or metadata service. Registration requires a discoverable credential with user verification, which is what lets a passkey sign in without a username.

**Three uses.**
1. *Passwordless sign-in* ("Sign in with a passkey" on the sign-in page): a discoverable-credential assertion with user verification required (PIN, fingerprint or face on the device). Possession plus verification count as password plus second factor, so this satisfies the hosted mandatory-2FA rule.
2. *Second factor after a password*, alongside TOTP and emailed codes. The sign-in answer names the options (`method`, and `passkey: true` when the account has a passkey and passkeys are available). An account that has passkeys is asked for a second factor at password sign-in even if it has no TOTP or email codes: a passkey is a second factor, not just a login shortcut. User verification is `preferred` here, because possession is what this factor proves.
3. *Step-up.* A user-verified passkey alone re-verifies a session (`POST /session/step-up` with `passkey`), as password plus second factor would. Registering and removing passkeys need step-up.

**Relying party.** The RP ID is the host of `GUNKLY_BASE_URL` (no port) and the only allowed origin is its origin. Passkeys work only with an `https` base URL or `http://localhost`, and never with an IP address (not a valid RP ID); otherwise `GET /api/v1/instance` reports `passkeysAvailable: false`, the sign-in options endpoints answer 404 and registration answers `400 passkeys_require_https`, and the account screen explains why. Changing the base URL host later orphans existing passkeys (the browser will not offer them); recovery codes and the other factors still work.

**Ceremony state.** Each ceremony is two calls. "Options" stores the library's session data (challenge, allowed credentials, user-verification rule) in `passkey_challenges` for five minutes under a random ID (only its SHA-256 is stored), bound to a purpose (`register`, `login`, `second`, `stepup`) and to the account; "finish" takes and deletes the row in one transaction, so a challenge works once and a failed attempt spends it. State lives in the database, not in memory, so it works behind several instances. Expired rows are deleted when a challenge is saved and by the daily retention job. A ceremony started for one account or purpose is useless for another.

**Sign counter.** After a verified assertion the counter is stored with a conditional update (`sign_count < new`, or both zero for authenticators that never count, as synced passkeys do). A counter that did not increase means a cloned authenticator or a replay: the sign-in is refused (`401 invalid_passkey`), a warning is logged (user and passkey IDs, never the credential) and a `login_failure` audit event with `reason: passkey_sign_count` goes to the account's log.

**Limits and rate limits.** `passkeys_per_user` (default 20) in `internal/limits`. The ceremony endpoints that work without a session (`/session/passkey/*`) have their own per-IP bucket (`passkey`, 20 per minute); the second-factor options endpoint verifies the password first, with the usual per-login bucket and per-account backoff, so it reveals nothing about accounts it has not authenticated. Failed second-factor and step-up assertions count against the same per-account backoff as TOTP codes. Registration endpoints share the `passkey` bucket (a registration is several calls and the generic 2FA bucket is 5 per minute).

**Credential changes.** Adding a passkey signs out the account's other sessions, like enabling TOTP. Removing one signs out the other sessions and revokes API tokens and OAuth grants, like removing another second factor (audit `token_revoke` with `via: passkey_remove`). The first second factor of an account (no TOTP, no email codes, no passkey yet) also issues recovery codes, shown once, so losing the only passkey is recoverable; turning TOTP or email codes off keeps the recovery codes while a passkey remains, and removing the last second factor deletes them. In hosted mode the last second factor cannot be removed (`409 two_factor_mandatory`). `gunkly admin reset-2fa` removes passkeys too (an admin who is locked out of nothing else should not leave a passkey that still signs the user in). Account deletion deletes passkeys and challenges. The personal data export lists passkey names and dates, never key material.

**Front end.** `navigator.credentials` is used directly, with small base64url helpers (`web/src/lib/passkey.ts`), loaded with a dynamic import by the screens that need it, so the initial bundle did not grow. No npm dependency was added. All text is in the translation files.

## Decision: SSO

(See the section added with the SSO change.)

## Consequences
- Accounts with a passkey are asked for it at every password sign-in. That is intended (it is a second factor) and the recovery codes are the way back if the passkey is lost.
- Passkeys tie the instance to its base URL host. This is inherent to WebAuthn.
- The sign-count check rejects authenticators that go backwards; synced passkeys that always report 0 are accepted.
