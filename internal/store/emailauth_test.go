package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

type clockT struct{ t time.Time }

func (c *clockT) now() time.Time { return c.t }

func emailStore(t *testing.T) (*store.Store, *clockT) {
	t.Helper()
	clk := &clockT{time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)}
	return newStore(t).WithClock(clk.now), clk
}

func hashOTP(code string) func(string) string {
	return func(challenge string) string { return auth.HashEmailOTP(challenge, code) }
}

func TestEmailOTPAttemptsExpiryAndSingleUse(t *testing.T) {
	ctx := context.Background()
	s, clk := emailStore(t)
	u := mkUser(t, s, "ann")
	issue := func(code string) {
		t.Helper()
		noErr(t, s.IssueEmailOTP(ctx, u.UserID, "chal-"+code, auth.HashEmailOTP("chal-"+code, code), clk.t.Add(10*time.Minute)))
	}
	check := func(code string) bool {
		t.Helper()
		return must(s.ConsumeEmailOTP(ctx, u.UserID, func(ch string) string { return auth.HashEmailOTP(ch, code) }))
	}

	// Right code once; a replay fails.
	issue("111111")
	if !check("111111") || check("111111") {
		t.Fatal("a code must verify exactly once")
	}

	// Five wrong guesses invalidate the challenge, even for the right code.
	issue("222222")
	for range store.MaxOTPAttempts {
		if check("000000") {
			t.Fatal("wrong code accepted")
		}
	}
	if check("222222") {
		t.Fatal("code accepted after the attempt limit")
	}

	// The limit counts the attempts that were made: four wrong, then right works.
	issue("333333")
	for range store.MaxOTPAttempts - 1 {
		check("000000")
	}
	if !check("333333") {
		t.Fatal("right code on the last attempt refused")
	}

	// A newer code replaces the older one.
	issue("444444")
	issue("555555")
	if check("444444") || !check("555555") {
		t.Fatal("only the newest code may work")
	}

	// Expired codes never verify.
	issue("666666")
	clk.t = clk.t.Add(10*time.Minute + time.Second)
	if check("666666") {
		t.Fatal("expired code accepted")
	}

	// Nothing outstanding is simply false.
	if check("123456") {
		t.Fatal("no challenge but verified")
	}
	// Codes belong to their user.
	v := mkUser(t, s, "bob")
	clk.t = clk.t.Add(time.Hour)
	noErr(t, s.IssueEmailOTP(ctx, v.UserID, "chal-v", auth.HashEmailOTP("chal-v", "777777"), clk.t.Add(10*time.Minute)))
	if must(s.ConsumeEmailOTP(ctx, u.UserID, hashOTP("777777"))) {
		t.Fatal("another user's code accepted")
	}
}

func TestVerifyEmailAndChange(t *testing.T) {
	ctx := context.Background()
	s, clk := emailStore(t)
	a := mkUser(t, s, "ann")
	b := mkUser(t, s, "bob")

	noErr(t, s.IssueVerifyToken(ctx, a.UserID, "t1", "", clk.t.Add(time.Hour)))
	noErr(t, s.IssueVerifyToken(ctx, a.UserID, "t2", "", clk.t.Add(time.Hour))) // replaces t1
	if _, _, err := s.VerifyEmail(ctx, "t1", false, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("replaced token: %v", err)
	}
	u, enrolled, err := s.VerifyEmail(ctx, "t2", false, nil)
	if err != nil || !u.EmailVerified || enrolled {
		t.Fatalf("verify: %+v %v %v", u, enrolled, err)
	}
	if _, _, err := s.VerifyEmail(ctx, "t2", false, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("single use: %v", err)
	}

	// Expiry.
	noErr(t, s.IssueVerifyToken(ctx, a.UserID, "t3", "", clk.t.Add(time.Minute)))
	clk.t = clk.t.Add(2 * time.Minute)
	if _, _, err := s.VerifyEmail(ctx, "t3", false, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired: %v", err)
	}

	// Change: nothing happens until verified; a taken address conflicts.
	noErr(t, s.IssueVerifyToken(ctx, a.UserID, "t4", "New@Example.com", clk.t.Add(time.Hour)))
	if p := must(s.PendingEmail(ctx, a.UserID)); p != "New@Example.com" {
		t.Fatalf("pending = %q", p)
	}
	if got := must(s.UserByID(ctx, a.UserID)); got.Email != "ann@example.com" {
		t.Fatalf("email changed early: %q", got.Email)
	}
	u, _, err = s.VerifyEmail(ctx, "t4", false, nil)
	if err != nil || u.Email != "New@Example.com" || !u.EmailVerified {
		t.Fatalf("change: %+v %v", u, err)
	}
	if got := must(s.UserByLogin(ctx, "new@example.com")); got.ID != a.UserID {
		t.Fatal("new address does not resolve (case-insensitive)")
	}
	noErr(t, s.IssueVerifyToken(ctx, b.UserID, "t5", "new@example.com", clk.t.Add(time.Hour)))
	if _, _, err := s.VerifyEmail(ctx, "t5", false, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("taken address: %v", err)
	}
	if taken := must(s.EmailTaken(ctx, "NEW@example.com", b.UserID)); !taken {
		t.Fatal("EmailTaken false")
	}
}

