// Package bot routes Telegram updates to distortion jobs.
package bot

import (
	"context"
	"errors"
	"html"
	"log/slog"
	"math/rand/v2"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/graynk/distortioner/internal/admin"
	"github.com/graynk/distortioner/internal/config"
	"github.com/graynk/distortioner/internal/i18n"
	"github.com/graynk/distortioner/internal/jobs"
	"github.com/graynk/distortioner/internal/limiter"
	"github.com/graynk/distortioner/internal/media"
	"github.com/graynk/distortioner/internal/metrics"
	"github.com/graynk/distortioner/internal/settings"
	"github.com/graynk/distortioner/internal/store"
	"github.com/graynk/distortioner/internal/telegram"
)

// defaultFryStrength is the reference fry; strength moves it both ways.
const defaultFryStrength = 50

const (
	// maxUpdateAge drops updates that piled up while the bot was down.
	maxUpdateAge = 2 * time.Hour
	// noRightsTTL suppresses attempts in chats where we may not send media.
	noRightsTTL = 10 * time.Minute
	// positionRefresh is how often queued jobs get their place updated.
	positionRefresh = 5 * time.Second
	// botAPISweep is how often old files of a local Bot API server are deleted.
	botAPISweep = 15 * time.Minute
)

// Deps wires the bot.
type Deps struct {
	Cfg      *config.Config
	TG       *telegram.Client
	Store    *store.Store
	Settings *settings.Service
	Engine   *media.Engine
	Video    *jobs.Pool
	Image    *jobs.Pool
	Limiter  *limiter.Limiter
	Metrics  *metrics.Metrics
	Admin    *admin.Panel
	Log      *slog.Logger
	Me       *models.User
}

// Bot handles updates. It is safe for concurrent use.
type Bot struct {
	Deps

	albums  *albumCollector
	pending *pendingStore

	rndMu sync.Mutex
	rnd   *rand.Rand

	mu       sync.Mutex
	noRights map[int64]time.Time
	tracked  map[uint64]*tracked // queued heavy jobs with a progress message
}

// tracked is a queued job whose status message shows its place in line.
type tracked struct {
	pool     *jobs.Pool
	poolName string
	progress *telegram.Progress
	lang     i18n.Lang
	userID   int64
}

// New creates the bot.
func New(d Deps) *Bot {
	b := &Bot{Deps: d, noRights: map[int64]time.Time{}, tracked: map[uint64]*tracked{},
		pending: newPendingStore(), rnd: newRand()}
	b.albums = newAlbumCollector(albumWait, b.ask)
	return b
}

// Handle is the go-telegram default handler.
func (b *Bot) Handle(ctx context.Context, _ *tgbot.Bot, u *models.Update) {
	defer func() {
		if r := recover(); r != nil {
			b.Log.Error("handler panic", "panic", r, "stack", string(debug.Stack()))
		}
	}()
	switch {
	case u.CallbackQuery != nil:
		b.onCallback(ctx, u.CallbackQuery)
	case u.Message != nil:
		b.onMessage(ctx, u.Message)
	}
}

// request is one distortion request after routing.
type request struct {
	chatID  int64
	isGroup bool
	userID  int64
	replyTo int
	lang    i18n.Lang
	prefs   store.ChatSettings
	mode    Mode
	recipe  media.Recipe // for ModeRecipe
	wobble  media.Strength
	fry     media.Strength
	via     string // statistics source when it is not implied by the mode
	source  *models.Message
}

func (b *Bot) onMessage(ctx context.Context, m *models.Message) {
	if m.From == nil || m.From.IsBot {
		return
	}
	if time.Since(time.Unix(int64(m.Date), 0)) > maxUpdateAge {
		return
	}
	isAdmin := b.Cfg.IsAdmin(m.From.ID)
	if !isAdmin && b.Settings.IsBanned(m.Chat.ID) {
		return
	}
	isGroup := m.Chat.Type != models.ChatTypePrivate

	if !isGroup && isAdmin && b.Admin.Awaiting(m.From.ID) && m.Text != "" && !strings.HasPrefix(m.Text, "/") {
		b.Admin.HandleInput(ctx, m)
		return
	}

	cmd, isCmd, foreign := parseCommand(m.Text, b.Me.Username)
	if !isCmd && !foreign {
		// A command in the caption applies to the media it is attached to.
		cmd, isCmd, foreign = parseCommand(m.Caption, b.Me.Username)
	}
	if foreign {
		return
	}
	if isCmd {
		b.onCommand(ctx, m, cmd, isGroup, isAdmin)
		return
	}
	if isGroup {
		return // in groups we only react to commands
	}
	r := b.newRequest(ctx, m, m.From, m.ID)
	it, ok, _ := classify(m)
	if !ok || it.kind == KindText {
		b.process(ctx, r) // text is distorted right away; the rest explains itself
		return
	}
	// Media without a command: ask what to do.
	if m.MediaGroupID != "" {
		b.albums.add(m.MediaGroupID, r)
		return
	}
	b.ask(ctx, []request{r})
}

