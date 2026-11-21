package notify

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

// Message is a rendered plain-text notification.
type Message struct {
	Title string
	Body  string
}

// Sender delivers a message to one channel URL. Implementations must send
// every request through the egress guard and must not log the URL.
type Sender interface {
	Send(ctx context.Context, rawURL string, m Message) error
}

// maxErrLen bounds the error text stored in the delivery log.
const maxErrLen = 300

// SafeError renders a send error for the delivery log: every part of rawURL
// that could be secret (the whole URL, userinfo, host, path segments, query
// values) is replaced, and the text is truncated. Never store err.Error()
// directly.
func SafeError(err error, rawURL string) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	secrets := []string{rawURL}
	if u, perr := url.Parse(rawURL); perr == nil {
		secrets = append(secrets, u.User.String(), u.User.Username(), u.Host, u.Hostname(), u.RawQuery, u.Path, u.EscapedPath())
		if p, ok := u.User.Password(); ok {
			secrets = append(secrets, p)
		}
		secrets = append(secrets, strings.Split(u.Path, "/")...)
		for _, vs := range u.Query() {
			secrets = append(secrets, vs...)
		}
	}
	for _, v := range secrets {
		if len(v) >= 4 { // short fragments ("443", "/") are not secrets and would mangle the text
			s = strings.ReplaceAll(s, v, "[redacted]")
		}
	}
	if len(s) > maxErrLen {
		s = s[:maxErrLen]
	}
	return strings.ToValidUTF8(s, "")
}

// ErrNoSender is returned when no transport is configured.
var ErrNoSender = errors.New("notify: no sender configured")

// Unconfigured is a Sender that delivers nothing: every send fails with
// ErrNoSender (recorded once in the delivery log, never retried).
type Unconfigured struct{}

// Send implements Sender.
func (Unconfigured) Send(context.Context, string, Message) error { return ErrNoSender }
