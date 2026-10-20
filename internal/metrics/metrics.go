// Package metrics exposes Prometheus metrics.
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Byte buckets from 10 KB to 2 GB.
var sizeBuckets = prometheus.ExponentialBucketsRange(10<<10, 2<<30, 14)

// Duration buckets from 50 ms to 10 min.
var timeBuckets = []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 40, 60, 120, 300, 600}

// Metrics owns a private registry.
type Metrics struct {
	reg *prometheus.Registry

	updates    *prometheus.CounterVec
	commands   *prometheus.CounterVec
	buttons    *prometheus.CounterVec
	requests   *prometheus.CounterVec
	sources    *prometheus.CounterVec
	rejections *prometheus.CounterVec
	effects    *prometheus.CounterVec
	outcomes   *prometheus.CounterVec

	duration  *prometheus.HistogramVec
	stages    *prometheus.HistogramVec
	queueWait *prometheus.HistogramVec

	inputSize  *prometheus.HistogramVec
	outputSize *prometheus.HistogramVec
	mediaLen   *prometheus.HistogramVec
	fileBytes  *prometheus.CounterVec

	tgRequests *prometheus.CounterVec
	tgLatency  *prometheus.HistogramVec
	tgBytes    *prometheus.CounterVec

	toolRuns *prometheus.CounterVec
	toolCPU  *prometheus.CounterVec
	toolTime *prometheus.HistogramVec
}

// New registers the bot's metrics plus Go runtime and process collectors.
func New(version string) *Metrics {
	counter := func(name, help string, labels ...string) *prometheus.CounterVec {
		return prometheus.NewCounterVec(prometheus.CounterOpts{Name: "distortioner_" + name, Help: help}, labels)
	}
	histogram := func(name, help string, buckets []float64, labels ...string) *prometheus.HistogramVec {
		return prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "distortioner_" + name, Help: help, Buckets: buckets}, labels)
	}
	m := &Metrics{
		reg: prometheus.NewRegistry(),

		updates:    counter("updates_total", "Updates received from Telegram, by type.", "type"),
		commands:   counter("commands_total", "Bot commands received.", "command"),
		buttons:    counter("buttons_total", "Inline buttons pressed, by action.", "action"),
		requests:   counter("requests_total", "Requests by media kind and outcome.", "kind", "status"),
		sources:    counter("requests_by_source_total", "Successful requests by how they were asked for.", "source"),
		rejections: counter("rejections_total", "Requests turned away, by reason.", "reason"),
		effects:    counter("effects_total", "Effects applied in successful requests.", "effect"),
		outcomes:   counter("job_outcomes_total", "Finished jobs by pool and outcome.", "pool", "outcome"),

		duration:  histogram("job_duration_seconds", "Time spent on successful jobs, from start to the result sent.", timeBuckets, "kind"),
		stages:    histogram("stage_duration_seconds", "Time spent per stage of a job.", timeBuckets, "stage", "kind"),
		queueWait: histogram("queue_wait_seconds", "Time jobs waited in the queue.", timeBuckets, "pool"),

		inputSize:  histogram("input_bytes", "Size of downloaded media.", sizeBuckets, "kind"),
		outputSize: histogram("output_bytes", "Size of results sent.", sizeBuckets, "kind"),
		mediaLen:   histogram("media_duration_seconds", "Length of video and sound received.", []float64{1, 3, 5, 10, 15, 20, 30, 45, 60, 120, 300}, "kind"),
		fileBytes:  counter("file_bytes_total", "Media bytes moved: downloaded inputs and uploaded results.", "direction", "kind"),

		tgRequests: counter("telegram_requests_total", "Bot API calls by method and HTTP status.", "method", "code"),
		tgLatency:  histogram("telegram_request_duration_seconds", "Bot API call latency.", timeBuckets, "method"),
		tgBytes:    counter("telegram_bytes_total", "Bytes exchanged with the Bot API server.", "direction"),

		toolRuns: counter("tool_runs_total", "Runs of ffmpeg, ffprobe and ImageMagick.", "tool", "result"),
		toolCPU:  counter("tool_cpu_seconds_total", "CPU time used by ffmpeg, ffprobe and ImageMagick.", "tool"),
		toolTime: histogram("tool_duration_seconds", "Wall time of ffmpeg, ffprobe and ImageMagick runs.", timeBuckets, "tool"),
	}
	build := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "distortioner_build_info", Help: "Always 1; the version is a label.",
		ConstLabels: prometheus.Labels{"version": version},
	})
	build.Set(1)
	m.reg.MustRegister(build,
		m.updates, m.commands, m.buttons, m.requests, m.sources, m.rejections, m.effects, m.outcomes,
		m.duration, m.stages, m.queueWait,
		m.inputSize, m.outputSize, m.mediaLen, m.fileBytes,
		m.tgRequests, m.tgLatency, m.tgBytes,
		m.toolRuns, m.toolCPU, m.toolTime,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return m
}