func (b *Bot) newRequest(ctx context.Context, source *models.Message, from *models.User, replyTo int) request {
	prefs := b.prefs(ctx, source.Chat.ID)
	lang, ok := i18n.Parse(prefs.Lang)
	if !ok {
		lang = i18n.Detect(from.LanguageCode)
	}
	return request{
		chatID:  source.Chat.ID,
		isGroup: source.Chat.Type != models.ChatTypePrivate,
		userID:  from.ID,
		replyTo: replyTo,
		lang:    lang,
		prefs:   prefs,
		wobble:  media.Strength(prefs.Strength),
		fry:     media.Strength(prefs.FryStrength),
		source:  source,
	}
}

func (b *Bot) prefs(ctx context.Context, chatID int64) store.ChatSettings {
	p, ok, err := b.Store.ChatSettings(ctx, chatID)
	if err != nil {
		b.Log.Warn("load chat settings", "chat", chatID, "err", err)
	}
	if !ok {
		p = store.ChatSettings{Strength: b.Settings.Int(settings.DefaultStrength), FryStrength: defaultFryStrength, DistortAudio: true}
	}
	if p.FryStrength == 0 {
		p.FryStrength = defaultFryStrength
	}
	return p
}

func (b *Bot) onCommand(ctx context.Context, m *models.Message, cmd command, isGroup, isAdmin bool) {
	r := b.newRequest(ctx, m, m.From, m.ID)
	switch cmd.name {
	case "start":
		b.reply(ctx, r, i18n.T(r.lang, "start"))
	case "help":
		b.reply(ctx, r, i18n.T(r.lang, "help", settings.MinStrength, settings.MaxStrength,
			b.Settings.Int(settings.MaxFileMB), b.Settings.Int(settings.MaxVideoSeconds)))
	case "settings":
		b.openSettings(ctx, m, r)
	case "admin":
		if isAdmin && !isGroup {
			b.Admin.Open(ctx, m)
		}
	case "fx":
		// The menu of the private chat, for media in a group.
		target := m.ReplyToMessage
		if target == nil && m.Caption != "" {
			target = m
		}
		if target == nil {
			text := i18n.T(r.lang, "err.reply_needed", cmd.name)
			if isGroup {
				text += i18n.T(r.lang, "err.reply_group")
			}
			b.reply(ctx, r, text)
			return
		}
		r.source = target
		if _, ok, unsupported := classify(target); !ok || unsupported {
			b.process(ctx, r) // explains what is wrong with it
			return
		}
		b.ask(ctx, []request{r})
	case "distort", "wobble", "fry":
		r.mode = map[string]Mode{"distort": ModeRandom, "wobble": ModeWobble, "fry": ModeFry}[cmd.name]
		if cmd.args != "" {
			n, err := strconv.Atoi(cmd.args)
			if err != nil || n < settings.MinStrength || n > settings.MaxStrength {
				b.reply(ctx, r, i18n.T(r.lang, "err.strength", settings.MinStrength, settings.MaxStrength))
				return
			}
			if cmd.name != "fry" {
				r.wobble = media.Strength(n)
			}
			if cmd.name != "wobble" {
				r.fry = media.Strength(n)
			}
		}
		switch {
		case m.ReplyToMessage != nil:
			r.source = m.ReplyToMessage
		case m.Caption != "":
			r.source = m // the command was the caption of this media
		default:
			text := i18n.T(r.lang, "err.reply_needed", cmd.name)
			if isGroup {
				text += i18n.T(r.lang, "err.reply_group")
			}
			b.reply(ctx, r, text)
			return
		}
		b.process(ctx, r)
	}
}

