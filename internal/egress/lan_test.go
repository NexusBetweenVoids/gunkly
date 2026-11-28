package egress

import (
	"net/netip"
	"testing"
)

func TestLANAllowed(t *testing.T) {
	for addr, want := range map[string]bool{
		"192.168.1.20":           true, // a local model on the LAN
		"10.0.0.5":               true,
		"127.0.0.1":              true, // same host
		"::1":                    true,
		"fd12:3456::1":           true, // unique local
		"93.184.216.34":          true, // public
		"169.254.169.254":        false,
		"169.254.1.1":            false,
		"100.100.100.200":        false,
		"fd00:ec2::254":          false,
		"fe80::1":                false,
		"::ffff:169.254.169.254": false, // mapped form of the metadata address
		"0.0.0.0":                false,
		"224.0.0.1":              false,
		"255.255.255.255":        false,
		"::":                     false,
		"64:ff9b::a9fe:a9fe":     false, // NAT64 form of 169.254.169.254
		"2002:a9fe:a9fe::1":      false, // 6to4
		"::a9fe:a9fe":            false, // IPv4-compatible
		"::0.0.0.2":              false, // IPv4-compatible 0.0.0.0/8
		"64:ff9b::c0a8:114":      true,  // NAT64 form of 192.168.1.20
		"2002:c0a8:114::1":       true,
	} {
		if got := lanAllowed(netip.MustParseAddr(addr)); got != want {
			t.Errorf("lanAllowed(%s) = %v, want %v", addr, got, want)
		}
	}
	if lanAllowed(netip.Addr{}) {
		t.Error("the zero address must not be allowed")
	}
}
