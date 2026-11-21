package store

import "time"

// WithClock returns a Store that reads the time from now. Tests use it to
// drive expiry; production uses time.Now.
func (s *Store) WithClock(now func() time.Time) *Store {
	c := *s
	c.now = now
	return &c
}
