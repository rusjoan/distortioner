package metrics

import (
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// cached memoises an expensive value for a while, so that scrapes do not
// walk directories or query the database every few seconds.
type cached struct {
	ttl time.Duration
	fn  func() float64

	mu sync.Mutex
	at time.Time
	v  float64
}

func (c *cached) get() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.at) > c.ttl {
		c.v, c.at = c.fn(), time.Now()
	}
	return c.v
}

// CachedGauge registers a gauge whose value is computed at most once per ttl.
func (m *Metrics) CachedGauge(name, help string, labels prometheus.Labels, ttl time.Duration, fn func() float64) {
	c := &cached{ttl: ttl, fn: fn}
	m.Gauge(name, help, labels, c.get)
}

// Volume registers free and total space of the filesystem holding path.
func (m *Metrics) Volume(volume, path string) {
	labels := prometheus.Labels{"volume": volume}
	m.Gauge("distortioner_disk_free_bytes", "Free space on a volume the bot uses.", labels, func() float64 {
		free, _, _ := diskSpace(path)
		return free
	})
	m.Gauge("distortioner_disk_total_bytes", "Size of a volume the bot uses.", labels, func() float64 {
		_, total, _ := diskSpace(path)
		return total
	})
}

// Dir registers the size of a directory, recomputed at most once a minute.
// With prefix set, only entries whose name starts with it are counted.
func (m *Metrics) Dir(dir, path, prefix string) {
	m.CachedGauge("distortioner_dir_bytes", "Size of a directory the bot writes to.",
		prometheus.Labels{"dir": dir}, time.Minute, func() float64 { return dirSize(path, prefix) })
}

func dirSize(root, prefix string) float64 {
	var total int64
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // vanished or unreadable: skip
		}
		if prefix != "" && path != root && filepath.Dir(path) == root && !strings.HasPrefix(d.Name(), prefix) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return float64(total)
}
