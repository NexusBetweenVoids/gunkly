# Privacy policy (template)

> **Template for the maintainer to review. This is not legal advice.** Replace the `[placeholders]`, verify every statement against the running service and have it reviewed before publishing. It applies to the hosted service only; self-hosted instances are run by other people who are responsible for their own data.

Effective date: [Effective date]

## 1. Who is responsible

The hosted Gunkly service at [Service URL] is run by [Operator name], [Operator address] (the "operator"). The operator is the controller of the personal data described here. Contact for anything about your data: [Contact email].

Gunkly is a free, non-commercial service for tracking household chores. Its source code is public (AGPL-3.0); the hosted service runs the unmodified code.

## 2. What is collected, why and on what basis

| Data | Why | Legal basis (GDPR Art. 6) |
|-|-|-|
| **Account:** username, display name, email address (required on the hosted service), language and week-start preference, password (stored only as an Argon2id hash), second-factor secret (TOTP secret encrypted at rest; recovery codes and email codes stored as hashes) | To create your account, let you sign in securely and contact you about it | Contract (1)(b): providing the service you asked for |
| **Households and chores:** household names, time zones, areas, chores (names, notes, links, schedules), who a chore is assigned to, completion history (who completed what and when), your membership and role | To provide the service: the content you and the people you share a household with enter | Contract (1)(b) |
| **Notification settings:** the notification service addresses you add (stored encrypted), labels, Web Push subscriptions (endpoint and keys, keys encrypted), calendar feed tokens (stored as hashes), mute choices | To send reminders where you ask for them | Contract (1)(b); for third-party delivery, your instruction (see section 5) |
| **Delivery log:** which reminder was sent to which channel, whether it worked, error text | To retry failed reminders and let you see what was sent | Legitimate interest (1)(f): operating a reliable service |
| **Sessions:** device label (browser and operating system, derived from the user agent), when created and last used | To keep you signed in, show your devices, let you sign them out | Contract (1)(b); legitimate interest (1)(f) for security |
| **Audit log:** security-relevant actions on your account (sign-ins and failures, password and second-factor changes, membership changes), with the IP address | To protect your account and the service, and to let you see your own account history | Legitimate interest (1)(f): security |
| **IP addresses in rate limiting:** short-lived counters keyed by IP address (sign-in, sign-up and email limits) | To prevent abuse and guessing attacks | Legitimate interest (1)(f): security |
| **Last activity time:** when your account was last used | To remove accounts that stay inactive (section 6) and to keep sessions short | Legitimate interest (1)(f): data minimisation and security |
| **Emails sent to you:** verification, password reset, sign-in codes, security notices, inactivity warnings | To operate your account | Contract (1)(b); legitimate interest (1)(f) |
| **Operational logs of the web server and reverse proxy:** [Operator to complete: what is logged (for example IP address, time, request path, status), for how long] | Operating and securing the service | Legitimate interest (1)(f) |
| **Backups** of the database | Recovery after failure | Legitimate interest (1)(f); retention [backup retention, e.g. 30 days] |

Gunkly does not use analytics, advertising, tracking or profiling, does not sell data and has no telemetry. The only cookies are a session cookie and a CSRF cookie. Both are strictly necessary to sign you in, so no consent banner is needed.

You are not required to provide data beyond what is needed for the account. Without an email address the hosted service cannot be used, because verification, second factor and password reset depend on it.

## 3. Other people's data in your household

A household is shared. Other members of your household can see your display name, your role, which chores are assigned to you and the completions you record, as well as your username in exports. If you add other people's names to chores or notes you are responsible for having a good reason to do so. If you only use Gunkly for purely personal or household purposes, the GDPR household exemption may apply to you.

## 4. How long data is kept

| Data | Retention |
|-|-|
| Account and household data | Until you delete it or your account, or the account is removed for inactivity (section 6) |
| Audit log | 90 days |
| Delivery log | 30 days |
| Sessions | Signed out after 30 days without use, and after 90 days at most; expired entries are deleted daily |
| Email verification, reset and code tokens | Expire after 10 minutes to 24 hours; expired entries are deleted daily |
| Rate-limit counters | Minutes to hours |
| Completions of a chore | The most recent 1,000 per chore |
| Server logs | [Operator to complete] |
| Backups | [backup retention]; deleted data disappears from backups as they expire |

