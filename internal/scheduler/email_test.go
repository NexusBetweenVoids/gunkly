package scheduler

import (
	"strings"
	"testing"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// emailOn gives a a verified address and turns email reminders on.
func (f *fixture) emailOn(a store.Actor, addr string) {
	f.t.Helper()
	if _, err := f.db.ExecContext(f.ctx, f.db.Dialect.Rebind("UPDATE users SET email = ?, email_lower = ?, email_verified = TRUE WHERE id = ?"),
		addr, addr, a.UserID); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.st.SetEmailReminders(f.ctx, a.UserID, true); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) emailWorker(m *fakeMailer) *Worker {
	w := f.worker()
	w.cfg.Mailer = m
	return w
}

func (f *fixture) quietMorning() {
	f.t.Helper()
	quiet := &store.QuietHours{StartMin: 8 * 60, EndMin: 10 * 60}
	if _, err := f.st.UpdateHousehold(f.ctx, f.owner, f.hid, store.HouseholdUpdate{QuietHours: &quiet}); err != nil {
		f.t.Fatal(err)
	}
}

func TestEmailReminderDelivery(t *testing.T) {
	f := newFixture(t)
	m := &fakeMailer{}
	f.emailOn(f.owner, "owner@example.com")
	f.chore("Dishes", "2026-11-21", "gentle")
	w := f.emailWorker(m)
	f.tickAt(w, "2026-11-21 08:59:00")
	f.tickAt(w, "2026-11-21 09:01:00")
	out := m.take()
	if len(out) != 1 || out[0].To != "owner@example.com" {
		t.Fatalf("mails: %+v", out)
	}
	if !strings.Contains(out[0].Subject, "Dishes") || !strings.Contains(out[0].Body, "Dishes") || !strings.Contains(out[0].Body, "/done/") {
		t.Fatalf("content: %+v", out[0])
	}
	if got := f.deliveryStatuses(); len(got) != 1 || got[0] != "email:sent" {
		t.Fatalf("log: %v", got)
	}
	f.tickAt(w, "2026-11-21 09:06:00") // never twice
	if n := len(m.take()); n != 0 {
		t.Fatalf("re-sent: %d", n)
	}
}

func TestEmailReminderAlongsideChannels(t *testing.T) {
	f := newFixture(t)
	m := &fakeMailer{}
	f.emailOn(f.owner, "owner@example.com")
	f.channel(f.owner, "alice")
	f.chore("Dishes", "2026-11-21", "gentle")
	w := f.emailWorker(m)
	f.tickAt(w, "2026-11-21 08:59:00")
	f.tickAt(w, "2026-11-21 09:01:00")
	if n := len(m.take()); n != 1 {
		t.Fatalf("emails = %d, want 1", n)
	}
	if got := recipients(f.sender.take()); got != "alice" {
		t.Fatalf("channel got %q", got)
	}
}

func TestEmailReminderRecipientRules(t *testing.T) {
	f := newFixture(t)
	m := &fakeMailer{}
	guest := f.member("guest", store.RoleGuest)
	muter := f.member("muter", store.RoleMember)
	other := f.member("other", store.RoleMember)
	for i, a := range []store.Actor{guest, muter, other, f.owner} {
		f.emailOn(a, "u"+string(rune('a'+i))+"@example.com")
	}
	c := f.chore("Dishes", "2026-11-21", "gentle", other.UserID)
	if err := f.st.SetChoreMuted(f.ctx, muter, f.hid, c.ID, true); err != nil {
		t.Fatal(err)
	}
	w := f.emailWorker(m)
	f.tickAt(w, "2026-11-21 08:59:00")
	f.tickAt(w, "2026-11-21 09:01:00")
	out := m.take()
	if len(out) != 1 || out[0].To != "uc@example.com" { // assigned to "other" only; guests and muters excluded
		t.Fatalf("mails: %+v", out)
	}
}

func TestEmailReminderQuietHours(t *testing.T) {
	f := newFixture(t)
	m := &fakeMailer{}
	f.emailOn(f.owner, "owner@example.com")
	f.chore("Dishes", "2026-11-21", "gentle")
	f.quietMorning()
	w := f.emailWorker(m)
	f.tickAt(w, "2026-11-21 08:59:00")
	f.tickAt(w, "2026-11-21 09:01:00")
	if n := len(m.take()); n != 0 {
		t.Fatalf("sent during quiet hours: %d", n)
	}
	f.tickAt(w, "2026-11-21 10:01:00")
	if n := len(m.take()); n != 1 {
		t.Fatalf("after quiet hours: %d", n)
	}
}

func TestEmailReminderOff(t *testing.T) {
	f := newFixture(t)
	m := &fakeMailer{}
	f.emailOn(f.owner, "owner@example.com")
	f.chore("Dishes", "2026-11-21", "gentle")
	w := f.emailWorker(m)
	f.tickAt(w, "2026-11-21 08:59:00")
	if _, err := f.st.SetEmailReminders(f.ctx, f.owner.UserID, false); err != nil {
		t.Fatal(err)
	}
	f.tickAt(w, "2026-11-21 09:01:00")
	if n := len(m.take()); n != 0 {
		t.Fatalf("sent with email reminders off: %d", n)
	}
	// An unverified address cannot be turned on.
	u2 := f.user("plain")
	if _, err := f.st.SetEmailReminders(f.ctx, u2.UserID, true); err == nil {
		t.Fatal("enabled without a verified address")
	}
}

func TestEmailReminderNoMailer(t *testing.T) {
	f := newFixture(t)
	f.emailOn(f.owner, "owner@example.com")
	f.chore("Dishes", "2026-11-21", "gentle")
	w := f.worker() // email not configured: nothing is queued
	f.tickAt(w, "2026-11-21 08:59:00")
	f.tickAt(w, "2026-11-21 09:01:00")
	if got := f.deliveryStatuses(); len(got) != 0 {
		t.Fatalf("queued without a mailer: %v", got)
	}
}

func TestEmailReminderDroppedWhenUnverifiedAfterQueueing(t *testing.T) {
	f := newFixture(t)
	m := &fakeMailer{}
	f.emailOn(f.owner, "owner@example.com")
	f.chore("Dishes", "2026-11-21", "gentle")
	f.quietMorning()
	w := f.emailWorker(m)
	f.tickAt(w, "2026-11-21 08:59:00")
	f.tickAt(w, "2026-11-21 09:01:00") // queued, deferred to 10:00
	if _, err := f.db.ExecContext(f.ctx, f.db.Dialect.Rebind("UPDATE users SET email_verified = FALSE WHERE id = ?"), f.owner.UserID); err != nil {
		t.Fatal(err)
	}
	f.tickAt(w, "2026-11-21 10:01:00")
	if n := len(m.take()); n != 0 {
		t.Fatalf("sent to an unverified address: %d", n)
	}
}
