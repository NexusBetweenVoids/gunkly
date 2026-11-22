# Personal data breach procedure (template)

> **Template for the maintainer to review. This is not legal advice.** Replace the `[placeholders]`, check the authority's current reporting process, and have the procedure reviewed. Hosted service only.

A **personal data breach** is a security incident that leads to accidental or unlawful destruction, loss, alteration, unauthorised disclosure of, or access to, personal data (GDPR Art. 4(12)). Examples: a database or backup leaked, an account takeover that exposed another user's data, a bug that showed one household's content to another, a lost unencrypted device holding data, a compromised server or provider.

The clock matters: the controller must notify the supervisory authority **without undue delay and, where feasible, within 72 hours** of becoming aware of a breach, unless it is unlikely to result in a risk to people's rights and freedoms (Art. 33). Becoming "aware" means having a reasonable degree of certainty that a breach has occurred.

Roles: the **incident lead** is [Operator name] (backup: [Name/role]). Contact for the authority and users: [Contact email].

## 1. Detection

Sources: monitoring and alerts ([Operator to list: Prometheus `/metrics` alerts, log review, provider alerts]); reports from users or researchers to [Contact email] or through `SECURITY.md`; findings of the security review, dependency or vulnerability scanners; notices from processors (Art. 33(2): processors must tell the controller without undue delay).

On any suspicion, **start the log (section 6) immediately** and note the time of awareness. Do not wait for certainty before starting.

## 2. Contain

1. Stop the harm first: revoke or rotate affected credentials and keys (`gunkly rotate-key`, sessions, tokens), block the access path, take the affected component offline if needed, fix or roll back the faulty release.
2. Preserve evidence: copy relevant logs and database state before changing them where safe.
3. Do not destroy data to hide the incident.

## 3. Assess

Establish and record: what happened and when; how it was found; which systems and which data (categories and approximate number of people and records); whether data was only exposed or also copied, altered or lost; whether the data was encrypted and keys safe (secrets such as notification addresses and TOTP secrets are encrypted at rest; passwords are Argon2id hashes); who could have accessed it; whether it is ongoing.

Rate the risk to people:

| Level | Meaning | Examples | Action |
|-|-|-|-|
| No risk | Unlikely to harm anyone | Encrypted data lost with keys safe; data exposed only to the operator | Record in the log; no notification |
| Risk | Could cause harm (unwanted disclosure of email addresses, names, household content, account takeover) | Leak of email addresses; one household seeing another's chores | **Notify the authority within 72 hours** |
| High risk | Likely serious harm (credentials or secrets usable by attackers, large leak, sensitive content) | Leaked password hashes plus encryption keys; leaked notification URLs/tokens | Notify the authority **and inform the affected users** (Art. 34) without undue delay |

When in doubt, treat it as a risk and notify. If all details are not yet known, notify within 72 hours with what is known and supplement later in phases (Art. 33(4)).

## 4. Notify the supervisory authority (Art. 33)

For the Netherlands: the Autoriteit Persoonsgegevens, through its online data-breach reporting form (https://autoriteitpersoonsgegevens.nl, "Datalek melden"). [Operator to adjust to the authority competent for the operator's establishment and to confirm the current process.]

The report contains: the nature of the breach, categories and approximate numbers of people and records; contact point; likely consequences; measures taken or proposed. Keep a copy and the confirmation.

If a processor caused the breach, the operator is still the one to report.

## 5. Inform users (Art. 34)

Required for high risk; recommended whenever users can protect themselves (for example by changing a password). Use clear, plain language by email (and in the app banner if useful) to affected users, without undue delay:

- what happened and when, in plain terms;
- what data of theirs was involved;
- the likely consequences;
- what the operator has done and what they should do (for example sign in again, change the password, remove and re-add notification channels because their addresses may have been exposed, revoke tokens);
- who to contact: [Contact email].

Never include secrets in the message. If direct notice is impossible, publish a notice on [Service URL] (see [incident-response.md](incident-response.md)).

## 6. Breach log (Art. 33(5))

Record **every** breach, including those not reported. Keep the log for [e.g. 5 years]. Use this template for each entry:

```
Breach ID:                      [YYYY-NNN]
Date and time it happened:      [ ]
Date and time of awareness:     [ ]   (starts the 72-hour clock)
Reported by / detected via:     [ ]
Description of what happened:   [ ]
Systems and data affected:      [ ]
Categories and approx. number of data subjects / records: [ ]
Encrypted? Keys safe?:          [ ]
Risk assessment (none / risk / high risk) and reasoning: [ ]
Containment and fix (with times): [ ]
Authority notified? When, reference: [ ]   (if not: why not)
Users informed? When, how, how many: [ ]
Processors / third parties informed: [ ]
Consequences and damage:        [ ]
Lessons learned and follow-up actions (owner, due date): [ ]
Closed on:                      [ ]
```

## 7. Afterwards

Hold a short review within [2 weeks]: root cause, what detection missed, what to change in code, configuration, processor agreements or this procedure. Update the threat model (`docs/THREAT_MODEL.md`) and the processing register if something changed. Publish a post-incident note where appropriate.

## 8. Rehearse

Walk through this procedure at least once a year and after each change of processors, so the first time it is used is not a real breach.
