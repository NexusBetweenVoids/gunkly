package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

const (
	discordURL = "discord://tokensecret1234567890@1234567890"
	gotifyURL  = "gotify://gotify.example.com/AbCdEfGhIjKl"
)

// raw returns the JSON text of a decoded value, to scan for leaks.
func raw(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestUserChannelsWriteOnlyURL(t *testing.T) {
	f := newLinks(t)
	base := "/api/v1/account/channels"
	st, v := f.m.do("POST", base, map[string]string{"url": discordURL, "label": "Phone"})
	expect(t, st, v, 201, "")
	id := field(v, "id").(string)
	if field(v, "kind") != "discord" || field(v, "label") != "Phone" || field(v, "scope") != "user" || field(v, "hint") != "discord" {
		t.Fatalf("view: %v", v)
	}
	for _, leak := range []string{"tokensecret", "discord://", "url"} {
		if strings.Contains(raw(v), leak) {
			t.Fatalf("create response leaks %q: %v", leak, v)
		}
	}
	st, v = f.m.do("GET", base, nil)
	if l, _ := v.([]any); st != 200 || len(l) != 1 || strings.Contains(raw(v), "tokensecret") || strings.Contains(raw(v), "ciphertext") {
		t.Fatalf("list: %d %v", st, v)
	}
	// Other users see nothing, and cannot touch it.
	st, v = f.x.do("GET", base, nil)
	if l, _ := v.([]any); st != 200 || len(l) != 0 {
		t.Fatalf("x list: %v", v)
	}
	for _, c := range []struct{ method, path string }{{"PATCH", ""}, {"DELETE", ""}, {"POST", "/test"}} {
		st, v = f.x.do(c.method, base+"/"+id+c.path, map[string]string{"label": "x"})
		expect(t, st, v, 404, "")
	}
	// Rename and replace the URL (re-validated).
	st, v = f.m.do("PATCH", base+"/"+id, map[string]string{"label": "Tablet"})
	if st != 200 || field(v, "label") != "Tablet" || field(v, "hint") != "discord" {
		t.Fatalf("rename: %d %v", st, v)
	}
	st, v = f.m.do("PATCH", base+"/"+id, map[string]string{"url": "telegram://bottoken123456789@telegram?chats=42"})
	if st != 200 || field(v, "kind") != "telegram" || strings.Contains(raw(v), "bottoken") {
		t.Fatalf("replace: %d %v", st, v)
	}
	st, v = f.m.do("PATCH", base+"/"+id, map[string]string{"url": "logger://"})
	expect(t, st, v, 400, "invalid_channel_url")
	st, v = f.m.do("DELETE", base+"/"+id, nil)
	expect(t, st, v, 204, "")
	st, v = f.m.do("DELETE", base+"/"+id, nil)
	expect(t, st, v, 404, "")
}

func TestNotificationKindsForAnyUser(t *testing.T) {
	f := newLinks(t)
	f.e.user("root", true)
	root := f.e.signedInStepUp("root")
	const path = "/api/v1/notification-kinds"

	// Defaults, visible to a plain member (not just admins).
	st, v := f.m.do("GET", path, nil)
	got := raw(field(v, "enabledNotificationKinds"))
	if st != 200 || !strings.Contains(got, `"discord"`) || !strings.Contains(got, `"ntfy"`) || strings.Contains(got, `"gotify"`) {
		t.Fatalf("defaults: %d %v", st, v)
	}

	// It follows the admin's setting at once, sorted.
	st, v = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"enabledNotificationKinds": []string{"gotify", "discord"}})
	expect(t, st, v, 200, "")
	st, v = f.x.do("GET", path, nil)
	if st != 200 || raw(field(v, "enabledNotificationKinds")) != `["discord","gotify"]` {
		t.Fatalf("custom: %d %v", st, v)
	}
	st, v = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"enabledNotificationKinds": []string{}})
	expect(t, st, v, 200, "")
	if st, v = f.m.do("GET", path, nil); st != 200 || raw(field(v, "enabledNotificationKinds")) != "[]" {
		t.Fatalf("none enabled must be an empty list, not null: %d %v", st, v)
	}

	// Signed-out callers get nothing.
	anon := f.e.client()
	if st, v = anon.do("GET", path, nil); st != 401 {
		t.Fatalf("anonymous: %d %v", st, v)
	}
}

