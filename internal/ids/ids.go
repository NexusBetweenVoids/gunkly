// Package ids generates public identifiers and secret tokens.
package ids

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"time"
)

// New returns a random UUIDv7 string (RFC 9562). Time-ordered, not guessable.
func New() string {
	var b [16]byte
	rand.Read(b[:]) // crypto/rand.Read never returns an error (Go 1.24+).
	binary.BigEndian.PutUint64(b[:8], uint64(time.Now().UnixMilli())<<16|uint64(binary.BigEndian.Uint16(b[6:8])))
	b[6] = 0x70 | b[6]&0x0f
	b[8] = 0x80 | b[8]&0x3f
	var s [36]byte
	hex.Encode(s[0:8], b[0:4])
	s[8] = '-'
	hex.Encode(s[9:13], b[4:6])
	s[13] = '-'
	hex.Encode(s[14:18], b[6:8])
	s[18] = '-'
	hex.Encode(s[19:23], b[8:10])
	s[23] = '-'
	hex.Encode(s[24:], b[10:])
	return string(s[:])
}

// Token returns a URL-safe random secret with 256 bits of entropy.
func Token() string {
	var b [32]byte
	rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// Hash returns the hex SHA-256 of a secret token, for storage.
func Hash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// Equal compares two hashes in constant time.
func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
