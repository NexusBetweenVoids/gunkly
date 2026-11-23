// Package webhook is outgoing webhooks (ADR 0017): URL validation, the
// per-webhook signing secret (sealed with the keyring), the HMAC signature
// and the guarded sender. Queueing and retries live in internal/store and
// internal/scheduler.
package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/egress"
	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// ErrInvalidURL is returned for a URL that cannot be a webhook target. It
// never carries the URL.
var ErrInvalidURL = errors.New("webhook: invalid URL")

// MaxURLLen bounds a webhook URL.
const MaxURLLen = 2048

// ReplayWindow is how far a receiver should let the signature timestamp
// differ from its clock (documented in docs/webhooks.md).
const ReplayWindow = 5 * time.Minute

// ValidateURL checks a webhook target and returns its host for display. The
// scheme must be https, or http too when the egress guard is off (a
// self-hoster reaching a service on the LAN); no userinfo, fragment, control
// or non-ASCII character (IDN in punycode only), a valid port. With the guard
// on, a literal blocked IP address, "localhost" names and hosts whose last
// label is numeric (ambiguous IPv4 spellings) are rejected up front; the dial
// hook remains the authority at send time.
func ValidateURL(raw string, guarded bool) (host string, err error) {
	if raw == "" || len(raw) > MaxURLLen {
		return "", ErrInvalidURL
	}
	for _, r := range raw {
		if r <= ' ' || r >= 0x7f {
			return "", ErrInvalidURL
		}
	}
	u, perr := url.Parse(raw)
	if perr != nil || u.Opaque != "" || u.User != nil || u.Fragment != "" || u.RawFragment != "" || strings.Contains(raw, "#") {
		return "", ErrInvalidURL
	}
	if u.Scheme != "https" && (guarded || u.Scheme != "http") {
		return "", ErrInvalidURL
	}
	host = strings.ToLower(u.Hostname())
	if host == "" || strings.HasSuffix(host, ".") || strings.Contains(u.Host, "..") {
		return "", ErrInvalidURL
	}
	if p := u.Port(); p != "" {
		if n, perr := strconv.Atoi(p); perr != nil || n < 1 || n > 65535 {
			return "", ErrInvalidURL
		}
	} else if strings.HasSuffix(u.Host, ":") {
		return "", ErrInvalidURL
	}
	if guarded {
		if a, perr := netip.ParseAddr(host); perr == nil {
			if egress.AddrBlocked(a) {
				return "", ErrInvalidURL
			}
		} else if blockedName(host) {
			return "", ErrInvalidURL
		}
	}
	return host, nil
}

func blockedName(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".local") {
		return true
	}
	last := host[strings.LastIndexByte(host, '.')+1:]
	return strings.Trim(last, "0123456789") == "" || strings.HasPrefix(last, "0x")
}

// --- Secrets ---

func secretAD(id string) []byte { return []byte("webhook:" + id) }
func urlAD(id string) []byte    { return []byte("webhook-url:" + id) }

// NewSecret returns a fresh signing secret ("whsec_" and 256 random bits).
func NewSecret() string { return "whsec_" + ids.Token() }

// Prepared is a validated, sealed webhook and the secret to show once.
type Prepared struct {
	store.NewWebhook
	Secret string
}

// Prepare validates rawURL and seals it with a new signing secret for a new
// webhook. The caller sets Max and stores it with store.CreateWebhook.
func Prepare(k *auth.Keyring, rawURL, description string, events []string, guarded bool) (Prepared, error) {
	host, err := ValidateURL(rawURL, guarded)
	if err != nil {
		return Prepared{}, err
	}
	id, secret := ids.New(), NewSecret()
	return Prepared{Secret: secret, NewWebhook: store.NewWebhook{ID: id, Description: description, Events: events,
		URLCiphertext: k.Encrypt([]byte(rawURL), urlAD(id)), URLHint: host, SecretCiphertext: k.Encrypt([]byte(secret), secretAD(id))}}, nil
}

