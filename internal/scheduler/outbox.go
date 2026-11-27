package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/mail"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// The email outbox (ADR 0008): transactional mail is queued by the API and
// sent here, so a down SMTP server delays an email instead of losing it.
const (
	// MailEvery is how often the leader looks for due mail between ticks, so a
	// sign-in code does not wait for the next reminder tick.
	MailEvery = 2 * time.Second
	// MaxMailAttempts is how many sends are tried before giving up.
	MaxMailAttempts = 6
	mailClaimLease  = 2 * time.Minute
	mailBatch       = 50
)

// mailBackoff is the wait after the first, second, ... failed attempt (the
// last one is before the sixth and final attempt).
var mailBackoff = [...]time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}

// Outbox sends the queued emails. Worker runs one on the leader; tests and
// tools may drive it directly with Process.
type Outbox struct {
	Store   *store.Store
	Mailer  Mailer
	Sealer  mail.Sealer
	Now     func() time.Time // nil = time.Now
	Logger  *slog.Logger     // nil = slog.Default()
	Timeout time.Duration    // per send; 0 = sendTimeout
}

// Process sends every email that is due and returns how many went out. A
// failure schedules the next attempt (1 minute, 5 minutes, 30 minutes, 2 hours,
// 6 hours); after MaxMailAttempts, or once the next attempt would fall after the
// message's expiry, it is given up. Neither the address nor the text is ever
// logged or audited; the SMTP error is logged only by its kind.
func (o *Outbox) Process(ctx context.Context) int {
	now, log := time.Now, o.Logger
	if o.Now != nil {
		now = o.Now
	}
	if log == nil {
		log = slog.Default()
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = sendTimeout
	}
	sent := 0
	for round := 0; round < maxClaimRounds && ctx.Err() == nil; round++ {
		at := now().UTC().Truncate(time.Millisecond)
		claimed, expired, err := o.Store.ClaimMail(ctx, at, mailClaimLease, mailBatch)
		if err != nil {
			if ctx.Err() == nil {
				log.Error("mail outbox: claim", "err", err)
			}
			return sent
		}
		for _, m := range expired {
			log.Warn("mail outbox: gave up", "kind", m.Kind, "reason", store.MailGaveUpExpired, "attempts", m.Attempts)
		}
		for _, m := range claimed {
			if o.send(ctx, m, timeout, now, log) {
				sent++
			}
		}
		if len(claimed) < mailBatch {
			break
		}
	}
	return sent
}

func (o *Outbox) send(ctx context.Context, m store.OutboxMail, timeout time.Duration, now func() time.Time, log *slog.Logger) bool {
	giveUp := func(reason string) {
		log.Warn("mail outbox: gave up", "kind", m.Kind, "reason", reason, "attempts", m.Attempts)
		if err := o.Store.GiveUpMail(ctx, m, reason, now()); err != nil {
			log.Error("mail outbox: give up", "kind", m.Kind, "err", err)
		}
	}
	msg, err := mail.Open(o.Sealer, m.ID, m.Payload)
	if err != nil {
		giveUp(store.MailGaveUpUnreadable)
		return false
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	err = o.Mailer.Send(sctx, msg)
	cancel()
	if err == nil {
		if err := o.Store.MailAttemptOK(ctx, now()); err != nil {
			log.Error("mail outbox: record success", "err", err)
		}
		if err := o.Store.MailSent(ctx, m.ID); err != nil {
			log.Error("mail outbox: remove sent mail", "kind", m.Kind, "err", err)
		}
		return true
	}
	m.Attempts++
	// The SMTP error can carry the address: log that it failed, not why.
	log.Warn("mail outbox: send failed", "kind", m.Kind, "attempt", m.Attempts)
	if kind := mailErrKind(err); kind != "" {
		if started, rerr := o.Store.MailAttemptFailed(ctx, now(), kind); rerr != nil {
			log.Error("mail outbox: record failure", "err", rerr)
		} else if started {
			log.Error("mail outbox: email delivery is failing", "reason", kind)
		}
	}
	if m.Attempts >= MaxMailAttempts {
		giveUp(store.MailGaveUpAttempts)
		return false
	}
	next := now().Add(mailBackoff[m.Attempts-1])
	if !next.Before(m.ExpiresAt) {
		giveUp(store.MailGaveUpExpired)
		return false
	}
	if err := o.Store.RetryMail(ctx, m.ID, m.Attempts, next); err != nil {
		log.Error("mail outbox: schedule retry", "kind", m.Kind, "err", err)
	}
	return false
}

// mailErrKind sorts a send error into a store.MailErr category from the stage
// prefix mail.Sender puts on its errors; the text itself (it can hold an address
// or server output) is dropped. "" means the failure is the recipient's or the
// message's, not the provider's, so it does not count towards an outage.
func mailErrKind(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return store.MailErrTimeout
	}
	msg := err.Error()
	has := func(stage string) bool { return strings.HasPrefix(msg, "mail: "+stage) }
	switch {
	case has("invalid"), has("address"), has("recipient"), has("unexpected"), strings.Contains(msg, "line break"):
		return ""
	case has("connect"), has("greeting"), has("hello"):
		return store.MailErrConnect
	case has("tls"), has("starttls"), has("server does not offer"):
		return store.MailErrTLS
	case has("auth"):
		return store.MailErrAuth
	case has("sender"), has("data"):
		return store.MailErrRefused
	}
	return store.MailErrOther
}