When you delete your account the data is removed from the live database in one step. The copies in backups are overwritten when the backups expire.

## 5. Who receives data

**Processors** act on the operator's instructions. [Operator to complete and keep in step with the processing register; delete rows that do not apply.]

| Processor | Purpose | Location | Agreement |
|-|-|-|-|
| [Hosting provider] | Servers and storage for the application and database | EU ([country]) | [Data processing agreement reference] |
| [Email provider (SMTP relay)] | Sending the emails in section 2 | [EU / location] | [Data processing agreement reference] |
| [Backup storage provider] | Encrypted backups | EU ([country]) | [Data processing agreement reference] |
| [Other, e.g. DNS or CDN provider] | [purpose] | [location] | [reference] |

The operator hosts the application and the database on infrastructure inside the EU.

**Notification services you choose (third countries).** Reminders are only sent where you tell Gunkly to send them. If you add a notification channel (for example Telegram, Discord, Slack, ntfy, Gotify, Pushover, a webhook or email) or enable browser notifications (Web Push), Gunkly sends the reminder text (chore name and due date, and a one-time "mark as done" link) to that service at your instruction. Those services, and the browser push services of Apple, Google, Mozilla and Microsoft, are independent controllers or processors with their own privacy policies and are often outside the EU, including in the United States. The operator has no control over what they do with the message. Do not put information into chore names that you do not want these services to see. You can remove a channel or subscription at any time; this stops the transfers.

The operator does not disclose personal data to anyone else unless the law requires it.

## 6. Inactive accounts

To avoid keeping data nobody uses, accounts that have not been used for **12 months** receive a warning by email and through the notification channels and browser notifications you set up. A **final warning** follows about 30 days before the deletion date. If you still do not sign in, the account and its personal data are deleted **after 18 months** of inactivity, using the same procedure as deleting your account (section 7). Signing in at any time cancels the warnings. Instance administrators are never deleted this way. Household content that belongs to a household with other members stays with them; ownership passes to the member who has been in the household longest (never a guest). A household with no other member who can use it is deleted.

## 7. Your rights and how to use them

You have the right to access, rectification, erasure, restriction, data portability and objection (GDPR Art. 15-21), and to withdraw consent where processing is based on consent. In the app:

- **Access and portability:** Account, "Export my data" downloads your personal data as a JSON file. A household export is available to household members from the household settings.
- **Rectification:** change your display name, email address, language and password under Account.
- **Erasure:** Account, "Delete account". The app first shows which households would be deleted and asks you to choose a new owner where you are the last owner. Deleting an account removes your profile, sign-ins, notification channels, Web Push subscriptions, calendar feeds and tokens. Chores that were only assigned to you become unassigned, you are removed from other chores, and your past completions stay in the household without your name ("Deleted user"). Your audit log entries are anonymised.
- **Leaving a household:** open the household and choose "Leave household".
- **Objection and restriction, or anything not covered in the app:** write to [Contact email]. The operator answers within one month.

You can complain to the supervisory authority. For the Netherlands this is the Autoriteit Persoonsgegevens (https://autoriteitpersoonsgegevens.nl). [Operator to adjust to the authority competent for the operator's establishment.]

## 8. Age

Gunkly is for people aged **16 or older**. Do not create an account if you are younger. If you believe a child has an account, write to [Contact email] and it will be deleted.

## 9. Security

Passwords are hashed with Argon2id; secrets such as notification addresses and second-factor secrets are encrypted at rest; connections use HTTPS; accounts on the hosted service require two-step verification; the operator cannot read household content through the admin tools. No system is perfectly secure: if a breach affects your data the operator will notify the authorities and affected users as the law requires (see the breach procedure).

## 10. Automated decisions, transfers outside the EU

There is no automated decision-making with legal effect. Transfers outside the EU happen only through the services you choose in section 5.

## 11. Changes

Changes to this policy are published at [Service URL] with a new effective date, and announced in the app for material changes.
