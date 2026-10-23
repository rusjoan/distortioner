package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/graynk/distortioner/internal/i18n"
	"github.com/graynk/distortioner/internal/media"
	"github.com/graynk/distortioner/internal/settings"
	"github.com/graynk/distortioner/internal/store"
)

// Callback data formats:
//
//	c:<pool>:<job id>      cancel a job
//	r:<strength>           distort the message with the button again
//	f                      deep-fry the message with the button again
//	s:str:<n> | s:audio | s:lang:<code>   chat settings
//	a:…                    admin panel
func (b *Bot) onCallback(ctx context.Context, cq *models.CallbackQuery) {
	data := cq.Data
	if msg := cq.Message.Message; msg != nil && oneShot(data) &&
		!b.taps.first(fmt.Sprintf("%d:%d:%d:%s", msg.Chat.ID, msg.ID, cq.From.ID, data), time.Now()) {
		b.TG.Answer(ctx, cq.ID, "", false) // a double tap or a retried update
		return
	}
	b.Metrics.Button(buttonAction(data))
	switch {
	case strings.HasPrefix(data, "a:"):
		if b.Cfg.IsAdmin(cq.From.ID) {
			b.Admin.HandleCallback(ctx, cq)
		} else {
			b.TG.Answer(ctx, cq.ID, "", false)
		}
	case strings.HasPrefix(data, "c:"):
		b.onCancel(ctx, cq)
	case strings.HasPrefix(data, "x:"):
		b.onChoice(ctx, cq)
	case strings.HasPrefix(data, "p:"):
		b.onPicker(ctx, cq)
	case strings.HasPrefix(data, "r:"), data == "f":
		b.onMore(ctx, cq) // buttons on results sent before the choice buttons existed
	case strings.HasPrefix(data, "s:"):
		b.onSettings(ctx, cq)
	default:
		b.TG.Answer(ctx, cq.ID, "", false)
	}
}

// tapTTL is how long a repeated tap counts as the same one: a double tap
// or a retried update rather than a wish to run it again.
const tapTTL = 10 * time.Second

// oneShot reports whether a button starts something, so a repeated tap
// must not start it twice. Toggles (picker effects, settings) are left
// alone: tapping one again is how a slip is undone.
func oneShot(data string) bool {
	return strings.HasPrefix(data, "x:") || strings.HasPrefix(data, "c:") || strings.HasPrefix(data, "r:") ||
		data == "f" || data == "p:!" || data == "p:x"
}

// taps remembers recent taps by their idempotency key.
type taps struct {
	mu sync.Mutex
	m  map[string]time.Time
}

func newTaps() *taps { return &taps{m: map[string]time.Time{}} }

// first reports whether key was not seen within tapTTL, and remembers it.
// Keys are one-off and never looked up after they expire, so each call
// drops the expired ones; within a few seconds there are only a handful.
func (t *taps) first(key string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, at := range t.m {
		if now.Sub(at) >= tapTTL {
			delete(t.m, k)
		}
	}
	if _, seen := t.m[key]; seen {
		return false
	}
	t.m[key] = now
	return true
}

func (b *Bot) onCancel(ctx context.Context, cq *models.CallbackQuery) {
	lang := i18n.Detect(cq.From.LanguageCode)
	parts := strings.Split(cq.Data, ":")
	if len(parts) != 3 {
		b.TG.Answer(ctx, cq.ID, "", false)
		return
	}
	id, _ := strconv.ParseUint(parts[2], 10, 64)
	pool := b.Image
	if parts[1] == "v" {
		pool = b.Video
	}

	// Only the requester (or a bot admin) may cancel. Queued jobs remember
	// their owner; for running ones the status message replies to the request.
	var owner int64
	b.mu.Lock()
	if t := b.tracked[id]; t != nil {
		owner = t.userID
	}
	b.mu.Unlock()
	if msg := cq.Message.Message; owner == 0 && msg != nil && msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil {
		owner = msg.ReplyToMessage.From.ID
	}
	if owner != 0 && owner != cq.From.ID && !b.Cfg.IsAdmin(cq.From.ID) {
		b.TG.Answer(ctx, cq.ID, i18n.T(lang, "err.not_yours"), true)
		return
	}
	pool.Cancel(id)
	b.TG.Answer(ctx, cq.ID, "", false)
}

func (b *Bot) onMore(ctx context.Context, cq *models.CallbackQuery) {
	b.TG.Answer(ctx, cq.ID, "", false)
	msg := cq.Message.Message
	// In groups results have no buttons any more; old ones there are dead.
	if msg == nil || msg.Chat.Type != models.ChatTypePrivate || b.Settings.IsBanned(msg.Chat.ID) {
		return
	}
	r := b.newRequest(ctx, msg, &cq.From, msg.ID)
	r.via = store.SourceButton
	r.mode = ModeWobble
	if cq.Data == "f" {
		r.mode = ModeFry
	} else if n, err := strconv.Atoi(strings.TrimPrefix(cq.Data, "r:")); err == nil &&
		n >= settings.MinStrength && n <= settings.MaxStrength {
		r.wobble = media.Strength(n)
	}
	b.process(ctx, r)
}

