-- Personal quiet hours: minutes after local midnight in the household's time
-- zone (users have no time zone of their own). Both NULL = none. They apply
-- to the user's personal reminder routes, never to household channels.
ALTER TABLE users ADD COLUMN quiet_start_min INTEGER CHECK (quiet_start_min BETWEEN 0 AND 1439);
ALTER TABLE users ADD COLUMN quiet_end_min INTEGER CHECK (quiet_end_min BETWEEN 0 AND 1439);
