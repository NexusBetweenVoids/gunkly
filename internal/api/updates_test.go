package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/egress"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
	"github.com/NexusBetweenVoids/gunkly/internal/updates"
)

func TestAdminUpdates(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Version = "1.0.0" })
	e.user("root", true)
	e.user("plain", false)
	root := e.signedInStepUp("root")
	plain := e.signedIn("plain")
	const path = "/api/v1/admin/updates"

	// Admin only.
	st, v := plain.do("GET", path, nil)
	expect(t, st, v, 403, "")
	st, v = e.client().do("GET", path, nil)
	expect(t, st, v, 401, "")

	// Never checked: running version only.
	st, v = root.do("GET", path, nil)
	expect(t, st, v, 200, "")
	if field(v, "running") != "1.0.0" || field(v, "latest") != "" || field(v, "newer") != false || field(v, "enabled") != true || field(v, "checkedAt") != nil {
		t.Fatalf("fresh: %v", v)
	}

	// A cached newer release, changelog returned as plain text.
	at := time.Date(2026, 11, 22, 9, 0, 0, 0, time.UTC)
	if err := e.st.SetUpdateCache(context.Background(), store.UpdateCache{Latest: "v1.1.0", Changelog: "- <b>fix</b>", CheckedAt: at, AttemptedAt: at}); err != nil {
		t.Fatal(err)
	}
	st, v = root.do("GET", path, nil)
	expect(t, st, v, 200, "")
	if field(v, "latest") != "v1.1.0" || field(v, "newer") != true || field(v, "changelog") != "- <b>fix</b>" || field(v, "checkedAt") != "2026-11-22T09:00:00Z" {
		t.Fatalf("cached: %v", v)
	}

	// The admin toggle: off hides everything, and is visible in settings.
	st, v = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"updateCheck": false})
	expect(t, st, v, 200, "")
	if field(v, "updateCheck") != false {
		t.Fatalf("settings: %v", v)
	}
	st, v = root.do("GET", path, nil)
	expect(t, st, v, 200, "")
	if field(v, "enabled") != false || field(v, "latest") != "" || field(v, "newer") != false || field(v, "changelog") != "" || field(v, "checkedAt") != nil {
		t.Fatalf("disabled: %v", v)
	}
	st, v = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"updateCheck": true})
	expect(t, st, v, 200, "")
	if st, v = root.do("GET", path, nil); field(v, "latest") != "v1.1.0" {
		t.Fatalf("re-enabled: %d %v", st, v)
	}
}

func TestAdminUpdatesDevBuildNeverOutdated(t *testing.T) {
	e := newEnv(t, func(o *Options) { o.Version = "dev" })
	e.user("root", true)
	root := e.signedIn("root")
	if err := e.st.SetUpdateCache(context.Background(), store.UpdateCache{Latest: "v9.0.0", CheckedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if st, v := root.do("GET", "/api/v1/admin/updates", nil); st != 200 || field(v, "newer") != false || field(v, "latest") != "v9.0.0" {
		t.Fatalf("dev: %d %v", st, v)
	}
}

// ghStub stands in for GitHub; tests never reach the real one.
type ghStub struct {
	mu    sync.Mutex
	calls int
	tag   string
	fail  bool
}

func (g *ghStub) count() int { g.mu.Lock(); defer g.mu.Unlock(); return g.calls }

func updatesEnv(t *testing.T, version string) (*env, *ghStub) {
	t.Helper()
	g := &ghStub{tag: "v0.2.0"}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		g.mu.Lock()
		g.calls++
		tag, fail := g.tag, g.fail
		g.mu.Unlock()
		if fail {
			w.WriteHeader(500)
			return
		}
		_, _ = fmt.Fprintf(w, `{"tag_name":%q,"body":"notes"}`, tag)
	}))
	t.Cleanup(ts.Close)
	e := newEnv(t, func(o *Options) {
		o.Version = version
		o.Updates = &updates.Checker{Store: o.Store, Client: egress.NewClient(egress.Guard{Enabled: false}), URL: ts.URL, Now: o.Now}
	})
	return e, g
}

