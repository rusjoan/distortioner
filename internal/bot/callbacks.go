package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"

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
	switch {
	case strings.HasPrefix(data, "a:"):
		if b.Cfg.IsAdmin(cq.From.ID) {
			b.Admin.HandleCallback(ctx, cq)
		} else {
			b.TG.Answer(ctx, cq.ID, "", false)
		}
	case strings.HasPrefix(data, "c:"):
		b.onCancel(ctx, cq)
	case strings.HasPrefix(data, "r:"), data == "f":
		b.onMore(ctx, cq)
	case strings.HasPrefix(data, "s:"):
		b.onSettings(ctx, cq)
	default:
		b.TG.Answer(ctx, cq.ID, "", false)
	}
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
	if msg == nil || b.Settings.IsBanned(msg.Chat.ID) {
		return
	}
	r := b.newRequest(ctx, msg, &cq.From, msg.ID)
	if cq.Data == "f" {
		r.effect = EffectFry
	} else if n, err := strconv.Atoi(strings.TrimPrefix(cq.Data, "r:")); err == nil &&
		n >= settings.MinStrength && n <= settings.MaxStrength {
		r.strength = media.Strength(n)
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
	case len(parts) == 3 && parts[1] == "str":
		if n, err := strconv.Atoi(parts[2]); err == nil && n >= settings.MinStrength && n <= settings.MaxStrength {
			prefs.Strength = n
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
	text := i18n.T(lang, "settings.title", p.Strength, audio, langName)

	var strengths []models.InlineKeyboardButton
	for _, n := range strengthChoices {
		label := strconv.Itoa(n)
		if n == p.Strength {
			label = "• " + label
		}
		strengths = append(strengths, models.InlineKeyboardButton{Text: label, CallbackData: fmt.Sprintf("s:str:%d", n)})
	}
	var langs []models.InlineKeyboardButton
	for _, l := range i18n.Langs {
		langs = append(langs, models.InlineKeyboardButton{Text: i18n.T(l, "lang.name"), CallbackData: "s:lang:" + string(l)})
	}
	return text, &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		strengths,
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
