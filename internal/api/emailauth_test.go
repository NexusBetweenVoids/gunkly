package api

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/mail"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// fakeMailer records every email; nothing leaves the process.
type fakeMailer struct {
	mu   sync.Mutex
	sent []mail.Message
	err  error
}

func (f *fakeMailer) Send(_ context.Context, m mail.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, m)
	return nil
}

// mailEnv is an env with email enabled.
type mailEnv struct {
	*env
	fm *fakeMailer
}

func newMailEnv(t *testing.T, mutate ...func(*Options)) *mailEnv {
	t.Helper()
	fm := &fakeMailer{}
	e := newEnv(t, append([]func(*Options){func(o *Options) { o.Mailer = fm; o.BaseURL = "http://app.test"; o.PoWMaxNumber = 1000 }}, mutate...)...)
	return &mailEnv{env: e, fm: fm}
}

// mails waits for background sends and returns (and clears) what was sent.
func (e *mailEnv) mails() []mail.Message {
	e.srv.mailWG.Wait()
	e.fm.mu.Lock()
	defer e.fm.mu.Unlock()
	out := e.fm.sent
	e.fm.sent = nil
	return out
}

func (e *mailEnv) oneMail(to string) mail.Message {
	e.t.Helper()
	ms := e.mails()
	if len(ms) != 1 || ms[0].To != to {
		e.t.Fatalf("want one email to %s, got %+v", to, ms)
	}
	return ms[0]
}

var (
	tokenRE = regexp.MustCompile(`token=([A-Za-z0-9_-]+)`)
	codeRE  = regexp.MustCompile(`code is (\d{6})`)
)

func tokenIn(t *testing.T, m mail.Message) string {
	t.Helper()
	g := tokenRE.FindStringSubmatch(m.Body)
	if g == nil || !strings.Contains(m.Body, "http://app.test/") {
		t.Fatalf("no link in %q", m.Body)
	}
	return g[1]
}

func codeIn(t *testing.T, m mail.Message) string {
	t.Helper()
	g := codeRE.FindStringSubmatch(m.Body)
	if g == nil {
		t.Fatalf("no code in %q", m.Body)
	}
	return g[1]
}

// verified marks the user's current email verified, as a clicked link would.
func (e *mailEnv) verified(name string) {
	e.t.Helper()
	ctx := context.Background()
	u, err := e.st.UserByLogin(ctx, name)
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.st.IssueVerifyToken(ctx, u.ID, "seed-"+name, "", e.clock.Now().Add(time.Hour)); err != nil {
		e.t.Fatal(err)
	}
	if _, _, err := e.st.VerifyEmail(ctx, "seed-"+name, false, nil); err != nil {
		e.t.Fatal(err)
	}
}

// stepUpEmail re-verifies a user whose second factor is email codes.
func (e *mailEnv) stepUpEmail(c *client, name string) {
	e.t.Helper()
	st, v := c.do("POST", "/api/v1/session/step-up", map[string]string{"password": "password-" + name})
	expect(e.t, st, v, 401, "two_factor_required")
	code := codeIn(e.t, e.oneMail(name+"@example.com"))
	st, v = c.do("POST", "/api/v1/session/step-up", map[string]string{"password": "password-" + name, "emailCode": code})
	expect(e.t, st, v, 204, "")
}

func (e *mailEnv) enableEmailOTP(name string) []string {
	e.t.Helper()
	c := e.signedInStepUp(name)
	st, v := c.do("POST", "/api/v1/account/email-otp", nil)
	if st != 200 {
		e.t.Fatalf("enable email otp: %d %v", st, v)
	}
	var codes []string
	for _, x := range field(v, "recoveryCodes").([]any) {
		codes = append(codes, x.(string))
	}
	return codes
}

