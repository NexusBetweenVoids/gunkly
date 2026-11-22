package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

// solvePoW is the client's job: find n with SHA-256(salt + n) = challenge.
func solvePoW(t *testing.T, ch any) map[string]any {
	t.Helper()
	salt, _ := field(ch, "salt").(string)
	want, _ := field(ch, "challenge").(string)
	maxn, _ := field(ch, "maxnumber").(float64)
	for n := 0; n <= int(maxn); n++ {
		h := sha256.Sum256([]byte(salt + strconv.Itoa(n)))
		if hex.EncodeToString(h[:]) == want {
			return map[string]any{"algorithm": field(ch, "algorithm"), "challenge": want, "salt": salt,
				"signature": field(ch, "signature"), "number": n}
		}
	}
	t.Fatal("unsolvable challenge")
	return nil
}

func (e *env) openSignup(pow bool) {
	e.t.Helper()
	e.user("root", true)
	root := e.signedInStepUp("root")
	st, v := root.do("PATCH", "/api/v1/admin/settings", map[string]any{"openSignup": true, "requirePow": pow})
	expect(e.t, st, v, 200, "")
}

func signupBody(name string, extra map[string]any) map[string]any {
	b := map[string]any{"username": name, "email": name + "@example.org", "displayName": name, "password": "a much longer password"}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

func TestInstanceMCPAndBaseURL(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.BaseURL = "https://gunkly.example" })
	st, v := e.client().do("GET", "/api/v1/instance", nil)
	if st != 200 || field(v, "baseUrl") != "https://gunkly.example" || field(v, "mcpEnabled") != true {
		t.Fatalf("instance: %d %v", st, v)
	}
}

func TestSignupClosedByDefault(t *testing.T) {
	e := newMailEnv(t)
	c := e.client()
	st, v := c.do("POST", "/api/v1/signup", signupBody("ann", nil))
	expect(t, st, v, 403, "registration_disabled")
	st, v = c.do("GET", "/api/v1/signup/challenge", nil)
	expect(t, st, v, 404, "not_found")
	if st, v = c.do("GET", "/api/v1/instance", nil); field(v, "openSignup") != false || field(v, "requirePow") != false || field(v, "emailEnabled") != true {
		t.Fatalf("instance: %d %v", st, v)
	}
}

func TestSignupWithProofOfWork(t *testing.T) {
	e := newMailEnv(t, func(o *Options) { o.HostedMode = true })
	e.openSignup(true)
	root := e.signedInStepUp("root")
	st, v := root.do("PATCH", "/api/v1/admin/settings", map[string]any{"limits": map[string]int{"signups_per_ip_hour": 50}})
	expect(t, st, v, 200, "")
	c := e.client()
	if st, v := c.do("GET", "/api/v1/instance", nil); field(v, "openSignup") != true || field(v, "requirePow") != true {
		t.Fatalf("instance: %d %v", st, v)
	}

	// No solution, a wrong one and a forged signature are refused.
	st, v = c.do("POST", "/api/v1/signup", signupBody("ann", nil))
	expect(t, st, v, 400, "pow_invalid")
	st, ch := c.do("GET", "/api/v1/signup/challenge", nil)
	expect(t, st, ch, 200, "")
	if field(ch, "algorithm") != "SHA-256" || field(ch, "maxnumber") != 1000.0 {
		t.Fatalf("challenge: %v", ch)
	}
	sol := solvePoW(t, ch)
	bad := map[string]any{}
	for k, x := range sol {
		bad[k] = x
	}
	bad["number"] = (sol["number"].(int) + 1) % 1001
	st, v = c.do("POST", "/api/v1/signup", signupBody("ann", map[string]any{"pow": bad}))
	expect(t, st, v, 400, "pow_invalid")
	bad["number"], bad["signature"] = sol["number"], "00"+sol["signature"].(string)[2:]
	st, v = c.do("POST", "/api/v1/signup", signupBody("ann", map[string]any{"pow": bad}))
	expect(t, st, v, 400, "pow_invalid")

	// The solution is spent before the form is validated (and the password
	// hashed), so a mistake in the form burns it and a replay is refused.
	st, v = c.do("POST", "/api/v1/signup", signupBody("a", map[string]any{"pow": sol}))
	expect(t, st, v, 400, "invalid")
	st, v = c.do("POST", "/api/v1/signup", signupBody("ann", map[string]any{"pow": sol}))
	expect(t, st, v, 400, "pow_invalid")
	_, ch = c.do("GET", "/api/v1/signup/challenge", nil)
	sol = solvePoW(t, ch)
	st, v = c.do("POST", "/api/v1/signup", signupBody("ann", map[string]any{"pow": sol}))
	expect(t, st, v, 202, "")
	if field(v, "authenticated") == true || field(v, "accepted") != true {
		t.Fatalf("signup: %v", v)
	}
	if st, _ = c.do("GET", "/api/v1/me", nil); st != 401 {
		t.Fatalf("signed in before confirming the address: %d", st)
	}
	msg := e.oneMail("ann@example.org")
	if tokenIn(t, msg) == "" {
		t.Fatal("no verification link")
	}

	// Single use.
	st, v = e.client().do("POST", "/api/v1/signup", signupBody("bob", map[string]any{"pow": sol}))
	expect(t, st, v, 400, "pow_invalid")

	// A taken email answers exactly like success (no oracle) and mails its
	// owner instead; no account is created.
	_, ch = c.do("GET", "/api/v1/signup/challenge", nil)
	st, dup := e.client().do("POST", "/api/v1/signup", signupBody("other", map[string]any{"pow": solvePoW(t, ch), "email": "ann@example.org"}))
	expect(t, st, dup, 202, "")
	if !reflect.DeepEqual(dup, any(map[string]any{"accepted": true})) {
		t.Fatalf("differs from success: %v", dup)
	}
	if m := e.oneMail("ann@example.org"); !strings.Contains(m.Subject, "already have a Gunkly account") || strings.Contains(m.Body, "token=") {
		t.Fatalf("notice: %+v", m)
	}
	if _, err := e.st.UserByLogin(context.Background(), "other"); err == nil {
		t.Fatal("account created for a taken email")
	}
	// A taken username is still reported.
	_, ch = c.do("GET", "/api/v1/signup/challenge", nil)
	st, v = e.client().do("POST", "/api/v1/signup", signupBody("ann", map[string]any{"pow": solvePoW(t, ch), "email": "fresh@example.org"}))
	expect(t, st, v, 400, "registration_failed")
}

