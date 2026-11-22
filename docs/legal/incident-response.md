# Incident response and announcements (template)

> **Template for the maintainer to review. This is not legal advice.** Replace the `[placeholders]`. For incidents that involve personal data, also follow [breach-procedure.md](breach-procedure.md). Hosted service only.

This covers outages, degraded service, data loss risk and security incidents of the hosted service at [Service URL]. The service is free and run by one person without an on-call rota: the goals are honest communication and careful recovery, not a response-time promise.

## 1. Severity

| Level | Definition | Examples | Announce |
|-|-|-|-|
| SEV1 | Data loss or exposure, security compromise, or service completely down for more than [1 hour] | Leaked database, defaced service, lost volume | Yes, status page and email to affected users |
| SEV2 | Major feature broken or degraded for many users | Reminders not sending, sign-in failing, scheduler stopped | Yes, status page |
| SEV3 | Minor problem or a short, planned interruption | A notification service failing, slow responses, a short maintenance window | Optional, status page or in-app note |

Any incident that may involve personal data is at least SEV2 and starts the breach log (clock: 72 hours from awareness).

## 2. Channels

- **Status page / notice:** [Service URL/status or repository issue or other channel]. Keep it up even when the service is down (host it separately).
- **In-app notice:** an administrator announcement, if the app is reachable.
- **Email:** to affected users for SEV1 and for breaches, from [Contact email]. Never include secrets or links that ask for credentials; tell users to open the service by typing its address.
- **Reports from users:** [Contact email]; security reports through `SECURITY.md`.

## 3. Steps

1. **Detect and acknowledge.** Note the time. Check `/healthz`, `/readyz`, `/metrics` (scheduler last-tick age, delivery queue depth), logs and the provider's status. Acknowledge publicly within [a few hours] for SEV1 and SEV2 when you are able.
2. **Assess.** Severity, who is affected, whether personal data is involved (if yes: breach procedure).
3. **Contain and mitigate.** Stop the harm (disable the broken feature, roll back, block abusers, rotate keys). Prefer a safe degraded service over a risky fix.
4. **Communicate.** Post the first notice with what is known, then updates at least every [few hours] while the incident is open, even if there is nothing new.
5. **Recover.** Restore from the latest good backup if needed (`gunkly backup` snapshots; Postgres `pg_dump`; never copy the volume while running). Missed reminders are caught up once by the scheduler after downtime (one per chore), not replayed. Verify with a test account.
6. **Close.** Post the resolution and the time. For SEV1, publish a short post-incident note within [a week]: what happened, impact, cause, what changes.
7. **Learn.** Add tests or monitoring, update the runbook, the threat model and these documents.

## 4. Notice templates

**Investigating**

> [Time, timezone] We are investigating [problem, in plain words]. [Who is affected.] Next update by [time]. Your data [is / is not known to be affected].

**Update**

> [Time] Update: [what we learned, what we have done]. [What users can do, if anything.] Next update by [time].

**Resolved**

> [Time] Resolved: [what was wrong]. Service has been normal since [time]. Reminders due during the outage [were sent late / were skipped; one catch-up reminder per chore was sent]. [Cause and what we are changing.] We apologise for the trouble.

**Planned maintenance**

> On [date], from [time] to [time] ([timezone]) Gunkly will be unavailable for up to [duration] while we [reason]. Reminders due in that window will be sent once the service is back.

**Security incident affecting users (email)**

> Subject: Security notice about your Gunkly account
>
> Hello [name], on [date] we found that [what happened]. [Data of yours involved.] [What it could mean.] We have [measures taken]. We recommend that you [actions]. Questions: [Contact email]. We are sorry this happened.

## 5. Contacts and dependencies

| What | Contact / where | Notes |
|-|-|-|
| Hosting provider | [Hosting provider support] | Server, network, storage |
| Email relay | [Email provider support] | Verification and sign-in codes stop if it is down |
| DNS / domain | [Provider] | |
| Backups | [Location, restore steps] | Test a restore at least [every 6 months] |
| Supervisory authority | [Autoriteit Persoonsgegevens or competent authority] | For breaches |
