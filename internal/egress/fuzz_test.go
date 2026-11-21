package egress

import (
	"net/netip"
	"net/url"
	"strings"
	"testing"
)

func FuzzBlocked(f *testing.F) {
	for _, s := range []string{"127.0.0.1", "::ffff:127.0.0.1", "64:ff9b::a9fe:a9fe", "2002:a9fe:a9fe::", "8.8.8.8", "fe80::1%eth0"} {
		a := netip.MustParseAddr(s)
		f.Add(a.AsSlice())
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		a, ok := netip.AddrFromSlice(b)
		if !ok {
			return
		}
		got := blocked(a)
		// Mapped and unmapped forms must agree.
		if a.Is4() && blocked(netip.AddrFrom16(a.As16())) != got {
			t.Fatalf("%s and its IPv4-mapped form disagree", a)
		}
		if a.Is4In6() && blocked(a.Unmap()) != got {
			t.Fatalf("%s and its unmapped form disagree", a)
		}
		// Anything the stdlib classifies as non-public must be blocked.
		u := a.Unmap()
		if (u.IsLoopback() || u.IsPrivate() || u.IsLinkLocalUnicast() || u.IsLinkLocalMulticast() ||
			u.IsMulticast() || u.IsUnspecified() || u.IsInterfaceLocalMulticast()) && !got {
			t.Fatalf("%s: stdlib says non-public but not blocked", a)
		}
	})
}

func FuzzWebPushAllowed(f *testing.F) {
	for _, s := range []string{
		"https://fcm.googleapis.com/fcm/send/x", "https://a.notify.windows.com/w/",
		"https://user@fcm.googleapis.com/", "https://127.0.0.1/", "https://fcm.googleapis.com.evil.com/",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if WebPushAllowed(s) != nil {
			return
		}
		u, err := url.Parse(s)
		if err != nil {
			t.Fatalf("%q allowed but unparseable", s)
		}
		h := u.Hostname()
		if u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
			t.Fatalf("%q allowed with bad scheme/userinfo/port", s)
		}
		if !pushHosts[h] && (!strings.HasSuffix(h, pushSuffix) || len(h) <= len(pushSuffix)) {
			t.Fatalf("%q allowed with host %q", s, h)
		}
		if _, err := netip.ParseAddr(h); err == nil {
			t.Fatalf("%q allowed with IP host", s)
		}
	})
}
