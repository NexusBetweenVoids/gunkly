-- Deleting a push subscription nulls deliveries.push_subscription_id (ON DELETE
-- SET NULL); without an index each delete scans the delivery log.
CREATE INDEX deliveries_push_subscription ON deliveries (push_subscription_id) WHERE push_subscription_id IS NOT NULL;