func TestChannelKindAllowlist(t *testing.T) {
	f := newLinks(t)
	f.e.user("root", true)
	root := f.e.signedInStepUp("root")
	base := "/api/v1/account/channels"

	// Defaults: fixed providers only.
	st, v := f.m.do("POST", base, map[string]string{"url": gotifyURL})
	expect(t, st, v, 400, "service_disabled")
	for _, bad := range []string{"logger://", "nope://x", "telegram://tok@telegram?apiurl=http://127.0.0.1", "discord://a@b#frag", ""} {
		st, v = f.m.do("POST", base, map[string]string{"url": bad})
		expect(t, st, v, 400, "invalid_channel_url")
	}
	st, v = root.do("GET", "/api/v1/admin/settings", nil)
	enabled := raw(field(v, "enabledNotificationKinds"))
	if st != 200 || !strings.Contains(enabled, `"discord"`) || strings.Contains(enabled, `"gotify"`) || !strings.Contains(raw(field(v, "availableNotificationKinds")), `"gotify"`) {
		t.Fatalf("settings: %d %v", st, v)
	}

	// Validation of the admin setting itself.
	st, v = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"enabledNotificationKinds": []string{"logger"}})
	expect(t, st, v, 400, "invalid")
	if st, v = f.m.do("PATCH", "/api/v1/admin/settings", map[string]any{"enabledNotificationKinds": []string{"gotify"}}); st != 403 {
		t.Fatalf("non-admin changed the allowlist: %d %v", st, v)
	}

	// Enabling a kind takes effect at once, for channels and for the scheduler's allowlist.
	st, v = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"enabledNotificationKinds": []string{"gotify", "discord", "gotify"}})
	if st != 200 || raw(field(v, "enabledNotificationKinds")) != `["discord","gotify"]` {
		t.Fatalf("enable: %d %v", st, v)
	}
	st, v = f.m.do("POST", base, map[string]string{"url": gotifyURL})
	expect(t, st, v, 201, "")
	gid := field(v, "id").(string)
	if kinds, err := f.e.srv.EnabledKinds(context.Background()); err != nil || !kinds["gotify"] || kinds["telegram"] {
		t.Fatalf("EnabledKinds: %v %v", kinds, err)
	}

	// Disabling it again: new channels are refused, and a test send to the
	// existing one fails without calling the sender.
	st, v = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"enabledNotificationKinds": []string{"discord"}})
	expect(t, st, v, 200, "")
	st, v = f.m.do("POST", base, map[string]string{"url": gotifyURL})
	expect(t, st, v, 400, "service_disabled")
	st, v = f.m.do("POST", base+"/"+gid+"/test", nil)
	if st != 200 || field(v, "sent") != false || len(f.send.sent()) != 0 {
		t.Fatalf("test to disabled kind: %d %v sent=%v", st, v, f.send.sent())
	}

	// null restores the defaults; [] disables everything.
	st, v = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"enabledNotificationKinds": nil})
	if st != 200 || strings.Contains(raw(field(v, "enabledNotificationKinds")), "gotify") || !strings.Contains(raw(field(v, "enabledNotificationKinds")), "telegram") {
		t.Fatalf("reset: %d %v", st, v)
	}
	st, v = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"enabledNotificationKinds": []string{}})
	if st != 200 || raw(field(v, "enabledNotificationKinds")) != "[]" {
		t.Fatalf("empty: %d %v", st, v)
	}
	st, v = f.m.do("POST", base, map[string]string{"url": discordURL})
	expect(t, st, v, 400, "service_disabled")
}

func TestChannelLimits(t *testing.T) {
	f := newLinks(t)
	if err := f.e.st.UpdateInstanceSettings(context.Background(), store.SettingsUpdate{
		Limits: map[string]int{"channels_per_user": 1, "channels_per_household": 1}}); err != nil {
		t.Fatal(err)
	}
	for _, base := range []string{"/api/v1/account/channels", "/api/v1/households/" + f.hA + "/channels"} {
		st, v := f.m.do("POST", base, map[string]string{"url": discordURL})
		expect(t, st, v, 201, "")
		st, v = f.m.do("POST", base, map[string]string{"url": discordURL})
		expect(t, st, v, 409, "limit_reached")
	}
}