func TestEmailVerificationFlow(t *testing.T) {
	e := newMailEnv(t)
	e.user("ann", false)
	c := e.signedIn("ann")

	st, v := c.do("GET", "/api/v1/me", nil)
	if st != 200 || field(v, "emailVerified") != false {
		t.Fatalf("me: %d %v", st, v)
	}
	st, v = c.do("POST", "/api/v1/account/email/resend", nil)
	expect(t, st, v, 202, "")
	msg := e.oneMail("ann@example.com")
	if !strings.Contains(msg.Subject, "Confirm your email") || strings.Contains(msg.Body, "ann") && !strings.Contains(msg.Body, "ann@") {
		t.Fatalf("mail: %+v", msg)
	}
	token := tokenIn(t, msg)

	// Public: the link is often opened on another device.
	anon := e.client()
	st, v = anon.do("POST", "/api/v1/account/email/verify", map[string]string{"token": "nonsense"})
	expect(t, st, v, 400, "invalid_token")
	st, v = anon.do("POST", "/api/v1/account/email/verify", map[string]string{"token": token})
	expect(t, st, v, 200, "")
	if field(v, "verified") != true || field(v, "recoveryCodes") != nil {
		t.Fatalf("verify (self-hosted, no auto 2FA): %v", v)
	}
	st, v = anon.do("POST", "/api/v1/account/email/verify", map[string]string{"token": token})
	expect(t, st, v, 400, "invalid_token") // single use
	if _, v = c.do("GET", "/api/v1/me", nil); field(v, "emailVerified") != true {
		t.Fatalf("me after: %v", v)
	}
	st, v = c.do("POST", "/api/v1/account/email/resend", nil)
	expect(t, st, v, 409, "already_verified")
}

func TestEmailVerificationTokenExpiresAndResendReplaces(t *testing.T) {
	e := newMailEnv(t)
	e.user("ann", false)
	c := e.signedIn("ann")
	c.do("POST", "/api/v1/account/email/resend", nil)
	first := tokenIn(t, e.oneMail("ann@example.com"))
	c.do("POST", "/api/v1/account/email/resend", nil)
	second := tokenIn(t, e.oneMail("ann@example.com"))

	st, v := c.do("POST", "/api/v1/account/email/verify", map[string]string{"token": first})
	expect(t, st, v, 400, "invalid_token") // replaced by the newer link
	e.clock.Add(24*time.Hour + time.Second)
	st, v = c.do("POST", "/api/v1/account/email/verify", map[string]string{"token": second})
	expect(t, st, v, 400, "invalid_token") // 24 h is over
}

func TestEmailDisabledInstancesAnswer404(t *testing.T) {
	e := newEnv(t) // no mailer
	e.user("ann", false)
	c := e.signedInStepUp("ann")
	for _, r := range []struct{ m, p string }{
		{"POST", "/api/v1/account/email/verify"}, {"POST", "/api/v1/account/email/resend"}, {"PATCH", "/api/v1/account/email"},
		{"POST", "/api/v1/password-reset/request"}, {"POST", "/api/v1/password-reset/confirm"}, {"POST", "/api/v1/account/email-otp"},
	} {
		st, v := c.do(r.m, r.p, map[string]string{"token": "x", "login": "ann", "email": "n@example.com"})
		if st != 404 {
			t.Errorf("%s %s: %d %v", r.m, r.p, st, v)
		}
	}
	st, v := e.client().do("GET", "/api/v1/instance", nil)
	if st != 200 || field(v, "emailEnabled") != false || field(v, "hostedMode") != false || field(v, "openSignup") != false {
		t.Fatalf("instance: %d %v", st, v)
	}
}

