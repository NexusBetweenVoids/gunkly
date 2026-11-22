package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Sign-up proof-of-work, compatible with ALTCHA (https://altcha.org): the
// server picks a random secret number n in [0, MaxNumber] and hands out
// challenge = SHA-256(salt + n) and signature = HMAC-SHA-256(key, challenge).
// The client finds n by trying numbers until the hash matches. The signature
// makes challenges unforgeable without server state; the salt carries the
// expiry; single use is enforced by the caller recording used challenges
// until that expiry.

// PoWAlgorithm is the only hash the challenges use.
const PoWAlgorithm = "SHA-256"

// PoWTTL is how long a challenge can be solved and submitted.
const PoWTTL = 10 * time.Minute

// Default and bounds for the difficulty (the largest number the client may
// have to try). About 50k-100k takes a laptop around a second.
const (
	DefaultPoWMaxNumber = 75_000
	MinPoWMaxNumber     = 1_000
	MaxPoWMaxNumber     = 1_000_000
)

var (
	// ErrPoWInvalid means the solution is malformed, wrongly signed or wrong.
	ErrPoWInvalid = errors.New("auth: invalid proof of work")
	// ErrPoWExpired means the challenge is older than PoWTTL.
	ErrPoWExpired = errors.New("auth: proof of work expired")
)

// PoWChallenge is what GET /signup/challenge returns.
type PoWChallenge struct {
	Algorithm string `json:"algorithm"`
	Challenge string `json:"challenge"`
	Salt      string `json:"salt"`
	MaxNumber int    `json:"maxnumber"`
	Signature string `json:"signature"`
}

// PoWSolution is what the client submits: the challenge fields it received
// and the number it found.
type PoWSolution struct {
	Algorithm string `json:"algorithm"`
	Challenge string `json:"challenge"`
	Salt      string `json:"salt"`
	Signature string `json:"signature"`
	Number    int    `json:"number"`
}

// PoW issues and verifies challenges. Safe for concurrent use.
type PoW struct {
	key       []byte
	maxNumber int
}

// NewPoW returns a PoW signing with key (see Keyring.DeriveKey) and asking
// clients to search up to maxNumber (clamped to the allowed range).
func NewPoW(key []byte, maxNumber int) *PoW {
	return &PoW{key: key, maxNumber: min(max(maxNumber, MinPoWMaxNumber), MaxPoWMaxNumber)}
}

func (p *PoW) sign(challenge string) string {
	m := hmac.New(sha256.New, p.key)
	m.Write([]byte(challenge))
	return hex.EncodeToString(m.Sum(nil))
}

func powHash(salt string, n int) string {
	h := sha256.Sum256([]byte(salt + strconv.Itoa(n)))
	return hex.EncodeToString(h[:])
}

// New creates a challenge that expires PoWTTL after now.
func (p *PoW) New(now time.Time) PoWChallenge {
	var b [12]byte
	rand.Read(b[:])
	salt := hex.EncodeToString(b[:]) + "?expires=" + strconv.FormatInt(now.Add(PoWTTL).Unix(), 10) + "&"
	// The modulo bias of 64 random bits over at most a million values is nil.
	var r [8]byte
	rand.Read(r[:])
	n := int(binary.BigEndian.Uint64(r[:]) % uint64(p.maxNumber+1)) //nolint:gosec // G115: maxNumber is clamped to 1..1e6
	c := powHash(salt, n)
	return PoWChallenge{Algorithm: PoWAlgorithm, Challenge: c, Salt: salt, MaxNumber: p.maxNumber, Signature: p.sign(c)}
}

// Verify checks a submitted solution at time now. It returns the challenge
// (to record as used) and when it expires, or ErrPoWInvalid / ErrPoWExpired.
// It does not know whether the challenge was used before.
func (p *PoW) Verify(s PoWSolution, now time.Time) (challenge string, expires time.Time, err error) {
	if s.Algorithm != PoWAlgorithm || len(s.Challenge) != 64 || len(s.Signature) != 64 || len(s.Salt) > 128 || s.Number < 0 || s.Number > p.maxNumber {
		return "", time.Time{}, ErrPoWInvalid
	}
	// Signature first: nothing else is trusted (not even the expiry in the salt) before it.
	if subtle.ConstantTimeCompare([]byte(p.sign(s.Challenge)), []byte(strings.ToLower(s.Signature))) != 1 {
		return "", time.Time{}, ErrPoWInvalid
	}
	if subtle.ConstantTimeCompare([]byte(powHash(s.Salt, s.Number)), []byte(s.Challenge)) != 1 {
		return "", time.Time{}, ErrPoWInvalid
	}
	exp, ok := saltExpiry(s.Salt)
	if !ok {
		return "", time.Time{}, ErrPoWInvalid
	}
	if !now.Before(exp) {
		return "", time.Time{}, ErrPoWExpired
	}
	return s.Challenge, exp, nil
}

// saltExpiry reads the "?expires=<unix>&" part of a salt.
func saltExpiry(salt string) (time.Time, bool) {
	_, rest, ok := strings.Cut(salt, "?expires=")
	if !ok {
		return time.Time{}, false
	}
	n, _, _ := strings.Cut(rest, "&")
	sec, err := strconv.ParseInt(n, 10, 64)
	if err != nil || sec <= 0 {
		return time.Time{}, false
	}
	return time.Unix(sec, 0).UTC(), true
}
