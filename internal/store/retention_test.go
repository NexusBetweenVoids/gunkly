package store_test

import (
	"context"
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

// Accounts that never verified their email and belong to no household are
// removed after the retention period, freeing squatted addresses.
func TestPurgeUnverifiedAccounts(t *testing.T) {
	ctx := context.Background()
	s, clk := emailStore(t)
	day := 24 * time.Hour
	ann := mkUser(t, s, "ann") // unverified, no household: goes
	bob := mkUser(t, s, "bob") // unverified but in a household: stays
	mkHousehold(t, s, bob, "H")
	cat := mkUser(t, s, "cat") // verified: stays
	noErr(t, s.IssueVerifyToken(ctx, cat.UserID, "vt", "", clk.t.Add(time.Hour)))
	if _, _, err := s.VerifyEmail(ctx, "vt", false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser(ctx, store.NewUser{Username: "noemail", DisplayName: "n", PasswordHash: "x"}); err != nil { // no address: stays
		t.Fatal(err)
	}
	clk.t = clk.t.Add(6 * day)
	dan := mkUser(t, s, "dan") // too young: stays
	p := store.RetentionPolicy{UnverifiedAccounts: store.UnverifiedAccountMaxAge}
	must(s.PurgeExpired(ctx, p, clk.t.Add(2*day)))
	for name, want := range map[string]bool{"ann": false, "bob": true, "cat": true, "noemail": true, "dan": true} {
		_, err := s.UserByLogin(ctx, name)
		if (err == nil) != want {
			t.Errorf("%s present=%v, want %v", name, err == nil, want)
		}
	}
	_, _ = ann, dan
	// Without the policy nothing is touched.
	must(s.PurgeExpired(ctx, store.RetentionPolicy{}, clk.t.Add(30*day)))
	if _, err := s.UserByLogin(ctx, "dan"); err != nil {
		t.Errorf("dan purged without a policy: %v", err)
	}
}
