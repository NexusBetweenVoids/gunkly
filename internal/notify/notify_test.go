package notify

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/db/dbtest"
	"github.com/NexusBetweenVoids/gunkly/internal/schedule"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

func TestValidateURL(t *testing.T) {
	all := map[string]bool{}
	for _, k := range AllKinds() {
		all[k] = true
	}
	cases := []struct {
		url     string
		enabled map[string]bool
		kind    string
		err     error
	}{
		{"discord://token@123456", nil, KindDiscord, nil},
		{"discord://token@123456?title=Hi&thread_id=9", nil, KindDiscord, nil},
		{"slack://xoxb:token@C123", nil, KindSlack, nil},
		{"telegram://123:abc@telegram?chats=@chan", nil, KindTelegram, nil},
		{"pushover://shoutrrr:apitoken@userkey/?devices=a", nil, KindPushover, nil},
		{"pushbullet://token", nil, KindPushbullet, nil},
		{"googlechat://chat.googleapis.com/v1/spaces/X/messages?key=k&token=t", nil, KindGoogleChat, nil},
		{"hangouts://chat.googleapis.com/v1/spaces/X/messages?key=k&token=t", nil, KindGoogleChat, nil},
		{"ntfy://ntfy.sh/topic", nil, KindNtfy, nil},
		{"ntfy://NTFY.sh/topic?priority=4", nil, KindNtfy, nil},

		// Host / transport overrides on fixed providers.
		{"telegram://123:abc@telegram?chats=1&apiurl=http://169.254.169.254", nil, "", ErrHostOverride},
		{"telegram://123:abc@evil.example?chats=1", nil, "", ErrHostOverride},
		{"telegram://123:abc@telegram?chats=1&skiptlsverify=yes", nil, "", ErrHostOverride},
		{"googlechat://evil.example/v1/spaces/X/messages?key=k&token=t", nil, "", ErrHostOverride},
		{"ntfy://ntfy.sh/topic?disabletls=yes", nil, "", ErrHostOverride},
		{"ntfy://ntfy.sh/topic?scheme=http", nil, "", ErrHostOverride},
		{"ntfy://ntfy.sh:8080/topic", nil, "", ErrServiceDisabled}, // another server: self-hosted kind
		{"discord://token@123456:80", nil, "", ErrHostOverride},
		{"discord+https://evil.example/api", nil, "", ErrHostOverride},
		{"discord://token@123456?json=yes", nil, "", ErrHostOverride},

		// Optional kinds: off by default, on when enabled.
		{"ntfy://ntfy.example.com/topic", nil, "", ErrServiceDisabled},
		{"ntfy://ntfy.example.com/topic", all, KindNtfySelfHosted, nil},
		{"generic://example.com/hook", nil, "", ErrServiceDisabled},
		{"generic+https://example.com/hook", all, KindGeneric, nil},
		{"gotify://gotify.example.com/token", all, KindGotify, nil},
		{"smtp://u:p@mail.example.com:587/?from=a@b&to=c@d", all, KindSMTP, nil},
		{"mqtts://broker.example.com/topic", all, KindMQTT, nil},
		{"generic+ftp://example.com/x", all, "", ErrHostOverride},

		// Never allowed / malformed.
		{"logger://", all, "", ErrUnknownService},
		{"https://discord.com/api/webhooks/1/x", all, "", ErrUnknownService},
		{"", nil, "", ErrInvalidURL},
		{" discord://t@1", nil, "", ErrInvalidURL},
		{"discord://t@1#frag", nil, "", ErrInvalidURL},
		{"discord://t@1\n", nil, "", ErrInvalidURL},
		{"generic://", all, "", ErrInvalidURL},
		{"discord:opaque", nil, "", ErrInvalidURL},
		{"discord://t@1/" + strings.Repeat("a", maxURLLen), nil, "", ErrInvalidURL},
	}
	for _, c := range cases {
		kind, err := ValidateURL(c.url, c.enabled)
		if kind != c.kind || !errors.Is(err, c.err) {
			t.Errorf("ValidateURL(%q) = %q, %v; want %q, %v", c.url, kind, err, c.kind, c.err)
		}
	}
}

