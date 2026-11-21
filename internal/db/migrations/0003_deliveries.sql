-- Reminder deliveries carry what the sender needs to render and re-check a
-- reminder at send time (the occurrence it was for and the kind of reminder),
-- so a delivery for an occurrence that has since been completed, snoozed or
-- skipped is dropped instead of sent.
ALTER TABLE deliveries ADD COLUMN kind INTEGER NOT NULL DEFAULT 0;      -- schedule.ReminderKind: 1 before, 2 on due, 3 overdue
ALTER TABLE deliveries ADD COLUMN due_date TEXT NOT NULL DEFAULT '';    -- local date of the occurrence the reminder is for
ALTER TABLE deliveries ADD COLUMN fire_at BIGINT;                       -- UTC ms the reminder fired (before quiet-hours deferral)

