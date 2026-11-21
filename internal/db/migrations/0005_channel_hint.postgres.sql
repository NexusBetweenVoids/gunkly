-- Channel URLs are never shown after saving; url_hint is the masked form the
-- UI may show ("...abcd"), computed when the URL is saved.
ALTER TABLE notification_channels ADD COLUMN url_hint TEXT NOT NULL DEFAULT '';

-- Test messages to a personal channel belong to no household, so a delivery's
-- household is optional.
ALTER TABLE deliveries ALTER COLUMN household_id DROP NOT NULL;