// FuzzValidateURL: never panics; an accepted URL is a parseable absolute URL
// of an enabled kind, and fixed providers never accept another host.
func FuzzValidateURL(f *testing.F) {
	for _, s := range []string{"discord://token@123456", "telegram://1:a@telegram?chats=1", "ntfy://ntfy.sh/t",
		"googlechat://chat.googleapis.com/v1/spaces/X/messages?key=k&token=t", "generic+https://h/p", "smtp://u:p@h:25/?to=a",
		"ntfy://[::1]/t", "telegram://x@telegram?apiurl=http://127.0.0.1", "%zz://x"} {
		f.Add(s)
	}
	enabled := DefaultKinds()
	f.Fuzz(func(t *testing.T, raw string) {
		kind, err := ValidateURL(raw, enabled)
		if err != nil {
			if kind != "" {
				t.Fatalf("kind %q with error", kind)
			}
			return
		}
		if !enabled[kind] {
			t.Fatalf("disabled kind %q accepted", kind)
		}
		u, perr := url.Parse(raw)
		if perr != nil || u.Host == "" {
			t.Fatalf("accepted unparseable %q", raw)
		}
		if f, ok := fixedServices[kind]; ok && f.host != "" && !strings.EqualFold(u.Host, f.host) {
			t.Fatalf("fixed provider %s accepted host %q", kind, u.Host)
		}
		if q := u.Query(); q.Has("apiurl") || q.Has("skiptlsverify") || q.Has("disabletls") || q.Has("scheme") {
			t.Fatalf("override accepted: %q", raw)
		}
	})
}

func TestRender(t *testing.T) {
	due := schedule.Date{Year: 2026, Month: 11, Day: 21}
	m := Render(Reminder{ChoreName: "Gutters", Kind: schedule.Overdue, Due: due, MarkDoneURL: MarkDoneURL("https://g.example", "tok")})
	if m.Title != "Gunkly: Gutters" || !strings.Contains(m.Body, "Gutters is overdue (was due on 2026-11-21).") ||
		!strings.Contains(m.Body, "Mark as done: https://g.example/done/tok") {
		t.Fatalf("message = %+v", m)
	}
	m = Render(Reminder{ChoreName: "X", Kind: schedule.Before, Due: due})
	if m.Body != "X is due on 2026-11-21." {
		t.Fatalf("body = %q", m.Body)
	}
	if MarkDoneURL("", "tok") != "" {
		t.Fatal("link without base URL")
	}
}

func TestSafeError(t *testing.T) {
	raw := "telegram://123456:SECRETTOKEN@telegram?chats=@mychannel"
	err := errors.New("Post https://api.telegram.org/bot123456:SECRETTOKEN/sendMessage: chat @mychannel failed; url " + raw)
	s := SafeError(err, raw)
	if strings.Contains(s, "SECRETTOKEN") || strings.Contains(s, "mychannel") {
		t.Fatalf("leaked: %q", s)
	}
	long := SafeError(errors.New(strings.Repeat("é", 400)), "x://y")
	if len(long) > maxErrLen || !utf8.ValidString(long) {
		t.Fatalf("not bounded/valid: %d", len(long))
	}
	if SafeError(nil, raw) != "" {
		t.Fatal("nil error")
	}
}

func TestSealAndRotate(t *testing.T) {
	ctx := context.Background()
	st := store.New(dbtest.Open(t))
	old, err := auth.NewKeyring(1, map[uint32]string{1: strings.Repeat("a", 32)})
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser(ctx, store.NewUser{Username: "u", DisplayName: "u", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	raw := "discord://token@123456"
	nc, err := Prepare(old, raw, "Mine", nil)
	if err != nil || nc.Service != KindDiscord || strings.Contains(nc.Ciphertext, "token") {
		t.Fatalf("Prepare = %+v, %v", nc, err)
	}
	if _, err := Prepare(old, "generic://h/p", "x", nil); !errors.Is(err, ErrServiceDisabled) {
		t.Fatalf("err = %v", err)
	}
	nc.Max = 10
	if _, err := st.CreateUserChannel(ctx, store.Actor{UserID: u.ID}, nc); err != nil {
		t.Fatal(err)
	}
	// Bound to the channel ID.
	if _, err := Open(old, "other-id", nc.Ciphertext); err == nil {
		t.Fatal("ciphertext opened under another channel ID")
	}
	upd, err := Reseal(old, nc.ID, "slack://xoxb:t@C1", nil)
	if err != nil || upd.Service != KindSlack {
		t.Fatalf("Reseal = %+v, %v", upd, err)
	}

	ring, err := auth.NewKeyring(2, map[uint32]string{1: strings.Repeat("a", 32), 2: strings.Repeat("b", 32)})
	if err != nil {
		t.Fatal(err)
	}
	for want := range []int{1, 0} { // second run: nothing left to rotate
		n, err := RotateSecrets(ctx, st, ring)
		if err != nil || n != 1-want {
			t.Fatalf("RotateSecrets = %d, %v", n, err)
		}
	}
	secrets, err := st.ChannelSecrets(ctx, "", 10)
	if err != nil || len(secrets) != 1 || ring.NeedsRotation(secrets[0].Ciphertext) {
		t.Fatalf("secrets = %+v, %v", secrets, err)
	}
	if got, err := Open(ring, nc.ID, secrets[0].Ciphertext); err != nil || got != raw {
		t.Fatalf("Open = %q, %v", got, err)
	}
}

func TestUnconfigured(t *testing.T) {
	if err := (Unconfigured{}).Send(context.Background(), "x", Message{}); !errors.Is(err, ErrNoSender) {
		t.Fatal(err)
	}
}
