package webhook

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/egress"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// maxDrain bounds how much of a response body is read (and thrown away).
const maxDrain = 4 << 10

// Delivery is one send: the finished JSON body and its identifiers.
type Delivery struct {
	ID        string // Gunkly-Delivery: the delivery row, the same on every retry
	EventType string
	Body      []byte
}

// Sender posts signed deliveries through the egress guard. Its guard follows
// an instance setting, asked on every send, so an admin change applies at once.
// Redirects are never followed and the response body is ignored.
type Sender struct {
	guarded, open *http.Client
	guard         func(context.Context) bool
	version       string
	now           func() time.Time
}

// NewSender returns a Sender. guard must return true when it cannot decide
// (fail closed). version is the release, for the User-Agent. now may be nil.
func NewSender(guard func(context.Context) bool, version string, now func() time.Time) *Sender {
	if now == nil {
		now = time.Now
	}
	return &Sender{guarded: egress.NewNoRedirectClient(egress.Guard{Enabled: true}), open: egress.NewNoRedirectClient(egress.Guard{Enabled: false}),
		guard: guard, version: version, now: now}
}

// Guarded reports whether the guard is on now.
func (s *Sender) Guarded(ctx context.Context) bool { return s.guard(ctx) }

// Send delivers d to rawURL signed with secret. The result's Error is a coarse,
// fixed category (never the URL, an address or the response): it is shown to
// owners and logged. retry says whether trying again can help.
func (s *Sender) Send(ctx context.Context, rawURL, secret string, d Delivery) (res store.WebhookResult, retry bool) {
	guarded := s.guard(ctx)
	if _, err := ValidateURL(rawURL, guarded); err != nil {
		return store.WebhookResult{Error: "invalid or not allowed URL"}, false
	}
	client := s.open
	if guarded {
		client = s.guarded
	}
	ctx, cancel := context.WithTimeout(ctx, egress.RequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(d.Body))
	if err != nil {
		return store.WebhookResult{Error: "invalid or not allowed URL"}, false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Gunkly-Webhook/"+s.version)
	req.Header.Set("Gunkly-Event", d.EventType)
	req.Header.Set("Gunkly-Delivery", d.ID)
	req.Header.Set("Gunkly-Signature", Sign(secret, s.now().Unix(), d.Body))
	start := time.Now()
	resp, err := client.Do(req) //nolint:gosec // G704: the target is user-chosen by design; every connection goes through the egress guard
	res.DurationMS = int(time.Since(start).Milliseconds())
	if err != nil {
		res.Error = classify(err)
		return res, !errors.Is(err, egress.ErrBlocked)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrain)) // ignored; read so the connection can be reused
	res.HTTPStatus = resp.StatusCode
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		res.OK = true
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		res.Error = "HTTP " + strconv.Itoa(resp.StatusCode) + " (redirects are not followed)"
	default:
		res.Error = "HTTP " + strconv.Itoa(resp.StatusCode)
		retry = resp.StatusCode >= 500 || resp.StatusCode == http.StatusRequestTimeout ||
			resp.StatusCode == http.StatusTooEarly || resp.StatusCode == http.StatusTooManyRequests
	}
	return res, retry
}

// classify names a transport error without quoting it (it could carry the
// URL or the resolved address).
func classify(err error) string {
	var ne net.Error
	switch {
	case errors.Is(err, egress.ErrBlocked):
		return "destination address not allowed"
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		return "timed out"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return "host not found"
	}
	var op *net.OpError
	if errors.As(err, &op) {
		return "connection failed"
	}
	return "request failed"
}
