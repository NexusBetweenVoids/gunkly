package api

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/mail"
)

// adminMails returns what was mailed to the admin; mails to other recipients (sign-up
// confirmations) are dropped.
func (e *mailEnv) adminMails() []mail.Message {
	var out []mail.Message
	for _, m := range e.mails() {
		if m.To == "root@example.com" {
			out = append(out, m)
		}
	}
	return out
}

// secEvents counts the listed security events of one kind.
func (e *mailEnv) secEvents(event string) int {
	e.t.Helper()
	_, v := e.signedIn("root").do("GET", "/api/v1/admin/health", nil)
	evs, _ := v.(map[string]any)["securityEvents"].([]any)
	n := 0
	for _, ev := range evs {
		if str(ev, "event") == event {
			n++
		}
	}
	return n
}

func (e *mailEnv) alertsOff() {
	e.t.Helper()
	st, v := e.signedInStepUp("root").do("PATCH", "/api/v1/admin/settings", map[string]any{"securityAlerts": false})
	expect(e.t, st, v, 200, "")
}

func (e *mailEnv) failLogins(c *client, n int, login func(i int) string) {
	e.t.Helper()
	for i := 0; i < n; i++ {
		st, _ := c.do("POST", "/api/v1/session", map[string]string{"login": login(i), "password": "wrong password " + strconv.Itoa(i)})
		if st != 401 {
			e.t.Fatalf("failed login %d: status %d", i, st)
		}
	}
}

func TestSigninAttackAccountAlert(t *testing.T) {
	e := newMailEnv(t)
	e.user("root", true)
	e.verified("root")
	e.user("ann", false)
	e.srv.acct.free = 100000 // no backoff: the test fails sign-ins back to back
	e.mails()
	c := e.client()
	same := func(int) string { return "ann" }

	e.failLogins(c, signinAccountFailures-1, same)
	if ms := e.adminMails(); len(ms) != 0 || e.secEvents("signin_attack_account") != 0 {
		t.Fatalf("below threshold: %+v", ms)
	}
	e.failLogins(c, 1, same)
	ms := e.adminMails()
	if len(ms) != 1 || !strings.Contains(ms[0].Body, "ann") || !strings.Contains(ms[0].Body, "20") {
		t.Fatalf("alert mail: %+v", ms)
	}
	if n := e.secEvents("signin_attack_account"); n != 1 {
		t.Fatalf("events: %d", n)
	}
	// More failures in the same burst: nothing new.
	e.failLogins(c, 5, same)
	if ms := e.adminMails(); len(ms) != 0 || e.secEvents("signin_attack_account") != 1 {
		t.Fatalf("same burst: %+v", ms)
	}
	// A new burst within the day: recorded, no second email.
	e.clock.Add(attackWindow + time.Minute)
	e.failLogins(c, signinAccountFailures, same)
	if ms := e.adminMails(); len(ms) != 0 {
		t.Fatalf("second alert mailed: %+v", ms)
	}
	if n := e.secEvents("signin_attack_account"); n != 2 {
		t.Fatalf("events: %d", n)
	}
	// Alerts off: recorded, no email.
	e.clock.Add(25 * time.Hour)
	e.alertsOff()
	e.failLogins(c, signinAccountFailures, same)
	if ms := e.adminMails(); len(ms) != 0 {
		t.Fatalf("mailed while off: %+v", ms)
	}
	if n := e.secEvents("signin_attack_account"); n != 3 {
		t.Fatalf("events: %d", n)
	}
}

