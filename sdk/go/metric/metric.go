// Package metric is the agenda first-party custom-metrics helper. It wraps
// prometheus/client_golang with just enough bootstrapping (identity labels,
// an optional dedicated HTTP listener) that app authors can define their own
// Counters/Gauges/Histograms without wiring up Prometheus themselves. It
// lives in the standalone module github.com/FredrickUnderwood/agenda-v2/sdk/go
// so importing it does not pull in the platform's dependencies.
package metric

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Config configures Init's optional HTTP server. ListenAddr, when set (or via
// the AGENDA_METRICS_ADDR env var the platform injects into deployed app
// containers that opt into metrics), starts a dedicated /metrics listener.
// Leaving both unset still registers metrics normally, just serves nothing —
// safe for local runs, and for apps that mount Handler() on their own router
// instead of a second listener.
//
// EnableGoCollectors additionally registers the standard Go runtime + process
// collectors. Off by default: this package's registry is private, so unlike
// prometheus.DefaultRegisterer it starts with zero noise until asked for.
type Config struct {
	ListenAddr         string
	EnableGoCollectors bool
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// registry and factory are package-level, not built inside Init, because
// custom metrics are normally declared as package-level vars in the
// importing app (var ordersFailed = metric.NewCounterVec(...)), which run
// during that package's var-init phase — guaranteed by Go to happen before
// main() runs, and therefore before main() could ever call Init. Building
// these lazily inside Init would leave New*/NewCounterVec with nothing to
// register against at the point apps actually declare their metrics.
var (
	registry = prometheus.NewRegistry()
	factory  = promauto.With(prometheus.WrapRegistererWith(buildConstLabels(), registry))
)

// buildConstLabels reads the platform-injected identity env vars once, at
// package load — the same AGENDA_APP_NAME / AGENDA_INSTANCE_NAME /
// AGENDA_SERVICE_NAME vars sdk/go/log reads, so a deployed app's metrics and
// logs carry matching identity without extra wiring. Because this runs at
// package load (not inside Init), setting these env vars only takes effect
// if done before the process's package-init phase — e.g. by the platform's
// container env injection, not by code that runs after main() starts.
func buildConstLabels() prometheus.Labels {
	labels := prometheus.Labels{}
	if v := os.Getenv("AGENDA_APP_NAME"); v != "" {
		labels["agenda_app"] = v
	}
	if v := os.Getenv("AGENDA_INSTANCE_NAME"); v != "" {
		labels["agenda_instance"] = v
	}
	if v := os.Getenv("AGENDA_SERVICE_NAME"); v != "" {
		labels["agenda_service"] = v
	}
	return labels
}

// NewCounter registers and returns a Counter on this package's registry.
func NewCounter(opts prometheus.CounterOpts) prometheus.Counter {
	return factory.NewCounter(opts)
}

// NewCounterVec registers and returns a CounterVec on this package's registry.
func NewCounterVec(opts prometheus.CounterOpts, labelNames []string) *prometheus.CounterVec {
	return factory.NewCounterVec(opts, labelNames)
}

// NewGauge registers and returns a Gauge on this package's registry.
func NewGauge(opts prometheus.GaugeOpts) prometheus.Gauge {
	return factory.NewGauge(opts)
}

// NewGaugeVec registers and returns a GaugeVec on this package's registry.
func NewGaugeVec(opts prometheus.GaugeOpts, labelNames []string) *prometheus.GaugeVec {
	return factory.NewGaugeVec(opts, labelNames)
}

// NewHistogram registers and returns a Histogram on this package's registry.
func NewHistogram(opts prometheus.HistogramOpts) prometheus.Histogram {
	return factory.NewHistogram(opts)
}

// NewHistogramVec registers and returns a HistogramVec on this package's registry.
func NewHistogramVec(opts prometheus.HistogramOpts, labelNames []string) *prometheus.HistogramVec {
	return factory.NewHistogramVec(opts, labelNames)
}

// Handler returns the promhttp handler for this package's private registry,
// for apps that want to mount /metrics on their own router instead of
// letting Init start a dedicated listener.
func Handler() http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
}

var (
	mu      sync.Mutex
	started bool
	server  *http.Server
)

// Init optionally starts a dedicated HTTP server serving Handler() at
// ListenAddr (falling back to the AGENDA_METRICS_ADDR env var). Leaving both
// unset is a no-op — metrics registered via New* above are still tracked and
// reachable through Handler(), just not served by this package. Calling Init
// a second time without an intervening Shutdown returns an error, since
// (unlike log.Init's core-swap) a listening socket is a resource, not a
// stateless config.
func Init(cfg Config) error {
	mu.Lock()
	defer mu.Unlock()
	if started {
		return errors.New("metric: Init already called; call Shutdown first")
	}

	if cfg.EnableGoCollectors {
		registry.MustRegister(collectors.NewGoCollector())
		registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	}

	addr := firstNonEmpty(cfg.ListenAddr, os.Getenv("AGENDA_METRICS_ADDR"))
	if addr == "" {
		started = true
		return nil
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.Handle("/metrics", Handler())
	srv := &http.Server{Handler: mux}
	server = srv
	started = true
	go func() {
		_ = srv.Serve(ln)
	}()
	return nil
}

// Shutdown gracefully stops Init's HTTP server, if one was started, and
// clears Init's idempotency guard so Init can be called again. Call on
// process exit alongside sdk/go/log.Shutdown.
func Shutdown(ctx context.Context) error {
	mu.Lock()
	srv := server
	server = nil
	started = false
	mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}
