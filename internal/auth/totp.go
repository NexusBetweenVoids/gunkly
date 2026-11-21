package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 TOTP requires HMAC-SHA1
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"net/url"
	"strings"
	"time"
)

// TOTP parameters (RFC 6238 defaults, what every authenticator app supports).
const (
	totpIssuer = "Gunkly"
	totpDigits = 6
	totpPeriod = 30 // seconds
	totpSkew   = 1  // steps accepted either side of now
)

var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateTOTPSecret returns a new 160-bit secret, base32 without padding.
func GenerateTOTPSecret() string {
	b := make([]byte, 20)
	rand.Read(b)
	return totpEncoding.EncodeToString(b)
}

// TOTPURI returns the otpauth:// URI (for a QR code) for secret and the
// user's account name.
func TOTPURI(secret, account string) string {
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", totpIssuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	// The colon separates issuer from account, so escape any in the account.
	label := totpIssuer + ":" + strings.ReplaceAll(url.PathEscape(account), ":", "%3A")
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// ValidateTOTP checks code against secret at now, allowing one step of clock
// skew either way. On success it returns the matched time step; the caller
// must store it and reject any later code whose step is not greater, so a
// code cannot be replayed.
func ValidateTOTP(secret, code string, now time.Time) (step int64, ok bool) {
	if len(code) != totpDigits || strings.Trim(code, "0123456789") != "" {
		return 0, false
	}
	key, err := totpEncoding.DecodeString(strings.ToUpper(secret))
	if err != nil || len(key) == 0 {
		return 0, false
	}
	cur := now.Unix() / totpPeriod
	// Check every window without returning early.
	for s := cur - totpSkew; s <= cur+totpSkew; s++ {
		if subtle.ConstantTimeCompare([]byte(hotp(key, s)), []byte(code)) == 1 && !ok {
			step, ok = s, true
		}
	}
	return step, ok
}

// hotp computes the RFC 4226 code for counter.
func hotp(key []byte, counter int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(counter)) //nolint:gosec // G115: counter is a non-negative time step
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	v %= 1_000_000
	var out [totpDigits]byte
	for i := totpDigits - 1; i >= 0; i-- {
		out[i] = byte('0' + v%10)
		v /= 10
	}
	return string(out[:])
}
