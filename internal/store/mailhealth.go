package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Email delivery health. The sender (scheduler outbox) reports every send
// attempt; the Administration page and /healthz/email read the result. The state is one
// settings row, so it needs no migration and survives restarts.
const (
	settingMailHealth = "mail_health"
	// AuditEmailFailing records the start of an email outage (a security alert).
	AuditEmailFailing = "email_failing"

	// MailFailThreshold failed sends within MailFailWindow, with none succeeding, make email "failing".
	MailFailThreshold = 3
	MailFailWindow    = time.Hour
	// A failing state with no further failure for this long (the longest retry
	// wait is 6 hours) is dropped, so a stale outage does not linger.
	mailFailStale = 8 * time.Hour
)

// Error categories stored for an outage; never text from the mail server.
const (
	MailErrConnect = "connect"
	MailErrTLS     = "tls"
	MailErrAuth    = "auth"
	MailErrTimeout = "timeout"
	MailErrRefused = "refused" // the server rejected the sender or the message
	MailErrOther   = "other"
)

type mailHealthState struct {
	Fails []int64 `json:"fails,omitempty"` // recent failed attempts (ms), at most MailFailThreshold kept
	Since int64   `json:"since,omitempty"` // when the outage was declared; 0 = none
	Last  int64   `json:"last,omitempty"`  // the most recent failure
	Err   string  `json:"err,omitempty"`   // category of the most recent failure
}

// MailFailure describes an ongoing email outage.
type MailFailure struct {
	Since     time.Time
	LastError string // one of the MailErr categories
}

func (t *tx) loadMailHealth(ctx context.Context) (mailHealthState, error) {
	var raw string
	var st mailHealthState
	err := t.queryRow(ctx, "SELECT value FROM settings WHERE key = ?", settingMailHealth).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	_ = json.Unmarshal([]byte(raw), &st) // unreadable state counts as none
	return st, nil
}

func (t *tx) saveMailHealth(ctx context.Context, st mailHealthState, now int64) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = t.exec(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, settingMailHealth, string(b), now)
	return err
}

// MailAttemptOK records a successful send: it ends any outage and forgets
// earlier failures.
func (s *Store) MailAttemptOK(ctx context.Context, now time.Time) error {
	return s.inTx(ctx, true, func(t *tx) error {
		st, err := t.loadMailHealth(ctx)
		if err != nil || (st.Since == 0 && len(st.Fails) == 0) {
			return err
		}
		return t.saveMailHealth(ctx, mailHealthState{}, now.UnixMilli())
	})
}

// MailAttemptFailed records a failed send (errKind: a MailErr category). When
// MailFailThreshold failures fall within MailFailWindow and none succeeded,
// the outage starts: one email_failing audit event is written and true is returned.
func (s *Store) MailAttemptFailed(ctx context.Context, now time.Time, errKind string) (started bool, err error) {
	err = s.inTx(ctx, true, func(t *tx) error {
		started = false
		st, err := t.loadMailHealth(ctx)
		if err != nil {
			return err
		}
		ms := now.UnixMilli()
		if st.Since != 0 && ms-st.Last > mailFailStale.Milliseconds() {
			st = mailHealthState{} // an old outage that went quiet
		}
		cut := ms - MailFailWindow.Milliseconds()
		fails := []int64{}
		for _, f := range st.Fails {
			if f > cut {
				fails = append(fails, f)
			}
		}
		fails = append(fails, ms)
		if len(fails) > MailFailThreshold {
			fails = fails[len(fails)-MailFailThreshold:]
		}
		st.Fails, st.Last, st.Err = fails, ms, errKind
		if st.Since == 0 && len(fails) >= MailFailThreshold {
			st.Since, started = ms, true
			if err := t.insertAudit(ctx, AuditEvent{Event: AuditEmailFailing, Meta: map[string]string{"scope": "", "reason": errKind}}, ms); err != nil {
				return err
			}
		}
		return t.saveMailHealth(ctx, st, ms)
	})
	return started, err
}

// MailFailing reports the ongoing email outage, if any.
func (s *Store) MailFailing(ctx context.Context, now time.Time) (*MailFailure, error) {
	var st mailHealthState
	err := s.inTx(ctx, false, func(t *tx) error {
		var err error
		st, err = t.loadMailHealth(ctx)
		return err
	})
	if err != nil || st.Since == 0 || now.UnixMilli()-st.Last > mailFailStale.Milliseconds() {
		return nil, err
	}
	return &MailFailure{Since: time.UnixMilli(st.Since).UTC(), LastError: st.Err}, nil
}
