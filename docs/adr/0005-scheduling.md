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
- **Rotating assignees ("Take turns"):** this is store logic, not scheduling math: the current-turn person (`chores.turn_user_id`, always one of the ordered `chore_assignees`) is part of the chore's state, changed in the same transaction as the due date. Completion moves the turn to the completer's successor (or, with `rotation_advance = 'assignee'`, only when the completer holds the turn); the turn held before is stored on the completion (`prev_turn_user_id`) so reverting the latest completion restores it together with the previous due date. Skip and snooze change the occurrence only and keep the turn. Reminders resolve their recipient from the turn when the reminder fires (`ReminderTargets`), so a turn change takes effect for pending reminders. Leaving, account deletion and demotion to guest remove the person from the rotation and move the turn on; fewer than two people left switches rotation off.

**Supported RRULE subset** (anything else is rejected at validation):

| Part | Support |
|-|-|
| `FREQ` | `DAILY`, `WEEKLY`, `MONTHLY`, `YEARLY` |
| `INTERVAL` | positive integer |
| `BYDAY` | weekdays; with ordinal for `MONTHLY`/`YEARLY` (e.g. `1SA`, `-1FR`) |
| `BYMONTHDAY` | `1..31` and `-1..-31` (counted from month end); days missing from a month are skipped (RFC 5545). The editor uses `-1` for "last day". |
| `BYMONTH` | `1..12` |
| `BYSETPOS` | `1..366` and `-1..-366`: picks the n-th (or n-th from last) of each `FREQ` period's dates (a month for `MONTHLY`, a year for `YEARLY`); needs `BYMONTH`, `BYMONTHDAY` or `BYDAY` (added in 0.9, for rules AI assistants write) |
| `COUNT`, `UNTIL`, `BYHOUR`, others | rejected |

Rules outside what the editor's rule builder can show (for example `BYSETPOS`, or several `BYMONTHDAY` values) are shown as "Custom rule" and kept exactly as stored when the chore is saved in the web app.

## Consequences
- A strict grammar makes the parser fuzzable and iCal generation simple.
- Calendar feeds expand calendar-rule chores 12 months ahead; completion-based chores show only the next date.
- Property tests cover DST, leap years, month ends and overdue advancement.
