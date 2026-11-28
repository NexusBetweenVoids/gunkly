package egress

import "net/netip"

var blocked4 = prefixes(
	"0.0.0.0/8",       // "this network", includes unspecified
	"10.0.0.0/8",      // RFC 1918
	"100.64.0.0/10",   // carrier-grade NAT (includes Alibaba metadata 100.100.100.200)
	"127.0.0.0/8",     // loopback
	"169.254.0.0/16",  // link-local, includes cloud metadata 169.254.169.254
	"172.16.0.0/12",   // RFC 1918
	"192.0.0.0/24",    // IETF protocol assignments
	"192.0.2.0/24",    // TEST-NET-1
	"192.88.99.0/24",  // 6to4 relay anycast (deprecated)
	"192.168.0.0/16",  // RFC 1918
	"198.18.0.0/15",   // benchmarking
	"198.51.100.0/24", // TEST-NET-2
	"203.0.113.0/24",  // TEST-NET-3
	"224.0.0.0/4",     // multicast
	"240.0.0.0/4",     // reserved, includes broadcast 255.255.255.255
)

var blocked6 = prefixes(
	"::/96",           // unspecified, loopback, deprecated IPv4-compatible
	"::ffff:0:0:0/96", // IPv4-translated (SIIT)
	"64:ff9b:1::/48",  // local-use NAT64
	"100::/64",        // discard-only
	"2001::/32",       // Teredo (embeds arbitrary IPv4; never a legitimate target)
	"2001:2::/48",     // benchmarking
	"2001:10::/28",    // ORCHID
	"2001:20::/28",    // ORCHIDv2
	"2001:db8::/32",   // documentation
	"3fff::/20",       // documentation (RFC 9637)
	"fc00::/7",        // unique local, includes AWS metadata fd00:ec2::254
	"fe80::/10",       // link-local
	"fec0::/10",       // site-local (deprecated)
	"ff00::/8",        // multicast
)

var (
	nat64     = netip.MustParsePrefix("64:ff9b::/96")
	sixToFour = netip.MustParsePrefix("2002::/16")
)

func prefixes(ss ...string) []netip.Prefix {
	ps := make([]netip.Prefix, len(ss))
	for i, s := range ss {
		ps[i] = netip.MustParsePrefix(s)
	}
	return ps
}

// blocked reports whether connecting to a is forbidden when the guard is on.
// IPv6 forms that embed an IPv4 address (IPv4-mapped, NAT64, 6to4) are judged
// by the embedded address.
func blocked(a netip.Addr) bool {
	if !a.IsValid() {
		return true
	}
	a = a.WithZone("").Unmap() // zoned addresses never match a prefix
	if a.Is4() {
		return inAny(a, blocked4)
	}
	if v4, ok := embedded4(a); ok {
		return blocked(v4)
	}
	return inAny(a, blocked6)
}

// embedded4 returns the IPv4 address a NAT64 or 6to4 address carries.
func embedded4(a netip.Addr) (netip.Addr, bool) {
	b := a.As16()
	switch {
	case nat64.Contains(a):
		return netip.AddrFrom4([4]byte(b[12:16])), true
	case sixToFour.Contains(a):
		return netip.AddrFrom4([4]byte(b[2:6])), true
	}
	return netip.Addr{}, false
}

func inAny(a netip.Addr, ps []netip.Prefix) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
