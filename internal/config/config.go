// Package config loads the bot configuration from environment variables.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const envPrefix = "DISTORTIONER_"

// Mode selects how updates are received from Telegram.
type Mode string

const (
	ModePolling Mode = "polling"
	ModeWebhook Mode = "webhook"
)

// Config is the static configuration. Values that admins can change at runtime
// live in the settings package instead.
type Config struct {
	Token    string
	APIURL   string // custom Bot API server, e.g. a local telegram-bot-api instance
	AdminIDs []int64

	Mode          Mode
	WebhookURL    string
	WebhookSecret string
	ListenAddr    string // HTTP server for metrics, health checks and the webhook

	DataDir string
	TempDir string

	VideoWorkers int
	ImageWorkers int
	CPULimit     int // max concurrent ImageMagick processes
	Codec        string
	// MaxFileMB is the initial file size limit (admins can change it);
	// MaxFileLimitMB is what the Bot API server allows at all.
	MaxFileMB      int
	MaxFileLimitMB int

	JobTimeout    time.Duration
	ShutdownGrace time.Duration

	KeepFailedInputs bool
	FailedTTL        time.Duration
	FailedMaxBytes   int64

	StatsRetention time.Duration

	// PriorityChats seeds the priority list in the database on startup.
	PriorityChats []int64

	LogLevel  slog.Level
	LogFormat string
}

// IsAdmin reports whether the user is one of the configured admins.
func (c *Config) IsAdmin(userID int64) bool {
	for _, id := range c.AdminIDs {
		if id == userID {
			return true
		}
	}
	return false
}

// Load reads the configuration from the environment.
func Load() (*Config, error) {
	return load(os.Getenv)
}

func load(getenv func(string) string) (*Config, error) {
	r := reader{getenv: getenv}
	cpus := runtime.NumCPU()

	c := &Config{
		Token:            r.str("BOT_TOKEN", ""),
		APIURL:           strings.TrimRight(r.str("API_URL", ""), "/"),
		AdminIDs:         r.ids("ADMIN_IDS"),
		Mode:             Mode(r.str("MODE", string(ModePolling))),
		WebhookURL:       r.str("WEBHOOK_URL", ""),
		WebhookSecret:    r.str("WEBHOOK_SECRET", ""),
		ListenAddr:       r.str("LISTEN_ADDR", ":8080"),
		DataDir:          r.str("DATA_DIR", "data"),
		TempDir:          r.str("TEMP_DIR", os.TempDir()),
		VideoWorkers:     r.int("VIDEO_WORKERS", 2),
		ImageWorkers:     r.int("IMAGE_WORKERS", cpus),
		CPULimit:         r.int("CPU_LIMIT", cpus),
		Codec:            r.str("CODEC", "libx264"),
		JobTimeout:       r.duration("JOB_TIMEOUT", 5*time.Minute),
		ShutdownGrace:    r.duration("SHUTDOWN_GRACE", 30*time.Second),
		KeepFailedInputs: r.bool("KEEP_FAILED_INPUTS", false),
		FailedTTL:        r.duration("FAILED_TTL", 72*time.Hour),
		FailedMaxBytes:   int64(r.int("FAILED_MAX_MB", 500)) << 20,
		StatsRetention:   time.Duration(r.int("STATS_RETENTION_DAYS", 365)) * 24 * time.Hour,
		PriorityChats:    r.ids("PRIORITY_CHATS"),
		LogFormat:        r.str("LOG_FORMAT", "json"),
	}

	// The public Bot API refuses to serve files above 20 MB; a self-hosted
	// server lifts that limit to 2 GB.
	c.MaxFileLimitMB = 20
	if c.APIURL != "" {
		c.MaxFileLimitMB = 2000
	}
	c.MaxFileMB = r.int("MAX_FILE_MB", c.MaxFileLimitMB)

	// Backwards compatibility with the single-admin variable of v1.
	if len(c.AdminIDs) == 0 {
		c.AdminIDs = r.ids("ADMIN_ID")
	}

	if err := c.LogLevel.UnmarshalText([]byte(r.str("LOG_LEVEL", "info"))); err != nil {
		r.errs = append(r.errs, fmt.Errorf("%sLOG_LEVEL: %w", envPrefix, err))
	}

	return c, errors.Join(append(r.errs, c.validate()...)...)
}

func (c *Config) validate() []error {
	var errs []error
	if c.Token == "" {
		errs = append(errs, errors.New(envPrefix+"BOT_TOKEN is required"))
	}
	switch c.Mode {
	case ModePolling:
	case ModeWebhook:
		if c.WebhookURL == "" {
			errs = append(errs, errors.New(envPrefix+"WEBHOOK_URL is required in webhook mode"))
		}
	default:
		errs = append(errs, fmt.Errorf("%sMODE must be polling or webhook, got %q", envPrefix, c.Mode))
	}
	if c.LogFormat != "json" && c.LogFormat != "text" {
		errs = append(errs, fmt.Errorf("%sLOG_FORMAT must be json or text, got %q", envPrefix, c.LogFormat))
	}
	for name, v := range map[string]int{
		"VIDEO_WORKERS": c.VideoWorkers,
		"IMAGE_WORKERS": c.ImageWorkers,
		"CPU_LIMIT":     c.CPULimit,
	} {
		if v < 1 {
			errs = append(errs, fmt.Errorf("%s%s must be positive", envPrefix, name))
		}
	}
	if c.MaxFileMB <= 0 || c.MaxFileMB > c.MaxFileLimitMB {
		errs = append(errs, fmt.Errorf("%sMAX_FILE_MB must be between 1 and %d (the Bot API server's limit)",
			envPrefix, c.MaxFileLimitMB))
	}
	return errs
}

type reader struct {
	getenv func(string) string
	errs   []error
}

func (r *reader) str(key, def string) string {
	if v := strings.TrimSpace(r.getenv(envPrefix + key)); v != "" {
		return v
	}
	return def
}

func (r *reader) int(key string, def int) int {
	v := r.str(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s%s: %w", envPrefix, key, err))
		return def
	}
	return n
}

func (r *reader) bool(key string, def bool) bool {
	v := r.str(key, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s%s: %w", envPrefix, key, err))
		return def
	}
	return b
}

func (r *reader) duration(key string, def time.Duration) time.Duration {
	v := r.str(key, "")
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s%s: %w", envPrefix, key, err))
		return def
	}
	return d
}

func (r *reader) ids(key string) []int64 {
	var ids []int64
	for _, s := range strings.Split(r.str(key, ""), ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			r.errs = append(r.errs, fmt.Errorf("%s%s: %q is not a valid id", envPrefix, key, s))
			continue
		}
		ids = append(ids, id)
	}
	return ids
}
