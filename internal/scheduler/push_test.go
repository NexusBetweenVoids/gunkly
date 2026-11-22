package scheduler

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/NexusBetweenVoids/gunkly/internal/ids"
	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/notify"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
	"github.com/NexusBetweenVoids/gunkly/internal/webpush"
)

// pushServer is a fake push service on loopback: it records requests and
// answers with status. It never leaves the machine.
type pushServer struct {
	*httptest.Server
	mu     sync.Mutex
	status int
	reqs   []pushReq
}

type pushReq struct {
	header http.Header
	body   []byte
}

func newPushServer(t *testing.T) *pushServer {
	t.Helper()
	p := &pushServer{status: http.StatusCreated}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		p.mu.Lock()
		p.reqs = append(p.reqs, pushReq{r.Header.Clone(), b})
		st := p.status
		p.mu.Unlock()
		w.WriteHeader(st)
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *pushServer) requests() []pushReq {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]pushReq(nil), p.reqs...)
}

func (p *pushServer) setStatus(s int) {
	p.mu.Lock()
	p.status = s
	p.mu.Unlock()
}

// device is a browser's side of a subscription: the keys it can decrypt with.
type device struct {
	key  *ecdh.PrivateKey
	auth []byte
}

// pushSub registers a subscription for a at endpoint and returns its device.
func (f *fixture) pushSub(a store.Actor, endpoint string) (device, string) {
	f.t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		f.t.Fatal(err)
	}
	d := device{key: key, auth: []byte("0123456789abcdef")}
	id := ids.New()
	sealed, err := notify.SealPush(f.ring, id, webpush.Keys{
		P256dh: base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()), Auth: base64.RawURLEncoding.EncodeToString(d.auth)})
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.st.CreatePushSubscription(f.ctx, a, store.NewPushSubscription{ID: id, Endpoint: endpoint, Ciphertext: sealed, Max: 10}); err != nil {
		f.t.Fatal(err)
	}
	return d, id
}

