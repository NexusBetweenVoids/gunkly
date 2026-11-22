package api

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/NexusBetweenVoids/gunkly/internal/notify"
	"github.com/NexusBetweenVoids/gunkly/internal/webpush"
)

const pushEndpoint = "https://fcm.googleapis.com/fcm/send/abc-secret-token-123"

func pushBody(t *testing.T, endpoint string) map[string]any {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"endpoint": endpoint, "keys": map[string]string{
		"p256dh": base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()),
		"auth":   base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef")),
	}}
}

func TestPushSubscriptionsCRUDAndIsolation(t *testing.T) {
	f := newLinks(t)
	base := "/api/v1/account/push-subscriptions"
	body := pushBody(t, pushEndpoint)
	st, v := f.m.do("POST", base, body)
	expect(t, st, v, 201, "")
	id := field(v, "id").(string)
	sum := sha256.Sum256([]byte(pushEndpoint))
	if field(v, "endpointHash") != hex.EncodeToString(sum[:]) {
		t.Fatalf("view: %v", v)
	}
	if strings.Contains(raw(v), "fcm.googleapis") || strings.Contains(raw(v), "p256dh") || strings.Contains(raw(v), "secret-token") {
		t.Fatalf("create response leaks the endpoint or keys: %v", v)
	}
	st, v = f.m.do("GET", base, nil)
	if l, _ := v.([]any); st != 200 || len(l) != 1 || strings.Contains(raw(v), "secret-token") || strings.Contains(raw(v), "ciphertext") {
		t.Fatalf("list: %d %v", st, v)
	}
	// Keys are sealed at rest, bound to the subscription ID.
	secrets, err := f.e.st.PushSecrets(context.Background(), "", 10)
	if err != nil || len(secrets) != 1 || !strings.HasPrefix(secrets[0].Ciphertext, "v1:") {
		t.Fatalf("stored: %+v %v", secrets, err)
	}
	if _, err := notify.OpenPush(f.e.srv.keyring, id, secrets[0].Ciphertext); err != nil {
		t.Fatal(err)
	}
	if _, err := notify.OpenPush(f.e.srv.keyring, "other-id", secrets[0].Ciphertext); err == nil {
		t.Fatal("ciphertext opens under another ID")
	}
	// Isolation: another user sees nothing and cannot delete it.
	st, v = f.x.do("GET", base, nil)
	if l, _ := v.([]any); st != 200 || len(l) != 0 {
		t.Fatalf("x list: %d %v", st, v)
	}
	st, v = f.x.do("DELETE", base+"/"+id, nil)
	expect(t, st, v, 404, "")
	if l, _ := f.m.get(t, base).([]any); len(l) != 1 {
		t.Fatal("foreign delete removed the subscription")
	}
	// Re-registering the same endpoint replaces it (one browser, one entry).
	st, v = f.m.do("POST", base, body)
	expect(t, st, v, 201, "")
	if l, _ := f.m.get(t, base).([]any); len(l) != 1 {
		t.Fatalf("same endpoint twice: %v", l)
	}
	// Another account in the same browser takes the endpoint over.
	st, v = f.x.do("POST", base, body)
	expect(t, st, v, 201, "")
	if l, _ := f.m.get(t, base).([]any); len(l) != 0 {
		t.Fatalf("previous owner keeps it: %v", l)
	}
	id = field(v, "id").(string)
	st, v = f.x.do("DELETE", base+"/"+id, nil)
	expect(t, st, v, 204, "")
	st, v = f.x.do("DELETE", base+"/"+id, nil)
	expect(t, st, v, 404, "")
}

func (c *client) get(t *testing.T, path string) any {
	t.Helper()
	st, v := c.do("GET", path, nil)
	if st != 200 {
		t.Fatalf("GET %s: %d %v", path, st, v)
	}
	return v
}

// Real PushSubscription.toJSON() shapes: always carries expirationTime (null),
// endpoints of very different length and alphabet per vendor.
func TestPushSubscriptionRealBrowserShapes(t *testing.T) {
	f := newLinks(t)
	base := "/api/v1/account/push-subscriptions"
	rnd := func(alphabet string, n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = alphabet[(i*7+3)%len(alphabet)]
		}
		return string(b)
	}
	const b64u = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	for name, ep := range map[string]string{
		"chrome fcm":   "https://fcm.googleapis.com/fcm/send/" + rnd(b64u+":", 150),
		"safari apple": "https://web.push.apple.com/" + rnd(b64u, 160),
		"firefox":      "https://updates.push.services.mozilla.com/wpush/v2/" + rnd(b64u, 400),
		"edge windows": "https://wns2-par02p.notify.windows.com/w/?token=BQYAAAB" + rnd(b64u, 300) + "%2b%2fAbc%3d",
	} {
		for _, exp := range []any{nil, float64(1893456000000)} {
			body := pushBody(t, ep)
			body["expirationTime"] = exp
			st, v := f.m.do("POST", base, body)
			if st != 201 {
				t.Errorf("%s (expirationTime %v): %d %v", name, exp, st, v)
			}
			sum := sha256.Sum256([]byte(ep))
			if field(v, "endpointHash") != hex.EncodeToString(sum[:]) {
				t.Errorf("%s: endpoint stored altered: %v", name, v)
			}
		}
	}
	if l, _ := f.m.get(t, base).([]any); len(l) != 4 {
		t.Fatalf("want 4 devices: %v", l)
	}
}

