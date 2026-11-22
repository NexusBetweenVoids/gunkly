package api

import (
	"context"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
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
