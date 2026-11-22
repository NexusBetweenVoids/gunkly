# 0008. Optional email

Status: Accepted

## Context
Many self-hosters have no SMTP server; the hosted service needs verified email.

## Decision
`EMAIL_ENABLED=false` is the self-hosted default: username sign-in, admin-panel or CLI password and 2FA resets, notifications still work. With email enabled (which needs `BASE_URL` for the links), any SMTP server provides verification, password reset, email-OTP 2FA and inactivity warnings. Hosted requires email.

**Transport.** `internal/mail` sends plain-text mail (quoted-printable UTF-8, encoded subject, `Message-ID`, `Date`) with `net/smtp`; `SMTP_TLS` is `starttls` (required, never downgraded), `tls` (implicit) or `none` (refused in hosted mode). Every header value is rejected if it contains a line break. Connections dial through the egress guard whenever it is on, which is always in hosted mode; so a hosted SMTP relay must be a public address, while a self-hoster with a LAN relay turns the guard off in the admin settings. Text comes from `internal/i18n` in the recipient's locale. Sends are queued in the background so a response never waits on, or reveals anything about, the SMTP server.

**Caps.** 10 emails/hour per account, 5/hour per IP for requests without a session, 500/hour global (`internal/limits`, admin-overridable). They are in-memory sliding windows per instance (ADR 0013); a refused password-reset request is still answered 202. Requests without a session (reset, sign-up, verification) count only against the per-IP and global caps, never the target account's bucket, because anyone can send them for any victim. Sign-in and step-up codes have their own per-account bucket (`email_codes_account_hour`, 20/hour), so mail spam cannot lock the account owner out of 2FA. Reaching the global cap from many addresses can still delay everyone's mail; that is the accepted cost of a global cap.

**Verification.** A single-use token (24 h, stored hashed) is emailed on registration (sign-up, invite, admin-created account, first admin) and for an email change; the new address only takes effect when its link is opened and the old address is notified. Unverified users can sign in and see a banner. In hosted mode (and, when email is on, on instances with open sign-up for accounts that have an address) an unverified user cannot create or join households through the API; invite registration is exempt at sign-up because the owner vouched for the invitee, but in hosted mode the same gate (verified email, second factor) sits in the router in front of every household-scoped route, so an invited account sees no household data until it passes it. Import goes through the gate too. Hosted also requires a second factor for those actions.

**Sign-up.** With email on, `POST /signup` answers 202 and signs nobody in, whether the account was created or the address already had one; in the second case the address's owner is mailed "you already have an account" (sign in or reset). A taken username still answers `registration_failed` (usernames are visible inside households). Accounts that never verified their address and belong to no household are deleted after 7 days by the retention job, which stops addresses being squatted.

**Password reset.** `POST /password-reset/request` always answers 202 and does the lookup, token and mail in the background for known and unknown logins alike, so the response time tells nothing. Up to 3 unexpired tokens per account may be outstanding, so a stranger's request cannot invalidate the link the owner already has; beyond 3 nothing new is issued until one expires. Each token (1 h, hashed, single use) is consumed together with the password change in one transaction that also revokes every session and mark-done link. A second factor, if any, still applies at the next sign-in.

**Email OTP.** Users with a verified address may use emailed codes instead of TOTP (exclusive; switching replaces the other factor and issues new recovery codes). Codes are 6 digits, stored hashed bound to a challenge ID, single use, expire in 10 minutes, and each challenge allows 5 verification attempts before it is invalidated; a newer code replaces the older one; resends count against the email caps. Recovery codes still work. In hosted mode 2FA is mandatory: verifying the address enrols email codes automatically (the recovery codes are shown once), and 2FA cannot be turned off, only switched. `gunkly admin reset-2fa` clears email codes too.

**Open sign-up.** `open_signup` and `require_pow` are instance settings that default to on in hosted mode and off otherwise. The proof of work is ALTCHA-compatible (SHA-256, HMAC-signed challenge, expiry in the salt, difficulty `SIGNUP_POW_MAX`), implemented with the standard library; solved challenges are stored until they expire so each works once. A sign-up first takes the per-IP hourly cap, then marks the solution used, before anything is hashed. The default `SIGNUP_POW_MAX` is 250000. The proof of work is a nuisance filter against naive bots, not a barrier: a determined attacker can solve it. The real controls are the per-IP sign-up cap and the mail caps.

## Consequences
- Two identity modes (email vs username) must both be tested.
- Email OTP shares a trust boundary with email password reset (see THREAT_MODEL).
- Mail caps are per instance until the shared limiter ships (see ADR 0013).
- Mail send failures are logged, not retried.