func TestPushSubscriptionValidation(t *testing.T) {
	f := newLinks(t)
	base := "/api/v1/account/push-subscriptions"
	for _, ep := range []string{
		"https://example.com/push/x",                     // not a push service
		"http://fcm.googleapis.com/fcm/send/x",           // not https
		"https://127.0.0.1/push",                         // IP literal
		"https://localhost/push",                         // not allowlisted
		"https://fcm.googleapis.com.evil.example/push/x", // suffix trick
		"https://user@fcm.googleapis.com/fcm/send/x",     // userinfo
		"https://fcm.googleapis.com:8443/fcm/send/x",     // port
		"",
	} {
		st, v := f.m.do("POST", base, pushBody(t, ep))
		expect(t, st, v, 400, "invalid_push_endpoint")
	}
	good := pushBody(t, pushEndpoint)
	for name, keys := range map[string]map[string]string{
		"short p256dh": {"p256dh": "AAAA", "auth": "MDEyMzQ1Njc4OWFiY2RlZg"},
		"bad auth":     {"p256dh": good["keys"].(map[string]string)["p256dh"], "auth": "AAAA"},
		"empty":        {},
	} {
		st, v := f.m.do("POST", base, map[string]any{"endpoint": pushEndpoint, "keys": keys})
		if st != 400 || code(v) != "invalid_push_keys" {
			t.Errorf("%s: %d %v", name, st, v)
		}
	}
	for name, ep := range map[string]string{
		"over 2048": "https://fcm.googleapis.com/fcm/send/" + strings.Repeat("a", 2100),
	} {
		st, v := f.m.do("POST", base, pushBody(t, ep))
		if st != 400 || code(v) != "invalid_push_endpoint" {
			t.Errorf("%s: %d %v", name, st, v)
		}
	}
	st, v := f.m.do("POST", base, map[string]any{"endpoint": pushEndpoint, "keys": good["keys"], "extra": 1})
	expect(t, st, v, 400, "")
	if l, _ := f.m.get(t, base).([]any); len(l) != 0 {
		t.Fatalf("invalid subscriptions were stored: %v", l)
	}
}

func TestPushSubscriptionLimit(t *testing.T) {
	f := newLinks(t)
	base := "/api/v1/account/push-subscriptions"
	for i := range 10 {
		st, v := f.m.do("POST", base, pushBody(t, "https://fcm.googleapis.com/fcm/send/dev"+string(rune('a'+i))))
		expect(t, st, v, 201, "")
	}
	st, v := f.m.do("POST", base, pushBody(t, "https://fcm.googleapis.com/fcm/send/one-too-many"))
	expect(t, st, v, 409, "")
	// Re-registering an existing endpoint is not a new device.
	st, v = f.m.do("POST", base, pushBody(t, "https://fcm.googleapis.com/fcm/send/deva"))
	expect(t, st, v, 201, "")
}

func TestVAPIDPublicKey(t *testing.T) {
	f := newLinks(t)
	st, v := f.m.do("GET", "/api/v1/push/vapid-public-key", nil)
	expect(t, st, v, 200, "")
	key, _ := field(v, "publicKey").(string)
	raw, err := base64.RawURLEncoding.DecodeString(key)
	if err != nil || len(raw) != 65 || raw[0] != 4 {
		t.Fatalf("public key %q: %v", key, err)
	}
	// Stable across calls, and the private half is stored sealed.
	if _, v = f.x.do("GET", "/api/v1/push/vapid-public-key", nil); field(v, "publicKey") != key {
		t.Fatalf("key changed: %v", v)
	}
	sealed, err := f.e.st.VAPIDSecret(context.Background())
	if err != nil || !strings.HasPrefix(sealed, "v1:") {
		t.Fatalf("sealed key: %q %v", sealed, err)
	}
	priv, err := f.e.srv.keyring.Decrypt(sealed, []byte("vapid:1"))
	if err != nil {
		t.Fatal(err)
	}
	if w, err := webpush.ParseVAPID(priv); err != nil || w.PublicKey() != key {
		t.Fatalf("stored private key does not match: %v", err)
	}
	// Needs a session.
	c := f.e.client()
	st, v = c.do("GET", "/api/v1/push/vapid-public-key", nil)
	expect(t, st, v, 401, "unauthenticated")
}
