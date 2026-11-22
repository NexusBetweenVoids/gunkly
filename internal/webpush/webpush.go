// Package webpush implements the sending side of Web Push with the standard
// library only: message encryption (RFC 8291, aes128gcm content coding of
// RFC 8188), VAPID authentication (RFC 8292, ES256 JWT) and delivery over HTTP
// (RFC 8030). The HTTP client is supplied by the caller, so every request goes
// through the egress guard (ADR 0006).
package webpush

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Limits of the aes128gcm coding as used by push services: one record of at
// most 4096 bytes, of which 16 are the GCM tag and 1 the padding delimiter.
const (
	recordSize = 4096
	// MaxPayload is the longest plaintext Encrypt accepts: the record minus
	// the GCM tag, the delimiter and the header (salt, record size, key).
	MaxPayload = recordSize - 16 - 1 - 86
	authLen    = 16
	saltLen    = 16
	pubLen     = 65 // uncompressed P-256 point
	vapidTTL   = 12 * time.Hour
)

var (
	// ErrGone means the push service no longer knows the subscription (404 or
	// 410): delete it.
	ErrGone = errors.New("webpush: subscription gone")
	// ErrRejected is any other non-2xx answer.
	ErrRejected = errors.New("webpush: push service rejected the message")
	// ErrBadKey is returned for malformed subscription keys.
	ErrBadKey = errors.New("webpush: invalid subscription key")
	// ErrTooLong is returned for payloads over MaxPayload.
	ErrTooLong = errors.New("webpush: payload too long")
)

// Keys are a subscription's encryption keys, base64url as the browser's
// PushSubscription.toJSON() gives them.
type Keys struct {
	P256dh string `json:"p256dh"`
	Auth   string `json:"auth"`
}

// Decode returns the raw user-agent public key (65 bytes, validated as a
// P-256 point) and the 16-byte auth secret. Padded input is accepted.
func (k Keys) Decode() (pub, authSecret []byte, err error) {
	pub, err = b64(k.P256dh)
	if err != nil || len(pub) != pubLen {
		return nil, nil, ErrBadKey
	}
	if _, err = ecdh.P256().NewPublicKey(pub); err != nil {
		return nil, nil, ErrBadKey
	}
	authSecret, err = b64(k.Auth)
	if err != nil || len(authSecret) != authLen {
		return nil, nil, ErrBadKey
	}
	return pub, authSecret, nil
}

// Canonical returns k re-encoded as unpadded base64url.
func (k Keys) Canonical() (Keys, error) {
	pub, a, err := k.Decode()
	if err != nil {
		return Keys{}, err
	}
	return Keys{P256dh: enc(pub), Auth: enc(a)}, nil
}

func enc(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func b64(s string) ([]byte, error) {
	for _, e := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding} {
		if b, err := e.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, ErrBadKey
}

// Encrypt encrypts payload for a subscription (RFC 8291) with a fresh
// ephemeral key and random salt, returning the complete aes128gcm body.
func Encrypt(payload []byte, k Keys) ([]byte, error) {
	pub, authSecret, err := k.Decode()
	if err != nil {
		return nil, err
	}
	as, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return encrypt(payload, pub, authSecret, as, salt)
}

// encrypt is Encrypt with the sender key and salt supplied (RFC test vector).
func encrypt(payload, uaPub, authSecret []byte, as *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	if len(payload) > MaxPayload {
		return nil, ErrTooLong
	}
	ua, err := ecdh.P256().NewPublicKey(uaPub)
	if err != nil {
		return nil, ErrBadKey
	}
	secret, err := as.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPub := as.PublicKey().Bytes()
	keyInfo := append([]byte("WebPush: info\x00"), uaPub...)
	keyInfo = append(keyInfo, asPub...)
	prkKey, err := hkdf.Extract(sha256.New, secret, authSecret)
	if err != nil {
		return nil, err
	}
	ikm, err := hkdf.Expand(sha256.New, prkKey, string(keyInfo), 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, []byte(ikm), salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher([]byte(cek))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// One record: payload, then the 0x02 "last record" delimiter.
	plain := append(append([]byte{}, payload...), 0x02)

	out := make([]byte, 0, saltLen+5+len(asPub)+len(plain)+16)
	out = append(out, salt...)
	out = binary.BigEndian.AppendUint32(out, recordSize)
	out = append(out, pubLen) // key ID length: the sender key is a 65-byte point
	out = append(out, asPub...)
	return gcm.Seal(out, []byte(nonce), plain, nil), nil
}

// VAPID is the application server's identity key pair (RFC 8292).
type VAPID struct {
	key *ecdsa.PrivateKey
}

// NewVAPID generates a P-256 key pair.
func NewVAPID() (*VAPID, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return &VAPID{key: k}, nil
}

// ParseVAPID restores a key pair from PrivateBytes.
func ParseVAPID(raw []byte) (*VAPID, error) {
	k, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), raw)
	if err != nil {
		return nil, ErrBadKey
	}
	return &VAPID{key: k}, nil
}