// process validates a request and queues the work.
func (b *Bot) process(ctx context.Context, r request) {
	it, ok, unsupported := classify(r.source)
	switch {
	case unsupported:
		b.reply(ctx, r, i18n.T(r.lang, "err.not_supported"))
		return
	case !ok:
		if r.isGroup {
			b.reply(ctx, r, i18n.T(r.lang, "err.nothing"))
		}
		return
	case it.kind == KindText:
		b.distortText(ctx, r)
		return
	}
	b.submit(ctx, r, it.kind, []item{it})
}

func (b *Bot) distortText(ctx context.Context, r request) {
	var recipe media.Recipe
	switch r.mode {
	case ModeWobble:
		recipe = media.Recipe{{ID: "mock"}} // the classic
	case ModeFry:
		recipe = media.FriedText
	case ModeRecipe:
		recipe = r.recipe
	default:
		b.rndMu.Lock()
		recipe = media.RandomText(b.rnd)
		b.rndMu.Unlock()
	}
	b.rndMu.Lock()
	text := media.ApplyText(b.rnd, recipe, r.source.Text)
	b.rndMu.Unlock()
	if _, err := b.TG.SendText(ctx, r.chatID, r.replyTo, html.EscapeString(text), textChoiceMarkup(r.lang, true)); err != nil {
		b.handleSendError(ctx, r, err)
		b.record(r, KindText, store.StatusFailed, 0, nil)
		return
	}
	b.record(r, KindText, store.StatusOK, 0, recipe)
}

// admit applies maintenance mode, size and rate limits. It replies itself
// when the request is rejected.
func (b *Bot) admit(ctx context.Context, r request, kind Kind, items []item) bool {
	if b.blockedByRights(r.chatID) {
		return false
	}
	if on, text := b.Settings.Maintenance(); on && !b.Cfg.IsAdmin(r.userID) {
		if text == "" {
			text = i18n.T(r.lang, "err.maintenance")
		} else {
			text = html.EscapeString(text)
		}
		b.reject(ctx, r, kind, text)
		return false
	}
	maxMB := b.Settings.Int(settings.MaxFileMB)
	for _, it := range items {
		if it.size > int64(maxMB)<<20 {
			b.reject(ctx, r, kind, i18n.T(r.lang, "err.too_big", maxMB))
			return false
		}
	}
	if ok, wait := b.Limiter.Allow(r.chatID, time.Now()); !ok && !b.Cfg.IsAdmin(r.userID) {
		b.reject(ctx, r, kind, i18n.T(r.lang, "err.rate_limited", humanDuration(r.lang, wait)))
		return false
	}
	return true
}

func (b *Bot) reject(ctx context.Context, r request, kind Kind, text string) {
	b.reply(ctx, r, text)
	b.record(r, kind, store.StatusRejected, 0, nil)
}

func (b *Bot) reply(ctx context.Context, r request, text string) {
	if _, err := b.TG.SendText(ctx, r.chatID, r.replyTo, text, nil); err != nil {
		b.handleSendError(ctx, r, err)
	}
}

// handleSendError reacts to errors that say something about the chat.
func (b *Bot) handleSendError(ctx context.Context, r request, err error) {
	switch {
	case telegram.IsBlocked(err):
		// Nobody will see the results: drop the rest of their jobs.
		n := b.Video.CancelChat(r.chatID) + b.Image.CancelChat(r.chatID)
		b.Log.Info("bot blocked in chat", "chat", r.chatID, "canceled", n)
	case telegram.IsNoRights(err):
		b.mu.Lock()
		b.noRights[r.chatID] = time.Now().Add(noRightsTTL)
		b.mu.Unlock()
		_, _ = b.TG.SendText(ctx, r.chatID, r.replyTo, i18n.T(r.lang, "err.no_rights"), nil)
	default:
		b.Log.Warn("send message", "chat", r.chatID, "err", err)
	}
}

func (b *Bot) blockedByRights(chatID int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	until, ok := b.noRights[chatID]
	if ok && time.Now().After(until) {
		delete(b.noRights, chatID)
		return false
	}
	return ok
}