func TestVerifyAutoEnrolsEmailOTP(t *testing.T) {
	ctx := context.Background()
	s, clk := emailStore(t)
	a := mkUser(t, s, "ann")
	noErr(t, s.IssueVerifyToken(ctx, a.UserID, "t1", "", clk.t.Add(time.Hour)))
	u, enrolled, err := s.VerifyEmail(ctx, "t1", true, []string{"rc1", "rc2"})
	if err != nil || !enrolled || !u.EmailOTPEnabled {
		t.Fatalf("auto enrol: %+v %v %v", u, enrolled, err)
	}
	// A user who already has a second factor is left alone.
	b := mkUser(t, s, "bob")
	noErr(t, s.SetPendingTOTP(ctx, b.UserID, "v1:x"))
	noErr(t, s.EnableTOTP(ctx, b.UserID, "", 1, []string{"h"}))
	noErr(t, s.IssueVerifyToken(ctx, b.UserID, "t2", "", clk.t.Add(time.Hour)))
	u, enrolled, err = s.VerifyEmail(ctx, "t2", true, []string{"rc"})
	if err != nil || enrolled || u.EmailOTPEnabled || !u.TOTPEnabled {
		t.Fatalf("existing TOTP: %+v %v %v", u, enrolled, err)
	}
}

func TestEnableEmailOTPNeedsVerifiedEmailAndExcludesTOTP(t *testing.T) {
	ctx := context.Background()
	s, clk := emailStore(t)
	a := mkUser(t, s, "ann")
	if err := s.EnableEmailOTP(ctx, a.UserID, "", []string{"r"}); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("unverified: %v", err)
	}
	noErr(t, s.IssueVerifyToken(ctx, a.UserID, "t", "", clk.t.Add(time.Hour)))
	if _, _, err := s.VerifyEmail(ctx, "t", false, nil); err != nil {
		t.Fatal(err)
	}
	noErr(t, s.SetPendingTOTP(ctx, a.UserID, "v1:x"))
	noErr(t, s.EnableTOTP(ctx, a.UserID, "", 1, []string{"h"}))
	noErr(t, s.EnableEmailOTP(ctx, a.UserID, "", []string{"r"}))
	u := must(s.UserByID(ctx, a.UserID))
	if !u.EmailOTPEnabled || u.TOTPEnabled || u.TOTPSecret != "" {
		t.Fatalf("switch to email: %+v", u)
	}
	// And back: enabling TOTP turns email codes off.
	noErr(t, s.SetPendingTOTP(ctx, a.UserID, "v1:y"))
	noErr(t, s.EnableTOTP(ctx, a.UserID, "", 2, []string{"h"}))
	if u = must(s.UserByID(ctx, a.UserID)); u.EmailOTPEnabled || !u.TOTPEnabled {
		t.Fatalf("switch to totp: %+v", u)
	}
	noErr(t, s.DisableTwoFactor(ctx, a.UserID, ""))
	if u = must(s.UserByID(ctx, a.UserID)); u.EmailOTPEnabled || u.TOTPEnabled {
		t.Fatalf("reset: %+v", u)
	}
}

