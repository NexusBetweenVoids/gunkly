package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/egress"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
	"github.com/NexusBetweenVoids/gunkly/internal/webpush"
)

// KindWebPush is the delivery-log kind of browser push deliveries.
const KindWebPush = "webpush"

// Push delivery parameters: reminders are useless after a day, and "high"
// wakes a sleeping phone, which is what a due reminder is for.
const (
	PushTTL     = 24 * time.Hour
	PushUrgency = webpush.UrgencyHigh
)

// ErrPushGone is returned when the push service reports the subscription gone
// (404/410): the caller deletes it.
var ErrPushGone = errors.New("notify: push subscription gone")

func pushAD(id string) []byte { return []byte("push:" + id) }

// vapidAD binds the sealed VAPID key to its purpose and key-pair generation
// (there is one pair; a future regeneration would use "vapid:2").
var vapidAD = []byte("vapid:1")

// SealPush encrypts a subscription's keys bound to its ID.
func SealPush(k *auth.Keyring, id string, keys webpush.Keys) (string, error) {
	b, err := json.Marshal(keys)
	if err != nil {
		return "", err
	}
	return k.Encrypt(b, pushAD(id)), nil
}

// OpenPush decrypts keys sealed by SealPush.
func OpenPush(k *auth.Keyring, id, ciphertext string) (webpush.Keys, error) {
	var keys webpush.Keys
	b, err := k.Decrypt(ciphertext, pushAD(id))
	if err != nil {
		return keys, err
	}
	return keys, json.Unmarshal(b, &keys)
}

// PushMessage is what the service worker shows. URL is a same-origin path.
type PushMessage struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
}

// Pusher delivers a push to one subscription. Implementations must send every
// request through the egress guard and must not log endpoints.
type Pusher interface {
	Push(ctx context.Context, subID, endpoint, sealedKeys string, m PushMessage) error
}

// WebPush is the Pusher over the egress-guarded HTTP clients. Like
// SwitchSender, the guard follows an instance setting asked on every send.
type WebPush struct {
	st      *store.Store
	keyring *auth.Keyring
	subject string
	guard   func(context.Context) bool
	guarded *http.Client
	open    *http.Client
	now     func() time.Time

	mu    sync.Mutex
	vapid *webpush.VAPID
}

// NewWebPush returns a WebPush. subject is the VAPID contact claim (an https
// URL or mailto: address); guard returns whether the egress guard is on (it
// must return true when it cannot decide). now may be nil.
func NewWebPush(st *store.Store, k *auth.Keyring, subject string, guard func(context.Context) bool, now func() time.Time) *WebPush {
	if now == nil {
		now = time.Now
	}
	return &WebPush{st: st, keyring: k, subject: subject, guard: guard, now: now,
		guarded: egress.NewClient(egress.Guard{Enabled: true}), open: egress.NewClient(egress.Guard{Enabled: false})}
}

// VAPID returns the instance's key pair, generating and storing it (sealed
// with the keyring) on first use. Concurrent first use on several instances
// converges on the first stored key.
func (w *WebPush) VAPID(ctx context.Context) (*webpush.VAPID, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.vapid != nil {
		return w.vapid, nil
	}
	sealed, err := w.st.VAPIDSecret(ctx)
	if err != nil {
		return nil, err
	}
	if sealed == "" {
		v, err := webpush.NewVAPID()
		if err != nil {
			return nil, err
		}
		priv, err := v.PrivateBytes()
		if err != nil {
			return nil, err
		}
		stored, err := w.st.InsertVAPIDSecret(ctx, w.keyring.Encrypt(priv, vapidAD))
		if err != nil {
			return nil, err
		}
		if stored {
			w.vapid = v
			return v, nil
		}
		if sealed, err = w.st.VAPIDSecret(ctx); err != nil {
			return nil, err
		}
	}
	raw, err := w.keyring.Decrypt(sealed, vapidAD)
	if err != nil {
		return nil, fmt.Errorf("notify: vapid key: %w", err)
	}
	if w.vapid, err = webpush.ParseVAPID(raw); err != nil {
		return nil, err
	}
	return w.vapid, nil
}

// PublicKey is the applicationServerKey browsers subscribe with.
func (w *WebPush) PublicKey(ctx context.Context) (string, error) {
	v, err := w.VAPID(ctx)
	if err != nil {
		return "", err
	}
	return v.PublicKey(), nil
}

// Push implements Pusher.
func (w *WebPush) Push(ctx context.Context, subID, endpoint, sealedKeys string, m PushMessage) error {
	keys, err := OpenPush(w.keyring, subID, sealedKeys)
	if err != nil {
		return errors.New("notify: cannot decrypt push subscription")
	}
	v, err := w.VAPID(ctx)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(m)
	if err != nil {
		return err
	}
	c := w.open
	if w.guard(ctx) {
		c = w.guarded
	}
	err = webpush.Post(ctx, c, v, w.subject, endpoint, keys,
		webpush.Message{Payload: payload, TTL: PushTTL, Urgency: PushUrgency}, w.now())
	if errors.Is(err, webpush.ErrGone) {
		return ErrPushGone
	}
	return err
}

// RotatePushSecrets re-encrypts the VAPID key and every subscription's keys
// not sealed with the keyring's current key version (gunkly rotate-key).
// Idempotent; a row changed meanwhile is skipped. Returns the number
// re-encrypted.
func RotatePushSecrets(ctx context.Context, st *store.Store, k *auth.Keyring) (int, error) {
	n := 0
	sealed, err := st.VAPIDSecret(ctx)
	if err != nil {
		return n, err
	}
	if sealed != "" && k.NeedsRotation(sealed) {
		pt, err := k.Decrypt(sealed, vapidAD)
		if err != nil {
			return n, fmt.Errorf("notify: vapid key: %w", err)
		}
		switch err := st.ReplaceVAPIDSecret(ctx, sealed, k.Encrypt(pt, vapidAD)); {
		case err == nil:
			n++
		case errors.Is(err, store.ErrConflict):
		default:
			return n, err
		}
	}
	after := ""
	for {
		page, err := st.PushSecrets(ctx, after, 500)
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
			pt, err := k.Decrypt(c.Ciphertext, pushAD(c.ID))
			if err != nil {
				return n, fmt.Errorf("notify: push subscription %s: %w", c.ID, err)
			}
			switch err := st.ReplacePushCiphertext(ctx, c.ID, c.Ciphertext, k.Encrypt(pt, pushAD(c.ID))); {
			case err == nil:
				n++
			case errors.Is(err, store.ErrConflict), errors.Is(err, store.ErrNotFound):
			default:
				return n, err
			}
		}
	}
}