func TestEmailChange(t *testing.T) {
	e := newMailEnv(t)
	e.user("ann", false)
	e.user("bob", false)
	c := e.signedIn("ann")

	st, v := c.do("PATCH", "/api/v1/account/email", map[string]string{"email": "new@example.com"})
	expect(t, st, v, 403, "step_up_required")
	e.stepUp(c, "ann")
	for _, bad := range []string{"", "nope", "ann@example.com", "ANN@example.com", "a@b@c"} {
		st, v = c.do("PATCH", "/api/v1/account/email", map[string]string{"email": bad})
		expect(t, st, v, 400, "invalid")
	}

	st, v = c.do("PATCH", "/api/v1/account/email", map[string]string{"email": "new@example.com"})
	expect(t, st, v, 202, "")
	ms := e.mails()
	if len(ms) != 2 {
		t.Fatalf("want a verification and a notice, got %+v", ms)
	}
	var link, notice mail.Message
	for _, m := range ms {
		switch m.To {
		case "new@example.com":
			link = m
		case "ann@example.com":
			notice = m
		}
	}
	if link.To == "" || notice.To == "" || !strings.Contains(notice.Body, "new@example.com") || strings.Contains(notice.Body, "token=") {
		t.Fatalf("mails: %+v", ms)
	}
	if _, v = c.do("GET", "/api/v1/me", nil); field(v, "email") != "ann@example.com" {
		t.Fatalf("changed before verification: %v", v)
	}
	// Resend goes to the pending address.
	c.do("POST", "/api/v1/account/email/resend", nil)
	link = e.oneMail("new@example.com")
	st, v = c.do("POST", "/api/v1/account/email/verify", map[string]string{"token": tokenIn(t, link)})
	expect(t, st, v, 200, "")
	if _, v = c.do("GET", "/api/v1/me", nil); field(v, "email") != "new@example.com" || field(v, "emailVerified") != true {
		t.Fatalf("after verification: %v", v)
	}

	// A taken address looks the same from outside and sends nothing.
	st, v = c.do("PATCH", "/api/v1/account/email", map[string]string{"email": "bob@example.com"})
	expect(t, st, v, 202, "")
	if ms := e.mails(); len(ms) != 0 {
		t.Fatalf("mail for a taken address: %+v", ms)
	}
}

func TestPasswordResetFlow(t *testing.T) {
	e := newMailEnv(t)
	e.user("ann", false)
	ann := e.signedIn("ann")

	// Same answer for a real login, an email, an unknown login and an account without email.
	e.st.CreateUser(context.Background(), store.NewUser{Username: "noemail", DisplayName: "n", PasswordHash: "fake:x"}) //nolint:errcheck // fixture
	var bodies []any
	for i, login := range []string{"ann", "ANN@example.com", "ghost", "noemail"} {
		st, v := e.client().do("POST", "/api/v1/password-reset/request", map[string]string{"login": login})
		expect(t, st, v, 202, "")
		bodies = append(bodies, v)
		if n, want := len(e.mails()), []int{1, 1, 0, 0}[i]; n != want {
			t.Fatalf("%s: %d emails, want %d", login, n, want)
		}
	}
	for _, b := range bodies[1:] {
		if field(b, "accepted") != field(bodies[0], "accepted") {
			t.Fatalf("answers differ: %v vs %v", b, bodies[0])
		}
	}
	e.clock.Add(time.Hour + time.Minute) // make room in the per-account caps and expire the earlier links
	st, v := e.client().do("POST", "/api/v1/password-reset/request", map[string]string{"login": "ann"})
	expect(t, st, v, 202, "")
	msg := e.oneMail("ann@example.com")
	if !strings.Contains(msg.Subject, "Reset your Gunkly password") {
		t.Fatalf("mail: %+v", msg)
	}
	token := tokenIn(t, msg)

	anon := e.client()
	st, v = anon.do("POST", "/api/v1/password-reset/confirm", map[string]string{"token": token, "password": "short"})
	expect(t, st, v, 400, "password_too_short")
	st, v = anon.do("POST", "/api/v1/password-reset/confirm", map[string]string{"token": "bogus", "password": "a much longer password"})
	expect(t, st, v, 400, "invalid_token")
	st, v = anon.do("POST", "/api/v1/password-reset/confirm", map[string]string{"token": token, "password": "a much longer password"})
	expect(t, st, v, 204, "")
	st, v = anon.do("POST", "/api/v1/password-reset/confirm", map[string]string{"token": token, "password": "another long password"})
	expect(t, st, v, 400, "invalid_token") // single use

	// Every session is gone and the new password works.
	if st, v = ann.do("GET", "/api/v1/me", nil); st != 401 {
		t.Fatalf("old session survived: %d %v", st, v)
	}
	st, v = e.client().do("POST", "/api/v1/session", map[string]string{"login": "ann", "password": "password-ann"})
	expect(t, st, v, 401, "invalid_credentials")
	st, v = e.client().do("POST", "/api/v1/session", map[string]string{"login": "ann", "password": "a much longer password"})
	expect(t, st, v, 200, "")
}

