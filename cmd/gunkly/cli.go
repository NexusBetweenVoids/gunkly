package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/api"
	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/db"
	"github.com/NexusBetweenVoids/gunkly/internal/notify"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
	"github.com/NexusBetweenVoids/gunkly/internal/webhook"
)

func splitHostPort(addr string) (string, string, error) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return "", "", fmt.Errorf("listen address %q: %w", addr, err)
	}
	return h, p, nil
}

// backup writes a consistent SQLite snapshot: "-" streams to stdout, a path
// is written as given (must not exist), and no argument writes
// <data dir>/backups/gunkly-<timestamp>.db.
func (a *app) backup(ctx context.Context, dst string, stdout io.Writer) error {
	if a.db.Dialect != db.SQLite {
		return errors.New("backup supports SQLite only; use pg_dump for Postgres")
	}
	if dst == "-" {
		return a.db.Backup(ctx, stdout)
	}
	if dst == "" {
		dir := filepath.Join(filepath.Dir(a.sqlitePath), "backups")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		dst = filepath.Join(dir, "gunkly-"+time.Now().UTC().Format("20060102T150405Z")+".db")
	}
	if err := a.db.BackupFile(ctx, dst); err != nil {
		return err
	}
	_, err := fmt.Fprintln(stdout, dst)
	return err
}

// adminCommand implements `gunkly admin reset-password|reset-2fa <user>`.
func (a *app) adminCommand(ctx context.Context, sub, login string, stdout io.Writer) error {
	if err := a.requireCurrent(ctx); err != nil {
		return err
	}
	u, err := a.store.UserByLogin(ctx, login)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("no user %q", login)
	} else if err != nil {
		return err
	}
	via := map[string]string{"user": u.ID, "via": "cli"}
	switch sub {
	case "reset-password":
		pw := api.NewRandomPassword()
		if err := a.store.SetPassword(ctx, u.ID, auth.HashPassword(pw), ""); err != nil {
			return err
		}
		// Recorded against the account so the owner sees it in their own audit log.
		if err := a.store.Audit(ctx, store.AuditEvent{ActorUserID: u.ID, Event: store.AuditPasswordReset, Meta: via}); err != nil {
			return err
		}
		// Printed once to the operator's terminal; never logged.
		_, err = fmt.Fprintf(stdout, "New password for %s: %s\nAll of their sessions were signed out.\n", u.Username, pw)
		return err
	case "reset-2fa":
		if err := a.store.DisableTwoFactor(ctx, u.ID, ""); err != nil {
			return err
		}
		if err := a.store.Audit(ctx, store.AuditEvent{Event: store.AuditTOTPDisable, Meta: via}); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "Two-factor authentication (authenticator app, email codes, recovery codes) removed for %s.\n", u.Username)
		return err
	}
	return fmt.Errorf("unknown admin command %q", sub)
}

// rotator re-encrypts one kind of stored ciphertext with the current key.
type rotator struct {
	name string
	run  func(ctx context.Context, s *store.Store, k *auth.Keyring) (int, error)
}

// rotators lists every encrypted column `gunkly rotate-key` must rewrite.
// Add an entry for each new ciphertext column (notification URLs, VAPID
// keys, ...); each must use the same associated data as its writer.
var rotators = []rotator{
	{name: "totp secrets", run: func(ctx context.Context, s *store.Store, k *auth.Keyring) (int, error) {
		return s.RotateTOTPSecrets(ctx, func(userID, ct string) (string, error) {
			return reencrypt(k, ct, []byte("totp:"+userID))
		})
	}},
	{name: "notification channel URLs", run: notify.RotateSecrets},
	{name: "web push keys", run: notify.RotatePushSecrets},
	{name: "webhook URLs and signing secrets", run: webhook.RotateSecrets},
}

// reencrypt returns ct unchanged if it already uses the current key.
func reencrypt(k *auth.Keyring, ct string, ad []byte) (string, error) {
	if !k.NeedsRotation(ct) {
		return ct, nil
	}
	pt, err := k.Decrypt(ct, ad)
	if err != nil {
		return "", err
	}
	return k.Encrypt(pt, ad), nil
}

func (a *app) rotateKey(ctx context.Context, stdout io.Writer) error {
	if err := a.requireCurrent(ctx); err != nil {
		return err
	}
	for _, r := range rotators {
		n, err := r.run(ctx, a.store, a.keyring)
		if err != nil {
			return fmt.Errorf("rotate %s: %w", r.name, err)
		}
		if _, err := fmt.Fprintf(stdout, "%s: %d re-encrypted\n", r.name, n); err != nil {
			return err
		}
	}
	return nil
}
