package notify

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/egress"
)

// fakeDNS answers every A query with ip and every other query with no
// records, so fixed provider hosts (discord.com, api.telegram.org, ...)
// "resolve" to a blocked address.
func fakeDNS(t *testing.T, answer *atomic.Pointer[net.IP]) string {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			q := buf[:n]
			if len(q) < 12 {
				continue
			}
			// End of the single question: name labels, then QTYPE, QCLASS.
			i := 12
			for i < len(q) && q[i] != 0 {
				i += int(q[i]) + 1
			}
			if i+5 > len(q) {
				continue
			}
			qtype := binary.BigEndian.Uint16(q[i+1:])
			qend := i + 5
			resp := append([]byte{}, q[:qend]...)
			resp[2] = 0x81 // QR, RD
			resp[3] = 0x80 // RA, NOERROR
			binary.BigEndian.PutUint16(resp[6:], 0)
			binary.BigEndian.PutUint16(resp[8:], 0)
			binary.BigEndian.PutUint16(resp[10:], 0)
			if qtype == 1 {
				binary.BigEndian.PutUint16(resp[6:], 1)
				resp = append(resp, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4)
				resp = append(resp, answer.Load().To4()...)
			}
			_, _ = pc.WriteTo(resp, addr)
		}
	}()
	return pc.LocalAddr().String()
}

// useResolver points the default resolver (used by the guard's dialer) at
// dns for the rest of the test binary: restoring it would race with
// connection attempts Shoutrrr services may still be retrying.
func useResolver(dns string) {
	net.DefaultResolver.PreferGo = true
	net.DefaultResolver.Dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "udp4", dns)
	}
}

// TestSendersBlocked is the CI guard for ADR 0006: every default-enabled
// service and a sample of admin-enableable ones, pointed (by URL or by DNS) at
// loopback and the cloud metadata address, must fail with egress.ErrBlocked
// without a single request reaching the target.
func TestSendersBlocked(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	defer srv.Close()
	tcp, err := net.Listen("tcp4", "127.0.0.1:0") // SMTP / MQTT target
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	go func() {
		for {
			c, err := tcp.Accept()
			if err != nil {
				return
			}
			hits.Add(1)
			_ = c.Close()
		}
	}()
	httpAddr := strings.TrimPrefix(srv.URL, "http://")
	tcpAddr := tcp.Addr().String()

	urls := map[string]string{
		// Default-enabled fixed providers: reach their real host names, which
		// the fake DNS resolves to the blocked address.
		KindDiscord:    "discord://token@123456789012345678",
		KindSlack:      "slack://hook:WNA3PBYV6-F20DUQND3RQ-Webc4MAvoacrpPakR8phF0zi@webhook",
		KindTelegram:   "telegram://110201543:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw@telegram?chats=@test",
		KindPushover:   "pushover://shoutrrr:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa@uuuuuuuuuuuuuuuuuuuuuuuuuuuuuu/",
		KindPushbullet: "pushbullet://o.aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		KindGoogleChat: "googlechat://chat.googleapis.com/v1/spaces/FOO/messages?key=bar&token=baz",
		KindNtfy:       "ntfy://ntfy.sh/topic",
		// Admin-enableable, user-chosen hosts: literal blocked addresses.
		"generic-ip":       "generic+http://" + httpAddr + "/hook",
		"generic-dns":      "generic+https://hooks.internal.example/hook",
		KindGotify:         "gotify://" + httpAddr + "/Aaaaaaaaaaaaaaa?disabletls=yes",
		KindNtfySelfHosted: "ntfy://" + httpAddr + "/topic?disabletls=yes",
		KindSMTP:           "smtp://user:pass@" + tcpAddr + "/?from=a@example.com&to=b@example.com&encryption=none&auth=none",
		KindMQTT:           "mqtt://" + tcpAddr + "/gunkly",
	}
	var answer atomic.Pointer[net.IP]
	useResolver(fakeDNS(t, &answer))
	for _, blocked := range []string{"127.0.0.1", "169.254.169.254"} {
		ip := net.ParseIP(blocked)
		answer.Store(&ip)
		t.Run(blocked, func(t *testing.T) {
			g := egress.Guard{Enabled: true}
			for kind, raw := range urls {
				raw = strings.ReplaceAll(raw, "127.0.0.1", blocked)
				t.Run(kind, func(t *testing.T) {
					t.Parallel() // each kind has its own sender and counters
					s := NewShoutrrrSender(g)
					// Count raw dials (SMTP, MQTT): none may connect, and the
					// guard must have blocked at least one. (A retry cut short
					// by context cancellation fails without reaching the guard;
					// that is safe.)
					var blockedDials, connected atomic.Int64
					s.dial = func(ctx context.Context, network, addr string) (net.Conn, error) {
						c, err := g.DialContext(ctx, network, addr)
						switch {
						case errors.Is(err, egress.ErrBlocked):
							blockedDials.Add(1)
						case err == nil:
							connected.Add(1)
						}
						return c, err
					}
					timeout := 30 * time.Second
					if kind == KindMQTT {
						timeout = 2 * time.Second // retries until its budget ends; one blocked dial is enough
					}
					ctx, cancel := context.WithTimeout(context.Background(), timeout)
					defer cancel()
					err := s.Send(ctx, raw, Message{Title: "t", Body: "b"})
					if kind == KindMQTT {
						// Shoutrrr's MQTT client retries a failed connect until
						// its send budget ends and reports a timeout, not the
						// dial error: assert the dials themselves were blocked.
						if err == nil || blockedDials.Load() == 0 || connected.Load() != 0 {
							t.Fatalf("err = %v, blocked %d, connected %d", err, blockedDials.Load(), connected.Load())
						}
						return
					}
					if !errors.Is(err, egress.ErrBlocked) {
						t.Fatalf("err = %v, want egress.ErrBlocked", err)
					}
				})
			}
		})
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("%d requests reached the blocked target", n)
	}
	// Control: with the guard off the same generic URL does reach the target,
	// so the test above is not passing for an unrelated reason.
	s := NewShoutrrrSender(egress.Guard{})
	_ = s.Send(context.Background(), "generic+http://"+httpAddr+"/hook", Message{Body: "b"})
	if hits.Load() == 0 {
		t.Fatal("control request never arrived")
	}
}
