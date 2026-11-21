# 0008. Optional email

Status: Accepted

## Context
Many self-hosters have no SMTP server; the hosted service needs verified email.

## Decision
`EMAIL_ENABLED=false` is the self-hosted default: username sign-in, admin-panel or CLI password and 2FA resets, notifications still work. With email enabled, any SMTP server provides verification, password reset, email-OTP 2FA and inactivity warnings. Hosted requires email. SMTP traffic goes through the egress guard and is rate-limited (10/hour per account, 5/hour per IP unauthenticated, 500/hour global). Email OTP codes are single-use, hashed, expire in 10 minutes and have attempt limits.

## Consequences
- Two identity modes (email vs username) must both be tested.
- Email OTP shares a trust boundary with email password reset (see THREAT_MODEL).
