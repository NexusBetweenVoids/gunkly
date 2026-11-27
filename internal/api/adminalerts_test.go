package api

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// adminAlertEvents lists the alert events the admin health check reports, newest first, as event/username.
func adminAlertEvents(t *testing.T, c *client) []string {
	t.Helper()
	_, v := c.do("GET", "/api/v1/admin/health", nil)
	evs, _ := v.(map[string]any)["securityEvents"].([]any)
	var out []string
	for _, e := range evs {
		out = append(out, str(e, "event")+"/"+str(e, "username"))
	}
	return out
}

func TestAdminPowerChangesAlertAdmins(t *testing.T) {
	e := newMailEnv(t)
	e.user("root", true)
	e.user("boss", true)
	e.verified("root")
	e.verified("boss")
	root := e.signedInStepUp("root")
	patch := func(body map[string]any) {
		t.Helper()
		if st, v := root.do("PATCH", "/api/v1/admin/settings", body); st != 200 {
			t.Fatalf("patch %v: %d %v", body, st, v)
		}
	}
	// both admins get the mail, naming who did it
	both := func(subject string) {
		t.Helper()
		ms := e.mails()
		if len(ms) != 2 {
			t.Fatalf("want 2 mails about %q, got %+v", subject, ms)
		}
		for _, m := range ms {
			if !strings.Contains(m.Subject, subject) || !strings.Contains(m.Body, "root") || !strings.Contains(m.Body, "compromised") {
				t.Fatalf("mail about %q: %+v", subject, m)
			}
		}
	}
	none := func() {
		t.Helper()
		if ms := e.mails(); len(ms) != 0 {
			t.Fatalf("unexpected mail: %+v", ms)
		}
	}
	e.mails()

	// a new admin account
	st, v := root.do("POST", "/api/v1/admin/users", map[string]any{"username": "newadmin", "password": "password-newadmin", "displayName": "N", "isAdmin": true})
	if st != 201 {
		t.Fatalf("create admin: %d %v", st, v)
	}
	both("admin account")
	// a plain user is no alert
	if st, v := root.do("POST", "/api/v1/admin/users", map[string]any{"username": "plain", "password": "password-plain", "displayName": "P"}); st != 201 {
		t.Fatalf("create user: %d %v", st, v)
	}
	none()

	// open sign-up on, same value again, off
	patch(map[string]any{"openSignup": true})
	both("open sign-up was switched on")
	patch(map[string]any{"openSignup": true})
	none()
	patch(map[string]any{"openSignup": false})
	both("open sign-up was switched off")

	// proof of work: on is no alert, same value none, off alerts
	patch(map[string]any{"requirePow": true})
	none()
	patch(map[string]any{"requirePow": true})
	none()
	patch(map[string]any{"requirePow": false})
	both("proof of work")
	patch(map[string]any{"requirePow": false})
	none()

	// egress guard off
	patch(map[string]any{"egressGuard": false})
	both("outbound-connection guard")
	patch(map[string]any{"egressGuard": false})
	none()

	evs := adminAlertEvents(t, root)
	want := []string{"egress_guard_off/root", "pow_off/root", "signup_changed/root", "signup_changed/root", "admin_granted/root"}
	if !slices.Equal(evs, want) {
		t.Fatalf("events %v, want %v", evs, want)
	}

	// security alerts off: this one is still mailed, then nothing is
	patch(map[string]any{"securityAlerts": true})
	none()
	patch(map[string]any{"securityAlerts": false})
	both("alert emails were turned off")
	patch(map[string]any{"securityAlerts": false})
	none()
	e.clock.Add(2 * time.Minute) // past the one-minute dedup
	patch(map[string]any{"egressGuard": true})
	patch(map[string]any{"egressGuard": false})
	none() // recorded, not mailed
	evs = adminAlertEvents(t, root)
	if len(evs) != 7 || evs[0] != "egress_guard_off/root" || evs[1] != "security_alerts_off/root" {
		t.Fatalf("events after alerts off: %v", evs)
	}
}

// The admin health check marks the events the asking admin caused as byYou.
func TestSecurityEventsByYou(t *testing.T) {
	e := newMailEnv(t)
	e.user("root", true)
	e.user("boss", true)
	root := e.signedInStepUp("root")
	boss := e.signedIn("boss")
	if st, v := root.do("POST", "/api/v1/admin/users", map[string]any{"username": "newadmin", "password": "password-newadmin", "displayName": "N", "isAdmin": true}); st != 201 {
		t.Fatalf("create admin: %d %v", st, v)
	}
	byYou := func(c *client) []bool {
		t.Helper()
		_, v := c.do("GET", "/api/v1/admin/health", nil)
		evs, _ := v.(map[string]any)["securityEvents"].([]any)
		var out []bool
		for _, ev := range evs {
			if str(ev, "event") == "admin_granted" {
				b, ok := ev.(map[string]any)["byYou"].(bool)
				if !ok {
					t.Fatalf("byYou missing: %v", ev)
				}
				out = append(out, b)
			}
		}
		return out
	}
	if got := byYou(root); !slices.Equal(got, []bool{true}) {
		t.Fatalf("root sees %v, want [true]", got)
	}
	if got := byYou(boss); !slices.Equal(got, []bool{false}) {
		t.Fatalf("boss sees %v, want [false]", got)
	}
}
