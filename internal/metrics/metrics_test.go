package metrics

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func render(t *testing.T, r *Registry) string {
	t.Helper()
	var b bytes.Buffer
	if err := r.Write(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestExposition(t *testing.T) {
	r := New(`1.2.3"x`)
	r.ObserveRequest("GET /api/v1/me", 200, 3*time.Millisecond)
	r.ObserveRequest("GET /api/v1/me", 204, 30*time.Millisecond)
	r.ObserveRequest("POST /api/v1/session", 401, 700*time.Millisecond)
	r.ObserveRequest("GET /", 0, 20*time.Second) // nothing written: 200
	r.Delivery("discord", true)
	r.Delivery("discord", false)
	r.Delivery("webpush", true)
	r.AddGauge("gunkly_delivery_queue_depth", "Pending deliveries.", func(context.Context) (float64, error) { return 7, nil })
	r.AddGauge("gunkly_broken", "Fails.", func(context.Context) (float64, error) { return 0, errors.New("boom") })
	out := render(t, r)
	for _, want := range []string{
		"# TYPE gunkly_build_info gauge\n",
		`gunkly_build_info{version="1.2.3\"x"} 1` + "\n",
		"process_start_time_seconds ",
		"go_goroutines ",
		`gunkly_http_requests_total{route="GET /api/v1/me",code="2xx"} 2` + "\n",
		`gunkly_http_requests_total{route="POST /api/v1/session",code="4xx"} 1` + "\n",
		`gunkly_http_requests_total{route="GET /",code="2xx"} 1` + "\n",
		"# TYPE gunkly_http_request_duration_seconds histogram\n",
		`gunkly_http_request_duration_seconds_bucket{le="0.005"} 1` + "\n",
		`gunkly_http_request_duration_seconds_bucket{le="0.025"} 1` + "\n",
		`gunkly_http_request_duration_seconds_bucket{le="0.1"} 2` + "\n",
		`gunkly_http_request_duration_seconds_bucket{le="0.5"} 2` + "\n",
		`gunkly_http_request_duration_seconds_bucket{le="2.5"} 3` + "\n",
		`gunkly_http_request_duration_seconds_bucket{le="10"} 3` + "\n",
		`gunkly_http_request_duration_seconds_bucket{le="+Inf"} 4` + "\n",
		"gunkly_http_request_duration_seconds_count 4\n",
		`gunkly_deliveries_total{kind="discord",result="failed"} 1` + "\n",
		`gunkly_deliveries_total{kind="discord",result="sent"} 1` + "\n",
		`gunkly_deliveries_total{kind="webpush",result="sent"} 1` + "\n",
		"# TYPE gunkly_delivery_queue_depth gauge\ngunkly_delivery_queue_depth 7\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "gunkly_broken") {
		t.Error("failing gauge was exposed")
	}
	if !strings.Contains(out, "gunkly_http_request_duration_seconds_sum 20.733") {
		t.Errorf("sum: %s", out)
	}
	// Every non-comment line is "name{labels} value" or "name value".
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.LastIndex(line, " "); i < 1 || strings.Contains(line[i+1:], " ") {
			t.Errorf("malformed line %q", line)
		}
	}
}

func TestCardinalityIsBounded(t *testing.T) {
	r := New("dev")
	for i := range 1000 {
		r.ObserveRequest(fmt.Sprintf("GET /x/%d", i), 200, time.Millisecond)
		r.Delivery(fmt.Sprintf("kind%d", i), true)
	}
	out := render(t, r)
	if n := strings.Count(out, "gunkly_http_requests_total{"); n > maxRoutes+1 {
		t.Errorf("%d request series", n)
	}
	if n := strings.Count(out, "gunkly_deliveries_total{"); n > maxKinds+1 {
		t.Errorf("%d delivery series", n)
	}
	if !strings.Contains(out, `route="other"`) || !strings.Contains(out, `kind="other"`) {
		t.Error("overflow not folded into other")
	}
}
