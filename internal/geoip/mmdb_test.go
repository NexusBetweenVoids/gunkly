package geoip

import (
	"bytes"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// writer builds a tiny MMDB file for tests: a binary trie, a data section
// that uses pointers for repeated strings, and the metadata.
type writer struct {
	ipVersion  int
	recordBits int
	nodes      [][2]int // child: >=0 node index, -1 empty, <=-2 data (-2-offset)
	data       bytes.Buffer
	strPos     map[string]int
}

func newWriter(ipVersion, recordBits int) *writer {
	w := &writer{ipVersion: ipVersion, recordBits: recordBits, strPos: map[string]int{}}
	w.nodes = append(w.nodes, [2]int{-1, -1})
	return w
}

func ctrl(typ, size int) []byte {
	var out []byte
	first := byte(size)
	switch {
	case size >= 65821:
		first, out = 31, []byte{byte((size - 65821) >> 16), byte((size - 65821) >> 8), byte(size - 65821)}
	case size >= 285:
		first, out = 30, []byte{byte((size - 285) >> 8), byte(size - 285)}
	case size >= 29:
		first, out = 29, []byte{byte(size - 29)}
	}
	if typ <= 7 {
		return append([]byte{byte(typ<<5) | first}, out...)
	}
	return append([]byte{first, byte(typ - 7)}, out...)
}

func (w *writer) str(s string) {
	if p, ok := w.strPos[s]; ok {
		switch q := p - 2048; {
		case p < 2048:
			w.data.Write([]byte{0x20 | byte(p>>8), byte(p)})
		case q < 1<<19:
			w.data.Write([]byte{0x28 | byte(q>>16), byte(q >> 8), byte(q)})
		default:
			panic("test fixture too large")
		}
		return
	}
	w.strPos[s] = w.data.Len()
	w.data.Write(ctrl(2, len(s)))
	w.data.WriteString(s)
}

func (w *writer) uintv(typ int, v uint64) {
	var b []byte
	for x := v; x > 0; x >>= 8 {
		b = append([]byte{byte(x)}, b...)
	}
	w.data.Write(ctrl(typ, len(b)))
	w.data.Write(b)
}

// record writes a country and/or ASN record and returns its data offset.
func (w *writer) record(country string, asn uint64) int {
	off := w.data.Len()
	n := 0
	if country != "" {
		n++
	}
	if asn != 0 {
		n++
	}
	w.data.Write(ctrl(7, n))
	if country != "" {
		w.str("country")
		w.data.Write(ctrl(7, 1))
		w.str("iso_code")
		w.str(country)
	}
	if asn != 0 {
		w.str("autonomous_system_number")
		w.uintv(6, asn)
	}
	return off
}

func (w *writer) insert(prefix string, dataOff int) {
	p := netip.MustParsePrefix(prefix)
	a := p.Addr().Unmap()
	bits := p.Bits()
	var raw []byte
	if a.Is4() {
		x := a.As4()
		raw = x[:]
		if w.ipVersion == 6 {
			raw = append(make([]byte, 12), raw...)
			bits += 96
		}
	} else {
		x := a.As16()
		raw = x[:]
	}
	node := 0
	for i := 0; i < bits; i++ {
		bit := int(raw[i/8]>>(7-uint(i%8))) & 1
		if i == bits-1 {
			w.nodes[node][bit] = -2 - dataOff
			return
		}
		if w.nodes[node][bit] < 0 {
			w.nodes = append(w.nodes, [2]int{-1, -1})
			w.nodes[node][bit] = len(w.nodes) - 1
		}
		node = w.nodes[node][bit]
	}
}

func (w *writer) bytes() []byte {
	count := len(w.nodes)
	val := func(c int) uint32 {
		switch {
		case c >= 0:
			return uint32(c)
		case c == -1:
			return uint32(count)
		}
		return uint32(count + 16 + (-2 - c))
	}
	var out bytes.Buffer
	for _, n := range w.nodes {
		l, r := val(n[0]), val(n[1])
		switch w.recordBits {
		case 24:
			out.Write([]byte{byte(l >> 16), byte(l >> 8), byte(l), byte(r >> 16), byte(r >> 8), byte(r)})
		case 28:
			out.Write([]byte{byte(l >> 16), byte(l >> 8), byte(l), byte(l>>24)<<4 | byte(r>>24), byte(r >> 16), byte(r >> 8), byte(r)})
		default:
			out.Write([]byte{byte(l >> 24), byte(l >> 16), byte(l >> 8), byte(l), byte(r >> 24), byte(r >> 16), byte(r >> 8), byte(r)})
		}
	}
	out.Write(make([]byte, 16))
	out.Write(w.data.Bytes())
	out.WriteString("\xAB\xCD\xEFMaxMind.com")
	m := &writer{strPos: map[string]int{}}
	m.data.Write(ctrl(7, 4))
	m.str("node_count")
	m.uintv(6, uint64(count))
	m.str("record_size")
	m.uintv(5, uint64(w.recordBits))
	m.str("ip_version")
	m.uintv(5, uint64(w.ipVersion))
	m.str("binary_format_major_version")
	m.uintv(5, 2)
	out.Write(m.data.Bytes())
	return out.Bytes()
}

func fixture(t *testing.T, ipVersion, bits int) *DB {
	t.Helper()
	w := newWriter(ipVersion, bits)
	de := w.record("DE", 0)
	us := w.record("US", 0)
	as := w.record("", 16509)
	big := w.record("FR", 4200000000)
	w.insert("81.2.69.0/24", de)
	w.insert("8.8.8.0/24", us)
	w.insert("3.0.0.0/9", as)
	w.insert("5.5.5.5/32", big)
	if ipVersion == 6 {
		w.insert("2001:db8::/32", de)
		w.insert("2a00::/12", us)
	}
	db, err := New(w.bytes())
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestLookup(t *testing.T) {
	for _, ver := range []int{4, 6} {
		for _, bits := range []int{24, 28, 32} {
			db := fixture(t, ver, bits)
			c := func(s string) string { return db.Country(netip.MustParseAddr(s)) }
			a := func(s string) uint32 { return db.ASN(netip.MustParseAddr(s)) }
			if c("81.2.69.77") != "DE" || c("8.8.8.8") != "US" || c("5.5.5.5") != "FR" {
				t.Errorf("v%d/%d: IPv4 countries %q %q %q", ver, bits, c("81.2.69.77"), c("8.8.8.8"), c("5.5.5.5"))
			}
			if c("::ffff:8.8.8.8") != "US" {
				t.Errorf("v%d/%d: mapped IPv4", ver, bits)
			}
			if a("3.4.5.6") != 16509 || a("5.5.5.5") != 4200000000 {
				t.Errorf("v%d/%d: asn %d %d", ver, bits, a("3.4.5.6"), a("5.5.5.5"))
			}
			// Missing entries, and records without the asked field.
			if c("9.9.9.9") != "" || a("9.9.9.9") != 0 || c("3.4.5.6") != "" || a("8.8.8.8") != 0 || c("5.5.5.6") != "" {
				t.Errorf("v%d/%d: missing entries answered", ver, bits)
			}
			if ver == 6 {
				if c("2001:db8::1") != "DE" || c("2a01::1") != "US" || c("2002::1") != "" {
					t.Errorf("v%d/%d: IPv6 lookups", ver, bits)
				}
			} else if c("2001:db8::1") != "" {
				t.Errorf("IPv6 in an IPv4 database answered")
			}
		}
	}
}

func TestOpenAndBadFiles(t *testing.T) {
	w := newWriter(4, 24)
	w.insert("1.2.3.0/24", w.record("NL", 0))
	p := filepath.Join(t.TempDir(), "c.mmdb")
	if err := os.WriteFile(p, w.bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := Open(p)
	if err != nil || db.Country(netip.MustParseAddr("1.2.3.4")) != "NL" {
		t.Fatalf("open: %v", err)
	}
	if _, err := Open(filepath.Join(t.TempDir(), "none")); err == nil {
		t.Error("missing file opened")
	}
	good := w.bytes()
	for name, b := range map[string][]byte{"empty": nil, "garbage": []byte("hello"), "truncated": good[len(good)-30:]} {
		if _, err := New(b); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// Corrupt data must never panic.
	for i := range good {
		c := slices.Clone(good)
		c[i] ^= 0xFF
		if db, err := New(c); err == nil {
			db.Country(netip.MustParseAddr("1.2.3.4"))
			db.ASN(netip.MustParseAddr("2001:db8::1"))
		}
	}
}

func TestIsHostingASN(t *testing.T) {
	if !slices.IsSorted(hostingASNs) {
		t.Fatal("list not sorted")
	}
	for _, n := range []uint32{16509, 14618, 24940, 213230, 396982, 197540} {
		if !IsHostingASN(n) {
			t.Errorf("%d not hosting", n)
		}
	}
	for _, n := range []uint32{0, 13335, 3320, 7922} {
		if IsHostingASN(n) {
			t.Errorf("%d hosting", n)
		}
	}
}
