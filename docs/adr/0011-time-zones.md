# 0011. Time zones

Status: Accepted

## Context
Daylight saving changes must not shift reminders, and users in a household may travel.

## Decision
- Instants are stored as UTC Unix milliseconds (0004).
- Each household has an IANA zone, defaulting to the creator's browser zone. Due dates, reminder times of day, quiet hours and "today" are evaluated in that zone.
- DST: nonexistent local times shift to the next valid time; ambiguous times use the first occurrence (0005).
- A user may set an optional zone of their own (empty = follow the household). Only personal quiet hours are read in it; household quiet hours, due dates and "today" keep using the household's zone, and both quiet windows still stack (0.5.1).
- Display formats and first weekday follow the user's language, with personal override; the zone is the household's.
- Zone data comes from the Go `time` package (embedded tzdata so distroless works).

## Consequences
- Changing a household zone reschedules pending reminders.
- Members in other zones see household-zone times for chores.
