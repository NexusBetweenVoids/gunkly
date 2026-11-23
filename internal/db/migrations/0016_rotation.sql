-- Rotating assignees ("Take turns", PRD "Later: rotating assignees"): the
-- assignee order is the rotation order, a chore with rotation on has a
-- current-turn person, and each completion remembers whose turn it was so a
-- revert can give the turn back.
ALTER TABLE chore_assignees ADD COLUMN position INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chores ADD COLUMN rotation BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE chores ADD COLUMN rotation_advance TEXT NOT NULL DEFAULT 'any'; -- any | assignee
ALTER TABLE chores ADD COLUMN turn_user_id TEXT REFERENCES users (id) ON DELETE SET NULL;
ALTER TABLE completions ADD COLUMN prev_turn_user_id TEXT;
