package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/auth"
	"github.com/NexusBetweenVoids/gunkly/internal/db/dbtest"
	"github.com/NexusBetweenVoids/gunkly/internal/egress"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

func TestValidateURL(t *testing.T) {
	for _, tc := range []struct {
		raw     string
		guarded bool
		ok      bool
	}{
		{"https://example.com/hook", true, true},
		{"https://example.com:8443/hook?x=1", true, true},
		{"https://xn--bcher-kva.example/", true, true},
		{"https://8.8.8.8/hook", true, true},
		{"https://[2606:4700:4700::1111]/hook", true, true},
		{"http://example.com/hook", true, false}, // https only while guarded
		{"http://example.com/hook", false, true}, // the admin turned the guard off
		{"http://192.168.1.20:8080/hook", false, true},
		{"http://127.0.0.1:8080/", true, false},
		{"https://127.0.0.1/", true, false},
		{"https://localhost/", true, false},
		{"https://app.localhost/", true, false},
		{"https://nas.local/", true, false},
		{"https://db.internal/", true, false},
		{"https://[::1]/", true, false},
		{"https://[::ffff:127.0.0.1]/", true, false},
		{"https://169.254.169.254/latest/meta-data/", true, false},
		{"https://10.0.0.5/", true, false},
		{"https://2130706433/", true, false}, // decimal 127.0.0.1
		{"https://0x7f.1/", true, false},
		{"https://127.1/", true, false},
		{"https://user:pw@example.com/", true, false},
		{"https://user:pw@example.com/", false, false},
		{"https://example.com/#frag", true, false},
		{"https://example.com./", true, false},
		{"https://example.com:0/", true, false},
		{"https://example.com:99999/", true, false},
		{"https://example.com:/", true, false},
		{"https://exa mple.com/", true, false},
		{"https://exämple.com/", true, false},
		{"https://example.com/\x00", true, false},
		{"ftp://example.com/", false, false},
		{"file:///etc/passwd", false, false},
		{"gopher://example.com/", false, false},
		{"//example.com/", true, false},
		{"example.com", true, false},
		{"https://", true, false},
		{"", true, false},
		{"https://" + strings.Repeat("a", MaxURLLen), true, false},
	} {
		host, err := ValidateURL(tc.raw, tc.guarded)
		if (err == nil) != tc.ok {
			t.Errorf("ValidateURL(%q, guarded=%v) = %q, %v; want ok=%v", tc.raw, tc.guarded, host, err, tc.ok)
		}
		if err == nil && host == "" {
			t.Errorf("ValidateURL(%q) returned no host", tc.raw)
		}
	}
	if h, _ := ValidateURL("https://Example.COM/x", true); h != "example.com" {
		t.Errorf("host = %q", h)
	}
}

func TestSignAndVerify(t *testing.T) {
	body := []byte(`{"id":"x"}`)
	// Independent computation of the documented scheme: HMAC-SHA256 over "<t>.<body>".
	m := hmac.New(sha256.New, []byte("whsec_test"))
	m.Write([]byte("1700000000." + string(body)))
	want := "t=1700000000,v1=" + hex.EncodeToString(m.Sum(nil))
	if got := Sign("whsec_test", 1700000000, body); got != want {
		t.Fatalf("Sign = %q, want %q", got, want)
	}
	now := time.Unix(1700000000, 0)
	if err := Verify("whsec_test", want, body, now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		secret, header string
		body           []byte
		at             time.Time
	}{
		"wrong secret": {"whsec_other", want, body, now},
		"tampered":     {"whsec_test", want, []byte(`{"id":"y"}`), now},
		"too old":      {"whsec_test", want, body, now.Add(6 * time.Minute)},
		"from future":  {"whsec_test", want, body, now.Add(-6 * time.Minute)},
		"retimed":      {"whsec_test", strings.Replace(want, "1700000000", "1700000001", 1), body, now},
		"no signature": {"whsec_test", "t=1700000000", body, now},
		"garbage":      {"whsec_test", "nonsense", body, now},
		"bad time":     {"whsec_test", "t=x,v1=00", body, now},
	} {
		if err := Verify(c.secret, c.header, c.body, c.at); err == nil {
			t.Errorf("%s: verified", name)
		}
	}
}

