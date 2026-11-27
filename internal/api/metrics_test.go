package api

import (
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/NexusBetweenVoids/gunkly/internal/metrics"
)

const scrapeToken = "0123456789abcdef0123456789abcdef"

func withMetrics(o *Options) {
	o.Metrics, o.MetricsToken, o.Version = metrics.New("9.9.9"), scrapeToken, "9.9.9"
}

func scrape(t *testing.T, e *env, auth string) (int, string, http.Header) {
	t.Helper()
	req, _ := http.NewRequest("GET", e.ts.URL+"/metrics", nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header
}

func TestMetricsRequiresToken(t *testing.T) {
	e := newEnv(t, withMetrics)
	for _, auth := range []string{"", "Bearer ", "Bearer wrong", "Bearer " + scrapeToken + "x", "Basic " + scrapeToken, scrapeToken, "Bearer  " + scrapeToken} {
		st, body, h := scrape(t, e, auth)
		if st != 401 || strings.Contains(body, "gunkly_") || h.Get("WWW-Authenticate") == "" {
			t.Errorf("auth %q: %d %q", auth, st, body)
		}
	}
	// A signed-in session does not stand in for the token.
	e.user("root", true)
	c := e.signedIn("root")
	if st, v := c.do("GET", "/metrics", nil); st != 401 {
		t.Fatalf("session accepted: %d %v", st, v)
	}
	st, body, h := scrape(t, e, "Bearer "+scrapeToken)
	if st != 200 || h.Get("Content-Type") != metrics.ContentType || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("scrape: %d %v", st, h)
	}
	for _, want := range []string{
		`gunkly_build_info{version="9.9.9"} 1`,
		`gunkly_http_requests_total{route="GET /metrics",code="4xx"}`,
		`gunkly_http_requests_total{route="POST /api/v1/session",code="2xx"}`,
		"gunkly_http_request_duration_seconds_count ",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "root") || strings.Contains(body, scrapeToken) {
		t.Errorf("metrics leak user data or the token:\n%s", body)
	}
	// The scheme is case-insensitive (RFC 7235).
	if st, _, _ := scrape(t, e, "bearer "+scrapeToken); st != 200 {
		t.Fatalf("lower-case scheme: %d", st)
	}
}

func TestMetricsOffByDefault(t *testing.T) {
	e := newEnv(t)
	st, body, _ := scrape(t, e, "Bearer "+scrapeToken)
	if st == 200 && strings.Contains(body, "gunkly_") {
		t.Fatal("metrics served without configuration")
	}
	for _, r := range e.srv.routes {
		if strings.Contains(r.pattern, "/metrics") {
			t.Fatalf("route registered: %s", r.pattern)
		}
	}
	// A token without a registry (or the reverse) stays off too.
	e = newEnv(t, func(o *Options) { o.MetricsToken = scrapeToken })
	if _, body, _ := scrape(t, e, "Bearer "+scrapeToken); strings.Contains(body, "gunkly_build_info") {
		t.Fatal("served without a registry")
	}
}

func TestMetricsRateLimited(t *testing.T) {
	e := newEnv(t, withMetrics)
	e.srv.rl = newRateLimiter(10000, 10000, e.clock.Now)
	e.srv.rl.perMinute, e.srv.rl.burstOf = bucketRates, bucketBursts
	var last int
	for range 40 {
		last, _, _ = scrape(t, e, "Bearer nope")
	}
	if last != 429 {
		t.Fatalf("token guessing not throttled: last status %d", last)
	}
}

// TestMetricsRoutePolicyAndDocs: the route carries the bearer-token policy, and
// every route outside /api/v1 other than probes and the SPA is documented under
// x-outside-api-v1 in the OpenAPI file.
func TestMetricsRoutePolicyAndDocs(t *testing.T) {
	e := newEnv(t, withMetrics)
	found := false
	for _, r := range e.srv.routes {
		if r.pattern == "GET /metrics" {
			found = true
			if r.policy.auth != authMetrics || r.policy.rate == "" {
				t.Errorf("metrics policy: %+v", r.policy)
			}
		}
	}
	if !found {
		t.Fatal("GET /metrics not registered")
	}
	for _, bad := range []struct {
		pattern string
		p       policy
	}{
		{"GET /metrics", policy{auth: authMetrics}},     // needs a rate limit
		{"POST /metrics", metricsToken},                 // GET only
		{"GET /metrics", metricsToken.withStepUp()},     // no step-up
		{"GET /api/v1/households/{hh}/m", metricsToken}, // not household scoped
		{"GET /metrics", metricsToken.withTokenAuth()},  // not the mark-done kind
	} {
		if bad.p.validate(bad.pattern) == nil {
			t.Errorf("%s %+v accepted", bad.pattern, bad.p)
		}
	}
	spec, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_, outside, ok := strings.Cut(string(spec), "\nx-outside-api-v1:")
	if !ok {
		t.Fatal("no x-outside-api-v1 section")
	}
	for _, r := range e.srv.routes {
		if strings.Contains(r.pattern, "/api/v1/") || strings.HasSuffix(r.pattern, " /healthz") || strings.HasSuffix(r.pattern, " /readyz") || strings.HasSuffix(r.pattern, " /healthz/email") || r.pattern == "GET /" {
			continue
		}
		if !strings.Contains(outside, `"`+r.pattern+`"`) {
			t.Errorf("route %q is not documented under x-outside-api-v1", r.pattern)
		}
	}
}