func TestPasswordResetExpiresAfterAnHourAndKeepsSecondFactor(t *testing.T) {
	e := newMailEnv(t)
	e.user("ann", false)
	e.client().do("POST", "/api/v1/password-reset/request", map[string]string{"login": "ann"})
	late := tokenIn(t, e.oneMail("ann@example.com"))
	e.clock.Add(time.Hour + time.Second)
	st, v := e.client().do("POST", "/api/v1/password-reset/confirm", map[string]string{"token": late, "password": "a much longer password"})
	expect(t, st, v, 400, "invalid_token")

	// A user with 2FA still needs it after a reset.
	e.verified("ann")
	e.enableEmailOTP("ann")
	e.client().do("POST", "/api/v1/password-reset/request", map[string]string{"login": "ann"})
	tok := tokenIn(t, e.oneMail("ann@example.com"))
	st, v = e.client().do("POST", "/api/v1/password-reset/confirm", map[string]string{"token": tok, "password": "a much longer password"})
	expect(t, st, v, 204, "")
	st, v = e.client().do("POST", "/api/v1/session", map[string]string{"login": "ann", "password": "a much longer password"})
	if st != 200 || field(v, "twoFactorRequired") != true || field(v, "authenticated") == true {
		t.Fatalf("2FA skipped after reset: %d %v", st, v)
	}
}

func TestPasswordResetCaps(t *testing.T) {
	e := newMailEnv(t)
	for _, n := range []string{"u1", "u2", "u3", "u4", "u5", "u6", "u7"} {
		e.user(n, false)
	}
	// Five emails per IP and hour for unauthenticated flows (PRD); every
	// request still gets the same 202, but the sixth sends nothing.
	sent := 0
	for _, n := range []string{"u1", "u2", "u3", "u4", "u5", "u6", "u7"} {
		st, v := e.client().do("POST", "/api/v1/password-reset/request", map[string]string{"login": n})
		expect(t, st, v, 202, "")
		sent += len(e.mails())
	}
	if sent != 5 {
		t.Fatalf("sent %d emails, want 5", sent)
	}
	// The cap slides: an hour later there is room again.
	e.clock.Add(time.Hour + time.Second)
	e.client().do("POST", "/api/v1/password-reset/request", map[string]string{"login": "u6"})
	if len(e.mails()) != 1 {
		t.Fatal("no email after the window passed")
	}
}

// A new reset request must not invalidate the link the owner already holds,
// and at most three are outstanding (ADR 0008).
func TestPasswordResetKeepsEarlierLinks(t *testing.T) {
	e := newMailEnv(t)
	e.user("ann", false)
	var toks []string
	for range 4 {
		e.client().do("POST", "/api/v1/password-reset/request", map[string]string{"login": "ann"})
		for _, m := range e.mails() {
			toks = append(toks, tokenIn(t, m))
		}
	}
	if len(toks) != store.MaxResetTokens {
		t.Fatalf("%d links issued, want %d", len(toks), store.MaxResetTokens)
	}
	st, v := e.client().do("POST", "/api/v1/password-reset/confirm", map[string]string{"token": toks[0], "password": "a much longer password"})
	expect(t, st, v, 204, "") // the first link still works
}

