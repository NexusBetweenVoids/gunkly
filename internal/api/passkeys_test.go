package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

const pkOrigin = "http://localhost:4865"

func passkeyEnv(t *testing.T, mutate ...func(*Options)) *env {
	t.Helper()
	return newEnv(t, append([]func(*Options){func(o *Options) { o.BaseURL = pkOrigin }}, mutate...)...)
}

// register adds a passkey to the (already stepped-up) client's account and
// returns the authenticator and the response of the registration.
func registerKey(t *testing.T, c *client, name string) (*softKey, any) {
	t.Helper()
	key := newSoftKey(t, pkOrigin, "localhost")
	st, opts := c.do("POST", "/api/v1/account/passkeys/options", nil)
	expect(t, st, opts, 200, "")
	st, v := c.do("POST", "/api/v1/account/passkeys", map[string]any{
		"challenge": field(opts, "challenge"), "credential": key.createCredential(opts), "name": name})
	expect(t, st, v, 201, "")
	return key, v
}

func TestPasskeyRegisterListAndPasswordlessLogin(t *testing.T) {
	e := passkeyEnv(t)
	e.user("ann", false)
	c := e.signedIn("ann")
	if st, v := c.do("POST", "/api/v1/account/passkeys/options", nil); st != 403 || code(v) != "step_up_required" {
		t.Fatalf("registration without step-up: %d %v", st, v)
	}
	e.stepUp(c, "ann")
	other := e.signedIn("ann")
	key, v := registerKey(t, c, "Laptop")
	if codes, _ := field(v, "recoveryCodes").([]any); len(codes) == 0 {
		t.Fatalf("first second factor issues recovery codes: %v", v)
	}
	if st, _ := other.do("GET", "/api/v1/me", nil); st != 401 {
		t.Fatalf("other session survived adding a passkey: %d", st)
	}
	st, list := c.do("GET", "/api/v1/account/passkeys", nil)
	expect(t, st, list, 200, "")
	items := list.([]any)
	if len(items) != 1 || field(items[0], "name") != "Laptop" || field(items[0], "lastUsedAt") != nil {
		t.Fatalf("list: %v", list)
	}
	for _, secret := range []string{"publicKey", "credentialId", "signCount"} {
		if _, has := items[0].(map[string]any)[secret]; has {
			t.Fatalf("list leaks %s", secret)
		}
	}
	if st, me := c.do("GET", "/api/v1/me", nil); st != 200 || field(me, "passkeys") != float64(1) {
		t.Fatalf("me: %d %v", st, me)
	}

	// Passwordless sign-in: no password, one call pair; counts as fully signed in.
	pl := e.client()
	st, opts := pl.do("POST", "/api/v1/session/passkey/login/options", nil)
	expect(t, st, opts, 200, "")
	if uv := field(field(field(opts, "options"), "publicKey"), "userVerification"); uv != "required" {
		t.Fatalf("passwordless must require user verification: %v", uv)
	}
	st, v = pl.do("POST", "/api/v1/session/passkey/login", map[string]any{"challenge": field(opts, "challenge"), "credential": key.assertion(opts)})
	expect(t, st, v, 200, "")
	if field(v, "authenticated") != true {
		t.Fatalf("not signed in: %v", v)
	}
	if st, _ := pl.do("GET", "/api/v1/me", nil); st != 200 {
		t.Fatalf("session: %d", st)
	}
	st, list = c.do("GET", "/api/v1/account/passkeys", nil)
	if expect(t, st, list, 200, ""); field(list.([]any)[0], "lastUsedAt") == nil {
		t.Fatalf("last used not recorded: %v", list)
	}

	// The challenge is single use: replaying the same answer fails.
	replay := e.client()
	st, v = replay.do("POST", "/api/v1/session/passkey/login", map[string]any{"challenge": field(opts, "challenge"), "credential": key.assertionFor("x", 99)})
	expect(t, st, v, 401, "invalid_passkey")

	// A passkey without user verification does not satisfy a passwordless sign-in.
	key.flags = flagUP
	nouv := e.client()
	st, opts = nouv.do("POST", "/api/v1/session/passkey/login/options", nil)
	expect(t, st, opts, 200, "")
	st, v = nouv.do("POST", "/api/v1/session/passkey/login", map[string]any{"challenge": field(opts, "challenge"), "credential": key.assertion(opts)})
	expect(t, st, v, 401, "invalid_passkey")
}

