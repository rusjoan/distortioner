// Package metrics exposes Prometheus metrics.
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics owns a private registry.
type Metrics struct {
	reg      *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
}

// New registers the bot's metrics plus Go runtime and process collectors.
func New() *Metrics {
	m := &Metrics{
		reg: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "distortioner_requests_total",
			Help: "Distortion requests by media kind and outcome.",
		}, []string{"kind", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "distortioner_job_duration_seconds",
			Help:    "Time spent processing successful jobs.",
			Buckets: []float64{0.5, 1, 2, 5, 10, 20, 40, 60, 120, 300},
		}, []string{"kind"}),
	}
	m.reg.MustRegister(m.requests, m.duration,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

// Request counts a finished request.
func (m *Metrics) Request(kind, status string) {
	m.requests.WithLabelValues(kind, status).Inc()
}

// Duration observes how long a successful job took.
func (m *Metrics) Duration(kind string, d time.Duration) {
	m.duration.WithLabelValues(kind).Observe(d.Seconds())
}

// Gauge registers a gauge computed on scrape.
func (m *Metrics) Gauge(name, help string, labels prometheus.Labels, fn func() float64) {
	m.reg.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: name, Help: help, ConstLabels: labels,
	}, fn))
}

// Handler serves the metrics.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{Registry: m.reg})
}