// settle waits for the in-flight refresh to finish.
func settle(t *testing.T, e *env) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for e.srv.updates.Checking() {
		if time.Now().After(deadline) {
			t.Fatal("refresh did not finish")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAdminUpdatesStaleCacheRefreshesInBackground(t *testing.T) {
	e, g := updatesEnv(t, "v0.2.0")
	e.user("root", true)
	root := e.signedIn("root")
	const path = "/api/v1/admin/updates"
	at := e.clock.Now()
	if err := e.st.SetUpdateCache(context.Background(), store.UpdateCache{Latest: "v0.1.1", CheckedAt: at, AttemptedAt: at.Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	// Cache is fresh but older than the running version: instant answer from
	// the cache, plus a background refresh.
	_, v := root.do("GET", path, nil)
	if field(v, "latest") != "v0.1.1" || field(v, "ahead") != true || field(v, "newer") != false || field(v, "checking") != true {
		t.Fatalf("stale: %v", v)
	}
	settle(t, e)
	_, v = root.do("GET", path, nil)
	if field(v, "latest") != "v0.2.0" || field(v, "ahead") != false || field(v, "checking") != false || g.count() != 1 {
		t.Fatalf("refreshed: %v calls=%d", v, g.count())
	}
}

func TestAdminUpdatesRules(t *testing.T) {
	e, g := updatesEnv(t, "v0.2.0")
	e.user("root", true)
	root := e.signedIn("root")
	const path = "/api/v1/admin/updates"
	get := func() map[string]any { _, v := root.do("GET", path, nil); settle(t, e); return v.(map[string]any) }

	// Never checked: refresh.
	get()
	if g.count() != 1 {
		t.Fatalf("never checked: %d calls", g.count())
	}
	// Fresh: nothing, including after 59 minutes.
	e.clock.Add(59 * time.Minute)
	get()
	if g.count() != 1 {
		t.Fatalf("fresh cache: %d calls", g.count())
	}
	// Older than an hour: refresh, and a failure is remembered.
	e.clock.Add(2 * time.Minute)
	g.mu.Lock()
	g.fail = true
	g.mu.Unlock()
	get()
	if g.count() != 2 {
		t.Fatalf("1h rule: %d calls", g.count())
	}
	// The 5-minute floor holds after a failure, for GET and for Check now.
	e.clock.Add(4 * time.Minute)
	get()
	st, v := root.do("POST", path+"/check", nil)
	expect(t, st, v, 200, "")
	if field(v, "checking") != false || g.count() != 2 {
		t.Fatalf("floor: %v calls=%d", v, g.count())
	}
	// Past the floor Check now calls GitHub even though the cache is fresh enough.
	e.clock.Add(2 * time.Minute)
	g.mu.Lock()
	g.fail, g.tag = false, "v0.3.0"
	g.mu.Unlock()
	st, v = root.do("POST", path+"/check", nil)
	expect(t, st, v, 200, "")
	if field(v, "checking") != true {
		t.Fatalf("check now: %v", v)
	}
	settle(t, e)
	if v := get(); g.count() != 3 || v["latest"] != "v0.3.0" || v["newer"] != true {
		t.Fatalf("after check now: %v calls=%d", v, g.count())
	}
}

func TestAdminUpdatesCheckPolicy(t *testing.T) {
	e, g := updatesEnv(t, "v0.2.0")
	e.user("root", true)
	e.user("plain", false)
	root := e.signedInStepUp("root")
	const path = "/api/v1/admin/updates/check"
	st, v := e.signedIn("plain").do("POST", path, nil)
	expect(t, st, v, 403, "")
	st, v = e.client().do("POST", path, nil)
	expect(t, st, v, 401, "")
	resp := root.raw("POST", path, map[string]string{}, "")
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("without CSRF: %d", resp.StatusCode)
	}
	for _, r := range e.srv.routes {
		if r.pattern == "POST /api/v1/admin/updates/check" && r.policy != admin.noToken() {
			t.Errorf("policy %+v", r.policy)
		}
	}
	// Switched off: never asks, never shows anything.
	st, v = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"updateCheck": false})
	expect(t, st, v, 200, "")
	st, v = root.do("POST", path, nil)
	expect(t, st, v, 200, "")
	_, v2 := root.do("GET", "/api/v1/admin/updates", nil)
	if field(v, "enabled") != false || field(v, "checking") != false || field(v2, "checking") != false || g.count() != 0 {
		t.Fatalf("disabled: %v %v calls=%d", v, v2, g.count())
	}
}

func TestAdminUpdatesDevBuildRefreshesButIsNeverOutdated(t *testing.T) {
	e, g := updatesEnv(t, "dev")
	e.user("root", true)
	root := e.signedIn("root")
	root.do("GET", "/api/v1/admin/updates", nil)
	settle(t, e)
	_, v := root.do("GET", "/api/v1/admin/updates", nil)
	if field(v, "latest") != "v0.2.0" || field(v, "newer") != false || field(v, "ahead") != false || g.count() != 1 {
		t.Fatalf("dev: %v calls=%d", v, g.count())
	}
}
