-- Invites are limited-use: max_uses people may join with one link (default 1).
ALTER TABLE invites ADD COLUMN max_uses INTEGER NOT NULL DEFAULT 1;
ALTER TABLE invites ADD COLUMN uses INTEGER NOT NULL DEFAULT 0;
