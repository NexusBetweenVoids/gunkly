package main

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/db"
	"github.com/NexusBetweenVoids/gunkly/internal/db/dbtest"
	"github.com/NexusBetweenVoids/gunkly/internal/mail"
	"github.com/NexusBetweenVoids/gunkly/internal/notify"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
	"github.com/NexusBetweenVoids/gunkly/internal/webpush"
)

// TestResetPasswordInvalidatesLinks: `gunkly admin reset-password` must kill
// the user's mark-done links like the API paths do.
func TestResetPasswordInvalidatesLinks(t *testing.T) {
	ctx := context.Background()
	d := dbtest.Open(t)
	st := store.New(d)
	u, err := st.CreateUser(ctx, store.NewUser{Username: "ann", DisplayName: "Ann", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	actor := store.Actor{UserID: u.ID}
	h, err := st.CreateHousehold(ctx, actor, store.NewHousehold{Name: "Home", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	next := time.Now().Add(time.Hour).UTC().Truncate(time.Millisecond)
	c, err := st.CreateChore(ctx, actor, h.ID, store.ChoreInput{Name: "Gutters", Kind: store.KindRecurring, ScheduleType: store.ScheduleInterval, IntervalN: 1,
		IntervalUnit: "week", Anchor: store.AnchorFromCompletion, FirstDue: "2026-11-21", DueDate: "2026-11-21", NextDue: next})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := st.CreateMarkDone(ctx, store.MarkDoneLink{ChoreID: c.ID, OccurrenceDue: next, UserID: u.ID, ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PeekMarkDone(ctx, tok); err != nil {
		t.Fatalf("link not valid before reset: %v", err)
	}
	a := &app{db: d, store: st}
	var out bytes.Buffer
	if err := a.adminCommand(ctx, "reset-password", "ann", &out); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PeekMarkDone(ctx, tok); err == nil {
		t.Fatal("mark-done link survived the CLI password reset")
	}
}

func testApp(t *testing.T) *app {
	t.Helper()
	d := dbtest.Open(t)
	return &app{db: d, store: store.New(d), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func mkUser(t *testing.T, a *app, name string) store.User {
	t.Helper()
	u, err := a.store.CreateUser(context.Background(), store.NewUser{Username: name, DisplayName: name, PasswordHash: auth.HashPassword("old-password-" + name)})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestHealthcheck(t *testing.T) {
	ctx := context.Background()
	status := http.StatusOK
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	listen := strings.TrimPrefix(srv.URL, "http://")
	if err := healthcheck(ctx, listen); err != nil {
		t.Fatalf("healthy: %v", err)
	}
	status = http.StatusServiceUnavailable
	if err := healthcheck(ctx, listen); err == nil {
		t.Fatal("503 reported healthy")
	}
	// Nothing listening.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	if err := healthcheck(ctx, addr); err == nil {
		t.Fatal("closed port reported healthy")
	}
	if err := healthcheck(ctx, "not-an-address"); err == nil {
		t.Fatal("bad listen address accepted")
	}
}

func TestHealthURLWildcardHosts(t *testing.T) {
	for in, want := range map[string]string{
		"":          "http://127.0.0.1:4865/healthz",
		":9000":     "http://127.0.0.1:9000/healthz",
		"0.0.0.0:1": "http://127.0.0.1:1/healthz",
		"[::]:2":    "http://127.0.0.1:2/healthz",
		"[::1]:3":   "http://[::1]:3/healthz",
	} {
		if got, err := healthURL(in); err != nil || got != want {
			t.Errorf("healthURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestAdminResetPassword(t *testing.T) {
	ctx := context.Background()
	a := testApp(t)
	u := mkUser(t, a, "ann")
	if _, err := a.store.CreateSession(ctx, store.NewSession{UserID: u.ID, TokenHash: "t", Label: "x", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := a.adminCommand(ctx, "reset-password", "ann", &out); err != nil {
		t.Fatal(err)
	}
	_, pw, ok := strings.Cut(strings.SplitN(out.String(), "\n", 2)[0], ": ")
	if !ok || len(pw) < 16 {
		t.Fatalf("output lacks a password: %q", out.String())
	}
	got, err := a.store.UserByLogin(ctx, "ann")
	if err != nil {
		t.Fatal(err)
	}
	if okNew, _ := auth.VerifyPassword(got.PasswordHash, pw); !okNew {
		t.Fatal("printed password does not work")
	}
	if okOld, _ := auth.VerifyPassword(got.PasswordHash, "old-password-ann"); okOld {
		t.Fatal("old password still works")
	}
	if ss, err := a.store.ListSessions(ctx, u.ID); err != nil || len(ss) != 0 {
		t.Fatalf("sessions left: %d %v", len(ss), err)
	}
	if err := a.adminCommand(ctx, "reset-password", "nobody", &out); err == nil {
		t.Fatal("unknown user accepted")
	}
	if err := a.adminCommand(ctx, "bogus", "ann", &out); err == nil {
		t.Fatal("unknown subcommand accepted")
	}
}

func TestAdminReset2FA(t *testing.T) {
	ctx := context.Background()
	a := testApp(t)
	u := mkUser(t, a, "ann")
	if err := a.store.SetPendingTOTP(ctx, u.ID, "v1:secret"); err != nil {
		t.Fatal(err)
	}
	if err := a.store.EnableTOTP(ctx, u.ID, "", 1, []string{"hash1", "hash2"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.store.UserByLogin(ctx, "ann"); !got.TOTPEnabled {
		t.Fatal("TOTP not enabled in setup")
	}
	var out bytes.Buffer
	if err := a.adminCommand(ctx, "reset-2fa", "ann", &out); err != nil {
		t.Fatal(err)
	}
	got, _ := a.store.UserByLogin(ctx, "ann")
	if got.TOTPEnabled || got.TOTPSecret != "" {
		t.Fatalf("2FA not removed: %+v", got)
	}
	if !strings.Contains(out.String(), "ann") {
		t.Fatalf("output: %q", out.String())
	}
}

// reset-2fa must also clear email-code 2FA, or a user locked out of their
// mailbox-based factor could never be recovered by the operator.
func TestAdminReset2FAClearsEmailOTP(t *testing.T) {
	ctx := context.Background()
	a := testApp(t)
	u, err := a.store.CreateUser(ctx, store.NewUser{Username: "bob", Email: "bob@example.com", DisplayName: "bob", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.store.IssueVerifyToken(ctx, u.ID, "h", "", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.store.VerifyEmail(ctx, "h", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := a.store.EnableEmailOTP(ctx, u.ID, "", []string{"r1"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.store.UserByLogin(ctx, "bob"); !got.EmailOTPEnabled {
		t.Fatal("email OTP not enabled in setup")
	}
	var out bytes.Buffer
	if err := a.adminCommand(ctx, "reset-2fa", "bob", &out); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.store.UserByLogin(ctx, "bob"); got.EmailOTPEnabled || got.TOTPEnabled {
		t.Fatalf("2FA not removed: %+v", got)
	}
}

// reset-2fa must also remove passkeys (a second factor and a sign-in method).
func TestAdminReset2FARemovesPasskeys(t *testing.T) {
	ctx := context.Background()
	a := testApp(t)
	u, err := a.store.CreateUser(ctx, store.NewUser{Username: "cy", DisplayName: "cy", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.store.AddPasskey(ctx, u.ID, "", store.NewPasskey{Name: "Key", CredentialID: []byte("c"), PublicKey: []byte("p")}, 5, []string{"r1"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.store.UserByLogin(ctx, "cy"); got.Passkeys != 1 {
		t.Fatal("passkey not added in setup")
	}
	var out bytes.Buffer
	if err := a.adminCommand(ctx, "reset-2fa", "cy", &out); err != nil {
		t.Fatal(err)
	}
	if got, _ := a.store.UserByLogin(ctx, "cy"); got.Passkeys != 0 || got.HasSecondFactor() {
		t.Fatalf("passkeys not removed: %+v", got)
	}
}

func TestRotateKey(t *testing.T) {
	ctx := context.Background()
	a := testApp(t)
	oldRing, err := auth.NewKeyring(1, map[uint32]string{1: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	bothRing, err := auth.NewKeyring(2, map[uint32]string{1: strings.Repeat("a", 40), 2: strings.Repeat("b", 40)})
	if err != nil {
		t.Fatal(err)
	}
	newOnly, err := auth.NewKeyring(2, map[uint32]string{2: strings.Repeat("b", 40)})
	if err != nil {
		t.Fatal(err)
	}

	u := mkUser(t, a, "ann")
	totpCT := oldRing.Encrypt([]byte("JBSWY3DPEHPK3PXP"), []byte("totp:"+u.ID))
	if err := a.store.SetPendingTOTP(ctx, u.ID, totpCT); err != nil {
		t.Fatal(err)
	}
	actor := store.Actor{UserID: u.ID}
	nc, err := notify.Prepare(oldRing, "ntfy://ntfy.sh/gunkly-private-topic-1234", "phone", map[string]bool{"ntfy": true})
	if err != nil {
		t.Fatal(err)
	}
	nc.Max = 5
	ch, err := a.store.CreateUserChannel(ctx, actor, nc)
	if err != nil {
		t.Fatal(err)
	}

	// A Web Push subscription and the instance VAPID key, both on the old key.
	ua, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	subKeys := webpush.Keys{P256dh: base64.RawURLEncoding.EncodeToString(ua.PublicKey().Bytes()), Auth: base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef"))}
	sealed, err := notify.SealPush(oldRing, "sub-1", subKeys)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.store.CreatePushSubscription(ctx, actor, store.NewPushSubscription{ID: "sub-1", Endpoint: "https://fcm.googleapis.com/fcm/send/x", Ciphertext: sealed, Max: 5}); err != nil {
		t.Fatal(err)
	}
	vapidPub, err := notify.NewWebPush(a.store, oldRing, "https://gunkly.example", func(context.Context) bool { return true }, nil).PublicKey(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// A queued email (say, during an SMTP outage), sealed with the old key.
	queued := mail.Message{To: "ann@example.com", Subject: "Code", Body: "123456"}
	if err := a.store.EnqueueMail(ctx, store.OutboxMail{ID: "mail-1", Kind: "otp", UserID: u.ID, Payload: mail.Seal(oldRing, "mail-1", queued),
		ExpiresAt: time.Now().Add(time.Hour)}, time.Now()); err != nil {
		t.Fatal(err)
	}

	a.keyring = bothRing
	var out bytes.Buffer
	if err := a.rotateKey(ctx, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "totp secrets: 1 re-encrypted") || !strings.Contains(out.String(), "notification channel URLs: 1 re-encrypted") ||
		!strings.Contains(out.String(), "web push keys: 2 re-encrypted") || !strings.Contains(out.String(), "queued emails: 1 re-encrypted") {
		t.Fatalf("output: %q", out.String())
	}

	got, _ := a.store.UserByLogin(ctx, "ann")
	if !strings.HasPrefix(got.TOTPSecret, "v2:") {
		t.Fatalf("TOTP secret not on the new key: %.4s", got.TOTPSecret)
	}
	secrets, err := a.store.ChannelSecrets(ctx, "", 10)
	if err != nil || len(secrets) != 1 || !strings.HasPrefix(secrets[0].Ciphertext, "v2:") {
		t.Fatalf("channel not on the new key: %+v %v", secrets, err)
	}

	// With the old key removed everything still decrypts.
	pt, err := newOnly.Decrypt(got.TOTPSecret, []byte("totp:"+u.ID))
	if err != nil || string(pt) != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("TOTP after old key removal: %q %v", pt, err)
	}
	url, err := notify.Open(newOnly, ch.ID, secrets[0].Ciphertext)
	if err != nil || url != "ntfy://ntfy.sh/gunkly-private-topic-1234" {
		t.Fatalf("channel after old key removal: %q %v", url, err)
	}

	pushSecrets, err := a.store.PushSecrets(ctx, "", 10)
	if err != nil || len(pushSecrets) != 1 || !strings.HasPrefix(pushSecrets[0].Ciphertext, "v2:") {
		t.Fatalf("push keys not on the new key: %+v %v", pushSecrets, err)
	}
	if k, err := notify.OpenPush(newOnly, "sub-1", pushSecrets[0].Ciphertext); err != nil || k != subKeys {
		t.Fatalf("push keys after old key removal: %+v %v", k, err)
	}
	if pub, err := notify.NewWebPush(a.store, newOnly, "https://gunkly.example", func(context.Context) bool { return true }, nil).PublicKey(ctx); err != nil || pub != vapidPub {
		t.Fatalf("VAPID key after old key removal: %q (want %q) %v", pub, vapidPub, err)
	}

	outbox, err := a.store.OutboxSecrets(ctx, "", 10)
	if err != nil || len(outbox) != 1 {
		t.Fatalf("outbox: %+v %v", outbox, err)
	}
	if m, err := mail.Open(newOnly, "mail-1", outbox[0].Ciphertext); err != nil || m != queued {
		t.Fatalf("queued email after old key removal: %+v %v", m, err)
	}

	// Idempotent: a second run has nothing to do.
	out.Reset()
	if err := a.rotateKey(ctx, &out); err != nil || !strings.Contains(out.String(), "totp secrets: 0 re-encrypted") {
		t.Fatalf("second run: %q %v", out.String(), err)
	}
}

func TestBackupStreamIsValidDatabase(t *testing.T) {
	ctx := context.Background()
	a := testApp(t)
	if a.db.Dialect != db.SQLite {
		t.Skip("backup supports SQLite only")
	}
	mkUser(t, a, "ann")
	var buf bytes.Buffer
	if err := a.backup(ctx, "-", &buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("SQLite format 3\x00")) {
		t.Fatal("stream is not a SQLite database")
	}
	path := filepath.Join(t.TempDir(), "restored.db")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := db.Open(ctx, "sqlite://"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var n int
	if err := r.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE username = 'ann'").Scan(&n); err != nil || n != 1 {
		t.Fatalf("users table in backup: %d %v", n, err)
	}
}

func TestBackupRejectsPostgres(t *testing.T) {
	a := &app{db: &db.DB{Dialect: db.Postgres}}
	if err := a.backup(context.Background(), "-", io.Discard); err == nil {
		t.Fatal("postgres backup accepted")
	}
}

func TestMigrate(t *testing.T) {
	ctx := context.Background()
	url := dbtest.URL(t)
	d, err := db.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	a := &app{db: d, store: store.New(d), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := a.requireCurrent(ctx); err == nil {
		t.Fatal("empty database reported current")
	}
	if pending, _ := d.Pending(ctx); len(pending) == 0 {
		t.Fatal("no pending migrations on a fresh database")
	}
	if err := a.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.requireCurrent(ctx); err != nil {
		t.Fatalf("after migrate: %v", err)
	}
	if err := a.migrate(ctx); err != nil { // idempotent
		t.Fatal(err)
	}
	if ok, err := a.store.HasUsers(ctx); err != nil || ok {
		t.Fatalf("HasUsers: %v %v", ok, err)
	}
}

// An existing SQLite file gets a pre-migration snapshot before new
// migrations run.
func TestMigrateSnapshotsExistingSQLite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "gunkly.db")
	d, err := db.Open(ctx, "sqlite://"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	a := &app{db: d, store: store.New(d), log: slog.New(slog.NewTextHandler(io.Discard, nil)), sqlitePath: path, dbExisted: true}
	if err := a.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	snaps, _ := filepath.Glob(filepath.Join(dir, "backups", "pre-migrate-*.db"))
	if len(snaps) != 1 {
		t.Fatalf("snapshots: %v", snaps)
	}
}