func TestHouseholdChannels(t *testing.T) {
	f := newLinks(t)
	base := "/api/v1/households/" + f.hA + "/channels"
	st, v := f.m.do("POST", base, map[string]string{"url": discordURL, "label": "Family"})
	expect(t, st, v, 201, "")
	id := field(v, "id").(string)
	if field(v, "scope") != "household" || strings.Contains(raw(v), "tokensecret") {
		t.Fatalf("view: %v", v)
	}
	// Guests cannot see or manage; outsiders do not know the household exists.
	for _, c := range []struct{ method, path string }{{"GET", ""}, {"POST", ""}, {"PATCH", "/" + id}, {"DELETE", "/" + id}, {"POST", "/" + id + "/test"}} {
		st, v = f.g.do(c.method, base+c.path, map[string]string{"url": discordURL})
		expect(t, st, v, 403, "forbidden")
		st, v = f.b.do(c.method, base+c.path, map[string]string{"url": discordURL})
		expect(t, st, v, 404, "")
	}
	// Another household's owner cannot reach it through their own household's path either.
	st, v = f.b.do("DELETE", "/api/v1/households/"+f.hB+"/channels/"+id, nil)
	expect(t, st, v, 404, "")
	st, v = f.a.do("GET", base, nil)
	if l, _ := v.([]any); st != 200 || len(l) != 1 || strings.Contains(raw(v), "tokensecret") {
		t.Fatalf("list: %d %v", st, v)
	}
	st, v = f.a.do("PATCH", base+"/"+id, map[string]string{"label": "Fam"})
	if st != 200 || field(v, "label") != "Fam" {
		t.Fatalf("patch: %d %v", st, v)
	}
	st, v = f.m.do("DELETE", base+"/"+id, nil)
	expect(t, st, v, 204, "")
}

func TestChannelTestSendAndDeliveryLog(t *testing.T) {
	f := newLinks(t)
	st, v := f.m.do("POST", "/api/v1/account/channels", map[string]string{"url": discordURL, "label": "Phone"})
	expect(t, st, v, 201, "")
	uid := field(v, "id").(string)
	st, v = f.m.do("POST", "/api/v1/households/"+f.hA+"/channels", map[string]string{"url": discordURL, "label": "Family"})
	expect(t, st, v, 201, "")
	hid := field(v, "id").(string)

	// A successful test goes through the real sender with the real URL.
	st, v = f.m.do("POST", "/api/v1/account/channels/"+uid+"/test", nil)
	if st != 200 || field(v, "sent") != true {
		t.Fatalf("test: %d %v", st, v)
	}
	if got := f.send.sent(); len(got) != 1 || got[0] != discordURL {
		t.Fatalf("sender got %v", got)
	}
	// A failing one is recorded with a redacted error, even if the transport
	// quotes the URL back.
	f.send.err = func(u string) error {
		return errors.New("Post \"https://discord.com/api/webhooks/1234567890/tokensecret1234567890\" failed for " + u)
	}
	f.e.clock.Add(time.Minute)
	st, v = f.m.do("POST", "/api/v1/households/"+f.hA+"/channels/"+hid+"/test", nil)
	if st != 200 || field(v, "sent") != false || strings.Contains(raw(v), "tokensecret") || strings.Contains(raw(v), "1234567890") {
		t.Fatalf("failed test: %d %v", st, v)
	}

	// Personal log: the personal test only (private to the user).
	st, v = f.m.do("GET", "/api/v1/account/deliveries", nil)
	items, _ := field(v, "items").([]any)
	if st != 200 || len(items) != 1 || field(items[0], "type") != "test" || field(items[0], "status") != "sent" ||
		field(items[0], "channelLabel") != "Phone" || field(items[0], "kind") != "discord" {
		t.Fatalf("user log: %d %v", st, v)
	}
	// Household log: the household test, visible to members, not to guests or outsiders.
	st, v = f.a.do("GET", "/api/v1/households/"+f.hA+"/deliveries", nil)
	items, _ = field(v, "items").([]any)
	if st != 200 || len(items) != 1 || field(items[0], "status") != "failed" || field(items[0], "channelLabel") != "Family" {
		t.Fatalf("household log: %d %v", st, v)
	}
	for _, leak := range []string{"tokensecret", "1234567890", "discord://"} {
		if strings.Contains(raw(v), leak) {
			t.Fatalf("household log leaks %q: %v", leak, v)
		}
	}
	st, v = f.g.do("GET", "/api/v1/households/"+f.hA+"/deliveries", nil)
	expect(t, st, v, 403, "forbidden")
	st, v = f.x.do("GET", "/api/v1/households/"+f.hA+"/deliveries", nil)
	expect(t, st, v, 404, "")
	// Others never see someone else's personal log.
	st, v = f.x.do("GET", "/api/v1/account/deliveries", nil)
	if items, _ = field(v, "items").([]any); st != 200 || len(items) != 0 {
		t.Fatalf("x log: %v", v)
	}
}