// An attacker spamming reset or verification requests for a victim from
// anywhere must not cause the victim's own sign-in codes to be refused.
func TestMailSpamCannotLockOutSignInCodes(t *testing.T) {
	e := newMailEnv(t)
	e.user("ann", false)
	e.verified("ann")
	e.enableEmailOTP("ann")
	e.mails()
	u, _ := e.st.UserByLogin(context.Background(), "ann")
	ctx := context.Background()
	// Many attacker IPs (each within its own per-IP cap), all aimed at ann:
	// unauthenticated sends carry no account bucket.
	for i := range 30 {
		if wait := e.srv.mailQuota(ctx, u.ID, fmt.Sprintf("198.51.100.%d", i), ""); wait > 0 {
			t.Fatalf("attacker send %d refused: %v", i, wait)
		}
	}
	st, v := e.client().login("ann", nil)
	expect(t, st, v, 200, "")
	if m := e.oneMail("ann@example.com"); !strings.Contains(m.Subject, "sign-in code") {
		t.Fatalf("mail: %+v", m)
	}
	// Sign-in codes have their own account bucket: 20 an hour.
	for i := range 19 {
		if wait := e.srv.mailQuota(ctx, u.ID, fmt.Sprintf("203.0.113.%d", i), limits.EmailCodesAccountHour); wait > 0 {
			t.Fatalf("code %d refused: %v", i, wait)
		}
	}
	if wait := e.srv.mailQuota(ctx, u.ID, "203.0.113.99", limits.EmailCodesAccountHour); wait == 0 {
		t.Fatal("21st code allowed")
	}
}

func TestEmailOTPLoginFlow(t *testing.T) {
	e := newMailEnv(t)
	e.user("ann", false)
	e.verified("ann")
	recovery := e.enableEmailOTP("ann")
	if len(recovery) != auth.RecoveryCodeCount {
		t.Fatalf("recovery codes: %v", recovery)
	}

	c := e.client()
	st, v := c.login("ann", nil)
	expect(t, st, v, 200, "")
	if field(v, "twoFactorRequired") != true || field(v, "method") != "email" || field(v, "authenticated") == true {
		t.Fatalf("login: %v", v)
	}
	msg := e.oneMail("ann@example.com")
	code := codeIn(t, msg)
	if !strings.Contains(msg.Subject, "sign-in code") {
		t.Fatalf("subject %q", msg.Subject)
	}

	wrong := "000000"
	if code == wrong {
		wrong = "000001"
	}
	st, v = c.login("ann", map[string]string{"emailCode": wrong})
	expect(t, st, v, 401, "invalid_code")
	st, v = c.login("ann", map[string]string{"emailCode": code})
	expect(t, st, v, 200, "")
	if field(v, "authenticated") != true || field(field(v, "user"), "twoFactor") != "email" {
		t.Fatalf("signed in: %v", v)
	}
	// Single use.
	st, v = e.client().login("ann", map[string]string{"emailCode": code})
	if st == 200 {
		t.Fatalf("code reused: %v", v)
	}
}

func TestEmailOTPAttemptsExpiryAndResend(t *testing.T) {
	e := newMailEnv(t)
	e.user("ann", false)
	e.verified("ann")
	e.enableEmailOTP("ann")

	c := e.client()
	c.login("ann", nil)
	code := codeIn(t, e.oneMail("ann@example.com"))
	wrong := "000000"
	if code == wrong {
		wrong = "000001"
	}
	// Five wrong guesses burn the challenge, even for the right code.
	for range store.MaxOTPAttempts {
		st, v := c.login("ann", map[string]string{"emailCode": wrong})
		expect(t, st, v, 401, "invalid_code")
	}
	st, v := c.login("ann", map[string]string{"emailCode": code})
	expect(t, st, v, 401, "invalid_code")

	// A resend issues a new code (the old one stays dead) that expires after 10 minutes.
	e.clock.Add(time.Minute) // past the per-account failure backoff
	c.login("ann", nil)
	fresh := codeIn(t, e.oneMail("ann@example.com"))
	e.clock.Add(10*time.Minute + time.Second)
	st, v = c.login("ann", map[string]string{"emailCode": fresh})
	expect(t, st, v, 401, "invalid_code")

	e.clock.Add(time.Minute)
	c.login("ann", nil)
	fresh = codeIn(t, e.oneMail("ann@example.com"))
	st, v = e.client().login("ann", map[string]string{"emailCode": fresh})
	expect(t, st, v, 200, "")
}

