// Package metrics is a small Prometheus text-format exporter written by hand
// (no client library). Cardinality is bounded and nothing identifies a user or
// household: HTTP series are labelled by route pattern and status class,
// deliveries by channel kind and result.
package metrics

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

// ContentType is the exposition format version served.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// Bounds on label values, so a bug elsewhere cannot make the exporter grow
// without limit: anything past them is counted under "other".
const (
	maxRoutes = 256
	maxKinds  = 64
)

// Buckets are the request duration histogram's upper bounds, in seconds.
var Buckets = []float64{0.005, 0.025, 0.1, 0.5, 2.5, 10}

type gauge struct {
	name, help string
	fn         func(context.Context) (float64, error)
}

type reqKey struct{ route, class string }

type delKey struct{ kind, result string }

// Registry holds the counters and the scrape-time gauges. Safe for concurrent use.
type Registry struct {
	version string
	started time.Time

	mu       sync.Mutex
	reqs     map[reqKey]uint64
	routes   map[string]bool
	buckets  []uint64 // per bucket (not cumulative); the last is +Inf
	durSum   float64
	durCount uint64
	dels     map[delKey]uint64
	kinds    map[string]bool
	gauges   []gauge
}

// New returns a Registry reporting version in gunkly_build_info.
func New(version string) *Registry {
	return &Registry{version: version, started: time.Now(), reqs: map[reqKey]uint64{}, routes: map[string]bool{},
		buckets: make([]uint64, len(Buckets)+1), dels: map[delKey]uint64{}, kinds: map[string]bool{}}
}

// AddGauge registers a gauge computed at scrape time. A failing fn omits the
// sample for that scrape. name must be a valid metric name.
func (r *Registry) AddGauge(name, help string, fn func(context.Context) (float64, error)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gauges = append(r.gauges, gauge{name, help, fn})
}

func class(status int) string {
	if status < 100 || status > 599 {
		status = 200 // a handler that wrote nothing answered 200
	}
	return fmt.Sprintf("%dxx", status/100)
}

// ObserveRequest counts one finished request. route is the matched route
// pattern ("GET /api/v1/households/{hh}"), never a raw path.
func (r *Registry) ObserveRequest(route string, status int, d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.routes[route] {
		if len(r.routes) >= maxRoutes {
			route = "other"
		} else {
			r.routes[route] = true
		}
	}
	r.reqs[reqKey{route, class(status)}]++
	secs := d.Seconds()
	i := len(Buckets)
	for j, b := range Buckets {
		if secs <= b {
			i = j
			break
		}
	}
	r.buckets[i]++
	r.durSum += secs
	r.durCount++
}

// Delivery counts one finished delivery attempt by channel kind ("webpush"
// for browser push).
func (r *Registry) Delivery(kind string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.kinds[kind] {
		if len(r.kinds) >= maxKinds {
			kind = "other"
		} else {
			r.kinds[kind] = true
		}
	}
	res := "failed"
	if ok {
		res = "sent"
	}
	r.dels[delKey{kind, res}]++
}

// escape quotes a label value for the text format.
func escape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}

// Write renders every metric in the Prometheus text exposition format.
func (r *Registry) Write(ctx context.Context, w io.Writer) error {
	var b strings.Builder
	head := func(name, typ, help string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
	}

	head("gunkly_build_info", "gauge", "Build information; the value is always 1.")
	fmt.Fprintf(&b, "gunkly_build_info{version=\"%s\"} 1\n", escape(r.version))
	head("process_start_time_seconds", "gauge", "Start time of the process since the Unix epoch, in seconds.")
	fmt.Fprintf(&b, "process_start_time_seconds %d\n", r.started.Unix())
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	head("go_goroutines", "gauge", "Number of goroutines.")
	fmt.Fprintf(&b, "go_goroutines %d\n", runtime.NumGoroutine())
	head("go_memstats_alloc_bytes", "gauge", "Bytes of allocated heap objects.")
	fmt.Fprintf(&b, "go_memstats_alloc_bytes %d\n", ms.Alloc)

	r.mu.Lock()
	reqs := make([]reqKey, 0, len(r.reqs))
	for k := range r.reqs {
		reqs = append(reqs, k)
	}
	slices.SortFunc(reqs, func(a, b reqKey) int { return strings.Compare(a.route+"|"+a.class, b.route+"|"+b.class) })
	head("gunkly_http_requests_total", "counter", "HTTP requests by route pattern and status class.")
	for _, k := range reqs {
		fmt.Fprintf(&b, "gunkly_http_requests_total{route=\"%s\",code=\"%s\"} %d\n", escape(k.route), k.class, r.reqs[k])
	}
	head("gunkly_http_request_duration_seconds", "histogram", "HTTP request duration in seconds.")
	var cum uint64
	for i, le := range Buckets {
		cum += r.buckets[i]
		fmt.Fprintf(&b, "gunkly_http_request_duration_seconds_bucket{le=\"%g\"} %d\n", le, cum)
	}
	fmt.Fprintf(&b, "gunkly_http_request_duration_seconds_bucket{le=\"+Inf\"} %d\n", r.durCount)
	fmt.Fprintf(&b, "gunkly_http_request_duration_seconds_sum %g\n", r.durSum)
	fmt.Fprintf(&b, "gunkly_http_request_duration_seconds_count %d\n", r.durCount)

	dels := make([]delKey, 0, len(r.dels))
	for k := range r.dels {
		dels = append(dels, k)
	}
	slices.SortFunc(dels, func(a, b delKey) int { return strings.Compare(a.kind+"|"+a.result, b.kind+"|"+b.result) })
	head("gunkly_deliveries_total", "counter", "Reminder delivery attempts by channel kind and result (since process start, this instance).")
	for _, k := range dels {
		fmt.Fprintf(&b, "gunkly_deliveries_total{kind=\"%s\",result=\"%s\"} %d\n", escape(k.kind), k.result, r.dels[k])
	}
	gauges := slices.Clone(r.gauges)
	r.mu.Unlock()

	for _, g := range gauges {
		v, err := g.fn(ctx)
		if err != nil {
			continue
		}
		head(g.name, "gauge", g.help)
		fmt.Fprintf(&b, "%s %g\n", g.name, v)
	}
	_, err := io.WriteString(w, b.String())
	return err
}
