# 0005. Scheduling rules

Status: Accepted

## Context
Due-date math has many edge cases (overdue, early completion, month ends, DST). It must be pure, testable and fuzzable.

## Decision
`internal/schedule` is pure (no I/O). Rules:

- **Basis:** interval chores default to "from completion date"; calendar (RRULE subset) chores default to "from scheduled date". Changeable per chore.
- **Overdue, from scheduled:** completing jumps to the next upcoming occurrence; no backlog of past-due occurrences.
- **Early completion:** cancels pending reminders for the current cycle.
- **Month ends:** monthly on 29, 30 or 31 clamps to the month's last day.
- **DST:** a nonexistent local time shifts to the next valid time; an ambiguous time uses the first occurrence.
- **Time zone:** reminder times are evaluated in the household's IANA zone (0011).
- **Missed reminders:** one catch-up per chore after downtime.

**Supported RRULE subset** (anything else is rejected at validation):

| Part | Support |
|-|-|
| `FREQ` | `DAILY`, `WEEKLY`, `MONTHLY`, `YEARLY` |
| `INTERVAL` | positive integer |
| `BYDAY` | weekdays; with ordinal for `MONTHLY`/`YEARLY` (e.g. `1SA`, `-1FR`) |
| `BYMONTHDAY` | `1..31` and `-1..-31` (counted from month end); days missing from a month are skipped (RFC 5545). The editor uses `-1` for "last day". |
| `BYMONTH` | `1..12` |
| `COUNT`, `UNTIL`, `BYSETPOS`, `BYHOUR`, others | rejected |

## Consequences
- A strict grammar makes the parser fuzzable and iCal generation simple.
- Calendar feeds expand calendar-rule chores 12 months ahead; completion-based chores show only the next date.
- Property tests cover DST, leap years, month ends and overdue advancement.