// open decrypts a push body the way a browser does (RFC 8291).
func (d device) open(t *testing.T, body []byte) notify.PushMessage {
	t.Helper()
	salt, idlen := body[:16], int(body[20])
	if binary.BigEndian.Uint32(body[16:20]) != 4096 || idlen != 65 {
		t.Fatalf("bad coding header")
	}
	asPub, ct := body[21:21+idlen], body[21+idlen:]
	pk, err := ecdh.P256().NewPublicKey(asPub)
	if err != nil {
		t.Fatal(err)
	}
	secret, err := d.key.ECDH(pk)
	if err != nil {
		t.Fatal(err)
	}
	info := append(append([]byte("WebPush: info\x00"), d.key.PublicKey().Bytes()...), asPub...)
	prkKey, _ := hkdf.Extract(sha256.New, secret, d.auth)
	ikm, _ := hkdf.Expand(sha256.New, prkKey, string(info), 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	blk, _ := aes.NewCipher(cek)
	g, _ := cipher.NewGCM(blk)
	pt, err := g.Open(nil, nonce, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	var m notify.PushMessage
	if err := json.Unmarshal(pt[:len(pt)-1], &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// pushWorker returns a worker whose Pusher uses the real egress clients with
// the guard on or off.
func (f *fixture) pushWorker(guard bool, observed *[]string) *Worker {
	w := f.worker()
	w.cfg.Pusher = notify.NewWebPush(f.st, f.ring, "https://gunkly.example", func(context.Context) bool { return guard }, f.clock)
	if observed != nil {
		var mu sync.Mutex
		w.cfg.OnDelivery = func(kind string, ok bool) {
			mu.Lock()
			defer mu.Unlock()
			*observed = append(*observed, kind+map[bool]string{true: ":ok", false: ":fail"}[ok])
		}
	}
	return w
}

func (f *fixture) deliveryStatuses() []string {
	f.t.Helper()
	ds, err := f.st.ListUserDeliveries(f.ctx, f.owner, 50)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []string
	for _, d := range ds {
		out = append(out, d.Service+":"+d.Status)
	}
	return out
}

func TestPushDeliveredWhenGuardOff(t *testing.T) {
	f := newFixture(t)
	srv := newPushServer(t)
	dev, _ := f.pushSub(f.owner, srv.URL+"/push/abc")
	c := f.chore("Dishes", "2026-11-21", "gentle")
	var seen []string
	w := f.pushWorker(false, &seen)
	f.tickAt(w, "2026-11-21 08:59:00")
	f.tickAt(w, "2026-11-21 09:01:00")
	reqs := srv.requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	h := reqs[0].header
	if h.Get("TTL") != "86400" || h.Get("Urgency") != "high" || h.Get("Content-Encoding") != "aes128gcm" || !strings.HasPrefix(h.Get("Authorization"), "vapid t=") {
		t.Fatalf("headers: %v", h)
	}
	m := dev.open(t, reqs[0].body)
	if m.URL != "/chores/"+c.ID || !strings.Contains(m.Body, "Dishes") || m.Title == "" || strings.Contains(m.Body, "/done/") {
		t.Fatalf("payload: %+v", m)
	}
	if got := f.deliveryStatuses(); len(got) != 1 || got[0] != "webpush:sent" {
		t.Fatalf("delivery log: %v", got)
	}
	f.tickAt(w, "2026-11-21 09:06:00")
	if n := len(srv.requests()); n != 1 {
		t.Fatalf("second tick re-sent: %d requests", n)
	}
	subs, _ := f.st.ListPushSubscriptions(f.ctx, f.owner)
	if len(subs) != 1 || subs[0].LastUsedAt.IsZero() {
		t.Fatalf("subscriptions: %+v", subs)
	}
	if len(seen) != 1 || seen[0] != "webpush:ok" {
		t.Fatalf("observed: %v", seen)
	}
}

func TestPushBlockedByEgressGuard(t *testing.T) {
	f := newFixture(t)
	srv := newPushServer(t) // 127.0.0.1: a blocked destination
	f.pushSub(f.owner, srv.URL+"/push/abc")
	f.chore("Dishes", "2026-11-21", "gentle")
	var seen []string
	w := f.pushWorker(true, &seen)
	f.tickAt(w, "2026-11-21 08:59:00")
	f.tickAt(w, "2026-11-21 09:01:00")
	if n := len(srv.requests()); n != 0 {
		t.Fatalf("guard on: %d requests reached a loopback endpoint", n)
	}
	if got := f.deliveryStatuses(); len(got) != 1 || got[0] != "webpush:pending" { // retried with backoff
		t.Fatalf("delivery log: %v", got)
	}
	if len(seen) != 1 || seen[0] != "webpush:fail" {
		t.Fatalf("observed: %v", seen)
	}
}

func TestPushGonePrunesSubscription(t *testing.T) {
	f := newFixture(t)
	srv := newPushServer(t)
	srv.setStatus(http.StatusGone)
	f.pushSub(f.owner, srv.URL+"/push/abc")
	f.chore("Dishes", "2026-11-21", "gentle")
	w := f.pushWorker(false, nil)
	f.tickAt(w, "2026-11-21 08:59:00")
	f.tickAt(w, "2026-11-21 09:01:00")
	if n := len(srv.requests()); n != 1 {
		t.Fatalf("requests = %d", n)
	}
	subs, err := f.st.ListPushSubscriptions(f.ctx, f.owner)
	if err != nil || len(subs) != 0 {
		t.Fatalf("subscription not pruned: %+v %v", subs, err)
	}
	f.tickAt(w, "2026-11-21 09:30:00") // no retry of a failed-for-good delivery
	if n := len(srv.requests()); n != 1 {
		t.Fatalf("retried after 410: %d requests", n)
	}
}

func TestPushRecipientsLikeChannels(t *testing.T) {
	f := newFixture(t)
	srv := newPushServer(t)
	guest := f.member("guest", store.RoleGuest)
	muter := f.member("muter", store.RoleMember)
	other := f.member("other", store.RoleMember)
	f.pushSub(guest, srv.URL+"/g")
	f.pushSub(muter, srv.URL+"/m")
	f.pushSub(other, srv.URL+"/o")
	f.pushSub(f.owner, srv.URL+"/owner")
	c := f.chore("Dishes", "2026-11-21", "gentle", other.UserID)
	if err := f.st.SetChoreMuted(f.ctx, muter, f.hid, c.ID, true); err != nil {
		t.Fatal(err)
	}
	w := f.pushWorker(false, nil)
	f.tickAt(w, "2026-11-21 08:59:00")
	f.tickAt(w, "2026-11-21 09:01:00")
	reqs := srv.requests()
	if len(reqs) != 1 { // assigned to "other" only; guests and muters excluded
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
}

func TestPushQuietHoursDefer(t *testing.T) {
	f := newFixture(t)
	srv := newPushServer(t)
	f.pushSub(f.owner, srv.URL+"/p")
	f.chore("Bins", "2026-11-21", "gentle")
	quiet := &store.QuietHours{StartMin: 8 * 60, EndMin: 10 * 60}
	if _, err := f.st.UpdateHousehold(f.ctx, f.owner, f.hid, store.HouseholdUpdate{QuietHours: &quiet}); err != nil {
		t.Fatal(err)
	}
	w := f.pushWorker(false, nil)
	f.tickAt(w, "2026-11-21 08:59:00")
	f.tickAt(w, "2026-11-21 09:30:00")
	if n := len(srv.requests()); n != 0 {
		t.Fatalf("push during quiet hours: %d", n)
	}
	f.tickAt(w, "2026-11-21 10:00:30")
	if n := len(srv.requests()); n != 1 {
		t.Fatalf("after quiet hours: %d", n)
	}
}

func TestPushQuotaCounts(t *testing.T) {
	f := newFixture(t)
	srv := newPushServer(t)
	f.pushSub(f.owner, srv.URL+"/p")
	f.channel(f.owner, "owner")
	f.chore("Dishes", "2026-11-21", "gentle")
	w := f.pushWorker(false, nil)
	// A per-user quota of 1: the push and the channel share it.
	if err := f.st.UpdateInstanceSettings(f.ctx, store.SettingsUpdate{Limits: map[string]int{"notifications_user_day": 1}}); err != nil {
		t.Fatal(err)
	}
	w.cfg.Limits = limits.New(f.st)
	f.tickAt(w, "2026-11-21 08:59:00")
	f.tickAt(w, "2026-11-21 09:01:00")
	got := f.deliveryStatuses()
	var sentN, failedN int
	for _, g := range got {
		if strings.HasSuffix(g, ":sent") {
			sentN++
		}
		if strings.HasSuffix(g, ":failed") {
			failedN++
		}
	}
	if sentN != 1 || failedN != 1 {
		t.Fatalf("delivery log: %v", got)
	}
}
