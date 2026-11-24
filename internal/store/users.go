package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
)

// User is an account. PasswordHash and TOTP ciphertext never leave the
// server; API layers must not serialise this struct directly.
type User struct {
	ID              string
	Email           string // may be empty when email is disabled
	Username        string
	DisplayName     string
	PasswordHash    string
	Locale          string
	WeekStart       *int // nil = locale default
	IsAdmin         bool
	TOTPSecret      string // ciphertext
	TOTPEnabled     bool
	EmailVerified   bool
	EmailOTPEnabled bool // email codes as the second factor (exclusive with TOTP)
	EmailReminders  bool // reminders by email to the verified address
	QuietStartMin   *int // personal quiet hours (minutes after local midnight, household time zone); nil = none
	QuietEndMin     *int
	TimeZone        string // IANA name the personal quiet hours are read in; "" = the household's
	Passkeys        int    // registered passkeys (each is a sign-in method and a second factor)
	SSOLinked       bool   // linked to an identity at the single sign-on provider
	CreatedAt       time.Time
	LastActiveAt    time.Time
}

// NewUser is the input to CreateUser.
type NewUser struct {
	Email        string
	Username     string
	DisplayName  string
	PasswordHash string
	Locale       string
	IsAdmin      bool
}

// HasSecondFactor reports whether signing in with a password also takes a
// second factor: an authenticator app, emailed codes or a passkey.
func (u User) HasSecondFactor() bool { return u.TOTPEnabled || u.EmailOTPEnabled || u.Passkeys > 0 }

// normLogin lowercases emails and usernames for lookup and uniqueness.
func normLogin(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// CreateUser inserts a user. ErrConflict if the email or username is taken
// (case-insensitively); ErrInvalid if required fields are missing.
func (s *Store) CreateUser(ctx context.Context, in NewUser) (User, error) {
	var u User
	err := s.inTx(ctx, true, func(t *tx) error {
		var err error
		u, err = t.insertUser(ctx, in, s.nowMS())
		return err
	})
	return u, err
}

func (t *tx) insertUser(ctx context.Context, in NewUser, now int64) (User, error) {
	email, username := strings.TrimSpace(in.Email), strings.TrimSpace(in.Username)
	if username == "" || strings.TrimSpace(in.DisplayName) == "" || in.PasswordHash == "" ||
		strings.Contains(username, "@") {
		return User{}, ErrInvalid
	}
	u := User{
		ID: ids.New(), Email: email, Username: username, DisplayName: strings.TrimSpace(in.DisplayName),
		PasswordHash: in.PasswordHash, Locale: in.Locale, IsAdmin: in.IsAdmin,
		CreatedAt: time.UnixMilli(now).UTC(), LastActiveAt: time.UnixMilli(now).UTC(),
	}
	_, err := t.exec(ctx, `INSERT INTO users (id, email, email_lower, username, username_lower, display_name,
		password_hash, locale, is_admin, password_changed_at, created_at, last_active_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, nullStr(email), nullStr(normLogin(email)), username, normLogin(username), u.DisplayName,
		u.PasswordHash, u.Locale, u.IsAdmin, now, now, now)
	if err != nil {
		return User{}, conflict(err)
	}
	return u, nil
}

const userCols = `id, COALESCE(email, ''), username, display_name, password_hash, locale, week_start, is_admin,
	COALESCE(totp_secret_ciphertext, ''), totp_enabled, email_verified, email_otp_enabled, email_reminders, quiet_start_min, quiet_end_min, COALESCE(time_zone, ''), created_at, last_active_at,
	(SELECT count(*) FROM passkeys WHERE user_id = users.id), (SELECT count(*) > 0 FROM sso_identities WHERE user_id = users.id)`

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	var ws, qs, qe sql.NullInt64
	var created, active int64
	err := row.Scan(&u.ID, &u.Email, &u.Username, &u.DisplayName, &u.PasswordHash, &u.Locale, &ws, &u.IsAdmin,
		&u.TOTPSecret, &u.TOTPEnabled, &u.EmailVerified, &u.EmailOTPEnabled, &u.EmailReminders, &qs, &qe, &u.TimeZone, &created, &active, &u.Passkeys, &u.SSOLinked)
	if err != nil {
		return User{}, notFound(err)
	}
	if qs.Valid && qe.Valid {
		a, b := int(qs.Int64), int(qe.Int64)
		u.QuietStartMin, u.QuietEndMin = &a, &b
	}
	if ws.Valid {
		w := int(ws.Int64)
		u.WeekStart = &w
	}
	u.CreatedAt, u.LastActiveAt = time.UnixMilli(created).UTC(), time.UnixMilli(active).UTC()
	return u, nil
}

// UserByLogin finds a user by email or username, case-insensitively.
// Usernames cannot contain '@', so the two namespaces never collide.
func (s *Store) UserByLogin(ctx context.Context, login string) (User, error) {
	l := normLogin(login)
	col := "username_lower"
	if strings.Contains(l, "@") {
		col = "email_lower"
	}
	return scanUser(s.db.QueryRowContext(ctx, s.d.Rebind("SELECT "+userCols+" FROM users WHERE "+col+" = ?"), l))
}

// UserByID returns a user by ID.
func (s *Store) UserByID(ctx context.Context, id string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, s.d.Rebind("SELECT "+userCols+" FROM users WHERE id = ?"), id))
}
