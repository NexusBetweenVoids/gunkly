-- Email as a reminder channel (PRD "Reminders and notifications"): a per-user
-- opt-in (only honoured while the address is verified; cleared when the
-- address changes) and a flag on deliveries that go to the user's own address.
ALTER TABLE users ADD COLUMN email_reminders BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE deliveries ADD COLUMN via_email BOOLEAN NOT NULL DEFAULT FALSE;
