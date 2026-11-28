package notify

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

func channelAD(channelID string) []byte { return []byte("channel:" + channelID) }

// Prepare validates a user-supplied channel URL against the enabled kinds and
// seals it for a new channel. The caller sets Max and stores the result with
// store.CreateUserChannel / CreateHouseholdChannel.
func Prepare(k *auth.Keyring, rawURL, label string, enabled map[string]bool) (store.NewChannel, error) {
	kind, err := ValidateURL(rawURL, enabled)
	if err != nil {
		return store.NewChannel{}, err
	}
	id := ids.New()
	return store.NewChannel{ID: id, Service: kind, Ciphertext: Seal(k, id, rawURL), Label: label, Hint: Hint(kind, rawURL)}, nil
}

// Reseal validates a replacement URL for an existing channel and returns the
// store.ChannelUpdate fields (Service, Ciphertext) for it.
func Reseal(k *auth.Keyring, channelID, rawURL string, enabled map[string]bool) (store.ChannelUpdate, error) {
	kind, err := ValidateURL(rawURL, enabled)
	if err != nil {
		return store.ChannelUpdate{}, err
	}
	return store.ChannelUpdate{Service: kind, Ciphertext: Seal(k, channelID, rawURL), Hint: Hint(kind, rawURL)}, nil
}

// Hint is what may be shown about a channel after saving. It never contains
// characters of the secret: a fixed provider shows its kind (plus its fixed
// host where it has one); a service whose URL host is a server the user chose
// shows that host; every other service (its host part is a key, an ID or a
// phone number) shows its kind only.
func Hint(kind, rawURL string) string {
	if f, ok := fixedServices[kind]; ok {
		if f.host != "" && f.host != kind {
			return kind + " · " + f.host
		}
		return kind
	}
	if !serverHostKinds[kind] {
		return kind
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return kind
	}
	return u.Hostname()
}

// serverHostKinds are the optional kinds whose URL host names a server, not a
// credential. A kind missing here shows only its name, so a new service is
// safe by default. Migration 0023 resets stored hints of the other kinds.
var serverHostKinds = map[string]bool{
	KindNtfySelfHosted: true, KindGeneric: true, KindSMTP: true, KindMQTT: true, KindXMPP: true,
	KindGotify: true, KindMatrix: true, KindMattermost: true, KindRocketChat: true, KindZulip: true,
	KindHomeAssistant: true, KindSignal: true, KindBark: true, KindPagerDuty: true, KindOpsgenie: true,
	KindLark: true,
}

// Seal encrypts a channel URL bound to its channel ID.
func Seal(k *auth.Keyring, channelID, rawURL string) string {
	return k.Encrypt([]byte(rawURL), channelAD(channelID))
}

// Open decrypts a channel URL sealed by Seal.
func Open(k *auth.Keyring, channelID, ciphertext string) (string, error) {
	b, err := k.Decrypt(ciphertext, channelAD(channelID))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// RotateSecrets re-encrypts every channel URL not sealed with the keyring's
// current key version (gunkly rotate-key). It is idempotent and safe to run
// while the server is up: a channel edited meanwhile is skipped (its new
// ciphertext already uses the current key). Returns the number re-encrypted.
func RotateSecrets(ctx context.Context, st *store.Store, k *auth.Keyring) (int, error) {
	n, after := 0, ""
	for {
		page, err := st.ChannelSecrets(ctx, after, 500)
		if err != nil {
			return n, err
		}
		if len(page) == 0 {
			return n, nil
		}
		for _, c := range page {
			after = c.ID
			if !k.NeedsRotation(c.Ciphertext) {
				continue
			}
			pt, err := Open(k, c.ID, c.Ciphertext)
			if err != nil {
				return n, fmt.Errorf("notify: channel %s: %w", c.ID, err)
			}
			err = st.ReplaceChannelCiphertext(ctx, c.ID, c.Ciphertext, Seal(k, c.ID, pt))
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
