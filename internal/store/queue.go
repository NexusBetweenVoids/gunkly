package store

import "context"

// PendingDeliveries counts deliveries waiting to be sent (including those
// backing off for a retry): the reminder queue depth. System use: metrics.
func (s *Store) PendingDeliveries(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM deliveries WHERE status = 'pending'").Scan(&n)
	return n, err
}
