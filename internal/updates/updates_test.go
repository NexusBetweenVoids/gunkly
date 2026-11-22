package updates

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/db/dbtest"
	"github.com/NexusBetweenVoids/gunkly/internal/egress"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

type fake struct {
	*httptest.Server
	mu     sync.Mutex
	status int
	body   string
	hdrs   []http.Header
}

func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{status: 200, body: `{"tag_name":"v1.2.0","body":"## Changes\n- a thing\r\n- <script>alert(1)</script>\u0000\u0007","draft":false}`}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hdrs = append(f.hdrs, r.Header.Clone())
		st, body := f.status, f.body
		f.mu.Unlock()
		w.WriteHeader(st)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fake) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.hdrs)
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func setup(t *testing.T, srv *fake) (*Checker, *store.Store, *clock) {
	t.Helper()
	st := store.New(dbtest.Open(t))
	clk := &clock{t: time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)}
	// Guard off: the fake server is on loopback.
	return &Checker{Store: st, Client: egress.NewClient(egress.Guard{Enabled: false}), URL: srv.URL + "/latest", Now: clk.now}, st, clk
}

func TestCheckCachesAndSendsNothingAboutTheInstance(t *testing.T) {
	srv := newFake(t)
	c, st, clk := setup(t, srv)
	ctx := context.Background()
	if err := c.CheckIfDue(ctx); err != nil {
		t.Fatal(err)
	}
	cache, err := st.UpdateCache(ctx)
	if err != nil || cache.Latest != "v1.2.0" || !cache.CheckedAt.Equal(clk.now()) {
		t.Fatalf("cache: %+v %v", cache, err)
	}
	if strings.ContainsAny(cache.Changelog, "\x00\x07\r") || !strings.Contains(cache.Changelog, "<script>") || !strings.HasPrefix(cache.Changelog, "## Changes\n- a thing\n") {
		t.Fatalf("changelog not normalised: %q", cache.Changelog)
	}
	h := srv.hdrs[0]
	if h.Get("User-Agent") != "gunkly-update-check" || h.Get("Cookie") != "" || h.Get("Authorization") != "" || h.Get("Referer") != "" {
		t.Fatalf("headers: %v", h)
	}
	for k := range h {
		switch k {
		case "User-Agent", "Accept", "Accept-Encoding":
		default:
			t.Errorf("unexpected request header %q", k)
		}
	}
	// Within 12 hours: served from the cache, no request.
	clk.add(11 * time.Hour)
	if err := c.CheckIfDue(ctx); err != nil || srv.calls() != 1 {
		t.Fatalf("second check: calls=%d err=%v", srv.calls(), err)
	}
	clk.add(2 * time.Hour)
	if err := c.CheckIfDue(ctx); err != nil || srv.calls() != 2 {
		t.Fatalf("after interval: calls=%d err=%v", srv.calls(), err)
	}
}

func TestDisabledMakesNoRequest(t *testing.T) {
	srv := newFake(t)
	c, st, _ := setup(t, srv)
	off := false
	if err := st.UpdateInstanceSettings(context.Background(), store.SettingsUpdate{UpdateCheck: &off}); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckIfDue(context.Background()); err != nil || srv.calls() != 0 {
		t.Fatalf("disabled: calls=%d err=%v", srv.calls(), err)
	}
	if s, _ := st.InstanceSettings(context.Background()); s.UpdateCheck {
		t.Fatal("setting not stored")
	}
}

