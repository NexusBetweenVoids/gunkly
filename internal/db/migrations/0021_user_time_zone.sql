-- Optional time zone of the user (IANA name). NULL = follow the household's
-- zone. Only personal quiet hours read it; due dates stay in the household zone.
ALTER TABLE users ADD COLUMN time_zone TEXT;