func TestPasskeyAsSecondFactor(t *testing.T) {
	e := passkeyEnv(t)
	e.user("ann", false)
	c := e.signedInStepUp("ann")
	key, _ := registerKey(t, c, "Phone")

	// Password alone no longer signs in: the passkey is asked for.
	c2 := e.client()
	st, v := c2.login("ann", nil)
	expect(t, st, v, 200, "")
	if field(v, "twoFactorRequired") != true || field(v, "method") != "passkey" || field(v, "passkey") != true {
		t.Fatalf("second factor not requested: %v", v)
	}
	// Options need the right password.
	st, v = c2.do("POST", "/api/v1/session/passkey/options", map[string]string{"login": "ann", "password": "wrong"})
	expect(t, st, v, 401, "invalid_credentials")
	st, opts := c2.do("POST", "/api/v1/session/passkey/options", map[string]string{"login": "ann", "password": "password-ann"})
	expect(t, st, opts, 200, "")
	body := map[string]any{"login": "ann", "password": "password-ann", "passkey": map[string]any{"challenge": field(opts, "challenge"), "credential": key.assertion(opts)}}
	st, v = c2.do("POST", "/api/v1/session", body)
	expect(t, st, v, 200, "")
	if field(v, "authenticated") != true {
		t.Fatalf("second factor not accepted: %v", v)
	}
	// Spent: the same answer again is refused.
	st, v = e.client().do("POST", "/api/v1/session", body)
	expect(t, st, v, 401, "invalid_passkey")

	// A passkey issued for a password sign-in of another account is useless
	// for this one.
	e.user("bob", false)
	bob := e.signedInStepUp("bob")
	bobKey, _ := registerKey(t, bob, "Bob's")
	st, optsBob := e.client().do("POST", "/api/v1/session/passkey/options", map[string]string{"login": "bob", "password": "password-bob"})
	expect(t, st, optsBob, 200, "")
	st, v = e.client().do("POST", "/api/v1/session", map[string]any{"login": "ann", "password": "password-ann",
		"passkey": map[string]any{"challenge": field(optsBob, "challenge"), "credential": bobKey.assertion(optsBob)}})
	expect(t, st, v, 401, "invalid_passkey")

	// Recovery codes (issued with the first passkey) still work.
	st, v = e.client().do("POST", "/api/v1/session", map[string]any{"login": "ann", "password": "password-ann", "recoveryCode": "nope"})
	expect(t, st, v, 401, "invalid_code")
}