// SealURL validates a replacement URL and seals it for webhook id.
func SealURL(k *auth.Keyring, id, rawURL string, guarded bool) (ciphertext, host string, err error) {
	host, err = ValidateURL(rawURL, guarded)
	if err != nil {
		return "", "", err
	}
	return k.Encrypt([]byte(rawURL), urlAD(id)), host, nil
}

// SealSecret seals a signing secret for webhook id.
func SealSecret(k *auth.Keyring, id, secret string) string {
	return k.Encrypt([]byte(secret), secretAD(id))
}

// OpenURL decrypts a sealed webhook URL.
func OpenURL(k *auth.Keyring, id, ciphertext string) (string, error) {
	b, err := k.Decrypt(ciphertext, urlAD(id))
	return string(b), err
}

// OpenSecret decrypts a sealed signing secret.
func OpenSecret(k *auth.Keyring, id, ciphertext string) (string, error) {
	b, err := k.Decrypt(ciphertext, secretAD(id))
	return string(b), err
}

// RotateSecrets re-encrypts every webhook URL and signing secret not sealed
// with the keyring's current key (gunkly rotate-key). Idempotent and safe
// while the server is up: a row edited meanwhile is skipped. It returns the
// number of ciphertexts re-encrypted.
func RotateSecrets(ctx context.Context, st *store.Store, k *auth.Keyring) (int, error) {
	n := 0
	for _, col := range []struct {
		list    func(context.Context, string, int) ([]store.ChannelSecret, error)
		replace func(context.Context, string, string, string) error
		ad      func(string) []byte
	}{
		{st.WebhookURLSecrets, st.ReplaceWebhookURLCiphertext, urlAD},
		{st.WebhookSigningSecrets, st.ReplaceWebhookSecretCiphertext, secretAD},
	} {
		after := ""
		for {
			page, err := col.list(ctx, after, 500)
			if err != nil {
				return n, err
			}
			if len(page) == 0 {
				break
			}
			for _, c := range page {
				after = c.ID
				if !k.NeedsRotation(c.Ciphertext) {
					continue
				}
				pt, err := k.Decrypt(c.Ciphertext, col.ad(c.ID))
				if err != nil {
					return n, fmt.Errorf("webhook %s: %w", c.ID, err)
				}
				err = col.replace(ctx, c.ID, c.Ciphertext, k.Encrypt(pt, col.ad(c.ID)))
				switch {
				case err == nil:
					n++
				case errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrNotFound):
				default:
					return n, err
				}
			}
		}
	}
	return n, nil
}

// --- Signature ---

// Sign returns the Gunkly-Signature header value for body at Unix time ts:
// "t=<ts>,v1=<hex HMAC-SHA256 of '<ts>.<body>' keyed with the secret>".
func Sign(secret string, ts int64, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(strconv.FormatInt(ts, 10)))
	m.Write([]byte("."))
	m.Write(body)
	return "t=" + strconv.FormatInt(ts, 10) + ",v1=" + hex.EncodeToString(m.Sum(nil))
}

// Verify checks a Gunkly-Signature header against body: the signature must
// match (constant time) and its timestamp be within ReplayWindow of now. It is
// the reference for receivers (docs/webhooks.md).
func Verify(secret, header string, body []byte, now time.Time) error {
	var ts int64
	var sig string
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(part, "=")
		switch k {
		case "t":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return errors.New("webhook: bad timestamp")
			}
			ts = n
		case "v1":
			sig = v
		}
	}
	if ts == 0 || sig == "" {
		return errors.New("webhook: malformed signature")
	}
	want := Sign(secret, ts, body)
	if !hmac.Equal([]byte(want), []byte("t="+strconv.FormatInt(ts, 10)+",v1="+sig)) {
		return errors.New("webhook: signature mismatch")
	}
	if d := now.Sub(time.Unix(ts, 0)); d > ReplayWindow || d < -ReplayWindow {
		return errors.New("webhook: timestamp outside the replay window")
	}
	return nil
}
