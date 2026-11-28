package scheduler

import (
	"context"
	"errors"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/i18n"
	"github.com/NexusBetweenVoids/gunkly/internal/mail"
	"github.com/NexusBetweenVoids/gunkly/internal/notify"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// Mailer sends one email (api.Mailer, *mail.Sender); nil disables email.
type Mailer interface {
	Send(ctx context.Context, m mail.Message) error
}

// Inactive-account job parameters.
const (
	inactiveEvery   = 24 * time.Hour
	inactivePage    = 200
	inactiveBudget  = 500 // warnings per run; the rest wait for the next day
	finalWarnLead   = store.MinInactiveGapDays * 24 * time.Hour
	minWarnGap      = 12 * time.Hour // first and final warning are never sent in one run
	warnSendTimeout = 30 * time.Second
)

// inactive runs the inactive-account lifecycle once (PRD "Inactive accounts"):
// warn accounts idle longer than the warn period, send a final warning about
// 30 days before the delete period ends and delete accounts that stayed away
// since. Off unless the admin enabled it. Idempotent: the warnings are
// recorded per user and cleared by signing in, so a restart or a second
// instance repeats nothing; only the leader runs it (it is called from tick).
// Instance admins are never touched.
func (w *Worker) inactive(ctx context.Context, now time.Time) {
	st, err := w.cfg.Store.InstanceSettings(ctx)
	if err != nil {
		w.log.Error("scheduler: inactive settings", "err", err)
		return
	}
	if !st.InactiveEnabled {
		return
	}
	const day = 24 * time.Hour
	j := inactiveJob{w: w, now: now, delAfter: time.Duration(st.InactiveDeleteAfterDays) * day, budget: inactiveBudget}
	if j.enabled, err = w.enabledKinds(ctx); err != nil {
		w.log.Error("scheduler: inactive services", "err", err)
		return
	}
	cutoff := now.Add(-time.Duration(st.InactiveWarnAfterDays) * day)
	after := ""
	var warned, deleted int
	for ctx.Err() == nil {
		page, err := w.cfg.Store.InactiveUsers(ctx, cutoff, after, inactivePage)
		if err != nil {
			w.log.Error("scheduler: inactive users", "err", err)
			return
		}
		for _, u := range page {
			after = u.ID
			switch j.handle(ctx, u) {
			case actionWarned:
				warned++
			case actionDeleted:
				deleted++
			}
		}
		if len(page) < inactivePage {
			break
		}
	}
	if warned+deleted > 0 {
		w.log.Info("scheduler: inactive accounts", "warned", warned, "deleted", deleted)
	}
}

// enabledKinds is the admin's notification service allowlist.
func (w *Worker) enabledKinds(ctx context.Context) (map[string]bool, error) {
	if w.cfg.EnabledKinds != nil {
		return w.cfg.EnabledKinds(ctx)
	}
	return notify.DefaultKinds(), nil
}

type inactiveJob struct {
	w        *Worker
	now      time.Time
	delAfter time.Duration
	enabled  map[string]bool
	budget   int
}

type inactiveAction int

const (
	actionNone inactiveAction = iota
	actionWarned
	actionDeleted
)

func (j *inactiveJob) handle(ctx context.Context, u store.InactiveUser) inactiveAction {
	w := j.w
	log := w.log.With("user", u.ID)
	if u.IsAdmin {
		log.Info("scheduler: inactive instance admin skipped")
		return actionNone
	}
	deleteAt := u.LastActiveAt.Add(j.delAfter)
	switch {
	case u.WarnedAt.IsZero():
		return j.warn(ctx, u, false, deleteAt)
	case u.FinalWarnedAt.IsZero():
		if !j.now.Before(deleteAt.Add(-finalWarnLead)) && j.now.Sub(u.WarnedAt) >= minWarnGap {
			return j.warn(ctx, u, true, deleteAt)
		}
	case !j.now.Before(deleteAt) && !j.now.Before(u.FinalWarnedAt.Add(finalWarnLead)):
		// Ownership passes to the longest-standing non-guest member (never a
		// guest); a household without one is deleted with the account.
		_, err := w.cfg.Store.DeleteAccount(ctx, u.ID, store.AccountDeletion{AutoTransfer: true, IdleBefore: u.WarnedAt, RefuseAdmin: true})
		switch {
		case err == nil:
			log.Info("scheduler: inactive account deleted")
			return actionDeleted
		case errors.Is(err, store.ErrConflict):
			log.Info("scheduler: account became active, not deleted")
		default:
			log.Error("scheduler: delete inactive account", "err", err)
		}
	}
	return actionNone
}

// warn sends the first or final warning through every way to reach the user
// and records it. A user who cannot be reached at all is recorded as warned
// too (deletion would otherwise never happen); if every attempt failed the
// warning is retried in the next run.
func (j *inactiveJob) warn(ctx context.Context, u store.InactiveUser, final bool, deleteAt time.Time) inactiveAction {
	if j.budget <= 0 {
		return actionNone
	}
	w := j.w
	log := w.log.With("user", u.ID)
	date := deleteAt
	if min := j.now.Add(finalWarnLead); date.Before(min) {
		date = min // always at least 30 days of notice
	}
	kind := "warn"
	if final {
		kind = "final"
	}
	p := map[string]string{"name": u.DisplayName, "date": date.UTC().Format("2006-01-02"), "url": w.cfg.BaseURL}
	var attempted, delivered int
	try := func(err error, what string) {
		attempted++
		if err != nil {
			if what == "email" { // the SMTP error can carry the address
				log.Warn("scheduler: inactivity warning failed", "via", what, "reason", MailErrReason(err))
			} else {
				log.Warn("scheduler: inactivity warning failed", "via", what, "err", err)
			}
			return
		}
		delivered++
	}
	if mailerOn(w.cfg.Mailer) && u.Email != "" && u.EmailVerified {
		sctx, cancel := context.WithTimeout(ctx, warnSendTimeout)
		try(w.cfg.Mailer.Send(sctx, mail.Message{To: u.Email,
			Subject: i18n.T(u.Locale, "mail.inactive_"+kind+".subject", p), Body: i18n.T(u.Locale, "mail.inactive_"+kind+".body", p)}), "email")
		cancel()
	}
	chs, pushes, err := w.cfg.Store.WarningTargets(ctx, u.ID)
	if err != nil {
		log.Error("scheduler: warning targets", "err", err)
		return actionNone
	}
	msg := notify.Message{Title: i18n.T(u.Locale, "notify.inactive_title", p), Body: i18n.T(u.Locale, "notify.inactive_"+kind, p)}
	for _, c := range chs {
		if !j.enabled[c.Service] {
			continue
		}
		rawURL, err := notify.Open(w.cfg.Keyring, c.ID, c.Ciphertext)
		if err != nil {
			log.Error("scheduler: cannot decrypt channel", "channel", c.ID, "err", err)
			continue
		}
		sctx, cancel := context.WithTimeout(ctx, warnSendTimeout)
		err = w.cfg.Sender.Send(sctx, rawURL, msg)
		cancel()
		if err != nil {
			err = errors.New(notify.SafeError(err, rawURL))
		}
		try(err, c.Service)
	}
	if w.cfg.Pusher != nil {
		for _, ps := range pushes {
			sctx, cancel := context.WithTimeout(ctx, warnSendTimeout)
			err := w.cfg.Pusher.Push(sctx, ps.ID, ps.Endpoint, ps.Ciphertext, notify.PushMessage{Title: msg.Title, Body: msg.Body, URL: "/"})
			cancel()
			if errors.Is(err, notify.ErrPushGone) {
				_ = w.cfg.Store.DeleteGonePushSubscription(ctx, ps.ID)
				continue
			}
			try(err, notify.KindWebPush)
		}
	}
	if attempted > 0 && delivered == 0 {
		return actionNone
	}
	if err := w.cfg.Store.MarkInactiveWarned(ctx, u.ID, final, j.now); err != nil {
		if !errors.Is(err, store.ErrConflict) { // a sign-in in between is fine
			log.Error("scheduler: record inactivity warning", "err", err)
		}
		return actionNone
	}
	j.budget--
	if err := w.cfg.Store.Audit(ctx, store.AuditEvent{ActorUserID: u.ID, Event: store.AuditInactiveWarn, Meta: map[string]string{"kind": kind}}); err != nil {
		log.Error("scheduler: audit", "err", err)
	}
	return actionWarned
}

// mailerOn reports whether m can send: not nil, and on (a mailer that follows
// the admin configuration says whether email is set up right now).
func mailerOn(m Mailer) bool {
	if m == nil {
		return false
	}
	if e, ok := m.(interface{ Enabled() bool }); ok {
		return e.Enabled()
	}
	return true
}