// PrivateBytes is the 32-byte private scalar, for sealing at rest.
func (v *VAPID) PrivateBytes() ([]byte, error) { return v.key.Bytes() }

// PublicKey is the uncompressed public key, base64url without padding: the
// applicationServerKey the browser subscribes with.
func (v *VAPID) PublicKey() string {
	b, err := v.key.PublicKey.Bytes()
	if err != nil { // a key made by this package is always valid
		return ""
	}
	return enc(b)
}

// Authorization returns the Authorization header value for a push to
// endpoint: an ES256 JWT whose audience is the endpoint's origin.
func (v *VAPID) Authorization(endpoint, subject string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", errors.New("webpush: bad endpoint")
	}
	claims, err := json.Marshal(map[string]any{
		"aud": u.Scheme + "://" + u.Host,
		"exp": now.Add(vapidTTL).Unix(),
		"sub": subject,
	})
	if err != nil {
		return "", err
	}
	signing := enc([]byte(`{"typ":"JWT","alg":"ES256"}`)) + "." + enc(claims)
	h := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, v.key, h[:])
	if err != nil {
		return "", err
	}
	sig := append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	return "vapid t=" + signing + "." + enc(sig) + ", k=" + v.PublicKey(), nil
}

// Urgency values of RFC 8030 section 5.3.
const (
	UrgencyLow    = "low"
	UrgencyNormal = "normal"
	UrgencyHigh   = "high"
)

// Message is one push: the plaintext payload (Post encrypts it), how long the
// push service may hold it and how urgent it is.
type Message struct {
	Payload []byte
	TTL     time.Duration
	Urgency string
}

// Post encrypts m for the subscription and posts it to endpoint with c.
// 404 and 410 answers are ErrGone; other failures are ErrRejected (with the
// status) or the transport error. The endpoint is never part of an error.
func Post(ctx context.Context, c *http.Client, v *VAPID, subject, endpoint string, k Keys, m Message, now time.Time) error {
	body, err := Encrypt(m.Payload, k)
	if err != nil {
		return err
	}
	auth, err := v.Authorization(endpoint, subject, now)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("webpush: bad endpoint")
	}
	h := req.Header
	h.Set("Authorization", auth)
	h.Set("Content-Encoding", "aes128gcm")
	h.Set("Content-Type", "application/octet-stream")
	h.Set("TTL", strconv.FormatInt(int64(m.TTL/time.Second), 10))
	if m.Urgency != "" {
		h.Set("Urgency", m.Urgency)
	}
	resp, err := c.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // drop the URL
		}
		return fmt.Errorf("webpush: send: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return ErrGone
	}
	return fmt.Errorf("%w: status %d", ErrRejected, resp.StatusCode)
}