// recorder is a loopback test server that records requests.
type recorder struct {
	srv   *httptest.Server
	mu    sync.Mutex
	reqs  []*http.Request
	bodys [][]byte
	hits  atomic.Int32
	reply func(w http.ResponseWriter, r *http.Request)
}

func newRecorder(t *testing.T, reply func(w http.ResponseWriter, r *http.Request)) *recorder {
	t.Helper()
	rc := &recorder{reply: reply}
	rc.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc.hits.Add(1)
		b, _ := io.ReadAll(r.Body)
		rc.mu.Lock()
		rc.reqs, rc.bodys = append(rc.reqs, r), append(rc.bodys, b)
		rc.mu.Unlock()
		if rc.reply != nil {
			rc.reply(w, r)
		}
	}))
	t.Cleanup(rc.srv.Close)
	return rc
}

func sender(guarded bool) *Sender {
	return NewSender(func(context.Context) bool { return guarded }, "9.9.9", func() time.Time { return time.Unix(1700000000, 0) })
}

func TestSendSignsAndSetsHeaders(t *testing.T) {
	rc := newRecorder(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	body := []byte(`{"id":"e1","type":"chore.completed"}`)
	res, retry := sender(false).Send(context.Background(), rc.srv.URL+"/hook?token=abc", "whsec_s", Delivery{ID: "d1", EventType: "chore.completed", Body: body})
	if !res.OK || res.HTTPStatus != 204 || retry || res.Error != "" {
		t.Fatalf("res = %+v retry=%v", res, retry)
	}
	r := rc.reqs[0]
	if r.Method != "POST" || r.URL.RequestURI() != "/hook?token=abc" || string(rc.bodys[0]) != string(body) {
		t.Fatalf("request %s %s %q", r.Method, r.URL, rc.bodys[0])
	}
	for k, v := range map[string]string{"Content-Type": "application/json", "User-Agent": "Gunkly-Webhook/9.9.9",
		"Gunkly-Event": "chore.completed", "Gunkly-Delivery": "d1", "Gunkly-Signature": Sign("whsec_s", 1700000000, body)} {
		if got := r.Header.Get(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
}

func TestSendClassifiesResponses(t *testing.T) {
	for _, tc := range []struct {
		status int
		retry  bool
	}{{200, false}, {299, false}, {400, false}, {401, false}, {404, false}, {410, false}, {408, true}, {429, true}, {500, true}, {503, true}} {
		rc := newRecorder(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(tc.status) })
		res, retry := sender(false).Send(context.Background(), rc.srv.URL, "s", Delivery{ID: "d", EventType: "ping", Body: []byte("{}")})
		if res.OK != (tc.status < 300) || retry != tc.retry || res.HTTPStatus != tc.status {
			t.Errorf("status %d: res=%+v retry=%v", tc.status, res, retry)
		}
	}
}

func TestSendDoesNotFollowRedirects(t *testing.T) {
	target := newRecorder(t, nil)
	rc := newRecorder(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.srv.URL+"/elsewhere", http.StatusTemporaryRedirect)
	})
	res, retry := sender(false).Send(context.Background(), rc.srv.URL, "s", Delivery{ID: "d", EventType: "ping", Body: []byte("{}")})
	if res.OK || retry || res.HTTPStatus != 307 || !strings.Contains(res.Error, "redirect") {
		t.Fatalf("res = %+v retry=%v", res, retry)
	}
	if target.hits.Load() != 0 || rc.hits.Load() != 1 {
		t.Fatalf("redirect followed: target hit %d times, origin %d", target.hits.Load(), rc.hits.Load())
	}
}

