// Package geoip reads MaxMind-format (.mmdb) databases, such as the free
// DB-IP Lite country and ASN databases or GeoLite2, and knows a heuristic
// list of cloud and VPS networks. It is a small reader written for exactly two
// questions (the country code and the autonomous system number of an address);
// format: https://maxmind.github.io/MaxMind-DB/
package geoip

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
)

// maxDB bounds the database file read into memory.
const maxDB = 512 << 20

var metadataMarker = []byte("\xAB\xCD\xEFMaxMind.com")

// DB is an opened database. It is read-only after Open, so it is safe for
// concurrent use.
type DB struct {
	tree       []byte
	data       []byte // data section; pointers are relative to its start
	nodeCount  uint32
	recordBits uint32
	ipVersion  uint32
	ipv4Start  uint32
}

// Open reads the database file into memory.
func Open(path string) (*DB, error) {
	f, err := os.Open(path) //nolint:gosec // G304: an operator-configured path
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, maxDB+1))
	if err != nil {
		return nil, err
	}
	if len(buf) > maxDB {
		return nil, errors.New("geoip: database file too large")
	}
	return New(buf)
}

// New parses a database held in memory.
func New(buf []byte) (*DB, error) {
	i := bytes.LastIndex(buf, metadataMarker)
	if i < 0 {
		return nil, errors.New("geoip: not a MaxMind DB file (no metadata)")
	}
	d := &decoder{buf: buf[i+len(metadataMarker):]}
	meta, ok := d.value(0, 0).(map[string]any)
	if !ok || d.err != nil {
		return nil, errors.New("geoip: unreadable metadata")
	}
	num := func(k string) uint32 {
		if v, ok := meta[k].(uint64); ok && v < 1<<32 {
			return uint32(v)
		}
		return 0
	}
	db := &DB{nodeCount: num("node_count"), recordBits: num("record_size"), ipVersion: num("ip_version")}
	if num("binary_format_major_version") != 2 {
		return nil, errors.New("geoip: unsupported database format version")
	}
	if db.ipVersion != 4 && db.ipVersion != 6 {
		return nil, errors.New("geoip: bad ip_version")
	}
	if db.recordBits != 24 && db.recordBits != 28 && db.recordBits != 32 {
		return nil, fmt.Errorf("geoip: unsupported record size %d", db.recordBits)
	}
	treeSize := uint64(db.nodeCount) * uint64(db.recordBits) / 4
	if db.nodeCount == 0 || treeSize+16 > uint64(i) {
		return nil, errors.New("geoip: search tree does not fit the file")
	}
	db.tree = buf[:treeSize]
	db.data = buf[treeSize+16 : i]
	if db.ipVersion == 6 { // IPv4 lives under ::/96
		node := uint32(0)
		for bit := 0; bit < 96 && node < db.nodeCount; bit++ {
			node, _ = db.record(node, 0)
		}
		db.ipv4Start = node
	}
	return db, nil
}

// record returns the left (bit 0) or right (bit 1) record of a node.
func (db *DB) record(node uint32, bit int) (uint32, bool) {
	size := uint64(db.recordBits / 4) // bytes per node
	off := uint64(node) * size
	if off+size > uint64(len(db.tree)) {
		return 0, false
	}
	b := db.tree[off : off+size]
	switch db.recordBits {
	case 24:
		if bit == 0 {
			return uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2]), true
		}
		return uint32(b[3])<<16 | uint32(b[4])<<8 | uint32(b[5]), true
	case 28:
		if bit == 0 {
			return uint32(b[3]&0xF0)<<20 | uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2]), true
		}
		return uint32(b[3]&0x0F)<<24 | uint32(b[4])<<16 | uint32(b[5])<<8 | uint32(b[6]), true
	default:
		if bit == 0 {
			return binary.BigEndian.Uint32(b[0:4]), true
		}
		return binary.BigEndian.Uint32(b[4:8]), true
	}
}

// lookup finds the data record of addr, decoded; nil when there is none.
func (db *DB) lookup(addr netip.Addr) any {
	addr = addr.Unmap()
	var ip []byte
	node := uint32(0)
	switch {
	case addr.Is4():
		a := addr.As4()
		ip = a[:]
		if db.ipVersion == 6 {
			node = db.ipv4Start
		}
	case addr.Is6():
		if db.ipVersion == 4 {
			return nil
		}
		a := addr.As16()
		ip = a[:]
	default:
		return nil
	}
	for i := 0; i < len(ip)*8 && node < db.nodeCount; i++ {
		bit := int(ip[i/8]>>(7-uint(i%8))) & 1
		var ok bool
		if node, ok = db.record(node, bit); !ok {
			return nil
		}
	}
	if node <= db.nodeCount { // == nodeCount: no data; < : ran out of address bits
		return nil
	}
	off := uint64(node) - uint64(db.nodeCount) - 16
	if off >= uint64(len(db.data)) {
		return nil
	}
	d := &decoder{buf: db.data}
	v := d.value(int(off), 0) //nolint:gosec // G115: off < len(db.data)
	if d.err != nil {
		return nil
	}
	return v
}

