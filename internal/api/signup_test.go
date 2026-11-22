package api

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
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
	e := newMailEnv(t)
	e.openSignup(true)
	c := e.client()
	if st, v := c.do("GET", "/api/v1/instance", nil); field(v, "openSignup") != true || field(v, "requirePow") != true {
		t.Fatalf("instance: %d %v", st, v)
	}

	// No solution, a wrong one and a forged signature are refused.
	st, v := c.do("POST", "/api/v1/signup", signupBody("ann", nil))
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

	// A mistake in the form does not spend the solution.
	st, v = c.do("POST", "/api/v1/signup", signupBody("a", map[string]any{"pow": sol}))
	expect(t, st, v, 400, "invalid")
	st, v = c.do("POST", "/api/v1/signup", signupBody("ann", map[string]any{"pow": sol}))
	expect(t, st, v, 201, "")
	if field(v, "authenticated") != true || field(field(v, "user"), "emailVerified") != false {
		t.Fatalf("signup: %v", v)
	}
	msg := e.oneMail("ann@example.org")
	if tokenIn(t, msg) == "" {
		t.Fatal("no verification link")
	}

	// Single use.
	st, v = e.client().do("POST", "/api/v1/signup", signupBody("bob", map[string]any{"pow": sol}))
	expect(t, st, v, 400, "pow_invalid")

	// A taken username or email gets the same answer.
	for _, name := range []string{"ann", "other"} {
		_, ch = c.do("GET", "/api/v1/signup/challenge", nil)
		body := signupBody(name, map[string]any{"pow": solvePoW(t, ch)})
		if name == "other" {
			body["email"] = "ann@example.org"
		}
		st, v = e.client().do("POST", "/api/v1/signup", body)
		expect(t, st, v, 400, "registration_failed")
	}
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
	e := newMailEnv(t)
	e.openSignup(false)
	c := e.client()
	st, v := c.do("GET", "/api/v1/signup/challenge", nil)
	expect(t, st, v, 404, "not_found")
	st, v = c.do("POST", "/api/v1/signup", signupBody("ann", nil))
	expect(t, st, v, 201, "")
	// Email is required when the instance sends email.
	st, v = e.client().do("POST", "/api/v1/signup", signupBody("bob", map[string]any{"email": ""}))
	expect(t, st, v, 400, "invalid")
}

func TestSignupWithoutEmailInstance(t *testing.T) {
	e := newEnv(t)
	e.openSignup(false)
	st, v := e.client().do("POST", "/api/v1/signup", signupBody("ann", map[string]any{"email": ""}))
	expect(t, st, v, 201, "")
}

func TestSignupPerIPLimit(t *testing.T) {
	e := newMailEnv(t)
	e.openSignup(false)
	root := e.signedInStepUp("root")
	st, v := root.do("PATCH", "/api/v1/admin/settings", map[string]any{"limits": map[string]int{"signups_per_ip_hour": 2}})
	expect(t, st, v, 200, "")
	for _, n := range []string{"a1", "a2"} {
		st, v = e.client().do("POST", "/api/v1/signup", signupBody(n, nil))
		expect(t, st, v, 201, "")
	}
	st, v = e.client().do("POST", "/api/v1/signup", signupBody("a3", nil))
	expect(t, st, v, 429, "rate_limited")
	e.clock.Add(time.Hour + time.Second)
	st, v = e.client().do("POST", "/api/v1/signup", signupBody("a3", nil))
	expect(t, st, v, 201, "")
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
	expect(t, st, v, 201, "")

	// An admin can still close it.
	e.user("root", true)
	root := e.signedInStepUp("root")
	st, v = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"openSignup": false})
	expect(t, st, v, 200, "")
	if st, v = c.do("GET", "/api/v1/instance", nil); field(v, "openSignup") != false {
		t.Fatalf("instance: %d %v", st, v)
	}
}