func TestFailureIsRememberedAndKeepsOldResult(t *testing.T) {
	srv := newFake(t)
	c, st, clk := setup(t, srv)
	ctx := context.Background()
	if err := c.CheckIfDue(ctx); err != nil {
		t.Fatal(err)
	}
	srv.mu.Lock()
	srv.status = 503
	srv.mu.Unlock()
	clk.add(13 * time.Hour)
	if err := c.CheckIfDue(ctx); err == nil {
		t.Fatal("503 not reported")
	}
	clk.add(time.Hour) // retried only after the interval, not on every poll
	if err := c.CheckIfDue(ctx); err != nil || srv.calls() != 2 {
		t.Fatalf("retry too soon: calls=%d err=%v", srv.calls(), err)
	}
	cache, _ := st.UpdateCache(ctx)
	if cache.Latest != "v1.2.0" || cache.AttemptedAt.Before(cache.CheckedAt) || cache.CheckedAt.Equal(cache.AttemptedAt) {
		t.Fatalf("cache after failure: %+v", cache)
	}
}

func TestUnusableReleasesAreRejected(t *testing.T) {
	for _, body := range []string{
		`{"tag_name":"not-a-version","body":"x"}`,
		`{"tag_name":"v1.2.3","draft":true}`,
		`{"tag_name":"<script>","body":"x"}`,
		`{"tag_name":"v1.2.3-` + strings.Repeat("a", 80) + `"}`,
		`not json`,
	} {
		srv := newFake(t)
		srv.body = body
		c, st, _ := setup(t, srv)
		if err := c.CheckIfDue(context.Background()); err == nil {
			t.Errorf("%.30q accepted", body)
		}
		if cache, _ := st.UpdateCache(context.Background()); cache.Latest != "" {
			t.Errorf("%.30q cached %q", body, cache.Latest)
		}
	}
}

func TestChangelogCap(t *testing.T) {
	got := PlainText(strings.Repeat("é", MaxChangelog+500))
	if r := []rune(got); len(r) != MaxChangelog+1 || r[len(r)-1] != '…' {
		t.Fatalf("len %d", len(r))
	}
}

// The checker's client is the guarded one in production: a loopback or
// private target must not be reachable even if the URL were redirected there.
func TestGuardedClientBlocksLoopback(t *testing.T) {
	srv := newFake(t)
	c, _, _ := setup(t, srv)
	c.Client = egress.NewClient(egress.Guard{Enabled: true})
	if err := c.CheckIfDue(context.Background()); err == nil || srv.calls() != 0 {
		t.Fatalf("guard on: calls=%d err=%v", srv.calls(), err)
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		running, latest string
		want            bool
	}{
		{"1.0.0", "v1.0.1", true},
		{"v1.0.0", "v1.0.0", false},
		{"1.2.0", "1.1.9", false},
		{"1.2.0", "2.0.0", true},
		{"1.10.0", "1.9.0", false},
		{"1.2.0-rc1", "1.2.0", true},
		{"1.2.0", "1.2.0-rc1", false},
		{"dev", "1.0.0", false},
		{"", "1.0.0", false},
		{"1.0.0", "garbage", false},
		{"v0.3.1+abc", "v0.3.2", true},
	} {
		if got := Newer(c.running, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.running, c.latest, got, c.want)
		}
	}
}

func TestAheadAndStale(t *testing.T) {
	for _, c := range []struct {
		running, latest string
		ahead           bool
	}{
		{"v0.2.0", "v0.1.1", true}, {"0.2.0", "v0.2.0", false}, {"v0.2.0", "v0.3.0", false},
		{"v0.2.1-rc1", "v0.2.0", true}, {"v0.2.0-rc1", "v0.2.0", false}, {"dev", "v0.1.0", false},
	} {
		if got := Ahead(c.running, c.latest); got != c.ahead {
			t.Errorf("Ahead(%q,%q)=%v", c.running, c.latest, got)
		}
	}
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	fresh := store.UpdateCache{Latest: "v0.2.0", CheckedAt: now.Add(-59 * time.Minute)}
	if Stale(fresh, "v0.2.0", now) || Stale(fresh, "dev", now) {
		t.Error("fresh cache reported stale")
	}
	if !Stale(fresh, "v0.3.0-rc1", now.Add(2*time.Minute)) || !Stale(fresh, "v0.2.1", now) || !Stale(store.UpdateCache{}, "dev", now) {
		t.Error("stale cache not reported")
	}
}
