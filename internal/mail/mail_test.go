package mail

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/textproto"
	"strings"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/egress"
)

type received struct {
	from, to, data, auth string
}

// fakeSMTP is an in-process SMTP server on 127.0.0.1 (never a real host).
type fakeSMTP struct {
	ln       net.Listener
	got      chan received
	tlsConf  *tls.Config // implicit TLS when implicit, STARTTLS offer otherwise (nil: none)
	implicit bool
}

func startSMTP(t *testing.T, tlsConf *tls.Config, implicit bool) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln, got: make(chan received, 4), tlsConf: tlsConf, implicit: implicit}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c)
		}
	}()
	return f
}

func (f *fakeSMTP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeSMTP) serve(c net.Conn) {
	defer c.Close()
	if f.implicit {
		c = tls.Server(c, f.tlsConf)
	}
	tp := textproto.NewConn(c)
	_ = tp.PrintfLine("220 fake ESMTP")
	var r received
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		cmd := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(cmd, "EHLO"):
			_, _ = fmt.Fprint(c, "250-fake\r\n")
			if f.tlsConf != nil && !f.implicit {
				_, _ = fmt.Fprint(c, "250-STARTTLS\r\n")
			}
			_, _ = fmt.Fprint(c, "250 AUTH PLAIN\r\n")
		case cmd == "STARTTLS":
			_ = tp.PrintfLine("220 go ahead")
			c = tls.Server(c, f.tlsConf)
			tp = textproto.NewConn(c)
		case strings.HasPrefix(cmd, "AUTH PLAIN"):
			b, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(line[len("AUTH PLAIN"):]))
			r.auth = strings.ReplaceAll(string(b), "\x00", "|")
			_ = tp.PrintfLine("235 ok")
		case strings.HasPrefix(cmd, "MAIL FROM:"):
			r.from = strings.Trim(line[len("MAIL FROM:"):], "<> ")
			_ = tp.PrintfLine("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO:"):
			r.to = strings.Trim(line[len("RCPT TO:"):], "<> ")
			_ = tp.PrintfLine("250 ok")
		case cmd == "DATA":
			_ = tp.PrintfLine("354 go")
			b, _ := io.ReadAll(tp.DotReader())
			r.data = string(b)
			_ = tp.PrintfLine("250 queued")
			f.got <- r
		case cmd == "QUIT":
			_ = tp.PrintfLine("221 bye")
			return
		default:
			_ = tp.PrintfLine("250 ok")
		}
	}
}

func selfSigned(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}, pool
}

func sender(f *fakeSMTP, mode string) *Sender {
	s := New(Config{Host: "127.0.0.1", Port: f.port(), Username: "u", Password: "p w", From: "Gunkly <gunkly@example.com>", TLS: mode},
		func(context.Context) bool { return false }) // guard off: the fake listens on loopback
	return s
}

func wait(t *testing.T, f *fakeSMTP) received {
	t.Helper()
	select {
	case r := <-f.got:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("no message received")
		return received{}
	}
}

func TestSendPlainAndHeaders(t *testing.T) {
	f := startSMTP(t, nil, false)
	err := sender(f, "none").Send(context.Background(), Message{To: "Ann Lee <ann@example.org>", Subject: "Verify your email \u2013 caf\u00e9", Body: "Hello\nLine two\n.dot line\n"})
	if err != nil {
		t.Fatal(err)
	}
	r := wait(t, f)
	if r.from != "gunkly@example.com" || r.to != "ann@example.org" || r.auth != "|u|p w" {
		t.Fatalf("envelope/auth: %+v", r)
	}
	hdr, body, _ := strings.Cut(r.data, "\n\n") // the fake reader normalises line ends
	for _, want := range []string{"From: \"Gunkly\" <gunkly@example.com>", "To: <ann@example.org>", "Subject: =?utf-8?q?Verify_your_email_=E2=80=93_caf=C3=A9?=",
		"Date: ", "Message-ID: <", "@example.com>", "MIME-Version: 1.0", "Content-Type: text/plain; charset=utf-8"} {
		if !strings.Contains(hdr, want) {
			t.Errorf("header lacks %q:\n%s", want, hdr)
		}
	}
	if strings.Contains(hdr, "Ann Lee") {
		t.Errorf("recipient display name leaked into headers:\n%s", hdr)
	}
	if !strings.Contains(body, "Line two") || !strings.Contains(body, ".dot line") {
		t.Errorf("body mangled: %q", body)
	}
}

