package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
)

// MinSecretLen is the minimum length of a configured secret key.
const MinSecretLen = 32

// keyLabel is the HKDF info string; a new purpose gets a new label.
const keyLabel = "gunkly secret encryption v1"

var (
	ErrWeakSecret       = errors.New("auth: secret key too short")
	ErrUnknownKey       = errors.New("auth: unknown key version")
	ErrMalformedCipher  = errors.New("auth: malformed ciphertext")
	ErrDecrypt          = errors.New("auth: decryption failed")
	errNoCurrentVersion = errors.New("auth: current key version not in keyring")
)

// Keyring encrypts secrets at rest (notification URLs, VAPID private keys)
// with AES-256-GCM. Ciphertexts look like "v<version>:<base64(nonce|ct)>";
// new data uses the current version, and older versions stay readable until
// `gunkly rotate-key` has re-encrypted everything.
type Keyring struct {
	current uint32
	aeads   map[uint32]cipher.AEAD
	secret  string // the current secret, for DeriveKey
}

// NewKeyring derives one AES-256 key per configured secret with HKDF-SHA256.
// Versions must be >= 1 and secrets at least MinSecretLen bytes.
func NewKeyring(current uint32, secrets map[uint32]string) (*Keyring, error) {
	k := &Keyring{current: current, aeads: make(map[uint32]cipher.AEAD, len(secrets)), secret: secrets[current]}
	for v, s := range secrets {
		if v == 0 {
			return nil, ErrUnknownKey
		}
		if len(s) < MinSecretLen {
			return nil, ErrWeakSecret
		}
		key, err := hkdf.Key(sha256.New, []byte(s), nil, keyLabel, 32)
		if err != nil {
			return nil, err
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, err
		}
		if k.aeads[v], err = cipher.NewGCM(block); err != nil {
			return nil, err
		}
	}
	if k.aeads[current] == nil {
		return nil, errNoCurrentVersion
	}
	return k, nil
}

// DeriveKey returns a 32-byte key for label from the current secret (HKDF),
// for signing things that need no storage, such as sign-up proof-of-work
// challenges. Rotating the secret changes it; whatever it signed then simply
// stops verifying. Each purpose uses its own label.
func (k *Keyring) DeriveKey(label string) []byte {
	key, err := hkdf.Key(sha256.New, []byte(k.secret), nil, label, 32)
	if err != nil {
		panic(err) // only for an oversized length
	}
	return key
}

// Encrypt seals plaintext under the current key. ad binds the ciphertext to
// its context (for example table, row ID and purpose) so it cannot be moved
// to another row; the same ad must be passed to Decrypt.
func (k *Keyring) Encrypt(plaintext, ad []byte) string {
	aead := k.aeads[k.current]
	nonce := make([]byte, aead.NonceSize()) // Seal grows the slice as needed
	rand.Read(nonce)
	sealed := aead.Seal(nonce, nonce, plaintext, ad)
	return "v" + strconv.FormatUint(uint64(k.current), 10) + ":" + base64.RawURLEncoding.EncodeToString(sealed)
}

// Decrypt opens a ciphertext made by Encrypt with any key in the ring.
func (k *Keyring) Decrypt(ciphertext string, ad []byte) ([]byte, error) {
	v, body, err := parseCiphertext(ciphertext)
	if err != nil {
		return nil, err
	}
	aead := k.aeads[v]
	if aead == nil {
		return nil, ErrUnknownKey
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(body)
	if err != nil || len(raw) < aead.NonceSize()+aead.Overhead() {
		return nil, ErrMalformedCipher
	}
	pt, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], ad)
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}

// NeedsRotation reports whether ciphertext's version prefix is not the
// current one (or cannot be parsed), so rotate-key should re-encrypt it.
func (k *Keyring) NeedsRotation(ciphertext string) bool {
	v, _, err := parseCiphertext(ciphertext)
	return err != nil || v != k.current
}

func parseCiphertext(s string) (uint32, string, error) {
	prefix, body, ok := strings.Cut(s, ":")
	if !ok || len(prefix) < 2 || prefix[0] != 'v' || prefix[1] == '0' {
		return 0, "", ErrMalformedCipher
	}
	v, err := strconv.ParseUint(prefix[1:], 10, 32)
	if err != nil {
		return 0, "", ErrMalformedCipher
	}
	return uint32(v), body, nil
}
