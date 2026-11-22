-- Inactive-account lifecycle (PRD "Inactive accounts"): when the user was
-- warned that the account will be deleted for inactivity, and when the final
-- warning went out. Both are cleared when the user signs in again.
ALTER TABLE users ADD COLUMN inactive_warned_at BIGINT;
ALTER TABLE users ADD COLUMN inactive_final_warned_at BIGINT;