func TestChannelTestRateLimited(t *testing.T) {
	f := newLinks(t)
	st, v := f.m.do("POST", "/api/v1/account/channels", map[string]string{"url": discordURL})
	expect(t, st, v, 201, "")
	id := field(v, "id").(string)
	f.e.srv.rl = newRateLimiter(2, 100, f.e.clock.Now)
	var last int
	for range 4 {
		last, _ = f.m.do("POST", "/api/v1/account/channels/"+id+"/test", nil)
	}
	if last != 429 || len(f.send.sent()) != 2 {
		t.Fatalf("rate limit: %d, %d sends", last, len(f.send.sent()))
	}
}

func TestDeliveryPagination(t *testing.T) {
	f := newLinks(t)
	st, v := f.m.do("POST", "/api/v1/account/channels", map[string]string{"url": discordURL})
	expect(t, st, v, 201, "")
	id := field(v, "id").(string)
	for range 5 {
		f.e.clock.Add(time.Second)
		if err := f.e.st.RecordTestDelivery(context.Background(), f.mA, "", id, true, ""); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	path := "/api/v1/account/deliveries?limit=2"
	for pages := 0; ; pages++ {
		st, v = f.m.do("GET", path, nil)
		items, _ := field(v, "items").([]any)
		if st != 200 || pages > 3 {
			t.Fatalf("page %d: %d %v", pages, st, v)
		}
		for _, it := range items {
			if seen[field(it, "id").(string)] {
				t.Fatal("duplicate across pages")
			}
			seen[field(it, "id").(string)] = true
		}
		next, _ := field(v, "next").(string)
		if next == "" {
			if len(items) != 1 {
				t.Fatalf("last page has %d", len(items))
			}
			break
		}
		path = "/api/v1/account/deliveries?limit=2&before=" + next
	}
	if len(seen) != 5 {
		t.Fatalf("saw %d of 5", len(seen))
	}
	for _, bad := range []string{"limit=0", "limit=9999", "limit=x", "before=garbage", "before=1."} {
		st, v = f.m.do("GET", "/api/v1/account/deliveries?"+bad, nil)
		expect(t, st, v, 400, "invalid")
	}
}

func TestEgressGuardSetting(t *testing.T) {
	ctx := context.Background()
	f := newLinks(t)
	f.e.user("root", true)
	root := f.e.signedInStepUp("root")
	if !f.e.srv.EgressGuardEnabled(ctx) {
		t.Fatal("guard must default on")
	}
	st, v := root.do("PATCH", "/api/v1/admin/settings", map[string]any{"egressGuard": false})
	if st != 200 || field(v, "egressGuard") != false || field(v, "egressGuardLocked") != false || f.e.srv.EgressGuardEnabled(ctx) {
		t.Fatalf("self-hosted off: %d %v", st, v)
	}
	st, v = root.do("PATCH", "/api/v1/admin/settings", map[string]any{"egressGuard": true})
	if st != 200 || !f.e.srv.EgressGuardEnabled(ctx) {
		t.Fatalf("on again: %d %v", st, v)
	}

	// Hosted: forced on, cannot be turned off, even if the stored value says off.
	h := newLinks(t, func(o *Options) { o.HostedMode = true })
	h.e.user("root", true)
	hroot := h.e.signedInStepUp("root")
	st, v = hroot.do("PATCH", "/api/v1/admin/settings", map[string]any{"egressGuard": false})
	expect(t, st, v, 400, "egress_guard_locked")
	off := false
	if err := h.e.st.UpdateInstanceSettings(ctx, store.SettingsUpdate{EgressGuard: &off}); err != nil {
		t.Fatal(err)
	}
	if !h.e.srv.EgressGuardEnabled(ctx) {
		t.Fatal("hosted mode must force the guard on")
	}
	_, v = hroot.do("GET", "/api/v1/admin/settings", nil)
	if field(v, "egressGuard") != true || field(v, "egressGuardLocked") != true {
		t.Fatalf("hosted view: %v", v)
	}
}