func TestEmailOTPRecoveryCodeAndResendCap(t *testing.T) {
	e := newMailEnv(t)
	e.user("ann", false)
	e.verified("ann")
	recovery := e.enableEmailOTP("ann")
	st, v := e.client().login("ann", map[string]string{"recoveryCode": recovery[0]})
	expect(t, st, v, 200, "")
	st, v = e.client().login("ann", map[string]string{"recoveryCode": recovery[0]})
	expect(t, st, v, 401, "invalid_code") // single use
	e.mails()

	// The sign-in resend is capped at 5 emails per IP and hour.
	c := e.client()
	sent := 0
	for range 8 {
		if st, _ := c.login("ann", nil); st == 200 {
			sent++
		}
	}
	if sent != 5 || len(e.mails()) != 5 {
		t.Fatalf("sent %d", sent)
	}
	st, v = c.login("ann", nil)
	expect(t, st, v, 429, "rate_limited")
}

func TestEmailOTPStepUp(t *testing.T) {
	e := newMailEnv(t)
	e.user("ann", false)
	e.verified("ann")
	e.enableEmailOTP("ann")
	c := e.client()
	c.login("ann", nil)
	code := codeIn(t, e.oneMail("ann@example.com"))
	st, v := c.login("ann", map[string]string{"emailCode": code})
	expect(t, st, v, 200, "")

	st, v = c.do("POST", "/api/v1/session/step-up", map[string]string{"password": "password-ann"})
	expect(t, st, v, 401, "two_factor_required")
	code = codeIn(t, e.oneMail("ann@example.com"))
	st, v = c.do("POST", "/api/v1/session/step-up", map[string]string{"password": "password-ann", "emailCode": code})
	expect(t, st, v, 204, "")
}

func TestEnableEmailOTPNeedsVerifiedEmail(t *testing.T) {
	e := newMailEnv(t)
	e.user("ann", false)
	c := e.signedInStepUp("ann")
	st, v := c.do("POST", "/api/v1/account/email-otp", nil)
	expect(t, st, v, 409, "email_not_verified")
	e.verified("ann")
	st, v = c.do("POST", "/api/v1/account/email-otp", nil)
	expect(t, st, v, 200, "")
	st, v = c.do("POST", "/api/v1/account/email-otp", nil)
	expect(t, st, v, 409, "email_otp_already_enabled")

	// Self-hosted: it can be turned off again (hosted forbids it). Step-up now
	// needs an emailed code as well.
	e.mails()
	e.stepUpEmail(c, "ann")
	st, v = c.do("DELETE", "/api/v1/account/email-otp", nil)
	expect(t, st, v, 204, "")
	if st, v = c.do("GET", "/api/v1/me", nil); field(v, "twoFactor") != "" || st != 200 {
		t.Fatalf("me: %v", v)
	}
	st, v = c.do("DELETE", "/api/v1/account/email-otp", nil)
	expect(t, st, v, 404, "not_found")
}

func TestHostedRequiresVerificationAndTwoFactor(t *testing.T) {
	e := newMailEnv(t, func(o *Options) { o.HostedMode = true })
	e.user("ann", false)
	c := e.signedIn("ann")
	owner := e.signedIn("ann") // second device, same account
	_ = owner

	// Unverified: can sign in, cannot create or join households.
	st, v := c.do("POST", "/api/v1/households", map[string]string{"name": "A", "timezone": "Europe/Amsterdam"})
	expect(t, st, v, 403, "email_not_verified")
	if st, _ = c.do("GET", "/api/v1/me", nil); st != 200 {
		t.Fatalf("unverified user cannot read /me: %d", st)
	}
	st, v = c.do("POST", "/api/v1/invites/sometoken/accept", nil)
	expect(t, st, v, 403, "email_not_verified")

	// Verifying enrols email codes (2FA is mandatory) and shows recovery codes once.
	c.do("POST", "/api/v1/account/email/resend", nil)
	st, v = e.client().do("POST", "/api/v1/account/email/verify", map[string]string{"token": tokenIn(t, e.oneMail("ann@example.com"))})
	expect(t, st, v, 200, "")
	if rc, _ := field(v, "recoveryCodes").([]any); len(rc) != auth.RecoveryCodeCount {
		t.Fatalf("recovery codes: %v", v)
	}
	if _, v = c.do("GET", "/api/v1/me", nil); field(v, "twoFactor") != "email" {
		t.Fatalf("me: %v", v)
	}
	st, v = c.do("POST", "/api/v1/households", map[string]string{"name": "A", "timezone": "Europe/Amsterdam"})
	expect(t, st, v, 201, "")

	// 2FA cannot be switched off, only switched to another method.
	cs := e.client()
	cs.login("ann", nil)
	code := codeIn(t, e.oneMail("ann@example.com"))
	st, v = cs.login("ann", map[string]string{"emailCode": code})
	expect(t, st, v, 200, "")
	cs.do("POST", "/api/v1/session/step-up", map[string]string{"password": "password-ann"})
	code = codeIn(t, e.oneMail("ann@example.com"))
	st, v = cs.do("POST", "/api/v1/session/step-up", map[string]string{"password": "password-ann", "emailCode": code})
	expect(t, st, v, 204, "")
	st, v = cs.do("DELETE", "/api/v1/account/email-otp", nil)
	expect(t, st, v, 409, "two_factor_mandatory")
}

