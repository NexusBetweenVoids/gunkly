// Package auth holds credential primitives: password hashing, TOTP, recovery
// codes, email one-time codes and encryption of secrets at rest.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters (OWASP minimum: 19 MiB, 2 passes, 1 lane), chosen to
// fit small self-hosted machines.
const (
	argonMemory  = 19456 // KiB
	argonTime    = 2
	argonThreads = 1
	argonSaltLen = 16
	argonKeyLen  = 32
)

// Password length limits. Minimum counts characters; maximum counts bytes to
// bound hashing cost.
const (
	DefaultMinPasswordLen = 10
	MaxPasswordBytes      = 256
)

var (
	ErrPasswordTooShort = errors.New("auth: password too short")
	ErrPasswordTooLong  = errors.New("auth: password too long")
	ErrPasswordInvalid  = errors.New("auth: password is not valid UTF-8")
	ErrMalformedHash    = errors.New("auth: malformed password hash")
)

// ValidatePassword checks a new password against the length policy. minLen
// <= 0 means DefaultMinPasswordLen.
func ValidatePassword(pw string, minLen int) error {
	if minLen <= 0 {
		minLen = DefaultMinPasswordLen
	}
	switch {
	case len(pw) > MaxPasswordBytes:
		return ErrPasswordTooLong
	case !utf8.ValidString(pw):
		return ErrPasswordInvalid
	case utf8.RuneCountInString(pw) < minLen:
		return ErrPasswordTooShort
	}
	return nil
}

// HashPassword returns an Argon2id hash in PHC string format:
// $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>.
func HashPassword(pw string) string {
	salt := make([]byte, argonSaltLen)
	rand.Read(salt)
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return encodePHC(argonParams{argonMemory, argonTime, argonThreads}, salt, key)
}

// VerifyPassword reports whether pw matches the encoded hash, comparing in
// constant time. It returns ErrMalformedHash for an unparseable hash.
func VerifyPassword(encoded, pw string) (bool, error) {
	p, salt, want, err := decodePHC(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(pw), salt, p.time, p.memory, p.threads, uint32(len(want))) //nolint:gosec // G115: len(want) is bounded by the decoded PHC hash length
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// NeedsRehash reports whether encoded was made with parameters other than the
// current ones (or cannot be parsed), so it should be replaced after a
// successful login.
func NeedsRehash(encoded string) bool {
	p, salt, key, err := decodePHC(encoded)
	return err != nil || p != (argonParams{argonMemory, argonTime, argonThreads}) ||
		len(salt) != argonSaltLen || len(key) != argonKeyLen
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

var b64 = base64.RawStdEncoding

func encodePHC(p argonParams, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.memory, p.time, p.threads, b64.EncodeToString(salt), b64.EncodeToString(key))
}

// decodePHC parses a PHC string and bounds its parameters so a corrupted or
// hostile row cannot make verification allocate or spin without limit.
func decodePHC(s string) (p argonParams, salt, key []byte, err error) {
	parts := strings.Split(s, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" ||
		parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return p, nil, nil, ErrMalformedHash
	}
	var threads uint32
	// Sscanf is lenient; the round-trip comparison enforces canonical form.
	n, _ := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &threads)
	if n != 3 || parts[3] != fmt.Sprintf("m=%d,t=%d,p=%d", p.memory, p.time, threads) ||
		p.memory < 8*threads || p.memory > 1<<20 || p.time < 1 || p.time > 16 || threads < 1 || threads > 16 {
		return p, nil, nil, ErrMalformedHash
	}
	p.threads = uint8(threads)
	if salt, err = b64.Strict().DecodeString(parts[4]); err != nil || len(salt) < 8 || len(salt) > 64 {
		return p, nil, nil, ErrMalformedHash
	}
	if key, err = b64.Strict().DecodeString(parts[5]); err != nil || len(key) < 16 || len(key) > 64 {
		return p, nil, nil, ErrMalformedHash
	}
	return p, salt, key, nil
}