// Country returns the upper-case ISO 3166-1 alpha-2 code of addr, or "".
func (db *DB) Country(addr netip.Addr) string {
	rec, _ := db.lookup(addr).(map[string]any)
	c, _ := rec["country"].(map[string]any)
	code, _ := c["iso_code"].(string)
	if len(code) != 2 {
		return ""
	}
	return code
}

// ASN returns the autonomous system number of addr, or 0.
func (db *DB) ASN(addr netip.Addr) uint32 {
	rec, _ := db.lookup(addr).(map[string]any)
	if n, ok := rec["autonomous_system_number"].(uint64); ok && n < 1<<32 {
		return uint32(n)
	}
	return 0
}

// decoder reads the data section format. It never panics on malformed input:
// every read is bounds-checked and err is set instead.
type decoder struct {
	buf []byte
	err error
}

const (
	maxDepth = 16
	maxItems = 1 << 16
)

var errBad = errors.New("geoip: malformed data")

func (d *decoder) value(off, depth int) any {
	v, _ := d.decode(off, depth)
	return v
}

// decode returns the value at off and the offset just after it.
func (d *decoder) decode(off, depth int) (any, int) {
	if d.err != nil {
		return nil, off
	}
	if depth > maxDepth || off < 0 || off >= len(d.buf) {
		d.err = errBad
		return nil, off
	}
	ctrl := d.buf[off]
	off++
	typ := int(ctrl >> 5)
	if typ == 1 { // pointer
		ss, vvv := int(ctrl>>3)&3, int(ctrl&7)
		n := ss + 1
		if off+n > len(d.buf) {
			d.err = errBad
			return nil, off
		}
		var p int
		switch ss {
		case 0:
			p = vvv<<8 | int(d.buf[off])
		case 1:
			p = (vvv<<16 | int(d.buf[off])<<8 | int(d.buf[off+1])) + 2048
		case 2:
			p = (vvv<<24 | int(d.buf[off])<<16 | int(d.buf[off+1])<<8 | int(d.buf[off+2])) + 526336
		default:
			p = int(binary.BigEndian.Uint32(d.buf[off : off+4]))
		}
		v, _ := d.decode(p, depth+1)
		return v, off + n
	}
	if typ == 0 { // extended type
		if off >= len(d.buf) {
			d.err = errBad
			return nil, off
		}
		typ = 7 + int(d.buf[off])
		off++
	}
	size := int(ctrl & 0x1F)
	switch size {
	case 29:
		size, off = 29+d.uint(off, 1), off+1
	case 30:
		size, off = 285+d.uint(off, 2), off+2
	case 31:
		size, off = 65821+d.uint(off, 3), off+3
	}
	if d.err != nil {
		return nil, off
	}
	switch typ {
	case 7: // map
		if size > maxItems {
			d.err = errBad
			return nil, off
		}
		m := make(map[string]any, min(size, 64))
		for range size {
			var k, v any
			k, off = d.decode(off, depth+1)
			v, off = d.decode(off, depth+1)
			if d.err != nil {
				return nil, off
			}
			key, ok := k.(string)
			if !ok {
				d.err = errBad
				return nil, off
			}
			m[key] = v
		}
		return m, off
	case 11: // array
		if size > maxItems {
			d.err = errBad
			return nil, off
		}
		a := make([]any, 0, min(size, 64))
		for range size {
			var v any
			v, off = d.decode(off, depth+1)
			if d.err != nil {
				return nil, off
			}
			a = append(a, v)
		}
		return a, off
	case 14: // boolean: the size is the value
		return size != 0, off
	case 12, 13: // data cache container, end marker
		return nil, off
	}
	if size < 0 || off+size > len(d.buf) {
		d.err = errBad
		return nil, off
	}
	b := d.buf[off : off+size]
	off += size
	switch typ {
	case 2: // utf8 string
		return string(b), off
	case 3, 15: // double, float: not needed
		return nil, off
	case 4, 10: // bytes, uint128: not needed
		return b, off
	case 5, 6, 9: // uint16, uint32, uint64
		if size > 8 {
			d.err = errBad
			return nil, off
		}
		var n uint64
		for _, x := range b {
			n = n<<8 | uint64(x)
		}
		return n, off
	case 8: // int32 (two's complement; shorter values are zero extended)
		if size > 4 {
			d.err = errBad
			return nil, off
		}
		var n uint32
		for _, x := range b {
			n = n<<8 | uint32(x)
		}
		return int64(int32(n)), off
	}
	d.err = errBad
	return nil, off
}

// uint reads an n-byte big-endian integer at off.
func (d *decoder) uint(off, n int) int {
	if off+n > len(d.buf) {
		d.err = errBad
		return 0
	}
	v := 0
	for _, x := range d.buf[off : off+n] {
		v = v<<8 | int(x)
	}
	return v
}