func TestSignupChallengeExpires(t *testing.T) {
	e := newMailEnv(t)
	e.openSignup(true)
	c := e.client()
	_, ch := c.do("GET", "/api/v1/signup/challenge", nil)
	sol := solvePoW(t, ch)
	e.clock.Add(10*time.Minute + time.Second)
	st, v := c.do("POST", "/api/v1/signup", signupBody("ann", map[string]any{"pow": sol}))
	expect(t, st, v, 400, "pow_invalid")
}

func TestSignupWithoutProofOfWorkWhenAdminTurnsItOff(t *testing.T) {
	e := newMailEnv(t, func(o *Options) { o.HostedMode = true })
	e.openSignup(false)
	c := e.client()
	st, v := c.do("GET", "/api/v1/signup/challenge", nil)
	expect(t, st, v, 404, "not_found")
	st, v = c.do("POST", "/api/v1/signup", signupBody("ann", nil))
	expect(t, st, v, 202, "")
	// Hosted mode requires an email.
	st, v = e.client().do("POST", "/api/v1/signup", signupBody("bob", map[string]any{"email": ""}))
	expect(t, st, v, 400, "invalid")
}

// Self-hosted never requires an email, even with SMTP configured: sign-up
// signs the user in at once and household access needs no verification.
func TestSelfHostedSignupWithSMTPEmailOptional(t *testing.T) {
	e := newMailEnv(t)
	e.openSignup(false)

	// No email: 201 + session, can create a household, no mail.
	c := e.client()
	st, v := c.do("POST", "/api/v1/signup", signupBody("ann", map[string]any{"email": ""}))
	expect(t, st, v, 201, "")
	if field(v, "authenticated") != true {
		t.Fatalf("signup: %v", v)
	}
	if st, v = c.do("POST", "/api/v1/households", map[string]string{"name": "A", "timezone": "Europe/Amsterdam"}); st != 201 {
		t.Fatalf("household without email: %d %v", st, v)
	}
	if ms := e.mails(); len(ms) != 0 {
		t.Fatalf("mail without an address: %+v", ms)
	}

	// With an email: 201, a verification mail goes out, and the account has
	// household access without verifying.
	c = e.client()
	st, v = c.do("POST", "/api/v1/signup", signupBody("bob", nil))
	expect(t, st, v, 201, "")
	if tokenIn(t, e.oneMail("bob@example.org")) == "" {
		t.Fatal("no verification link")
	}
	if st, v = c.do("POST", "/api/v1/households", map[string]string{"name": "B", "timezone": "Europe/Amsterdam"}); st != 201 {
		t.Fatalf("household unverified: %d %v", st, v)
	}

	// A taken email or username is the generic failure; no notice is mailed.
	st, v = e.client().do("POST", "/api/v1/signup", signupBody("cat", map[string]any{"email": "bob@example.org"}))
	expect(t, st, v, 400, "registration_failed")
	st, v = e.client().do("POST", "/api/v1/signup", signupBody("bob", map[string]any{"email": "fresh@example.org"}))
	expect(t, st, v, 400, "registration_failed")
	if ms := e.mails(); len(ms) != 0 {
		t.Fatalf("mail for a duplicate: %+v", ms)
	}
	if _, err := e.st.UserByLogin(context.Background(), "cat"); err == nil {
		t.Fatal("account created for a taken email")
	}
}

