-- OAuth completions get their own source (the token_id is the grant id), so
-- they are told apart from API token completions. The registering client key
-- (an IP, IPv6 collapsed to its /64) bounds how many never-used clients one
-- address can keep; it is never logged.
ALTER TABLE completions DROP CONSTRAINT completions_source_check;
ALTER TABLE completions ADD CONSTRAINT completions_source_check
    CHECK (source IN ('app', 'mark_done_link', 'api_token', 'oauth', 'import'));
ALTER TABLE oauth_clients ADD COLUMN reg_key TEXT NOT NULL DEFAULT '';
CREATE INDEX oauth_clients_reg ON oauth_clients (reg_key);