func TestSendRejectsHeaderInjection(t *testing.T) {
	f := startSMTP(t, nil, false)
	s := sender(f, "none")
	for name, m := range map[string]Message{
		"subject lf":   {To: "a@example.org", Subject: "hi\nBcc: evil@example.net", Body: "x"},
		"subject crlf": {To: "a@example.org", Subject: "hi\r\nBcc: evil@example.net", Body: "x"},
		"to":           {To: "a@example.org\r\nBcc: evil@example.net", Subject: "s", Body: "x"},
		"to display":   {To: "\"a\r\nBcc: e@example.net\" <a@example.org>", Subject: "s", Body: "x"},
	} {
		if err := s.Send(context.Background(), m); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	bad := sender(f, "none")
	bad.cfg.From = "Gunkly\r\nBcc: e@example.net <g@example.com>"
	if err := bad.Send(context.Background(), Message{To: "a@example.org", Subject: "s", Body: "x"}); err == nil {
		t.Error("from with line break accepted")
	}
	select {
	case r := <-f.got:
		t.Fatalf("a message was delivered: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestBodyIsEncodedSafely(t *testing.T) {
	f := startSMTP(t, nil, false)
	// A body line that is just "." and 8-bit text must survive the dot-stuffing and encoding.
	if err := sender(f, "none").Send(context.Background(), Message{To: "a@example.org", Subject: "s", Body: "caf\u00e9\n.\nend"}); err != nil {
		t.Fatal(err)
	}
	r := wait(t, f)
	if !strings.Contains(r.data, "caf=C3=A9") || !strings.Contains(r.data, "\n.\nend") {
		t.Errorf("data: %q", r.data)
	}
}

func TestImplicitTLSAndSTARTTLS(t *testing.T) {
	conf, pool := selfSigned(t)
	for _, mode := range []string{"tls", "starttls"} {
		t.Run(mode, func(t *testing.T) {
			f := startSMTP(t, conf, mode == "tls")
			s := sender(f, mode)
			s.tlsConf = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
			if err := s.Send(context.Background(), Message{To: "a@example.org", Subject: "s", Body: "x"}); err != nil {
				t.Fatal(err)
			}
			if r := wait(t, f); r.to != "a@example.org" {
				t.Fatalf("got %+v", r)
			}
		})
	}
}

func TestSTARTTLSRequiredNeverDowngrades(t *testing.T) {
	f := startSMTP(t, nil, false) // offers no STARTTLS
	err := sender(f, "starttls").Send(context.Background(), Message{To: "a@example.org", Subject: "s", Body: "x"})
	if err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("err = %v", err)
	}
	select {
	case r := <-f.got:
		t.Fatalf("delivered in the clear: %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestSendGoesThroughEgressGuard(t *testing.T) {
	f := startSMTP(t, nil, false)
	s := sender(f, "none")
	s.guarded = func(context.Context) bool { return true }
	err := s.Send(context.Background(), Message{To: "a@example.org", Subject: "s", Body: "x"})
	if !errors.Is(err, egress.ErrBlocked) {
		t.Fatalf("err = %v, want egress.ErrBlocked", err)
	}
	// New(nil) fails closed.
	s2 := New(s.cfg, nil)
	if err := s2.Send(context.Background(), Message{To: "a@example.org", Subject: "s", Body: "x"}); !errors.Is(err, egress.ErrBlocked) {
		t.Fatalf("nil guard: %v", err)
	}
}

func TestSanitising(t *testing.T) {
	for _, a := range []string{"ann@example.com", "a.b+tag@mail.example.org"} {
		if !plainAddress(a) {
			t.Errorf("%q should be accepted", a)
		}
	}
	for _, a := range []string{"", "ann", "a b@example.com", "<a@example.com>", "a@b", "a@exa mple.com", "\"x\"@example.com", "a@example.com\r\nBcc: x@y.z", "a@-example.com"} {
		if plainAddress(a) {
			t.Errorf("%q should be refused", a)
		}
	}
	if got := cleanLine("Hi\tBcc: x\x00\u202eevil"); got != "Hi Bcc: x  evil" {
		t.Errorf("cleanLine = %q", got)
	}
	if got := cleanBody("a\r\nb\rc\x07d\u2066e\tf\n"); got != "a\nb\ncde\tf\n" {
		t.Errorf("cleanBody = %q", got)
	}
}
