package auth

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
)

// RecoveryCodeCount is how many recovery codes a user gets at a time.
const RecoveryCodeCount = 10

// Crockford base32: 32 symbols, so each character is exactly 5 bits. The
// look-alikes i, l and o are not in it and are read as 1, 1 and 0.
const recoveryAlphabet = "0123456789abcdefghjkmnpqrstvwxyz"

const recoveryChars = 12 // 60 bits, shown as xxxx-xxxx-xxxx

// GenerateRecoveryCodes returns n single-use recovery codes for display and
// their hashes for storage (ids.Hash of the normalised code; compare with
// ids.Equal or look up by hash).
func GenerateRecoveryCodes(n int) (codes, hashes []string) {
	codes = make([]string, n)
	hashes = make([]string, n)
	for i := range n {
		b := make([]byte, recoveryChars)
		rand.Read(b)
		for j := range b {
			b[j] = recoveryAlphabet[b[j]&31] // 256 is a multiple of 32: uniform
		}
		codes[i] = fmt.Sprintf("%s-%s-%s", b[0:4], b[4:8], b[8:12])
		hashes[i] = ids.Hash(string(b))
	}
	return codes, hashes
}

// HashRecoveryCode normalises user input (case, dashes, spaces and Crockford
// look-alikes) and returns its storage hash. ok is false when the input cannot
// be a recovery code.
func HashRecoveryCode(input string) (hash string, ok bool) {
	var b strings.Builder
	for _, r := range strings.ToLower(input) {
		switch r {
		case '-', ' ':
			continue
		case 'o':
			r = '0'
		case 'i', 'l':
			r = '1'
		}
		if !strings.ContainsRune(recoveryAlphabet, r) {
			return "", false
		}
		b.WriteRune(r)
	}
	if b.Len() != recoveryChars {
		return "", false
	}
	return ids.Hash(b.String()), true
}

var million = big.NewInt(1_000_000)

// GenerateEmailOTP returns a uniformly random 6-digit code.
func GenerateEmailOTP() string {
	n, _ := rand.Int(rand.Reader, million)
	return fmt.Sprintf("%06d", n.Int64())
}

// HashEmailOTP returns the storage hash of code for one challenge. Binding the
// challenge ID means a stored hash only ever matches its own challenge; the
// small code space is protected by expiry and attempt limits, not the hash.
func HashEmailOTP(challengeID, code string) string {
	return ids.Hash(challengeID + "\x00" + strings.TrimSpace(code))
}
