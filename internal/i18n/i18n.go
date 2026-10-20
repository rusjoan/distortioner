// Package i18n holds the bot's user-facing strings in Russian and English.
// Strings are HTML (Telegram's HTML parse mode).
package i18n

import (
	"fmt"
	"strings"
)

// Lang is a supported language.
type Lang string

const (
	EN Lang = "en"
	RU Lang = "ru"
)

// Langs lists supported languages in display order.
var Langs = []Lang{RU, EN}

// Detect maps a Telegram language_code to a supported language.
func Detect(code string) Lang {
	code = strings.ToLower(code)
	for _, prefix := range []string{"ru", "uk", "be", "kk"} {
		if strings.HasPrefix(code, prefix) {
			return RU
		}
	}
	return EN
}

// Parse returns the language for a stored value, or false if unknown.
func Parse(s string) (Lang, bool) {
	switch Lang(s) {
	case EN, RU:
		return Lang(s), true
	}
	return "", false
}

// T formats the message key in lang, falling back to English.
func T(lang Lang, key string, args ...any) string {
	s, ok := messages[lang][key]
	if !ok {
		s, ok = messages[EN][key]
	}
	if !ok {
		return key
	}
	if len(args) == 0 {
		return s
	}
	return fmt.Sprintf(s, args...)
}