func TestSignupWithoutEmailInstance(t *testing.T) {
	e := newEnv(t)
	e.openSignup(false)
	st, v := e.client().do("POST", "/api/v1/signup", signupBody("ann", map[string]any{"email": ""}))
	expect(t, st, v, 201, "")
}

func TestSignupPerIPLimit(t *testing.T) {
	e := newMailEnv(t, func(o *Options) { o.HostedMode = true })
	e.openSignup(false)
	root := e.signedInStepUp("root")
	st, v := root.do("PATCH", "/api/v1/admin/settings", map[string]any{"limits": map[string]int{"signups_per_ip_hour": 2}})
	expect(t, st, v, 200, "")
	for _, n := range []string{"a1", "a2"} {
		st, v = e.client().do("POST", "/api/v1/signup", signupBody(n, nil))
		expect(t, st, v, 202, "")
	}
	st, v = e.client().do("POST", "/api/v1/signup", signupBody("a3", nil))
	expect(t, st, v, 429, "rate_limited")
	e.clock.Add(time.Hour + time.Second)
	st, v = e.client().do("POST", "/api/v1/signup", signupBody("a3", nil))
	expect(t, st, v, 202, "")
}

func TestHostedModeDefaultsToOpenSignupWithProofOfWork(t *testing.T) {
	e := newMailEnv(t, func(o *Options) { o.HostedMode = true })
	c := e.client()
	if st, v := c.do("GET", "/api/v1/instance", nil); field(v, "openSignup") != true || field(v, "requirePow") != true || field(v, "hostedMode") != true {
		t.Fatalf("instance: %d %v", st, v)
	}
	st, ch := c.do("GET", "/api/v1/signup/challenge", nil)
	expect(t, st, ch, 200, "")
	st, v := c.do("POST", "/api/v1/signup", signupBody("ann", map[string]any{"pow": solvePoW(t, ch), "locale": "en"}))
	expect(t, st, v, 202, "")

	// An admin can still close it.
	e.user("root", true)
	root := e.signedInStepUp("root")
	st, v = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"openSignup": false})
	expect(t, st, v, 200, "")
	if st, v = c.do("GET", "/api/v1/instance", nil); field(v, "openSignup") != false {
		t.Fatalf("instance: %d %v", st, v)
	}
}

// A replayed or forged proof of work must cost one cheap check, never an
// Argon2id hash, and the per-IP sign-up cap is checked before the proof.
func TestSignupProofSpentAndCapCheckedBeforeHashing(t *testing.T) {
	e := newMailEnv(t, func(o *Options) { o.HostedMode = true })
	e.openSignup(true)
	root := e.signedInStepUp("root")
	st, v := root.do("PATCH", "/api/v1/admin/settings", map[string]any{"limits": map[string]int{"signups_per_ip_hour": 3}})
	expect(t, st, v, 200, "")
	hashes := 0
	e.srv.hashPassword = func(pw string) string { hashes++; return "fake:" + pw }
	c := e.client()
	_, ch := c.do("GET", "/api/v1/signup/challenge", nil)
	sol := solvePoW(t, ch)
	st, v = c.do("POST", "/api/v1/signup", signupBody("ann", map[string]any{"pow": sol}))
	expect(t, st, v, 202, "")
	if hashes != 1 {
		t.Fatalf("hashes %d", hashes)
	}
	st, v = c.do("POST", "/api/v1/signup", signupBody("bob", map[string]any{"pow": sol}))
	expect(t, st, v, 400, "pow_invalid")
	if hashes != 1 {
		t.Fatalf("replayed proof was hashed: %d", hashes)
	}
	// Cap reached (3 attempts): even garbage is refused with 429, before the proof is looked at.
	c.do("POST", "/api/v1/signup", signupBody("cat", nil))
	st, v = c.do("POST", "/api/v1/signup", signupBody("dan", map[string]any{"pow": sol}))
	expect(t, st, v, 429, "rate_limited")
}
