package store_test

import (
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

func TestInstanceSettingsRetentionAndSessions(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	st := must(s.InstanceSettings(ctx))
	p := st.Policy()
	day := 24 * time.Hour
	if p.AuditLog != 90*day || p.Deliveries != 30*day || p.SessionIdle != 30*day || p.SessionMax != 90*day {
		t.Fatalf("defaults: %+v", p)
	}
	i := func(n int) *int { return &n }
	for _, bad := range []store.SettingsUpdate{
		{SessionIdleDays: i(0)}, {SessionMaxDays: i(731)}, {SessionIdleDays: i(91)}, {AuditRetentionDays: i(0)},
		{SessionIdleDays: i(10), SessionMaxDays: i(5)},
	} {
		if err := s.UpdateInstanceSettings(ctx, bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	noErr(t, s.UpdateInstanceSettings(ctx, store.SettingsUpdate{SessionIdleDays: i(5), SessionMaxDays: i(6), AuditRetentionDays: i(1), DeliveryRetentionDays: i(2)}))
	p = must(s.InstanceSettings(ctx)).Policy()
	if p.AuditLog != day || p.Deliveries != 2*day || p.SessionIdle != 5*day || p.SessionMax != 6*day {
		t.Fatalf("updated: %+v", p)
	}
}

func TestPurgeHonoursSessionPolicy(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()
	a := mkUser(t, s, "ann")
	far := time.Now().Add(1000 * 24 * time.Hour)
	for _, h := range []string{"h1", "h2"} {
		must(s.CreateSession(ctx, store.NewSession{UserID: a.UserID, TokenHash: h, Label: "x", ExpiresAt: far}))
	}
	day := 24 * time.Hour
	now := time.Now()
	n := must(s.PurgeExpired(ctx, store.RetentionPolicy{SessionIdle: 30 * day, SessionMax: 90 * day}, now.Add(10*day)))
	if n != 0 {
		t.Fatalf("purged %d fresh sessions", n)
	}
	n = must(s.PurgeExpired(ctx, store.RetentionPolicy{SessionIdle: 5 * day, SessionMax: 90 * day}, now.Add(10*day)))
	if n != 2 {
		t.Fatalf("idle purge: %d", n)
	}
}