func (b *Bot) record(r request, k Kind, status string, d time.Duration, recipe media.Recipe) {
	kind := string(k)
	b.Metrics.Request(kind, status)
	if status == store.StatusOK && d > 0 {
		b.Metrics.Duration(kind, d)
	}
	var effects []string
	if status == store.StatusOK {
		effects = recipe.IDs()
		for _, e := range effects {
			b.Metrics.Effect(e)
		}
	}
	// Stats must not slow down or fail the request.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := b.Store.RecordEvent(ctx, store.Event{
			ChatID: r.chatID, IsGroup: r.isGroup, Kind: kind, Status: status, Duration: d,
			Source: r.statSource(), Effects: effects,
		}); err != nil {
			b.Log.Warn("record event", "err", err)
		}
	}()
}

// errorText maps a job error to what the user sees.
func errorText(lang i18n.Lang, err error) string {
	var tooLong *media.TooLongError
	switch {
	case errors.As(err, &tooLong):
		return i18n.T(lang, "err.too_long", tooLong.Limit)
	case errors.Is(err, media.ErrNoVideo):
		return i18n.T(lang, "err.no_video")
	case errors.Is(err, jobs.ErrCanceled):
		return i18n.T(lang, "err.canceled")
	case errors.Is(err, jobs.ErrShutdown):
		return i18n.T(lang, "err.shutdown")
	}
	return i18n.T(lang, "err.failed")
}

// userError reports errors caused by the input rather than by the bot.
func userError(err error) bool {
	var tooLong *media.TooLongError
	return errors.As(err, &tooLong) || errors.Is(err, media.ErrNoVideo) ||
		errors.Is(err, jobs.ErrCanceled) || errors.Is(err, jobs.ErrShutdown)
}

func humanDuration(lang i18n.Lang, d time.Duration) string {
	d = max(d.Round(time.Second), time.Second)
	if d < time.Minute {
		return i18n.T(lang, "dur.sec", int(d.Seconds()))
	}
	return i18n.T(lang, "dur.min", int(d.Minutes()), int(d.Seconds())%60)
}

// Run performs periodic housekeeping until ctx is done.
func (b *Bot) Run(ctx context.Context) {
	positions := time.NewTicker(positionRefresh)
	defer positions.Stop()
	hourly := time.NewTicker(time.Hour)
	defer hourly.Stop()
	sweep := time.NewTicker(botAPISweep)
	defer sweep.Stop()
	b.housekeeping(ctx)
	b.sweepBotAPIFiles()
	for {
		select {
		case <-ctx.Done():
			return
		case <-positions.C:
			b.refreshPositions(ctx)
		case <-hourly.C:
			b.housekeeping(ctx)
			b.pending.sweep(time.Now())
		case <-sweep.C:
			b.sweepBotAPIFiles()
		}
	}
}

// sweepBotAPIFiles deletes media a self-hosted Bot API server downloaded
// for us: the server keeps every file forever.
func (b *Bot) sweepBotAPIFiles() {
	if b.Cfg.BotAPIDir == "" {
		return
	}
	n, freed, err := telegram.SweepLocalFiles(b.Cfg.BotAPIDir, b.Cfg.BotAPIFilesTTL, time.Now())
	if err != nil {
		b.Log.Warn("sweep bot api files", "dir", b.Cfg.BotAPIDir, "err", err)
	}
	if n > 0 {
		b.Log.Info("deleted old bot api files", "count", n, "mb", freed>>20)
	}
}

func (b *Bot) refreshPositions(ctx context.Context) {
	b.mu.Lock()
	snapshot := make(map[uint64]*tracked, len(b.tracked))
	for id, t := range b.tracked {
		snapshot[id] = t
	}
	b.mu.Unlock()
	for id, t := range snapshot {
		pos, eta, waits := t.pool.Waits(id)
		if !waits {
			if _, _, queued := t.pool.Position(id); !queued { // started or finished
				b.untrack(id)
			}
			continue
		}
		t.progress.Queued(ctx, queuedText(t.lang, pos, eta), cancelMarkup(t.lang, t.poolName, id))
	}
}

func (b *Bot) untrack(id uint64) {
	b.mu.Lock()
	delete(b.tracked, id)
	b.mu.Unlock()
}

func queuedText(lang i18n.Lang, pos int, eta time.Duration) string {
	if eta <= 0 {
		return i18n.T(lang, "progress.queued", pos)
	}
	return i18n.T(lang, "progress.queued_eta", pos, humanDuration(lang, eta))
}