func TestSendIgnoresAndCapsResponseBody(t *testing.T) {
	rc := newRecorder(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 3<<20)))
	})
	res, _ := sender(false).Send(context.Background(), rc.srv.URL, "s", Delivery{ID: "d", EventType: "ping", Body: []byte("{}")})
	if !res.OK || res.Error != "" {
		t.Fatalf("res = %+v", res)
	}
}

// The guard is mandatory: with it on, loopback and metadata targets are
// refused before a single request leaves, and no secret reaches the target.
func TestSendGuardBlocksLoopbackAndMetadata(t *testing.T) {
	rc := newRecorder(t, nil)
	for _, u := range []string{rc.srv.URL, strings.Replace(rc.srv.URL, "127.0.0.1", "localhost", 1), "https://169.254.169.254/latest/meta-data/",
		"https://[::1]/", "http://" + strings.TrimPrefix(rc.srv.URL, "http://")} {
		res, retry := sender(true).Send(context.Background(), u, "s", Delivery{ID: "d", EventType: "ping", Body: []byte("{}")})
		if res.OK || retry || res.Error == "" {
			t.Errorf("%s: res=%+v retry=%v", u, res, retry)
		}
	}
	if n := rc.hits.Load(); n != 0 {
		t.Fatalf("%d requests reached the loopback server", n)
	}
}

// The dial hook is the authority even when validation passes: a public-looking
// name that resolves to loopback must still be refused. Here the guarded
// client dials a loopback address directly.
func TestGuardedClientRefusesLoopbackDial(t *testing.T) {
	rc := newRecorder(t, nil)
	c := egress.NewNoRedirectClient(egress.Guard{Enabled: true})
	resp, err := c.Get(rc.srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("loopback dial succeeded")
	}
	if classify(err) != "destination address not allowed" || rc.hits.Load() != 0 {
		t.Fatalf("err = %v, hits = %d", err, rc.hits.Load())
	}
	if !egress.AddrBlocked(netip.MustParseAddr("127.0.0.1")) {
		t.Fatal("AddrBlocked(127.0.0.1) = false")
	}
}

func TestSendErrorsDoNotQuoteTheURL(t *testing.T) {
	rc := newRecorder(t, nil)
	u := rc.srv.URL + "/secret-path-token"
	rc.srv.Close()
	res, retry := sender(false).Send(context.Background(), u, "s", Delivery{ID: "d", EventType: "ping", Body: []byte("{}")})
	if res.OK || !retry || res.Error == "" || strings.Contains(res.Error, "secret-path-token") || strings.Contains(res.Error, "127.0.0.1") {
		t.Fatalf("res = %+v retry=%v", res, retry)
	}
}