func TestHostedVerifiedWithoutSecondFactorMustSetOneUp(t *testing.T) {
	e := newMailEnv(t, func(o *Options) { o.HostedMode = true })
	e.user("ann", false)
	e.verified("ann") // verified before 2FA became mandatory
	c := e.signedIn("ann")
	st, v := c.do("POST", "/api/v1/households", map[string]string{"name": "A", "timezone": "Europe/Amsterdam"})
	expect(t, st, v, 403, "two_factor_setup_required")
	e.stepUp(c, "ann")
	st, v = c.do("POST", "/api/v1/account/email-otp", nil)
	expect(t, st, v, 200, "")
	st, v = c.do("POST", "/api/v1/households", map[string]string{"name": "A", "timezone": "Europe/Amsterdam"})
	expect(t, st, v, 201, "")
}

func TestSelfHostedUnverifiedUsersAreNotBlocked(t *testing.T) {
	e := newMailEnv(t)
	e.user("ann", false)
	c := e.signedIn("ann")
	st, v := c.do("POST", "/api/v1/households", map[string]string{"name": "A", "timezone": "Europe/Amsterdam"})
	expect(t, st, v, 201, "")
}

func TestSelfHostedOpenSignupDoesNotRequireVerifiedEmail(t *testing.T) {
	e := newMailEnv(t)
	e.user("root", true)
	e.user("ann", false)
	root := e.signedInStepUp("root")
	st, v := root.do("PATCH", "/api/v1/admin/settings", map[string]any{"openSignup": true})
	expect(t, st, v, 200, "")
	c := e.signedIn("ann")
	st, v = c.do("POST", "/api/v1/households", map[string]string{"name": "A", "timezone": "Europe/Amsterdam"})
	expect(t, st, v, 201, "")
}

func TestInviteRegistrationInHostedModeSendsVerification(t *testing.T) {
	e := newMailEnv(t, func(o *Options) { o.HostedMode = true })
	e.user("ann", false)
	e.verified("ann")
	e.enableEmailOTP("ann")
	owner := e.client()
	owner.login("ann", nil)
	owner.login("ann", map[string]string{"emailCode": codeIn(t, e.oneMail("ann@example.com"))})
	st, v := owner.do("POST", "/api/v1/households", map[string]string{"name": "A", "timezone": "Europe/Amsterdam"})
	expect(t, st, v, 201, "")
	hh := field(v, "id").(string)
	e.mails()
	st, v = owner.do("POST", "/api/v1/households/"+hh+"/invites", map[string]any{"role": "member"})
	expect(t, st, v, 201, "")
	tok := field(v, "token").(string)
	st, v = e.client().do("POST", "/api/v1/invites/"+tok+"/register", map[string]string{
		"username": "newbie", "email": "newbie@example.com", "password": "a much longer password", "displayName": "New"})
	expect(t, st, v, 201, "") // vouched by the owner: joins at once
	e.oneMail("newbie@example.com")
}

