package api

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A down SMTP server delays the email instead of losing it, and the retries
// do not count against the caps again.
func TestMailSurvivesSMTPOutage(t *testing.T) {
	e := newMailEnv(t)
	e.fm.err = errors.New("smtp down")
	e.user("ann", false)
	c := e.signedIn("ann")
	st, v := c.do("POST", "/api/v1/account/email/resend", nil)
	expect(t, st, v, 202, "")
	if got := e.mails(); len(got) != 0 {
		t.Fatalf("sent while down: %v", got)
	}
	e.clock.Add(30 * time.Second) // before the first retry (1 minute)
	e.fm.err = nil
	if got := e.mails(); len(got) != 0 {
		t.Fatalf("retried too early: %v", got)
	}
	e.clock.Add(31 * time.Second)
	msg := e.oneMail("ann@example.com")
	token := tokenIn(t, msg)
	st, v = e.client().do("POST", "/api/v1/account/email/verify", map[string]string{"token": token})
	expect(t, st, v, 200, "")
	e.clock.Add(24 * time.Hour)
	if got := e.mails(); len(got) != 0 {
		t.Fatalf("sent twice: %v", got)
	}
	e.srv.counts.mu.Lock()
	n := len(e.srv.counts.events["mail:all"])
	e.srv.counts.mu.Unlock()
	if n != 1 {
		t.Fatalf("the global email cap counted %d events, want 1 (queued once, retried without counting)", n)
	}
}

// A sign-in code is not retried once it has expired, and the user's audit
// log says an email could not be delivered, without address or text.
func TestMailCodeNotRetriedAfterExpiry(t *testing.T) {
	e := newMailEnv(t)
	e.user("ann", false)
	e.verified("ann")
	e.enableEmailOTP("ann")
	e.fm.err = errors.New("smtp down: ann@example.com")
	st, v := e.client().login("ann", nil)
	expect(t, st, v, 200, "")
	if got := e.mails(); len(got) != 0 {
		t.Fatalf("sent while down: %v", got)
	}
	e.clock.Add(otpTTL + time.Minute)
	e.fm.err = nil
	if got := e.mails(); len(got) != 0 {
		t.Fatalf("an expired code was sent: %v", got)
	}
	u, _ := e.st.UserByLogin(context.Background(), "ann")
	evs, err := e.st.ListAuditForUser(context.Background(), u.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range evs {
		if ev.Event == "mail_gave_up" {
			found = true
			if ev.Meta["kind"] != "otp" || ev.Meta["reason"] != "expired" || len(ev.Meta) != 3 {
				t.Fatalf("audit meta: %v", ev.Meta)
			}
		}
	}
	if !found {
		t.Fatalf("no mail_gave_up audit event: %v", evs)
	}
}
