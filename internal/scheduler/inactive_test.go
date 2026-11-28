package scheduler

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/db"
	"github.com/NexusBetweenVoids/gunkly/internal/db/dbtest"
	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/mail"
	"github.com/NexusBetweenVoids/gunkly/internal/notify"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

const day = 24 * time.Hour

type fakeMailer struct {
	mu   sync.Mutex
	out  []mail.Message
	fail error
}

func (f *fakeMailer) Send(_ context.Context, m mail.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.out = append(f.out, m)
	return nil
}

func (f *fakeMailer) take() []mail.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.out
	f.out = nil
	return out
}

type fakePusher struct {
	mu   sync.Mutex
	subs []string
	msgs []notify.PushMessage
}

func (f *fakePusher) Push(_ context.Context, subID, _, _ string, m notify.PushMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs = append(f.subs, subID)
	f.msgs = append(f.msgs, m)
	return nil
}

type inactiveFixture struct {
	t      *testing.T
	ctx    context.Context
	db     *db.DB
	st     *store.Store
	w      *Worker
	mailer *fakeMailer
	sender *fakeSender
	pusher *fakePusher
	ring   *auth.Keyring
	t0     time.Time
	now    time.Time
}

func newInactiveFixture(t *testing.T) *inactiveFixture {
	t.Helper()
	f := &inactiveFixture{t: t, ctx: context.Background(), mailer: &fakeMailer{}, sender: &fakeSender{}, pusher: &fakePusher{},
		t0: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	f.now = f.t0
	f.db = dbtest.Open(t)
	f.st = store.New(f.db).WithClock(func() time.Time { return f.now })
	var err error
	if f.ring, err = auth.NewKeyring(1, map[uint32]string{1: strings.Repeat("k", 32)}); err != nil {
		t.Fatal(err)
	}
	f.w = New(Config{Store: f.st, DB: f.db, Sender: f.sender, Keyring: f.ring, BaseURL: "https://gunkly.example",
		Mailer: f.mailer, Pusher: f.pusher, Now: func() time.Time { return f.now }, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	return f
}

func (f *inactiveFixture) enable() {
	f.t.Helper()
	on := true
	if err := f.st.UpdateInstanceSettings(f.ctx, store.SettingsUpdate{InactiveEnabled: &on}); err != nil {
		f.t.Fatal(err)
	}
}

// runAt runs the daily job at t0 + d days.
func (f *inactiveFixture) runAt(d int) {
	f.now = f.t0.Add(time.Duration(d) * day)
	f.w.inactive(f.ctx, f.now)
}

func (f *inactiveFixture) user(name string, admin bool) store.User {
	f.t.Helper()
	u, err := f.st.CreateUser(f.ctx, store.NewUser{Username: name, Email: name + "@example.com", DisplayName: name, PasswordHash: "x", IsAdmin: admin, Locale: "en"})
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.db.ExecContext(f.ctx, f.db.Dialect.Rebind("UPDATE users SET email_verified = ? WHERE id = ?"), true, u.ID); err != nil {
		f.t.Fatal(err)
	}
	return u
}

func (f *inactiveFixture) exists(id string) bool {
	_, err := f.st.UserByID(f.ctx, id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		f.t.Fatal(err)
	}
	return err == nil
}

func (f *inactiveFixture) warnedAt(id string) (warned, final bool) {
	f.t.Helper()
	us, err := f.st.InactiveUsers(f.ctx, f.now.Add(day), "", 1000)
	if err != nil {
		f.t.Fatal(err)
	}
	for _, u := range us {
		if u.ID == id {
			return !u.WarnedAt.IsZero(), !u.FinalWarnedAt.IsZero()
		}
	}
	f.t.Fatalf("user %s not found", id)
	return
}

func TestInactiveLifecycle(t *testing.T) {
	f := newInactiveFixture(t)
	alice, bob, carol, guest, admin := f.user("alice", false), f.user("bob", false), f.user("carol", false), f.user("guest", false), f.user("root", true)
	a, b, c, g := store.Actor{UserID: alice.ID}, store.Actor{UserID: bob.ID}, store.Actor{UserID: carol.ID}, store.Actor{UserID: guest.ID}

	// Alice owns "Home" with a guest, bob (joined first) and carol, and "Mine"
	// with only a guest. She has a channel and a push subscription.
	f.now = f.t0.Add(time.Hour)
	home, err := f.st.CreateHousehold(f.ctx, a, store.NewHousehold{Name: "Home", Timezone: "Europe/Amsterdam"})
	if err != nil {
		t.Fatal(err)
	}
	mine, err := f.st.CreateHousehold(f.ctx, a, store.NewHousehold{Name: "Mine", Timezone: "Europe/Amsterdam"})
	if err != nil {
		t.Fatal(err)
	}
	for i, m := range []struct {
		hh   string
		u    store.Actor
		role store.Role
	}{{home.ID, g, store.RoleGuest}, {home.ID, b, store.RoleMember}, {home.ID, c, store.RoleMember}, {mine.ID, g, store.RoleGuest}} {
		f.now = f.t0.Add(time.Duration(i+2) * time.Hour)
		if err := f.st.AddMember(f.ctx, a, m.hh, m.u.UserID, m.role); err != nil {
			t.Fatal(err)
		}
	}
	chID := ids.New()
	if _, err := f.st.CreateUserChannel(f.ctx, a, store.NewChannel{ID: chID, Service: "telegram", Ciphertext: notify.Seal(f.ring, chID, "telegram://token@telegram?chats=1"), Max: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CreatePushSubscription(f.ctx, a, store.NewPushSubscription{ID: ids.New(), Endpoint: "https://push.example/x", Ciphertext: "v1:k", Max: 5}); err != nil {
		t.Fatal(err)
	}
	// Reset activity to t0 for everyone: the memberships above were set up "later".
	if _, err := f.db.ExecContext(f.ctx, f.db.Dialect.Rebind("UPDATE users SET last_active_at = ?"), f.t0.UnixMilli()); err != nil {
		t.Fatal(err)
	}

	// Off by default: nothing happens however long they stay away.
	f.runAt(400)
	if len(f.mailer.take()) != 0 || f.exists(alice.ID) == false {
		t.Fatal("disabled job acted")
	}
	f.enable()

	// Before the warn period: nothing.
	f.runAt(300)
	if len(f.mailer.take()) != 0 {
		t.Fatal("warned too early")
	}

	// Warn period over: email (alice only: verified), her channel and push.
	f.runAt(366)
	mails := f.mailer.take()
	if len(mails) != 5-1 { // everyone but the admin has a verified address
		t.Fatalf("warning emails = %d, want 4", len(mails))
	}
	var amail mail.Message
	for _, m := range mails {
		if m.To == "alice@example.com" {
			amail = m
		}
		if m.To == "root@example.com" {
			t.Fatal("admin was warned")
		}
	}
	if !strings.Contains(amail.Subject, "deleted if you stay away") || !strings.Contains(amail.Body, "2027-07-") {
		t.Fatalf("warning mail = %+v", amail)
	}
	if got := f.sender.take(); len(got) != 1 || !strings.Contains(got[0].msg.Body, "https://gunkly.example") {
		t.Fatalf("channel warnings = %+v", got)
	}
	if len(f.pusher.subs) != 1 {
		t.Fatalf("push warnings = %d", len(f.pusher.subs))
	}
	if w, fin := f.warnedAt(alice.ID); !w || fin {
		t.Fatalf("alice warned/final = %v/%v", w, fin)
	}
	// Idempotent: a restart (or a second run) warns nobody again.
	f.runAt(366)
	f.runAt(380)
	f.w.lastInactive = time.Time{}
	f.runAt(381)
	if len(f.mailer.take())+len(f.sender.take()) != 0 || len(f.pusher.subs) != 1 {
		t.Fatal("warnings repeated")
	}

	// Bob signs in: his warnings are cleared and his clock restarts.
	f.now = f.t0.Add(500 * day)
	if _, err := f.st.CreateSession(f.ctx, store.NewSession{UserID: bob.ID, TokenHash: "bobtok", ExpiresAt: f.now.Add(day)}); err != nil {
		t.Fatal(err)
	}
	if us, _ := f.st.InactiveUsers(f.ctx, f.now, "", 100); len(us) != 4 { // everyone but bob
		t.Fatalf("idle users after sign-in = %d", len(us))
	}

	// About 30 days before deletion: the final warning (once).
	f.runAt(517)
	if len(f.mailer.take()) != 0 {
		t.Fatal("final warning too early")
	}
	f.runAt(519)
	finals := f.mailer.take()
	if len(finals) != 3 { // alice, carol, guest
		t.Fatalf("final warnings = %d, want 3", len(finals))
	}
	for _, m := range finals {
		if !strings.Contains(m.Subject, "Final notice") || !strings.Contains(m.Subject, "2027-07-") {
			t.Fatalf("final mail = %+v", m)
		}
	}
	if got := f.sender.take(); len(got) != 1 || !strings.HasPrefix(got[0].msg.Body, "Final notice") {
		t.Fatalf("final channel warnings = %+v", got)
	}
	f.runAt(520)
	if len(f.mailer.take()) != 0 {
		t.Fatal("final warning repeated")
	}
	if !f.exists(alice.ID) {
		t.Fatal("deleted before the delete period")
	}

	// Delete period over: alice, carol and the guest go; bob (signed in) stays.
	f.runAt(549)
	for _, u := range []store.User{alice, carol, guest} {
		if f.exists(u.ID) {
			t.Errorf("%s not deleted", u.Username)
		}
	}
	if !f.exists(bob.ID) || !f.exists(admin.ID) {
		t.Fatal("bob or the admin was deleted")
	}
	// Ownership went to bob, the longest-standing non-guest; the guest was
	// never promoted (and is gone anyway); the guest-only household is deleted.
	if r, err := f.st.Role(f.ctx, b, home.ID); err != nil || r != store.RoleOwner {
		t.Fatalf("bob's role = %v, %v", r, err)
	}
	if _, err := f.st.Household(f.ctx, b, mine.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("guest-only household kept: %v", err)
	}

	// The admin is never deleted, however long.
	f.runAt(5000)
	if !f.exists(admin.ID) {
		t.Fatal("admin deleted")
	}
}

// A guest is never promoted: with only a guest left the household goes.
func TestInactiveOwnerWithOnlyGuest(t *testing.T) {
	f := newInactiveFixture(t)
	owner, guest := f.user("owner", false), f.user("guest", false)
	o, g := store.Actor{UserID: owner.ID}, store.Actor{UserID: guest.ID}
	hh, err := f.st.CreateHousehold(f.ctx, o, store.NewHousehold{Name: "Home", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.AddMember(f.ctx, o, hh.ID, g.UserID, store.RoleGuest); err != nil {
		t.Fatal(err)
	}
	f.now = f.t0.Add(100 * day)
	if _, err := f.st.CreateSession(f.ctx, store.NewSession{UserID: guest.ID, TokenHash: "g", ExpiresAt: f.now.Add(day)}); err != nil {
		t.Fatal(err)
	}
	f.enable()
	f.runAt(366) // owner warned (the guest is active)
	f.runAt(519) // final
	f.runAt(548) // delete period over, but the final warning needs 30 days
	if !f.exists(owner.ID) {
		t.Fatal("deleted without 30 days notice")
	}
	f.runAt(549)
	if f.exists(owner.ID) {
		t.Fatal("owner kept")
	}
	if !f.exists(guest.ID) {
		t.Fatal("guest deleted")
	}
	if _, err := f.st.Household(f.ctx, g, hh.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("household with only a guest left must be deleted: %v", err)
	}
}

// Enabling the job on an instance with long-idle accounts: warn first, final
// next run, deletion no sooner than 30 days after the final warning.
func TestInactiveLateEnableGivesNotice(t *testing.T) {
	f := newInactiveFixture(t)
	u := f.user("late", false)
	f.enable()
	f.runAt(600)
	if len(f.mailer.take()) != 1 || !f.exists(u.ID) {
		t.Fatal("first run: want one warning, no deletion")
	}
	f.runAt(601)
	if m := f.mailer.take(); len(m) != 1 || !strings.Contains(m[0].Subject, "Final notice") {
		t.Fatalf("second run: %+v", m)
	}
	f.runAt(620)
	if !f.exists(u.ID) {
		t.Fatal("deleted inside the notice period")
	}
	f.runAt(632)
	if f.exists(u.ID) {
		t.Fatal("not deleted after the notice period")
	}
}

// If every way of reaching the user fails, the warning is not recorded and
// is retried; a user with no way to be reached is recorded as warned.
func TestInactiveWarningRetried(t *testing.T) {
	f := newInactiveFixture(t)
	u := f.user("flaky", false)
	f.enable()
	f.mailer.fail = errors.New("smtp down")
	f.runAt(366)
	if w, _ := f.warnedAt(u.ID); w {
		t.Fatal("recorded although nothing was delivered")
	}
	f.mailer.fail = nil
	f.runAt(367)
	if w, _ := f.warnedAt(u.ID); !w || len(f.mailer.take()) != 1 {
		t.Fatal("not retried")
	}
}

func TestInactiveWarningFailureLogsNoAddress(t *testing.T) {
	f := newInactiveFixture(t)
	f.user("victim", false)
	f.enable()
	var logs bytes.Buffer
	f.w.log = slog.New(slog.NewTextHandler(&logs, nil))
	f.mailer.fail = errors.New(`mail: recipient: 550 "5.1.1 <victim@example.com>: Recipient address rejected"`)
	f.runAt(366)
	if !strings.Contains(logs.String(), "inactivity warning failed") || !strings.Contains(logs.String(), "reason=recipient") {
		t.Fatalf("failure not logged: %s", logs.String())
	}
	if strings.Contains(logs.String(), "victim@") {
		t.Fatalf("log holds the address: %s", logs.String())
	}
}

// Unverified, household-less accounts are purged only in hosted mode, where
// sign-up requires an address; self-hosted accounts never expire this way.
func TestRetentionPurgesUnverifiedOnlyWhenHosted(t *testing.T) {
	for _, hosted := range []bool{false, true} {
		f := newInactiveFixture(t)
		f.w.cfg.Hosted = hosted
		u := f.user("ann", false)
		if _, err := f.db.ExecContext(f.ctx, f.db.Dialect.Rebind("UPDATE users SET email_verified = ? WHERE id = ?"), false, u.ID); err != nil {
			t.Fatal(err)
		}
		f.w.retention(f.ctx, f.t0.Add(8*day))
		_, err := f.st.UserByLogin(f.ctx, "ann")
		if (err != nil) != hosted {
			t.Errorf("hosted=%v: ann lookup err=%v", hosted, err)
		}
	}
}
