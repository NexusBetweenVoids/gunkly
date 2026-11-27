package api

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestEmailFailingAlert(t *testing.T) {
	e := newMailEnv(t)
	e.user("root", true)
	root := e.signedInStepUp("root")
	ctx := context.Background()
	u, err := e.st.UserByLogin(ctx, "root")
	if err != nil {
		t.Fatal(err)
	}
	u.Email = "root@example.test"
	queue := func(n int) {
		for i := 0; i < n; i++ {
			e.srv.dispatchMail(ctx, mailRequest{user: u, kind: "otp", params: map[string]string{"code": "123456"}})
		}
	}
	state := func(wantFailing bool) {
		t.Helper()
		st, v := root.do("GET", "/api/v1/admin/health", nil)
		got, _ := field(v, "emailFailing").(map[string]any)
		if st != 200 || (got != nil) != wantFailing {
			t.Fatalf("health: %d %v, want failing=%v", st, v, wantFailing)
		}
		if wantFailing && got["lastError"] != "other" {
			t.Fatalf("lastError %v", got["lastError"])
		}
		pst, pv := e.client().do("GET", "/healthz/email", nil)
		if wantFailing {
			expect(t, pst, pv, 503, "email_failing")
		} else if pst != 200 || field(pv, "status") != "ok" {
			t.Fatalf("probe: %d %v", pst, pv)
		}
		rst, _ := e.client().do("GET", "/readyz", nil)
		if rst != 200 {
			t.Fatalf("readyz %d", rst)
		}
	}
	events := func() int {
		evs, err := e.st.RecentSecurityEvents(ctx, e.clock.Now().Add(-24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, ev := range evs {
			if ev.Event == "email_failing" {
				n++
			}
		}
		return n
	}

	state(false)
	e.fm.err = errors.New("smtp password rejected")
	queue(2)
	e.ob.Process(ctx)
	state(false) // two failures are not yet an outage
	queue(1)
	e.ob.Process(ctx)
	state(true)
	if n := events(); n != 1 {
		t.Fatalf("events = %d", n)
	}
	// Further failures do not repeat the alert.
	e.clock.Add(2 * time.Minute)
	e.ob.Process(ctx)
	state(true)
	if n := events(); n != 1 {
		t.Fatalf("events after more failures = %d", n)
	}
	// A successful send clears it.
	e.fm.err = nil
	e.clock.Add(10 * time.Minute)
	e.ob.Process(ctx)
	state(false)
	// A new outage alerts again.
	e.fm.err = errors.New("down")
	queue(3)
	e.ob.Process(ctx)
	state(true)
	if n := events(); n != 2 {
		t.Fatalf("events after second outage = %d", n)
	}
}

func TestEmailProbeDisabled(t *testing.T) {
	e := newEnv(t)
	st, v := e.client().do("GET", "/healthz/email", nil)
	if st != 200 || field(v, "status") != "disabled" {
		t.Fatalf("%d %v", st, v)
	}
}
