// Command distortioner is a Telegram bot that distorts media with seam carving.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/graynk/distortioner/internal/admin"
	"github.com/graynk/distortioner/internal/bot"
	"github.com/graynk/distortioner/internal/config"
	"github.com/graynk/distortioner/internal/jobs"
	"github.com/graynk/distortioner/internal/limiter"
	"github.com/graynk/distortioner/internal/media"
	"github.com/graynk/distortioner/internal/metrics"
	"github.com/graynk/distortioner/internal/settings"
	"github.com/graynk/distortioner/internal/store"
	"github.com/graynk/distortioner/internal/telegram"
)

var version = "dev"

var allowedUpdates = []string{"message", "callback_query"}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	if len(os.Args) > 1 && os.Args[1] == "logout-cloud" {
		if err := logoutCloud(); err != nil {
			fmt.Fprintln(os.Stderr, "logout-cloud:", err)
			os.Exit(1)
		}
		fmt.Println("logged out from api.telegram.org; the bot can now use a local Bot API server")
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(version)
		return
	}
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	log := newLogger(cfg)
	slog.SetDefault(log)
	log.Info("starting", "version", version, "mode", cfg.Mode)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.DataDir)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer st.Close()

	sets, err := settings.Load(ctx, st, settings.Options{
		PriorityChats: cfg.PriorityChats, MaxFileMB: cfg.MaxFileMB, MaxFileLimitMB: cfg.MaxFileLimitMB,
	})
	if err != nil {
		return err
	}

	engine := media.NewEngine(cfg.CPULimit, cfg.Codec)
	if err := engine.Check(ctx); err != nil {
		return fmt.Errorf("media tools: %w", err)
	}

	limits := jobs.Limits{
		MaxQueued:  func() int { return sets.Int(settings.MaxQueue) },
		MaxPerChat: func() int { return sets.Int(settings.MaxJobsPerChat) },
		IsPriority: sets.IsPriority,
	}
	video := jobs.New(cfg.VideoWorkers, cfg.JobTimeout, limits)
	image := jobs.New(cfg.ImageWorkers, cfg.JobTimeout, limits)
	lim := limiter.New(
		func() int { return sets.Int(settings.RateLimit) },
		func() time.Duration { return time.Duration(sets.Int(settings.RateWindow)) * time.Second },
	)

	m := metrics.New()
	for name, p := range map[string]*jobs.Pool{"video": video, "image": image} {
		m.Gauge("distortioner_jobs_queued", "Jobs waiting in the queue.", prometheus.Labels{"pool": name},
			func() float64 { return float64(p.Snapshot().Queued) })
		m.Gauge("distortioner_jobs_running", "Jobs being processed.", prometheus.Labels{"pool": name},
			func() float64 { return float64(p.Snapshot().Running) })
	}
	m.Gauge("distortioner_cpu_slots_in_use", "ImageMagick processes running.", nil,
		func() float64 { return float64(engine.CPUInUse()) })
	m.Gauge("distortioner_maintenance", "1 when maintenance mode is on.", nil, func() float64 {
		if on, _ := sets.Maintenance(); on {
			return 1
		}
		return 0
	})

	// The handler is bound after the client exists: the bot needs both.
	var handler *bot.Bot
	opts := []tgbot.Option{
		tgbot.WithDefaultHandler(func(ctx context.Context, b *tgbot.Bot, u *models.Update) {
			handler.Handle(ctx, b, u)
		}),
		tgbot.WithAllowedUpdates(allowedUpdates),
		tgbot.WithErrorsHandler(func(err error) { log.Warn("telegram", "err", err) }),
		tgbot.WithSkipGetMe(), // called below, where we need the result
	}
	if cfg.APIURL != "" {
		opts = append(opts, tgbot.WithServerURL(cfg.APIURL))
	}
	if cfg.Mode == config.ModeWebhook && cfg.WebhookSecret != "" {
		opts = append(opts, tgbot.WithWebhookSecretToken(cfg.WebhookSecret))
	}
	tb, err := tgbot.New(cfg.Token, opts...)
	if err != nil {
		return fmt.Errorf("telegram: %w", err)
	}
	me, err := tb.GetMe(ctx)
	if err != nil {
		return fmt.Errorf("telegram getMe: %w", err)
	}
	tg := telegram.New(tb)

	panel := admin.New(admin.Deps{
		Cfg: cfg, TG: tg, Store: st, Settings: sets, Video: video, Image: image, Engine: engine, Log: log,
	})
	handler = bot.New(bot.Deps{
		Cfg: cfg, TG: tg, Store: st, Settings: sets, Engine: engine, Video: video, Image: image,
		Limiter: lim, Metrics: m, Admin: panel, Log: log, Me: me,
	})
	setCommands(ctx, tb, cfg, log)

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           httpHandler(cfg, tb, st, m),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server", "err", err)
			stop()
		}
	}()

	var background sync.WaitGroup
	background.Add(2)
	go func() { defer background.Done(); handler.Run(ctx) }()
	go func() { defer background.Done(); panel.Run(ctx) }()

	log.Info("bot is up", "username", me.Username, "workers", cfg.VideoWorkers, "cpu", cfg.CPULimit)
	if cfg.Mode == config.ModeWebhook {
		if _, err := tb.SetWebhook(ctx, &tgbot.SetWebhookParams{
			URL: cfg.WebhookURL, SecretToken: cfg.WebhookSecret, AllowedUpdates: allowedUpdates,
		}); err != nil {
			return fmt.Errorf("set webhook: %w", err)
		}
		tb.StartWebhook(ctx)
	} else {
		if _, err := tb.DeleteWebhook(ctx, &tgbot.DeleteWebhookParams{}); err != nil {
			return fmt.Errorf("delete webhook: %w", err)
		}
		tb.Start(ctx)
	}

	// ctx is done: no new updates are read. Let running jobs finish.
	log.Info("shutting down", "grace", cfg.ShutdownGrace)
	grace, cancel := context.WithTimeout(context.Background(), cfg.ShutdownGrace)
	defer cancel()
	var pools sync.WaitGroup
	for _, p := range []*jobs.Pool{video, image} {
		pools.Add(1)
		go func() { defer pools.Done(); p.Shutdown(grace) }()
	}
	pools.Wait()
	background.Wait()

	shutdownCtx, cancelHTTP := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelHTTP()
	_ = srv.Shutdown(shutdownCtx)
	log.Info("bye")
	return nil
}