func TestMailSendFailureDoesNotBreakRequests(t *testing.T) {
	e := newMailEnv(t)
	e.fm.err = errors.New("smtp down")
	e.user("ann", false)
	c := e.signedIn("ann")
	st, v := c.do("POST", "/api/v1/account/email/resend", nil)
	expect(t, st, v, 202, "")
	e.mails()
}

func TestWindowCounter(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	w := newWindowCounter(clk.Now, 3)
	for range 2 {
		if w.take(quota{"a", 2}, quota{"all", 5}) != 0 {
			t.Fatal("under the limit")
		}
		clk.Add(10 * time.Minute)
	}
	if wait := w.take(quota{"a", 2}, quota{"all", 5}); wait != 40*time.Minute {
		t.Fatalf("wait = %v, want 40m", wait)
	}
	// A refused take records nothing anywhere.
	if wait := w.take(quota{"b", 1}, quota{"a", 2}); wait == 0 {
		t.Fatal("a is full")
	}
	if wait := w.take(quota{"b", 1}); wait != 0 {
		t.Fatal("refused take consumed b")
	}
	clk.Add(41 * time.Minute)
	if w.take(quota{"a", 2}) != 0 {
		t.Fatal("window did not slide")
	}
	// Bounded: with the table full of live keys, a new key is refused.
	w.take(quota{"c", 9})
	if w.take(quota{"d", 9}) == 0 {
		t.Fatal("table should be full")
	}
}

func TestNewRoutesDeclareTheirPolicies(t *testing.T) {
	e := newEnv(t)
	want := map[string]policy{
		"GET /api/v1/instance":                  public,
		"GET /api/v1/signup/challenge":          publicRateLimited(rateSignup),
		"POST /api/v1/signup":                   publicRateLimited(rateSignup),
		"POST /api/v1/account/email/verify":     publicRateLimited(rateVerify),
		"POST /api/v1/account/email/resend":     session.noToken().withRate(rateEmail),
		"PATCH /api/v1/account/email":           session.noToken().withStepUp().withRate(rateEmail),
		"POST /api/v1/password-reset/request":   publicRateLimited(rateReset),
		"POST /api/v1/password-reset/confirm":   publicRateLimited(rateReset),
		"POST /api/v1/account/email-otp":        session.noToken().withStepUp().withRate(rateTwoFA),
		"DELETE /api/v1/account/email-otp":      session.noToken().withStepUp(),
		"POST /api/v1/account/totp/setup":       session.noToken().withStepUp(),
		"POST /api/v1/session/step-up":          session.noToken().withRate(rateTwoFA),
		"POST /api/v1/households":               session.noToken(),
		"POST /api/v1/invites/{token}/accept":   session.noToken().withRate(rateInvite),
		"POST /api/v1/invites/{token}/register": publicRateLimited(rateInvite),
	}
	seen := 0
	for _, r := range e.srv.routes {
		if w, ok := want[r.pattern]; ok {
			seen++
			if r.policy != w {
				t.Errorf("%s: policy %+v, want %+v", r.pattern, r.policy, w)
			}
		}
	}
	if seen != len(want) {
		t.Errorf("found %d of %d routes", seen, len(want))
	}
}

// Every sensitive email route refuses a request without the CSRF token.
func TestEmailRoutesRequireCSRF(t *testing.T) {
	e := newMailEnv(t)
	e.user("ann", false)
	c := e.signedInStepUp("ann")
	for _, r := range []struct{ m, p string }{
		{"POST", "/api/v1/account/email/verify"}, {"POST", "/api/v1/account/email/resend"}, {"PATCH", "/api/v1/account/email"},
		{"POST", "/api/v1/password-reset/request"}, {"POST", "/api/v1/password-reset/confirm"}, {"POST", "/api/v1/signup"},
		{"POST", "/api/v1/account/email-otp"}, {"DELETE", "/api/v1/account/email-otp"},
	} {
		resp := c.raw(r.m, r.p, map[string]string{}, "")
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Errorf("%s %s without CSRF: %d", r.m, r.p, resp.StatusCode)
		}
	}
}