// Update counts an incoming update.
func (m *Metrics) Update(kind string) { m.updates.WithLabelValues(kind).Inc() }

// Command counts a bot command.
func (m *Metrics) Command(name string) { m.commands.WithLabelValues(name).Inc() }

// Button counts a pressed inline button.
func (m *Metrics) Button(action string) { m.buttons.WithLabelValues(action).Inc() }

// Request counts a finished request.
func (m *Metrics) Request(kind, status string) { m.requests.WithLabelValues(kind, status).Inc() }

// Source counts how a successful request was asked for.
func (m *Metrics) Source(source string) { m.sources.WithLabelValues(source).Inc() }

// Rejection counts a request turned away.
func (m *Metrics) Rejection(reason string) { m.rejections.WithLabelValues(reason).Inc() }

// Effect counts one applied effect.
func (m *Metrics) Effect(id string) { m.effects.WithLabelValues(id).Inc() }

// Outcome counts a finished job.
func (m *Metrics) Outcome(pool, outcome string) { m.outcomes.WithLabelValues(pool, outcome).Inc() }

// Duration observes how long a successful job took.
func (m *Metrics) Duration(kind string, d time.Duration) {
	m.duration.WithLabelValues(kind).Observe(d.Seconds())
}

// Stage observes one stage of a job: download, process or upload.
func (m *Metrics) Stage(stage, kind string, d time.Duration) {
	m.stages.WithLabelValues(stage, kind).Observe(d.Seconds())
}

// QueueWait observes how long a job waited for a worker.
func (m *Metrics) QueueWait(pool string, d time.Duration) {
	m.queueWait.WithLabelValues(pool).Observe(d.Seconds())
}

// Input observes a downloaded file and its media length (0 if none).
func (m *Metrics) Input(kind string, bytes int64, seconds float64) {
	m.inputSize.WithLabelValues(kind).Observe(float64(bytes))
	m.fileBytes.WithLabelValues("in", kind).Add(float64(bytes))
	if seconds > 0 {
		m.mediaLen.WithLabelValues(kind).Observe(seconds)
	}
}

// Output observes a result file.
func (m *Metrics) Output(kind string, bytes int64) {
	m.outputSize.WithLabelValues(kind).Observe(float64(bytes))
	m.fileBytes.WithLabelValues("out", kind).Add(float64(bytes))
}

// TelegramCall observes one Bot API request.
func (m *Metrics) TelegramCall(method, code string, d time.Duration, sent, received int64) {
	m.tgRequests.WithLabelValues(method, code).Inc()
	m.tgLatency.WithLabelValues(method).Observe(d.Seconds())
	m.tgBytes.WithLabelValues("sent").Add(float64(sent))
	m.tgBytes.WithLabelValues("received").Add(float64(received))
}

// Tool observes a run of an external tool.
func (m *Metrics) Tool(tool string, ok bool, wall, cpu time.Duration) {
	result := "ok"
	if !ok {
		result = "error"
	}
	m.toolRuns.WithLabelValues(tool, result).Inc()
	m.toolCPU.WithLabelValues(tool).Add(cpu.Seconds())
	m.toolTime.WithLabelValues(tool).Observe(wall.Seconds())
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