func TestSigninAttackIPAlert(t *testing.T) {
	e := newMailEnv(t)
	e.user("root", true)
	e.verified("root")
	e.mails()
	c := e.client()
	burst := func(prefix string, n int) { // unknown logins: not audited per account
		e.failLogins(c, n, func(i int) string { return prefix + strconv.Itoa(i) })
	}

	burst("a", signinIPAccounts-1)
	if ms := e.adminMails(); len(ms) != 0 || e.secEvents("signin_attack_ip") != 0 {
		t.Fatalf("below threshold: %+v", ms)
	}
	// The same login again does not count as a new account.
	burst("a", 3)
	if ms := e.adminMails(); len(ms) != 0 {
		t.Fatalf("repeats counted: %+v", ms)
	}
	e.failLogins(c, 1, func(int) string { return "another" })
	ms := e.adminMails()
	if len(ms) != 1 || !strings.Contains(ms[0].Body, "127.0.0.1") || !strings.Contains(ms[0].Body, "10") {
		t.Fatalf("alert mail: %+v", ms)
	}
	if n := e.secEvents("signin_attack_ip"); n != 1 {
		t.Fatalf("events: %d", n)
	}
	burst("b", 15) // same window: nothing more
	if ms := e.adminMails(); len(ms) != 0 || e.secEvents("signin_attack_ip") != 1 {
		t.Fatalf("same burst: %+v", ms)
	}
	// A new window within the day: recorded, no second email.
	e.clock.Add(attackWindow + time.Minute)
	burst("c", signinIPAccounts)
	if ms := e.adminMails(); len(ms) != 0 {
		t.Fatalf("second alert mailed: %+v", ms)
	}
	if n := e.secEvents("signin_attack_ip"); n != 2 {
		t.Fatalf("events: %d", n)
	}
	// Alerts off: recorded, no email.
	e.clock.Add(25 * time.Hour)
	e.alertsOff()
	burst("d", signinIPAccounts)
	if ms := e.adminMails(); len(ms) != 0 {
		t.Fatalf("mailed while off: %+v", ms)
	}
	if n := e.secEvents("signin_attack_ip"); n != 3 {
		t.Fatalf("events: %d", n)
	}
}

func TestSigninSpreadBounded(t *testing.T) {
	var s signinSpread
	now := time.Unix(1000, 0)
	for i := 0; i < spreadMaxIPs+500; i++ {
		s.fail("ip"+strconv.Itoa(i), "x", now)
	}
	if len(s.m) > spreadMaxIPs {
		t.Fatalf("%d addresses tracked", len(s.m))
	}
	s.fail("fresh", "x", now.Add(2*attackWindow)) // expired entries make room
	if len(s.m) > spreadMaxIPs || s.m["fresh"] == nil {
		t.Fatalf("not pruned: %d", len(s.m))
	}
}

func TestSignupWaveAlert(t *testing.T) {
	e := newMailEnv(t)
	e.openSignup(false)
	e.verified("root")
	st, v := e.signedInStepUp("root").do("PATCH", "/api/v1/admin/settings", map[string]any{"limits": map[string]int{"signups_per_ip_hour": 1000}})
	expect(t, st, v, 200, "")
	e.mails()
	n := 0
	signups := func(count int) {
		for i := 0; i < count; i++ {
			n++
			st, v := e.client().do("POST", "/api/v1/signup", signupBody("user"+strconv.Itoa(n), nil))
			expect(t, st, v, 201, "")
		}
	}

	signups(signupWaveCount - 1)
	if ms := e.adminMails(); len(ms) != 0 || e.secEvents("signup_wave") != 0 {
		t.Fatalf("below threshold: %+v", ms)
	}
	signups(1)
	ms := e.adminMails()
	if len(ms) != 1 || !strings.Contains(ms[0].Body, "20") || !strings.Contains(ms[0].Subject, "new accounts") {
		t.Fatalf("alert mail: %+v", ms)
	}
	if c := e.secEvents("signup_wave"); c != 1 {
		t.Fatalf("events: %d", c)
	}
	// Another wave within the period: recorded, no second email.
	e.clock.Add(attackWindow + time.Minute)
	signups(signupWaveCount)
	if ms := e.adminMails(); len(ms) != 0 {
		t.Fatalf("second alert mailed: %+v", ms)
	}
	if c := e.secEvents("signup_wave"); c != 2 {
		t.Fatalf("events: %d", c)
	}
	// Alerts off: recorded, no email.
	e.clock.Add(7 * time.Hour)
	e.alertsOff()
	signups(signupWaveCount)
	if ms := e.adminMails(); len(ms) != 0 {
		t.Fatalf("mailed while off: %+v", ms)
	}
	if c := e.secEvents("signup_wave"); c != 3 {
		t.Fatalf("events: %d", c)
	}
}