func newLogger(cfg *config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == "text" {
		return slog.New(slog.NewTextHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, opts))
}

func httpHandler(cfg *config.Config, tb *tgbot.Bot, st *store.Store, m *metrics.Metrics) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", m.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := st.Ping(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	if cfg.Mode == config.ModeWebhook {
		path := "/webhook"
		if u, err := url.Parse(cfg.WebhookURL); err == nil && u.Path != "" {
			path = u.Path
		}
		mux.Handle("POST "+path, tb.WebhookHandler())
	}
	return mux
}

func setCommands(ctx context.Context, tb *tgbot.Bot, cfg *config.Config, log *slog.Logger) {
	type cmd struct{ name, en, ru string }
	public := []cmd{
		{"distort", "Distort the message you reply to", "Исказить сообщение, на которое отвечаешь"},
		{"settings", "Strength, sound and language", "Сила, звук и язык"},
		{"help", "What the bot can do", "Что умеет бот"},
	}
	build := func(cmds []cmd, ru bool) []models.BotCommand {
		out := make([]models.BotCommand, len(cmds))
		for i, c := range cmds {
			out[i] = models.BotCommand{Command: c.name, Description: c.en}
			if ru {
				out[i].Description = c.ru
			}
		}
		return out
	}
	for _, lang := range []string{"", "ru"} {
		if _, err := tb.SetMyCommands(ctx, &tgbot.SetMyCommandsParams{
			Commands: build(public, lang == "ru"), LanguageCode: lang,
		}); err != nil {
			log.Warn("set commands", "err", err)
		}
	}
	withAdmin := slices.Concat(public, []cmd{{"admin", "Admin panel", "Админка"}})
	for _, id := range cfg.AdminIDs {
		for _, lang := range []string{"", "ru"} {
			if _, err := tb.SetMyCommands(ctx, &tgbot.SetMyCommandsParams{
				Commands: build(withAdmin, lang == "ru"), LanguageCode: lang,
				Scope: &models.BotCommandScopeChat{ChatID: id},
			}); err != nil {
				log.Warn("set admin commands", "admin", id, "err", err)
			}
		}
	}
}

// logoutCloud logs the bot out of the cloud Bot API server, which Telegram
// requires before switching to a self-hosted one. Afterwards the cloud
// server cannot be used for 10 minutes.
func logoutCloud() error {
	token := os.Getenv("DISTORTIONER_BOT_TOKEN")
	if token == "" {
		return errors.New("DISTORTIONER_BOT_TOKEN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+token+"/logOut", nil) //nolint:gosec // fixed host
	if err != nil {
		return errors.New("bad token")
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // fixed host, token from our own env
	if err != nil {
		return errors.New("request to api.telegram.org failed") // the error text would contain the token
	}
	defer resp.Body.Close()
	var body struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if !body.OK {
		return fmt.Errorf("telegram: %s", body.Description)
	}
	return nil
}

// healthcheck is used by the container HEALTHCHECK (the image has no curl).
func healthcheck() int {
	addr := os.Getenv("DISTORTIONER_LISTEN_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return 1
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Always loopback; only the port comes from our own configuration.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/healthz", port), nil) //nolint:gosec // loopback probe
	if err != nil {
		return 1
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // loopback probe
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