func TestPasskeySignCountRegression(t *testing.T) {
	e := passkeyEnv(t)
	e.user("ann", false)
	c := e.signedInStepUp("ann")
	key, _ := registerKey(t, c, "Key")
	login := func(counter uint32) (int, any) {
		pl := e.client()
		_, opts := pl.do("POST", "/api/v1/session/passkey/login/options", nil)
		ch := field(opts, "challenge")
		cc := field(field(field(opts, "options"), "publicKey"), "challenge").(string)
		return pl.do("POST", "/api/v1/session/passkey/login", map[string]any{"challenge": ch, "credential": key.assertionFor(cc, counter)})
	}
	if st, v := login(5); st != 200 {
		t.Fatalf("first: %d %v", st, v)
	}
	if st, v := login(6); st != 200 {
		t.Fatalf("forward: %d %v", st, v)
	}
	// A counter that did not increase: cloned authenticator or replay.
	for _, n := range []uint32{6, 3} {
		st, v := login(n)
		expect(t, st, v, 401, "invalid_passkey")
	}
	// It is audited (the account sees it), and later honest use still works.
	evs, err := e.st.ListAuditForUser(context.Background(), myID(c), 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range evs {
		found = found || (ev.Event == store.AuditLoginFailure && ev.Meta["reason"] == "passkey_sign_count")
	}
	if !found {
		t.Fatal("sign count regression not audited")
	}
	if st, v := login(7); st != 200 {
		t.Fatalf("after regression: %d %v", st, v)
	}
}

func TestPasskeyStepUp(t *testing.T) {
	e := passkeyEnv(t)
	e.user("ann", false)
	c := e.signedInStepUp("ann")
	key, _ := registerKey(t, c, "Key")
	// A fresh session has no step-up; a user-verified passkey grants it alone.
	c2 := e.client()
	st, opts := c2.do("POST", "/api/v1/session/passkey/login/options", nil)
	expect(t, st, opts, 200, "")
	st, v := c2.do("POST", "/api/v1/session/passkey/login", map[string]any{"challenge": field(opts, "challenge"), "credential": key.assertion(opts)})
	expect(t, st, v, 200, "")
	if st, v := c2.do("DELETE", "/api/v1/account/passkeys/xyz", nil); st != 403 || code(v) != "step_up_required" {
		t.Fatalf("deleting without step-up: %d %v", st, v)
	}
	st, opts = c2.do("POST", "/api/v1/session/step-up/passkey/options", nil)
	expect(t, st, opts, 200, "")
	st, v = c2.do("POST", "/api/v1/session/step-up", map[string]any{"passkey": map[string]any{"challenge": field(opts, "challenge"), "credential": key.assertion(opts)}})
	expect(t, st, v, 204, "")
	st, list := c2.do("GET", "/api/v1/account/passkeys", nil)
	expect(t, st, list, 200, "")
	id := field(list.([]any)[0], "id").(string)
	// The options of a passwordless sign-in cannot be used for a step-up.
	c3 := e.client()
	_, o3 := c3.do("POST", "/api/v1/session/passkey/login/options", nil)
	if st, v := c3.do("POST", "/api/v1/session/passkey/login", map[string]any{"challenge": field(o3, "challenge"), "credential": key.assertion(o3)}); st != 200 {
		t.Fatalf("sign-in: %d %v", st, v)
	}
	st, optsLogin := e.client().do("POST", "/api/v1/session/passkey/login/options", nil)
	expect(t, st, optsLogin, 200, "")
	st, v = c3.do("POST", "/api/v1/session/step-up", map[string]any{"passkey": map[string]any{"challenge": field(optsLogin, "challenge"), "credential": key.assertion(optsLogin)}})
	expect(t, st, v, 401, "invalid_passkey")

	// Deleting needs the step-up; afterwards password sign-in is plain again.
	st, v = c2.do("DELETE", "/api/v1/account/passkeys/"+id, nil)
	expect(t, st, v, 204, "")
	st, v = e.client().login("ann", nil)
	expect(t, st, v, 200, "")
	if field(v, "authenticated") != true {
		t.Fatalf("removing the last passkey must remove the second factor: %v", v)
	}
	if st, v := c2.do("DELETE", "/api/v1/account/passkeys/"+id, nil); st != 404 {
		t.Fatalf("second delete: %d %v", st, v)
	}
}

func TestPasskeyIsolationBetweenUsers(t *testing.T) {
	e := passkeyEnv(t)
	e.user("ann", false)
	e.user("bob", false)
	ann := e.signedInStepUp("ann")
	registerKey(t, ann, "Ann's")
	bob := e.signedInStepUp("bob")
	_, list := ann.do("GET", "/api/v1/account/passkeys", nil)
	annID := field(list.([]any)[0], "id").(string)
	st, v := bob.do("GET", "/api/v1/account/passkeys", nil)
	expect(t, st, v, 200, "")
	if len(v.([]any)) != 0 {
		t.Fatalf("bob sees ann's passkeys: %v", v)
	}
	st, v = bob.do("DELETE", "/api/v1/account/passkeys/"+annID, nil)
	expect(t, st, v, 404, "")
	if st, l := ann.do("GET", "/api/v1/account/passkeys", nil); st != 200 || len(l.([]any)) != 1 {
		t.Fatalf("ann lost her passkey: %d %v", st, l)
	}
	// A registration started by ann cannot be finished by bob.
	key := newSoftKey(t, pkOrigin, "localhost")
	_, opts := ann.do("POST", "/api/v1/account/passkeys/options", nil)
	st, v = bob.do("POST", "/api/v1/account/passkeys", map[string]any{"challenge": field(opts, "challenge"), "credential": key.createCredential(opts), "name": "x"})
	expect(t, st, v, 400, "passkey_failed")
	// Neither can the same credential be registered twice.
	key2, _ := registerKey(t, bob, "Bob's")
	_, opts = ann.do("POST", "/api/v1/account/passkeys/options", nil)
	st, v = ann.do("POST", "/api/v1/account/passkeys", map[string]any{"challenge": field(opts, "challenge"), "credential": key2.createCredential(opts), "name": "dup"})
	if st == 201 {
		t.Fatalf("a credential ID registered twice: %v", v)
	}
}

func TestPasskeysNeedHTTPSBaseURL(t *testing.T) {
	for _, base := range []string{"", "http://192.168.1.5:4865", "http://gunkly.lan", "https://203.0.113.9"} {
		e := newEnv(t, func(o *Options) { o.BaseURL = base })
		e.user("ann", false)
		c := e.signedInStepUp("ann")
		st, v := c.do("POST", "/api/v1/account/passkeys/options", nil)
		expect(t, st, v, 400, "passkeys_require_https")
		st, v = e.client().do("POST", "/api/v1/session/passkey/login/options", nil)
		expect(t, st, v, 404, "")
		_, inst := e.client().do("GET", "/api/v1/instance", nil)
		if field(inst, "passkeysAvailable") != false {
			t.Fatalf("%q: passkeysAvailable %v", base, field(inst, "passkeysAvailable"))
		}
	}
	e := newEnv(t, func(o *Options) { o.BaseURL = "https://gunkly.example.com" })
	if _, inst := e.client().do("GET", "/api/v1/instance", nil); field(inst, "passkeysAvailable") != true {
		t.Fatalf("https base URL: %v", inst)
	}
}

func TestPasskeyLimit(t *testing.T) {
	e := passkeyEnv(t)
	e.user("ann", false)
	one := 1
	if err := e.st.UpdateInstanceSettings(context.Background(), store.SettingsUpdate{Limits: map[string]int{string(limits.PasskeysPerUser): one}}); err != nil {
		t.Fatal(err)
	}
	c := e.signedInStepUp("ann")
	registerKey(t, c, "First")
	st, v := c.do("POST", "/api/v1/account/passkeys/options", nil)
	expect(t, st, v, 409, "limit_reached")
	if limits.Defaults[limits.PasskeysPerUser] != 20 {
		t.Fatal("default passkey limit")
	}
}

func TestPasskeyWithOtherSecondFactors(t *testing.T) {
	e := passkeyEnv(t)
	e.user("ann", false)
	// With TOTP on, a passkey is added alongside it and no new recovery codes appear.
	c := e.signedInStepUp("ann")
	_, v := c.do("POST", "/api/v1/account/totp/setup", nil)
	secret := field(v, "secret").(string)
	st, v := c.do("POST", "/api/v1/account/totp/confirm", map[string]string{"code": totpCode(secret, e.clock.Now())})
	if st != 200 {
		t.Fatalf("totp: %d %v", st, v)
	}
	recovery := field(v, "recoveryCodes").([]any)[0].(string)
	e.stepUp2(c, "ann", secret)
	_, v = registerKey(t, c, "Key")
	if field(v, "recoveryCodes") != nil {
		t.Fatalf("recovery codes reissued: %v", v)
	}
	// Sign-in offers both; TOTP still works.
	e.clock.Add(30 * time.Second)
	c2 := e.client()
	st, v = c2.login("ann", nil)
	expect(t, st, v, 200, "")
	if field(v, "method") != "totp" || field(v, "passkey") != true {
		t.Fatalf("methods: %v", v)
	}
	if st, v := c2.login("ann", map[string]string{"totp": totpCode(secret, e.clock.Now())}); st != 200 || field(v, "authenticated") != true {
		t.Fatalf("totp login: %d %v", st, v)
	}
	// Turning TOTP off keeps the recovery codes while a passkey remains.
	e.clock.Add(30 * time.Second)
	e.stepUp2(c, "ann", secret)
	if st, v := c.do("DELETE", "/api/v1/account/totp", nil); st != 204 {
		t.Fatalf("totp off: %d %v", st, v)
	}
	st, v = e.client().login("ann", map[string]string{"recoveryCode": recovery})
	if st != 200 || field(v, "authenticated") != true {
		t.Fatalf("recovery codes dropped with a passkey left: %d %v", st, v)
	}
}

// stepUp2 re-verifies a TOTP account (password plus a fresh code).
func (e *env) stepUp2(c *client, name, secret string) {
	e.t.Helper()
	e.clock.Add(30 * time.Second)
	st, v := c.do("POST", "/api/v1/session/step-up", map[string]string{"password": "password-" + name, "totp": totpCode(secret, e.clock.Now())})
	if st != 204 {
		e.t.Fatalf("step-up: %d %v", st, v)
	}
}

func TestPasskeyExportAndAccountRemoval(t *testing.T) {
	e := passkeyEnv(t)
	e.user("ann", false)
	c := e.signedInStepUp("ann")
	registerKey(t, c, "Laptop")
	st, doc := c.do("GET", "/api/v1/account/export", nil)
	if st != 200 {
		t.Fatalf("export: %d", st)
	}
	b, _ := json.Marshal(doc)
	if !strings.Contains(string(b), `"passkeys":[{"createdAt"`) || !strings.Contains(string(b), "Laptop") {
		t.Fatalf("export lacks the passkey: %s", b)
	}
	for _, leak := range []string{"publicKey", "credential", "signCount", "aaguid"} {
		if strings.Contains(strings.ToLower(string(b)), strings.ToLower(leak)) {
			t.Fatalf("export leaks %s", leak)
		}
	}
}

func myID(c *client) string {
	_, me := c.do("GET", "/api/v1/me", nil)
	return field(me, "id").(string)
}