func TestResetPasswordIsAtomicAndSingleUse(t *testing.T) {
	ctx := context.Background()
	s, clk := emailStore(t)
	a := mkUser(t, s, "ann")
	sess := must(s.CreateSession(ctx, store.NewSession{UserID: a.UserID, TokenHash: "th", ExpiresAt: clk.t.Add(time.Hour)}))
	_ = sess
	mustIssue(t, s, ctx, a.UserID, "r1", clk.t.Add(time.Hour))
	// An empty hash is refused and must not consume the token.
	if _, err := s.ResetPassword(ctx, "r1", ""); err == nil {
		t.Fatal("empty password hash accepted")
	}
	id, err := s.ResetPassword(ctx, "r1", "newhash")
	if err != nil || id != a.UserID {
		t.Fatalf("reset: %q %v", id, err)
	}
	if u := must(s.UserByID(ctx, a.UserID)); u.PasswordHash != "newhash" {
		t.Fatal("password not set")
	}
	if ss := must(s.ListSessions(ctx, a.UserID)); len(ss) != 0 {
		t.Fatalf("%d sessions survived", len(ss))
	}
	if _, err := s.ResetPassword(ctx, "r1", "again"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("reuse: %v", err)
	}
	mustIssue(t, s, ctx, a.UserID, "r2", clk.t.Add(time.Hour))
	clk.t = clk.t.Add(time.Hour + time.Second)
	if _, err := s.ResetPassword(ctx, "r2", "late"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired: %v", err)
	}
	// A reset token cannot verify an email, and vice versa.
	mustIssue(t, s, ctx, a.UserID, "r3", clk.t.Add(time.Hour))
	if _, _, err := s.VerifyEmail(ctx, "r3", false, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("purpose mix-up: %v", err)
	}
}

func TestUsePoWOnce(t *testing.T) {
	ctx := context.Background()
	s, clk := emailStore(t)
	noErr(t, s.UsePoW(ctx, "c1", clk.t.Add(time.Minute)))
	if err := s.UsePoW(ctx, "c1", clk.t.Add(time.Minute)); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("reuse: %v", err)
	}
	// Expired rows are purged by the retention job.
	clk.t = clk.t.Add(time.Hour)
	if _, err := s.PurgeExpired(ctx, store.RetentionPolicy{}, clk.t); err != nil {
		t.Fatal(err)
	}
	noErr(t, s.UsePoW(ctx, "c1", clk.t.Add(time.Minute)))
}

func mustIssue(t *testing.T, s *store.Store, ctx context.Context, userID, hash string, exp time.Time) bool {
	t.Helper()
	ok, err := s.IssueResetToken(ctx, userID, hash, exp)
	noErr(t, err)
	return ok
}

// A new reset request keeps earlier unexpired tokens valid (up to the cap), so
// a stranger spamming requests cannot invalidate the owner's link.
func TestResetTokensCoexistUpToTheCap(t *testing.T) {
	ctx := context.Background()
	s, clk := emailStore(t)
	a := mkUser(t, s, "ann")
	exp := clk.t.Add(time.Hour)
	for _, h := range []string{"t1", "t2", "t3"} {
		if !mustIssue(t, s, ctx, a.UserID, h, exp) {
			t.Fatalf("%s not issued", h)
		}
	}
	if mustIssue(t, s, ctx, a.UserID, "t4", exp) {
		t.Fatal("issued beyond the cap")
	}
	// The oldest is still good; using it drops the others.
	if _, err := s.ResetPassword(ctx, "t1", "newhash"); err != nil {
		t.Fatalf("oldest token: %v", err)
	}
	if _, err := s.ResetPassword(ctx, "t2", "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("sibling token survived: %v", err)
	}
	// Expired tokens free their slots.
	for _, h := range []string{"u1", "u2", "u3"} {
		mustIssue(t, s, ctx, a.UserID, h, exp)
	}
	clk.t = clk.t.Add(2 * time.Hour)
	if !mustIssue(t, s, ctx, a.UserID, "u4", clk.t.Add(time.Hour)) {
		t.Fatal("expired tokens still count")
	}
}