var messages = map[Lang]map[string]string{
	EN: {
		"lang.name": "🇬🇧 English",

		"start": "Send me a picture, sticker, GIF, video, video note, voice message or just text, and I'll distort it.\n\n" +
			"In groups, reply to a message with /distort. Add a number for strength: <code>/distort 70</code>.\n\n" +
			"/settings — strength, sound and language.",
		"help": "<b>What I can distort</b>\nphotos, stickers (static and video), GIFs, videos, video notes, voice messages, " +
			"audio files, pictures and videos sent as files, albums and text.\n\n" +
			"<b>Groups</b>: reply with /distort to the message. Strength from %d to %d: <code>/distort 70</code>.\n" +
			"Under each result there is a <b>🔁 More</b> button: it distorts the result once again.\n\n" +
			"Limits: up to %d MB, videos up to %d s.",

		"progress.downloading": "⏬ Downloading…",
		"progress.queued":      "⏳ In queue: #%d",
		"progress.queued_eta":  "⏳ In queue: #%d, about %s",
		"progress.extracting":  "🎞 Extracting frames…",
		"progress.distorting":  "🌀 Distorting frames…\n<code>%s</code> %d%%",
		"progress.encoding":    "📼 Encoding…",

		"err.failed":        "😵 Failed to distort this one.",
		"err.too_big":       "Senpai, it's too big… (max %d MB)",
		"err.too_long":      "Senpai, it's too long… (max %d s)",
		"err.rate_limited":  "Please, not so often. Try again in %s.",
		"err.chat_limit":    "You already have %d jobs in the queue, wait for them to finish.",
		"err.queue_full":    "The queue is full right now, try again in a few minutes.",
		"err.maintenance":   "🛠 The bot is under maintenance, try again later.",
		"err.not_supported": "Animated stickers are not supported yet, sorry.",
		"err.no_rights":     "I don't have the rights to send media in this chat.",
		"err.canceled":      "❌ Canceled.",
		"err.shutdown":      "🔄 The bot is restarting. Please send it again in a minute.",
		"err.reply_needed":  "Reply with /distort to the message you want distorted.",
		"err.reply_group":   "\nIf the group is private, the bot may also need chat history to be visible to new members.",
		"err.nothing":       "There is nothing I can distort in that message.",
		"err.no_video":      "There is no video in this file.",
		"err.strength":      "Strength must be between %d and %d.",
		"err.admins_only":   "Only chat admins can change settings here.",
		"err.not_yours":     "Only the person who sent it can cancel this.",

		"dur.sec": "%d s",
		"dur.min": "%d min %d s",

		"btn.more":   "🔁 More",
		"btn.cancel": "❌ Cancel",

		"settings.title":    "<b>Settings for this chat</b>\nStrength: <b>%d%%</b>\nDistort sound in videos: <b>%s</b>\nLanguage: %s",
		"settings.strength": "Strength",
		"settings.audio":    "🔊 Sound: %s",
		"on":                "on",
		"off":               "off",
		"settings.saved":    "Saved",

		"admin.title":               "<b>🛡 Admin panel</b>\nQueue: %d queued, %d running\nMaintenance: <b>%s</b>",
		"admin.stats":               "📊 Statistics",
		"admin.queue":               "📥 Queue",
		"admin.maintenance":         "🛠 Maintenance",
		"admin.bans":                "🚫 Bans",
		"admin.priority":            "⭐ Priority chats",
		"admin.limits":              "⚙️ Limits",
		"admin.errors":              "❗ Errors",
		"admin.back":                "« Back",
		"admin.refresh":             "🔄 Refresh",
		"admin.yes":                 "✅ Yes",
		"admin.no":                  "✖️ No",
		"admin.cancel_input":        "✖️ Cancel",
		"admin.day":                 "24 h",
		"admin.week":                "Week",
		"admin.month":               "Month",
		"admin.stats_body":          "<b>📊 Statistics, %s</b>\nRequests: %d in %d chats (%d groups)\nFailed: %d · Rejected: %d\nAverage processing: %s\n\n%s",
		"admin.queue_body":          "<b>📥 Queue</b>\nVideo: %d queued, %d running of %d workers, avg %s\nImages: %d queued, %d running\nCPU slots: %d/%d",
		"admin.queue_chat":          "%d: %d queued, %d running",
		"admin.drop_chat":           "Drop %d",
		"admin.confirm_drop":        "Cancel all jobs of chat <code>%d</code>?",
		"admin.dropped":             "Canceled %d jobs.",
		"admin.maint_body":          "<b>🛠 Maintenance</b>: %s\nMessage: %s",
		"admin.maint_on":            "Turn on",
		"admin.maint_off":           "Turn off",
		"admin.maint_text":          "✏️ Edit message",
		"admin.maint_default":       "<i>default</i>",
		"admin.confirm_maint":       "Turn maintenance <b>%s</b>?",
		"admin.list_body":           "<b>%s</b>: %d total",
		"admin.list_empty":          "<b>%s</b>: empty",
		"admin.add":                 "➕ Add",
		"admin.remove":              "✖️ %d",
		"admin.confirm_ban":         "Ban chat <code>%d</code>? Its queued jobs will be canceled.",
		"admin.confirm_unban":       "Unban chat <code>%d</code>?",
		"admin.confirm_prio":        "Add chat <code>%d</code> to priority?",
		"admin.confirm_unprio":      "Remove chat <code>%d</code> from priority?",
		"admin.limits_body":         "<b>⚙️ Limits</b>\nTap a value to change it.",
		"admin.limit_button":        "%s: %d",
		"admin.errors_body":         "<b>❗ Last errors</b>\n%s",
		"admin.errors_empty":        "<b>❗ Last errors</b>\nNo errors 🎉",
		"admin.error_line":          "#%d · %s · %s · <code>%d</code>\n%s",
		"admin.send_file":           "📎 #%d",
		"admin.file_gone":           "The input file is no longer kept.",
		"admin.ask_chat":            "Send the chat id:",
		"admin.ask_value":           "Send a new value for <b>%s</b> (%d…%d):",
		"admin.ask_text":            "Send the new maintenance message (or <code>-</code> for the default):",
		"admin.bad_chat":            "That is not a chat id. Send a number like <code>-1001234567890</code>.",
		"admin.bad_value":           "The value must be a number from %d to %d.",
		"admin.saved":               "✅ Saved",
		"admin.input_expired":       "The input request has expired.",
		"admin.alert":               "❗ %d failed jobs in the last %s.\nLast: %s · <code>%d</code>\n%s",
		"admin.open_errors":         "Open ❗ Errors",
		"limit.max_file_mb":         "Max file size, MB",
		"limit.max_video_seconds":   "Max video length, s",
		"limit.max_jobs_per_chat":   "Jobs per chat",
		"limit.max_queue":           "Queue size",
		"limit.rate_limit":          "Requests per window",
		"limit.rate_window_seconds": "Rate window, s",
		"limit.default_strength":    "Default strength, %",
	},
	RU: {
		"lang.name": "🇷🇺 Русский",

		"start": "Пришли картинку, стикер, гифку, видео, кружок, голосовое или просто текст — я их искажу.\n\n" +
			"В группах ответь на сообщение командой /distort. Сила задаётся числом: <code>/distort 70</code>.\n\n" +
			"/settings — сила, звук и язык.",
		"help": "<b>Что я умею искажать</b>\nфото, стикеры (обычные и видео), гифки, видео, кружки, голосовые, " +
			"аудиофайлы, картинки и видео файлом, альбомы и текст.\n\n" +
			"<b>Группы</b>: ответь командой /distort на сообщение. Сила от %d до %d: <code>/distort 70</code>.\n" +
			"Под каждым результатом есть кнопка <b>🔁 Ещё</b> — она искажает результат повторно.\n\n" +
			"Лимиты: до %d МБ, видео до %d с.",

		"progress.downloading": "⏬ Скачиваю…",
		"progress.queued":      "⏳ В очереди: #%d",
		"progress.queued_eta":  "⏳ В очереди: #%d, примерно %s",
		"progress.extracting":  "🎞 Достаю кадры…",
		"progress.distorting":  "🌀 Искажаю кадры…\n<code>%s</code> %d%%",
		"progress.encoding":    "📼 Собираю видео…",

		"err.failed":        "😵 Не получилось это исказить.",
		"err.too_big":       "Семпай, он слишком большой… (максимум %d МБ)",
		"err.too_long":      "Семпай, оно слишком длинное… (максимум %d с)",
		"err.rate_limited":  "Не так часто, пожалуйста. Попробуй через %s.",
		"err.chat_limit":    "У тебя уже %d задачи в очереди, дождись их.",
		"err.queue_full":    "Очередь сейчас переполнена, попробуй через пару минут.",
		"err.maintenance":   "🛠 Бот на обслуживании, попробуй позже.",
		"err.not_supported": "Анимированные стикеры пока не поддерживаются, извини.",
		"err.no_rights":     "У меня нет прав отправлять медиа в этот чат.",
		"err.canceled":      "❌ Отменено.",
		"err.shutdown":      "🔄 Бот перезапускается. Пришли это ещё раз через минуту.",
		"err.reply_needed":  "Ответь командой /distort на сообщение, которое нужно исказить.",
		"err.reply_group":   "\nЕсли группа закрытая, может понадобиться открыть историю чата для новых участников.",
		"err.nothing":       "В этом сообщении нечего искажать.",
		"err.no_video":      "В этом файле нет видео.",
		"err.strength":      "Сила должна быть от %d до %d.",
		"err.admins_only":   "Менять настройки здесь могут только админы чата.",
		"err.not_yours":     "Отменить может только тот, кто это прислал.",

		"dur.sec": "%d с",
		"dur.min": "%d мин %d с",

		"btn.more":   "🔁 Ещё",
		"btn.cancel": "❌ Отмена",

		"settings.title":    "<b>Настройки этого чата</b>\nСила: <b>%d%%</b>\nИскажать звук в видео: <b>%s</b>\nЯзык: %s",
		"settings.strength": "Сила",
		"settings.audio":    "🔊 Звук: %s",
		"on":                "вкл",
		"off":               "выкл",
		"settings.saved":    "Сохранено",

		"admin.title":               "<b>🛡 Админка</b>\nОчередь: %d ждут, %d в работе\nОбслуживание: <b>%s</b>",
		"admin.stats":               "📊 Статистика",
		"admin.queue":               "📥 Очередь",
		"admin.maintenance":         "🛠 Обслуживание",
		"admin.bans":                "🚫 Баны",
		"admin.priority":            "⭐ Приоритетные чаты",
		"admin.limits":              "⚙️ Лимиты",
		"admin.errors":              "❗ Ошибки",
		"admin.back":                "« Назад",
		"admin.refresh":             "🔄 Обновить",
		"admin.yes":                 "✅ Да",
		"admin.no":                  "✖️ Нет",
		"admin.cancel_input":        "✖️ Отмена",
		"admin.day":                 "24 ч",
		"admin.week":                "Неделя",
		"admin.month":               "Месяц",
		"admin.stats_body":          "<b>📊 Статистика, %s</b>\nЗапросов: %d в %d чатах (групп: %d)\nОшибок: %d · Отказов: %d\nСреднее время обработки: %s\n\n%s",
		"admin.queue_body":          "<b>📥 Очередь</b>\nВидео: %d ждут, %d в работе из %d воркеров, в среднем %s\nКартинки: %d ждут, %d в работе\nСлоты CPU: %d/%d",
		"admin.queue_chat":          "%d: ждут %d, в работе %d",
		"admin.drop_chat":           "Снять %d",
		"admin.confirm_drop":        "Отменить все задачи чата <code>%d</code>?",
		"admin.dropped":             "Отменено задач: %d.",
		"admin.maint_body":          "<b>🛠 Обслуживание</b>: %s\nСообщение: %s",
		"admin.maint_on":            "Включить",
		"admin.maint_off":           "Выключить",
		"admin.maint_text":          "✏️ Изменить текст",
		"admin.maint_default":       "<i>по умолчанию</i>",
		"admin.confirm_maint":       "Обслуживание — <b>%s</b>?",
		"admin.list_body":           "<b>%s</b>: всего %d",
		"admin.list_empty":          "<b>%s</b>: пусто",
		"admin.add":                 "➕ Добавить",
		"admin.remove":              "✖️ %d",
		"admin.confirm_ban":         "Забанить чат <code>%d</code>? Его задачи в очереди будут отменены.",
		"admin.confirm_unban":       "Разбанить чат <code>%d</code>?",
		"admin.confirm_prio":        "Добавить чат <code>%d</code> в приоритетные?",
		"admin.confirm_unprio":      "Убрать чат <code>%d</code> из приоритетных?",
		"admin.limits_body":         "<b>⚙️ Лимиты</b>\nНажми на значение, чтобы изменить.",
		"admin.limit_button":        "%s: %d",
		"admin.errors_body":         "<b>❗ Последние ошибки</b>\n%s",
		"admin.errors_empty":        "<b>❗ Последние ошибки</b>\nОшибок нет 🎉",
		"admin.error_line":          "#%d · %s · %s · <code>%d</code>\n%s",
		"admin.send_file":           "📎 #%d",
		"admin.file_gone":           "Входной файл уже не хранится.",
		"admin.ask_chat":            "Пришли ID чата:",
		"admin.ask_value":           "Пришли новое значение для «%s» (%d…%d):",
		"admin.ask_text":            "Пришли новый текст для обслуживания (или <code>-</code>, чтобы вернуть стандартный):",
		"admin.bad_chat":            "Это не ID чата. Пришли число вроде <code>-1001234567890</code>.",
		"admin.bad_value":           "Нужно число от %d до %d.",
		"admin.saved":               "✅ Сохранено",
		"admin.input_expired":       "Время на ввод истекло.",
		"admin.alert":               "❗ Ошибок за последние %[2]s: %[1]d.\nПоследняя: %[3]s · <code>%[4]d</code>\n%[5]s",
		"admin.open_errors":         "Открыть ❗ Ошибки",
		"limit.max_file_mb":         "Макс. размер файла, МБ",
		"limit.max_video_seconds":   "Макс. длина видео, с",
		"limit.max_jobs_per_chat":   "Задач на чат",
		"limit.max_queue":           "Размер очереди",
		"limit.rate_limit":          "Запросов за окно",
		"limit.rate_window_seconds": "Окно лимита, с",
		"limit.default_strength":    "Сила по умолчанию, %",
	},
}
