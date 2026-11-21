package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NexusBetweenVoids/gunkly/internal/limits"
	"github.com/NexusBetweenVoids/gunkly/internal/notify"
	"github.com/NexusBetweenVoids/gunkly/internal/store"
)

// logBuf is a goroutine-safe log sink.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) { l.mu.Lock(); defer l.mu.Unlock(); return l.b.Write(p) }
func (l *logBuf) String() string              { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// fakeSender records what it is asked to send.
type fakeSender struct {
	mu   sync.Mutex
	urls []string
	msgs []notify.Message
	err  func(rawURL string) error
}

func (f *fakeSender) Send(_ context.Context, rawURL string, m notify.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.urls, f.msgs = append(f.urls, rawURL), append(f.msgs, m)
	if f.err != nil {
		return f.err(rawURL)
	}
	return nil
}

func (f *fakeSender) sent() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.urls...)
}

type linkFixture struct {
	*hhFixture
	logs       *logBuf
	send       *fakeSender
	chore      store.Chore
	oA, mA, gA store.Actor // the fixture users as store actors
}

func newLinks(t *testing.T, mutate ...func(*Options)) *linkFixture {
	t.Helper()
	f := &linkFixture{logs: &logBuf{}, send: &fakeSender{}}
	f.hhFixture = newHH(t, append([]func(*Options){func(o *Options) {
		o.Logger = slog.New(slog.NewJSONHandler(f.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		o.Sender = f.send
	}}, mutate...)...)
	ctx := context.Background()
	for name, dst := range map[string]*store.Actor{"a": &f.oA, "m": &f.mA, "g": &f.gA} {
		u, err := f.e.st.UserByLogin(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		*dst = store.Actor{UserID: u.ID}
	}
	c, err := f.e.st.CreateChore(ctx, f.oA, f.hA, store.ChoreInput{
		Name: "Clean gutters", Notes: "SECRET-NOTES", Link: "https://secret.example/manual",
		Kind: store.KindRecurring, ScheduleType: store.ScheduleInterval, IntervalN: 1, IntervalUnit: "week",
		Anchor: store.AnchorFromCompletion, FirstDue: "2026-11-21", DueDate: "2026-11-21",
		NextDue: time.Date(2026, 11, 21, 8, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	f.chore = c
	return f
}

func (f *linkFixture) link(t *testing.T, a store.Actor) string {
	t.Helper()
	tok, err := f.e.st.CreateMarkDone(context.Background(), store.MarkDoneLink{ChoreID: f.chore.ID, OccurrenceDue: f.chore.NextDue,
		UserID: a.UserID, ExpiresAt: f.e.clock.Now().Add(store.MarkDoneMaxTTL)})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

type anonResp struct {
	status int
	h      http.Header
	body   string
}

// anon sends a cookieless, session-less request, like a browser opening a
// link from a notification.
func (e *env) anon(method, path string, hdr map[string]string) anonResp {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.ts.URL+path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return anonResp{resp.StatusCode, resp.Header, string(b)}
}

func (f *linkFixture) completions(t *testing.T) int {
	t.Helper()
	cs, err := f.e.st.ListCompletions(context.Background(), f.oA, f.hA, f.chore.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range cs {
		if c.RevertedAt.IsZero() {
			n++
		}
	}
	return n // reverted entries stay in the history
}

func (f *linkFixture) dueDate(t *testing.T) string {
	t.Helper()
	c, err := f.e.st.Chore(context.Background(), f.oA, f.hA, f.chore.ID)
	if err != nil {
		t.Fatal(err)
	}
	return c.DueDate
}

// formAction extracts the form action from a done page.
func formAction(t *testing.T, body string) string {
	t.Helper()
	_, rest, ok := strings.Cut(body, `<form method="post" action="`)
	if !ok {
		t.Fatalf("no form in %q", body)
	}
	a, _, _ := strings.Cut(rest, `"`)
	return a
}

func noSetCookie(t *testing.T, r anonResp) {
	t.Helper()
	if r.h.Get("Set-Cookie") != "" {
		t.Fatalf("Set-Cookie on a token link: %q", r.h.Get("Set-Cookie"))
	}
	if r.h.Get("Cache-Control") != "no-store" || r.h.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("headers: %v", r.h)
	}
}

func TestMarkDoneFlow(t *testing.T) {
	f := newLinks(t)
	tok := f.link(t, f.mA)
	due := f.dueDate(t)

	// GET shows the chore name and a POST form, and changes nothing, however often.
	for range 3 {
		r := f.e.anon("GET", "/done/"+tok, nil)
		if r.status != 200 || !strings.Contains(r.body, "Clean gutters") || formAction(t, r.body) != "/done/"+tok {
			t.Fatalf("GET: %d %s", r.status, r.body)
		}
		if strings.Contains(r.body, "SECRET-NOTES") || strings.Contains(r.body, "secret.example") ||
			strings.Contains(r.body, "<script") || strings.Contains(r.body, "style=") || strings.Contains(r.body, "<style") {
			t.Fatalf("page leaks or is not CSP-compatible: %s", r.body)
		}
		if !strings.HasPrefix(r.h.Get("Content-Type"), "text/html") || !strings.Contains(r.h.Get("Content-Security-Policy"), "script-src 'self'") {
			t.Fatalf("headers: %v", r.h)
		}
		noSetCookie(t, r)
	}
	if f.completions(t) != 0 || f.dueDate(t) != due {
		t.Fatal("GET changed the chore")
	}
	if css := f.e.anon("GET", "/done.css", nil); css.status != 200 || !strings.HasPrefix(css.h.Get("Content-Type"), "text/css") {
		t.Fatalf("css: %d %v", css.status, css.h)
	}

	// POST completes once and offers undo.
	r := f.e.anon("POST", "/done/"+tok, map[string]string{"Origin": f.e.ts.URL, "Sec-Fetch-Site": "same-origin"})
	if r.status != 200 || !strings.Contains(r.body, "Done") || !strings.Contains(r.body, "Undo") {
		t.Fatalf("POST: %d %s", r.status, r.body)
	}
	noSetCookie(t, r)
	undo := formAction(t, r.body)
	if !strings.HasPrefix(undo, "/done/undo/") || f.completions(t) != 1 || f.dueDate(t) == due {
		t.Fatalf("after POST: %q completions=%d due=%s", undo, f.completions(t), f.dueDate(t))
	}
	// A second POST and a GET fail with the same generic page as a made-up token.
	generic := f.e.anon("GET", "/done/not-a-token", nil)
	if generic.status != 404 || strings.Contains(generic.body, "Clean gutters") {
		t.Fatalf("generic: %d %s", generic.status, generic.body)
	}
	for _, again := range []anonResp{f.e.anon("POST", "/done/"+tok, nil), f.e.anon("GET", "/done/"+tok, nil)} {
		if again.status != generic.status || again.body != generic.body {
			t.Fatalf("oracle: %d %q vs %d %q", again.status, again.body, generic.status, generic.body)
		}
	}
	if f.completions(t) != 1 {
		t.Fatal("second POST completed again")
	}

	// Undo works once.
	u := f.e.anon("POST", undo, nil)
	if u.status != 200 || f.completions(t) != 0 || f.dueDate(t) != due {
		t.Fatalf("undo: %d completions=%d due=%s", u.status, f.completions(t), f.dueDate(t))
	}
	noSetCookie(t, u)
	if again := f.e.anon("POST", undo, nil); again.status != 404 || again.body != generic.body {
		t.Fatalf("second undo: %d %s", again.status, again.body)
	}

	// Both uses were audited with the client IP; the audit log carries no tokens.
	evs, err := f.e.st.ListAuditForUser(context.Background(), f.mA.UserID, 50)
	if err != nil {
		t.Fatal(err)
	}
	var use bool
	for _, ev := range evs {
		if ev.Event == store.AuditMarkDoneUse && ev.IP == "127.0.0.1" && ev.HouseholdID == f.hA {
			use = true
		}
		if strings.Contains(ev.Event+ev.IP+strings.Join(mapValues(ev.Meta), ""), tok) {
			t.Fatal("token in audit log")
		}
	}
	if !use {
		t.Fatalf("no mark_done_use audit: %+v", evs)
	}
}

func mapValues(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func TestMarkDoneUndoWindowAndExpiry(t *testing.T) {
	f := newLinks(t)
	r := f.e.anon("POST", "/done/"+f.link(t, f.mA), nil)
	undo := formAction(t, r.body)
	f.e.clock.Add(store.UndoTTL + time.Second)
	if u := f.e.anon("POST", undo, nil); u.status != 404 || f.completions(t) != 1 {
		t.Fatalf("expired undo: %d completions=%d", u.status, f.completions(t))
	}

	// A link past its lifetime is generic too.
	f2 := newLinks(t)
	tok := f2.link(t, f2.mA)
	f2.e.clock.Add(store.MarkDoneMaxTTL + time.Minute)
	for _, r := range []anonResp{f2.e.anon("GET", "/done/"+tok, nil), f2.e.anon("POST", "/done/"+tok, nil)} {
		if r.status != 404 || strings.Contains(r.body, "Clean gutters") {
			t.Fatalf("expired: %d %s", r.status, r.body)
		}
	}
	if f2.completions(t) != 0 {
		t.Fatal("expired link completed the chore")
	}
}

func TestMarkDoneGuestsGetNoLinks(t *testing.T) {
	f := newLinks(t)
	_, err := f.e.st.CreateMarkDone(context.Background(), store.MarkDoneLink{ChoreID: f.chore.ID, OccurrenceDue: f.chore.NextDue,
		UserID: f.gA.UserID, ExpiresAt: f.e.clock.Now().Add(time.Hour)})
	if !errors.Is(err, store.ErrInvalid) {
		t.Fatalf("guest link: %v", err)
	}
	// A member's link stops working when they become a guest.
	tok := f.link(t, f.mA)
	if err := f.e.st.ChangeRole(context.Background(), f.oA, f.hA, f.mA.UserID, store.RoleGuest); err != nil {
		t.Fatal(err)
	}
	if r := f.e.anon("POST", "/done/"+tok, nil); r.status != 404 || f.completions(t) != 0 {
		t.Fatalf("demoted: %d completions=%d", r.status, f.completions(t))
	}
}

func TestMarkDoneCrossSiteAndRate(t *testing.T) {
	f := newLinks(t)
	tok := f.link(t, f.mA)
	for name, h := range map[string]map[string]string{
		"sec-fetch-site": {"Sec-Fetch-Site": "cross-site"},
		"same-site":      {"Sec-Fetch-Site": "same-site"},
		"origin":         {"Origin": "https://evil.example"},
	} {
		if r := f.e.anon("POST", "/done/"+tok, h); r.status != 403 || f.completions(t) != 0 {
			t.Fatalf("%s: %d completions=%d", name, r.status, f.completions(t))
		}
	}
	// Browsers may send Origin: null under Referrer-Policy: no-referrer.
	if r := f.e.anon("POST", "/done/"+tok, map[string]string{"Origin": "null"}); r.status != 200 {
		t.Fatalf("null origin: %d", r.status)
	}

	f.e.srv.rl = newRateLimiter(3, 100, f.e.clock.Now)
	var last int
	for range 5 {
		last = f.e.anon("GET", "/done/nope", nil).status
	}
	if last != 429 {
		t.Fatalf("rate limit: %d", last)
	}
}

// TestTokensNeverLogged: every URL token appears nowhere in the logs.
func TestTokensNeverLogged(t *testing.T) {
	f := newLinks(t)
	tok := f.link(t, f.mA)
	f.e.anon("GET", "/done/"+tok, nil)
	r := f.e.anon("POST", "/done/"+tok, nil)
	undo := strings.TrimPrefix(formAction(t, r.body), "/done/undo/")
	f.e.anon("POST", "/done/undo/"+undo, nil)
	f.e.anon("GET", "/done/bad-token-xyz", nil)
	st, v := f.m.do("POST", "/api/v1/households/"+f.hA+"/feeds", nil)
	expect(t, st, v, 201, "")
	feedTok := strings.TrimSuffix(strings.TrimPrefix(field(v, "url").(string), "/ical/"), ".ics")
	if r := f.e.anon("GET", "/ical/"+feedTok+".ics", nil); r.status != 200 {
		t.Fatalf("feed: %d", r.status)
	}
	f.e.anon("GET", "/ical/bad-feed-token.ics", nil)

	logs := f.logs.String()
	for _, secret := range []string{tok, undo, feedTok, "bad-token-xyz", "bad-feed-token"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("secret %q in logs:\n%s", secret, logs)
		}
	}
	if !strings.Contains(logs, "POST /done/{token}") || !strings.Contains(logs, "GET /ical/{file}") {
		t.Fatalf("expected route templates in the access log:\n%s", logs)
	}
}

func TestPasswordPathsInvalidateLinks(t *testing.T) {
	f := newLinks(t)
	f.e.user("root", true)

	// Own password change.
	tok := f.link(t, f.mA)
	st, v := f.m.do("POST", "/api/v1/account/password", map[string]string{"newPassword": "a-brand-new-password"})
	expect(t, st, v, 204, "")
	if r := f.e.anon("POST", "/done/"+tok, nil); r.status != 404 {
		t.Fatalf("link survived password change: %d", r.status)
	}

	// Admin reset.
	tok = f.link(t, f.oA)
	root := f.e.signedIn("root")
	st, v = root.do("POST", "/api/v1/admin/users/"+f.oA.UserID+"/reset-password", nil)
	expect(t, st, v, 200, "")
	if r := f.e.anon("POST", "/done/"+tok, nil); r.status != 404 {
		t.Fatalf("link survived admin reset: %d", r.status)
	}
	if f.completions(t) != 0 {
		t.Fatal("chore completed")
	}
}

func TestFeeds(t *testing.T) {
	f := newLinks(t)
	base := "/api/v1/households/" + f.hA + "/feeds"

	// Members and guests (read-only data) may have feeds; outsiders may not.
	st, v := f.x.do("POST", base, nil)
	expect(t, st, v, 404, "")
	mk := func(c *client) (id, path string) {
		st, v := c.do("POST", base, nil)
		expect(t, st, v, 201, "")
		u := field(v, "url").(string)
		if !strings.HasPrefix(u, "/ical/") || !strings.HasSuffix(u, ".ics") {
			t.Fatalf("url %q", u)
		}
		return field(v, "id").(string), u
	}
	id, path := mk(f.m)
	_, gpath := mk(f.g)

	r := f.e.anon("GET", path, nil)
	if r.status != 200 || r.h.Get("Content-Type") != "text/calendar; charset=utf-8" || r.h.Get("Cache-Control") != "no-store" {
		t.Fatalf("feed: %d %v", r.status, r.h)
	}
	noSetCookie(t, r)
	for _, want := range []string{"BEGIN:VCALENDAR", "Clean gutters", "X-WR-CALNAME:Gunkly: A"} {
		if !strings.Contains(r.body, want) {
			t.Fatalf("feed lacks %q:\n%s", want, r.body)
		}
	}
	for _, leak := range []string{"SECRET-NOTES", "secret.example", "/done/"} {
		if strings.Contains(r.body, leak) {
			t.Fatalf("feed leaks %q", leak)
		}
	}
	if f.e.anon("GET", strings.TrimSuffix(path, ".ics"), nil).status != 404 || f.e.anon("GET", "/ical/nope.ics", nil).status != 404 {
		t.Fatal("bad feed token served")
	}

	// Listing shows only the caller's feeds, never a URL.
	st, v = f.m.do("GET", base, nil)
	if l, _ := v.([]any); st != 200 || len(l) != 1 || field(l[0], "id") != id || field(l[0], "url") != nil {
		t.Fatalf("list: %d %v", st, v)
	}

	// Regenerate: the old URL dies, the new one works. Other users' feeds are untouchable.
	st, v = f.g.do("POST", base+"/"+id+"/regenerate", nil)
	expect(t, st, v, 404, "")
	st, v = f.m.do("POST", base+"/"+id+"/regenerate", nil)
	expect(t, st, v, 200, "")
	npath := field(v, "url").(string)
	if npath == path || f.e.anon("GET", path, nil).status != 404 || f.e.anon("GET", npath, nil).status != 200 {
		t.Fatal("regenerate did not swap the URL")
	}

	// Revoke.
	st, v = f.g.do("DELETE", base+"/"+id, nil)
	expect(t, st, v, 404, "")
	st, v = f.m.do("DELETE", base+"/"+id, nil)
	expect(t, st, v, 204, "")
	if f.e.anon("GET", npath, nil).status != 404 || f.e.anon("GET", gpath, nil).status != 200 {
		t.Fatal("revoke affected the wrong feed")
	}

	// A guest removed from the household loses the feed.
	if err := f.e.st.RemoveMember(context.Background(), f.oA, f.hA, f.gA.UserID); err != nil {
		t.Fatal(err)
	}
	if f.e.anon("GET", gpath, nil).status != 404 {
		t.Fatal("removed member's feed still works")
	}

	// Limit.
	lim := 1
	if err := f.e.st.UpdateInstanceSettings(context.Background(), store.SettingsUpdate{Limits: map[string]int{"calendar_feeds_per_user": lim}}); err != nil {
		t.Fatal(err)
	}
	st, v = f.m.do("POST", base, nil)
	expect(t, st, v, 201, "")
	st, v = f.m.do("POST", base, nil)
	expect(t, st, v, 409, "limit_reached")
}

func TestFeedURLUsesBaseURL(t *testing.T) {
	f := newLinks(t, func(o *Options) { o.BaseURL = "https://gunkly.example" })
	st, v := f.m.do("POST", "/api/v1/households/"+f.hA+"/feeds", nil)
	if st != 201 || !strings.HasPrefix(field(v, "url").(string), "https://gunkly.example/ical/") {
		t.Fatalf("%d %v", st, v)
	}
}

// TestLinkRateBuckets: mark-done pages and calendar feeds have their own
// per-IP buckets (30 and 60 per minute) apart from the strict login bucket.
func TestLinkRateBuckets(t *testing.T) {
	e := newEnv(t)
	e.srv.rl = newRateLimiter(limits.Defaults[limits.LoginAttemptsPerMinute], 100, e.clock.Now)
	e.srv.rl.perMinute = bucketRates
	count := func(path string) int { // requests until the first 429
		for i := 1; i <= 100; i++ {
			if e.anon("GET", path, nil).status == 429 {
				return i - 1
			}
		}
		return 100
	}
	if n := count("/done/nope"); n != limits.MarkDonePerMinute {
		t.Fatalf("mark-done allowed %d, want %d", n, limits.MarkDonePerMinute)
	}
	if n := count("/ical/nope.ics"); n != limits.ICalPerMinute {
		t.Fatalf("ical allowed %d, want %d", n, limits.ICalPerMinute)
	}
	// Exhausting both left login untouched, and login is still strict.
	c := e.client()
	var allowed int
	for range 10 {
		r := c.raw("POST", "/api/v1/session", map[string]string{"login": "x", "password": "password-x"}, c.cookie(csrfCookie))
		r.Body.Close()
		if r.StatusCode != 429 {
			allowed++
		}
	}
	if allowed != limits.Defaults[limits.LoginAttemptsPerMinute] {
		t.Fatalf("login allowed %d", allowed)
	}
}
