// Package mail sends Gunkly's plain-text transactional emails over SMTP
// (verification, password reset, email codes; ADR 0008). Connections go
// through the egress guard's dialer while the guard is on, so a hosted
// instance cannot be pointed at internal addresses; a self-hoster who turns
// the guard off may use a relay on their LAN.
package mail

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/NexusBetweenVoids/gunkly/internal/egress"
)

// Config is the SMTP connection (config.SMTP).
type Config struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string // "Name <addr>" or a bare address
	TLS      string // "starttls", "tls" (implicit TLS) or "none"
}

// Message is one email. Subject and Body are already translated.
type Message struct {
	To      string
	Subject string
	Body    string
}

// SendTimeout bounds one whole SMTP conversation.
const SendTimeout = 30 * time.Second

// Sender delivers Messages through one SMTP server.
type Sender struct {
	cfg     Config
	guarded func(context.Context) bool
	now     func() time.Time
	tlsConf *tls.Config // tests only; nil uses the system roots
}

// New returns a Sender. guarded reports, per send, whether to dial through
// the egress guard (nil means always). It must fail closed.
func New(cfg Config, guarded func(context.Context) bool) *Sender {
	if guarded == nil {
		guarded = func(context.Context) bool { return true }
	}
	return &Sender{cfg: cfg, guarded: guarded, now: time.Now}
}

// Send delivers m. It validates every header value first (no CR or LF
// anywhere, so a user-controlled name or address cannot inject headers).
func (s *Sender) Send(ctx context.Context, m Message) error {
	from, err := mail.ParseAddress(s.cfg.From)
	if err != nil {
		return fmt.Errorf("mail: invalid from address: %w", err)
	}
	to, err := mail.ParseAddress(m.To)
	if err != nil {
		return fmt.Errorf("mail: invalid recipient: %w", err)
	}
	to = &mail.Address{Address: to.Address} // never echo a display name taken from user input
	if !plainAddress(from.Address) || !plainAddress(to.Address) {
		return errors.New("mail: address must be a plain local@domain")
	}
	if err := noCRLF("subject", m.Subject); err != nil { // a line break here is a bug or an attack: refuse
		return err
	}
	msg, err := build(from, to, cleanLine(m.Subject), cleanBody(m.Body), s.now())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, SendTimeout)
	defer cancel()
	return s.deliver(ctx, from.Address, to.Address, msg)
}

// addrRE is a deliberately strict ASCII address: no spaces, quotes, angle
// brackets, comments or control characters can reach a header or SMTP command.
var addrRE = regexp.MustCompile(`^[A-Za-z0-9.!#$%&'*+/=?^_{|}~-]+@[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)+$`)

func plainAddress(a string) bool { return len(a) <= 254 && addrRE.MatchString(a) }

// unsafeRune reports characters that must never appear in mail text: control
// characters (except newline and tab in bodies) and Unicode direction
// overrides, which can make text read differently from what it is.
func unsafeRune(r rune, keepNewlines bool) bool {
	switch {
	case keepNewlines && (r == '\n' || r == '\t'):
		return false
	case unicode.IsControl(r):
		return true
	case r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069, r == 0x200E, r == 0x200F:
		return true
	}
	return false
}

// cleanLine makes a single-line header value safe (unsafe runes become spaces).
func cleanLine(v string) string {
	return strings.Map(func(r rune) rune {
		if unsafeRune(r, false) {
			return ' '
		}
		return r
	}, v)
}

// cleanBody normalises line ends to \n and drops every other unsafe rune.
func cleanBody(v string) string {
	v = strings.ReplaceAll(strings.ReplaceAll(v, "\r\n", "\n"), "\r", "\n")
	return strings.Map(func(r rune) rune {
		if unsafeRune(r, true) {
			return -1
		}
		return r
	}, v)
}

// noCRLF rejects values that could break out of a header or an SMTP command.
func noCRLF(what, v string) error {
	if strings.ContainsAny(v, "\r\n\x00") {
		return fmt.Errorf("mail: %s contains a line break", what)
	}
	return nil
}

// build renders the RFC 5322 message: encoded Subject, Message-ID, Date and a
// quoted-printable UTF-8 text body with CRLF line ends.
func build(from, to *mail.Address, subject, body string, now time.Time) ([]byte, error) {
	for what, v := range map[string]string{"from": from.String(), "to": to.String(), "subject": subject, "address": from.Address + to.Address} {
		if err := noCRLF(what, v); err != nil {
			return nil, err
		}
	}
	var id [16]byte
	rand.Read(id[:])
	domain := "localhost"
	if _, d, ok := strings.Cut(from.Address, "@"); ok && d != "" {
		domain = d
	}
	var b strings.Builder
	h := func(k, v string) { b.WriteString(k + ": " + v + "\r\n") }
	h("From", from.String())
	h("To", to.String())
	h("Subject", mime.QEncoding.Encode("utf-8", subject))
	h("Date", now.UTC().Format(time.RFC1123Z))
	h("Message-ID", "<"+hex.EncodeToString(id[:])+"@"+domain+">")
	h("MIME-Version", "1.0")
	h("Content-Type", "text/plain; charset=utf-8")
	h("Content-Transfer-Encoding", "quoted-printable")
	h("Auto-Submitted", "auto-generated")
	b.WriteString("\r\n")
	qp := quotedprintable.NewWriter(&b)
	if _, err := io.WriteString(qp, strings.ReplaceAll(body, "\n", "\r\n")); err != nil {
		return nil, err
	}
	if err := qp.Close(); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

func (s *Sender) dial(ctx context.Context, addr string) (net.Conn, error) {
	return egress.Guard{Enabled: s.guarded(ctx)}.DialContext(ctx, "tcp", addr)
}

func (s *Sender) deliver(ctx context.Context, from, to string, msg []byte) error {
	conn, err := s.dial(ctx, net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port)))
	if err != nil {
		return fmt.Errorf("mail: connect: %w", err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	tconf := s.tlsConf
	if tconf == nil {
		tconf = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	tconf = tconf.Clone()
	tconf.ServerName = s.cfg.Host
	if s.cfg.TLS == "tls" {
		tc := tls.Client(conn, tconf)
		if err := tc.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("mail: tls: %w", err)
		}
		conn = tc
	}
	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("mail: greeting: %w", err)
	}
	defer c.Close()
	if err := c.Hello("localhost"); err != nil {
		return fmt.Errorf("mail: hello: %w", err)
	}
	if s.cfg.TLS == "starttls" {
		if ok, _ := c.Extension("STARTTLS"); !ok {
			return errors.New("mail: server does not offer STARTTLS")
		}
		if err := c.StartTLS(tconf); err != nil {
			return fmt.Errorf("mail: starttls: %w", err)
		}
	}
	if s.cfg.Username != "" {
		if err := c.Auth(plainAuth{s.cfg.Username, s.cfg.Password}); err != nil {
			return fmt.Errorf("mail: auth: %w", err)
		}
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("mail: sender: %w", err)
	}
	if err := c.Rcpt(to); err != nil {
		return fmt.Errorf("mail: recipient: %w", err)
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("mail: data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("mail: data: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mail: data: %w", err)
	}
	return c.Quit()
}

// plainAuth is AUTH PLAIN without net/smtp's refusal to send credentials over
// a connection it does not consider encrypted: the operator chose TLS=none
// (never allowed in hosted mode) for a trusted relay.
type plainAuth struct{ user, pass string }

func (a plainAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.user + "\x00" + a.pass), nil
}

func (plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("mail: unexpected server challenge")
	}
	return nil, nil
}