func testKeyring(t *testing.T, version uint32, keys map[uint32]string) *auth.Keyring {
	t.Helper()
	k, err := auth.NewKeyring(version, keys)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestPrepareSealsWithBoundAD(t *testing.T) {
	k := testKeyring(t, 1, map[uint32]string{1: strings.Repeat("k", auth.MinSecretLen)})
	p, err := Prepare(k, "https://example.com/hook?token=zzz", "d", []string{store.EventChoreCompleted}, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.URLCiphertext+p.SecretCiphertext+p.URLHint, "zzz") || strings.Contains(p.SecretCiphertext, p.Secret) || p.URLHint != "example.com" {
		t.Fatalf("not sealed or hint leaks: %+v", p.NewWebhook)
	}
	if !strings.HasPrefix(p.Secret, "whsec_") || len(p.Secret) < 40 {
		t.Fatalf("secret = %q", p.Secret)
	}
	if u, err := OpenURL(k, p.ID, p.URLCiphertext); err != nil || u != "https://example.com/hook?token=zzz" {
		t.Fatalf("OpenURL = %q, %v", u, err)
	}
	if s, err := OpenSecret(k, p.ID, p.SecretCiphertext); err != nil || s != p.Secret {
		t.Fatalf("OpenSecret = %q, %v", s, err)
	}
	if _, err := OpenSecret(k, "another-id", p.SecretCiphertext); err == nil {
		t.Fatal("secret opens under another webhook's ID")
	}
	if _, err := OpenSecret(k, p.ID, p.URLCiphertext); err == nil {
		t.Fatal("URL ciphertext opens as a secret")
	}
	if _, err := Prepare(k, "http://example.com/", "", nil, true); err == nil {
		t.Fatal("http accepted while guarded")
	}
}

func TestRotateSecretsReencrypts(t *testing.T) {
	ctx := context.Background()
	st := store.New(dbtest.Open(t))
	owner, err := st.CreateUser(ctx, store.NewUser{Username: "o", DisplayName: "o", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	a := store.Actor{UserID: owner.ID}
	h, err := st.CreateHousehold(ctx, a, store.NewHousehold{Name: "H", Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	old := testKeyring(t, 1, map[uint32]string{1: strings.Repeat("a", auth.MinSecretLen)})
	p, err := Prepare(old, "https://example.com/hook", "", []string{store.EventChoreCompleted}, true)
	if err != nil {
		t.Fatal(err)
	}
	p.Max = 5
	if _, err := st.CreateWebhook(ctx, a, h.ID, p.NewWebhook); err != nil {
		t.Fatal(err)
	}
	both := testKeyring(t, 2, map[uint32]string{1: strings.Repeat("a", auth.MinSecretLen), 2: strings.Repeat("b", auth.MinSecretLen)})
	n, err := RotateSecrets(ctx, st, both)
	if err != nil || n != 2 {
		t.Fatalf("rotated %d, %v", n, err)
	}
	if n, err = RotateSecrets(ctx, st, both); err != nil || n != 0 {
		t.Fatalf("second run rotated %d, %v", n, err)
	}
	// The old key is no longer needed: only the new one opens both ciphertexts.
	only := testKeyring(t, 2, map[uint32]string{2: strings.Repeat("b", auth.MinSecretLen)})
	urlCT, secretCT, err := st.WebhookSealed(ctx, a, h.ID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if u, err := OpenURL(only, p.ID, urlCT); err != nil || u != "https://example.com/hook" {
		t.Fatalf("OpenURL = %q, %v", u, err)
	}
	if s, err := OpenSecret(only, p.ID, secretCT); err != nil || s != p.Secret {
		t.Fatalf("OpenSecret = %q, %v", s, err)
	}
}

func FuzzValidateURL(f *testing.F) {
	for _, s := range []string{"https://example.com/hook", "http://127.0.0.1:80/", "https://[::1]/", "https://2130706433/", "https://user@h/",
		"https://example.com:99999", "//x", "https://a b/", "https://\x00/", "HTTPS://EXAMPLE.COM", "https://[fe80::1%25eth0]/", "https://0x7f.1/"} {
		f.Add(s, true)
		f.Add(s, false)
	}
	f.Fuzz(func(t *testing.T, raw string, guarded bool) {
		host, err := ValidateURL(raw, guarded)
		if err != nil {
			return
		}
		if host == "" || len(raw) > MaxURLLen || strings.ContainsAny(raw, " \t\r\n\x00#") {
			t.Fatalf("accepted %q with host %q", raw, host)
		}
		low := strings.ToLower(raw)
		if !strings.HasPrefix(low, "https://") && (guarded || !strings.HasPrefix(low, "http://")) {
			t.Fatalf("accepted scheme of %q (guarded=%v)", raw, guarded)
		}
		if guarded {
			if a, perr := netip.ParseAddr(host); perr == nil && egress.AddrBlocked(a) {
				t.Fatalf("accepted blocked address %q", raw)
			}
			if host == "localhost" || strings.HasSuffix(host, ".localhost") {
				t.Fatalf("accepted %q", raw)
			}
		}
	})
}