func (b *Bot) openSettings(ctx context.Context, m *models.Message, r request) {
	if r.isGroup && !b.isChatAdmin(ctx, m.Chat.ID, m.From.ID) {
		b.reply(ctx, r, i18n.T(r.lang, "err.admins_only"))
		return
	}
	text, kb := settingsView(r.lang, r.prefs)
	if _, err := b.TG.SendText(ctx, r.chatID, r.replyTo, text, kb); err != nil {
		b.handleSendError(ctx, r, err)
	}
}

func (b *Bot) onSettings(ctx context.Context, cq *models.CallbackQuery) {
	msg := cq.Message.Message
	if msg == nil {
		b.TG.Answer(ctx, cq.ID, "", false)
		return
	}
	r := b.newRequest(ctx, msg, &cq.From, 0)
	if r.isGroup && !b.isChatAdmin(ctx, msg.Chat.ID, cq.From.ID) {
		b.TG.Answer(ctx, cq.ID, i18n.T(r.lang, "err.admins_only"), true)
		return
	}

	prefs := r.prefs
	parts := strings.Split(cq.Data, ":")
	switch {
	case len(parts) == 2 && parts[1] == "-":
		b.TG.Answer(ctx, cq.ID, "", false) // a row title
		return
	case len(parts) == 3 && (parts[1] == "str" || parts[1] == "fry"):
		if n, err := strconv.Atoi(parts[2]); err == nil && n >= settings.MinStrength && n <= settings.MaxStrength {
			if parts[1] == "fry" {
				prefs.FryStrength = n
			} else {
				prefs.Strength = n
			}
		}
	case len(parts) == 2 && parts[1] == "audio":
		prefs.DistortAudio = !prefs.DistortAudio
	case len(parts) == 3 && parts[1] == "lang":
		if l, ok := i18n.Parse(parts[2]); ok {
			prefs.Lang = string(l)
		}
	}
	if err := b.Store.SaveChatSettings(ctx, msg.Chat.ID, prefs); err != nil {
		b.Log.Warn("save chat settings", "err", err)
		b.TG.Answer(ctx, cq.ID, i18n.T(r.lang, "err.failed"), true)
		return
	}
	lang := r.lang
	if l, ok := i18n.Parse(prefs.Lang); ok {
		lang = l
	}
	b.TG.Answer(ctx, cq.ID, i18n.T(lang, "settings.saved"), false)
	text, kb := settingsView(lang, prefs)
	_ = b.TG.EditText(ctx, msg.Chat.ID, msg.ID, text, kb)
}

var strengthChoices = []int{30, 40, 50, 60, 70, 80}

func settingsView(lang i18n.Lang, p store.ChatSettings) (string, models.ReplyMarkup) {
	audio := i18n.T(lang, "off")
	if p.DistortAudio {
		audio = i18n.T(lang, "on")
	}
	langName := i18n.T(lang, "lang.name")
	if _, ok := i18n.Parse(p.Lang); !ok {
		langName += " (auto)"
	}
	text := i18n.T(lang, "settings.title", p.Strength, p.FryStrength, audio, langName)

	strengthRow := func(key string, current int) []models.InlineKeyboardButton {
		var row []models.InlineKeyboardButton
		for _, n := range strengthChoices {
			label := strconv.Itoa(n)
			if n == current {
				label = "• " + label
			}
			row = append(row, models.InlineKeyboardButton{Text: label, CallbackData: fmt.Sprintf("s:%s:%d", key, n)})
		}
		return row
	}
	title := func(key string) []models.InlineKeyboardButton {
		return []models.InlineKeyboardButton{{Text: "— " + i18n.T(lang, key) + " —", CallbackData: "s:-"}}
	}
	var langs []models.InlineKeyboardButton
	for _, l := range i18n.Langs {
		langs = append(langs, models.InlineKeyboardButton{Text: i18n.T(l, "lang.name"), CallbackData: "s:lang:" + string(l)})
	}
	return text, &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		title("settings.strength"),
		strengthRow("str", p.Strength),
		title("settings.fry"),
		strengthRow("fry", p.FryStrength),
		{{Text: i18n.T(lang, "settings.audio", audio), CallbackData: "s:audio"}},
		langs,
	}}
}

func (b *Bot) isChatAdmin(ctx context.Context, chatID, userID int64) bool {
	if b.Cfg.IsAdmin(userID) {
		return true
	}
	m, err := b.TG.GetChatMember(ctx, &tgbot.GetChatMemberParams{ChatID: chatID, UserID: userID})
	if err != nil {
		b.Log.Warn("get chat member", "chat", chatID, "err", err)
		return false
	}
	return m.Type == models.ChatMemberTypeOwner || m.Type == models.ChatMemberTypeAdministrator
}

// buttonAction names a callback for metrics with a bounded set of labels.
func buttonAction(data string) string {
	switch {
	case data == "x:r":
		return "random"
	case data == "x:w":
		return "wobble"
	case data == "x:f":
		return "fry"
	case data == "x:p":
		return "picker_open"
	case data == "p:!":
		return "picker_apply"
	case data == "p:x":
		return "picker_close"
	case strings.HasPrefix(data, "p:"):
		return "picker_toggle"
	case strings.HasPrefix(data, "c:"):
		return "cancel"
	case strings.HasPrefix(data, "s:"):
		return "settings"
	case strings.HasPrefix(data, "a:"):
		return "admin"
	case strings.HasPrefix(data, "r:"), data == "f":
		return "legacy"
	}
	return "other"
}
