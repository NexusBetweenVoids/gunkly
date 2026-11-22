# Legal and compliance templates for the hosted service

> **Templates for the maintainer to review. This is not legal advice.**
> Nothing here has been reviewed by a lawyer or a data-protection professional. Replace every `[placeholder]`, check each statement against how the hosted service is actually run, and have the result reviewed before publishing it.

These documents apply to the **hosted Gunkly service run by the maintainer** only. People who **self-host** Gunkly are the controller of their own instance; they are not bound by these texts and should write their own (they may copy and adapt them, subject to the AGPL-3.0 licence of the repository).

| Document | Audience | Purpose |
|-|-|-|
| [privacy-policy.md](privacy-policy.md) | Users | What is collected, why, how long, who receives it, and the user's rights (GDPR Art. 12-14) |
| [terms.md](terms.md) | Users | Terms of use of the free, non-commercial service, including the inactive-account policy |
| [processing-register.md](processing-register.md) | Operator | Record of processing activities (GDPR Art. 30) |
| [breach-procedure.md](breach-procedure.md) | Operator | Detecting, assessing and reporting personal data breaches (GDPR Art. 33-34) |
| [incident-response.md](incident-response.md) | Operator | Handling and announcing outages and security incidents |

## Placeholders

| Placeholder | Meaning |
|-|-|
| `[Operator name]` | The natural person or organisation running the hosted service (the controller) |
| `[Operator address]` | Postal address for the controller (required in a privacy policy for a data controller) |
| `[Contact email]` | Address for privacy and abuse requests |
| `[Service URL]` | The public address of the hosted service |
| `[Hosting provider]`, `[Email provider]`, ... | Processors; see the table in the privacy policy |
| `[Effective date]` | Date a version takes effect |

## Keeping the texts true

The retention periods and behaviour described in these documents match the defaults in the code at the time of writing (see `docs/PRD.md`: Data retention, Accounts, Inactive accounts). If an instance setting is changed (audit retention, delivery retention, session lifetimes, inactive-account periods), update the texts in the same change. Retention defaults are also listed in the admin settings screen.
