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

		"start": "Send me a picture, sticker, GIF, video, video note or voice message, and I'll ask what to do with it. Text gets mOcKeD right away.\n\n" +
			"/distort — random effects · /wobble — the classic distortion · /fry — deep fry · /fx — the menu.\n" +
			"In groups, reply to a message with one of these commands.\n\n" +
			"/settings — strength, sound and language.",
		"help": "<b>What I can process</b>\nphotos, stickers (static and video), GIFs, videos, video notes, voice messages, " +
			"audio files, pictures and videos sent as files, albums and text.\n\n" +
			"<b>Commands</b> (as a reply or as the caption of the media):\n" +
			"/distort — a random mix of effects\n" +
			"/wobble — the classic seam-carving distortion, strength %[1]d…%[2]d: <code>/wobble 70</code>\n" +
			"/fry — deep fry: burnt colours, shaking and earrape, strength %[1]d…%[2]d: <code>/fry 70</code>\n" +
			"/fx — the menu of effects, the same as in a private chat\n\n" +
			"In a private chat just send the media: I'll ask what to do, and <b>✨ Pick</b> lets you choose effects one by one. " +
			"The same buttons are under every result, so effects can be chained.\n\n" +
			"Limits: up to %[3]d MB, videos up to %[4]d s.",

		"progress.downloading":  "⏬ Downloading…",
		"progress.queued":       "⏳ In queue: #%d",
		"progress.queued_eta":   "⏳ In queue: #%d, about %s",
		"progress.extracting":   "🎞 Extracting frames…",
		"progress.distorting":   "🌀 Distorting frames…\n<code>%s</code> %d%%",
		"progress.encoding":     "📼 Encoding…",
		"progress.uploading":    "⏫ Uploading…",
		"progress.encoding_pct": "📼 Encoding…\n<code>%s</code> %d%%",
		"progress.processing":   "⚙️ Processing…\n<code>%s</code> %d%%",

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
		"err.reply_needed":  "Reply with /%s to the message you want processed, or send the media with /%[1]s as its caption.",
		"err.reply_group":   "\nIf the group is private, the bot may also need chat history to be visible to new members.",
		"err.nothing":       "There is nothing I can distort in that message.",
		"err.no_video":      "There is no video in this file.",
		"err.strength":      "Strength must be between %d and %d.",
		"err.admins_only":   "Only chat admins can change settings here.",
		"err.not_yours":     "Only the person who sent it can cancel this.",

		"dur.sec": "%d s",
		"dur.min": "%d min %d s",

		"btn.cancel":        "❌ Cancel",
		"ask.what":          "What should I do with it?",
		"ask.pick":          "Pick effects and tap Apply:",
		"btn.random":        "🎲 Random",
		"btn.random_again":  "🎲 Random again",
		"btn.wobble":        "🌀 Wobble",
		"btn.fry":           "🔥 Fry",
		"btn.pick":          "✨ Pick",
		"err.expired":       "This is too old, send the media again.",
		"picker.presets":    "presets",
		"picker.picture":    "picture",
		"picker.time":       "time",
		"picker.sound":      "sound",
		"picker.apply":      "▶️ Apply (%d)",
		"picker.close":      "✖️",
		"picker.empty":      "Pick at least one effect.",
		"fx.wobble":         "🌀 Wobble",
		"fx.fry":            "🔥 Fry",
		"fx.bulge":          "🫧 Bulge",
		"fx.swirl":          "🌪 Swirl",
		"fx.pixel":          "🟪 Pixels",
		"fx.rainbow":        "🌈 Rainbow",
		"fx.mirror":         "🪞 Mirror",
		"fx.trails":         "👻 Trails",
		"fx.drunk":          "🥴 Drunk",
		"fx.shake":          "📳 Shake",
		"fx.reverse":        "⏪ Reverse",
		"fx.fast":           "⏩ ×2",
		"fx.slow":           "🐌 ×0.5",
		"fx.chipmunk":       "🐿 Chipmunk",
		"fx.demon":          "😈 Demon",
		"fx.earrape":        "🔊 Earrape",
		"fx.echo":           "🗣 Echo",
		"fx.demotivator":    "🖼 Demotivator",
		"fx.mock":           "〰️ пРиВеТ",
		"fx.baby":           "👶 Baby talk",
		"fx.typos":          "🍺 Drunk typing",
		"fx.caps":           "📢 CAPS!!1",
		"fx.spaced":         "↔️ S p a c e d",
		"fx.emoji":          "😂 Emoji pasta",
		"picker.text":       "text",
		"picker.caption":    "caption",
		"settings.fry":      "Fry strength",
		"admin.effects":     "<b>Effects</b>",
		"admin.effect_line": "%s: %d (%d%%)",
		"admin.sources":     "Asked via: commands %d · random %d · question %d · picker %d · buttons %d",

		"settings.title":    "<b>Settings for this chat</b>\nWobble strength: <b>%d%%</b>\nFry strength: <b>%d%%</b>\nWobble distorts sound in videos: <b>%s</b>\nLanguage: %s",
		"settings.strength": "Wobble strength",
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
		"limit.max_fps":             "Max video fps",
		"limit.process_scale":       "Video processing scale, %",
		"limit.max_jobs_per_chat":   "Jobs per chat",
		"limit.max_queue":           "Queue size",
		"limit.rate_limit":          "Requests per window",
		"limit.rate_window_seconds": "Rate window, s",
		"limit.default_strength":    "Default strength, %",
	},
	RU: {
		"lang.name": "🇷🇺 Русский",

		"start": "Пришли картинку, стикер, гифку, видео, кружок или голосовое — я спрошу, что с ним сделать. Текст сразу превращается в «пРиВеТ».\n\n" +
			"/distort — случайные эффекты · /wobble — классическое искажение · /fry — прожарка · /fx — меню.\n" +
			"В группах отвечай одной из этих команд на сообщение.\n\n" +
			"/settings — сила, звук и язык.",
		"help": "<b>Что я умею обрабатывать</b>\nфото, стикеры (обычные и видео), гифки, видео, кружки, голосовые, " +
			"аудиофайлы, картинки и видео файлом, альбомы и текст.\n\n" +
			"<b>Команды</b> (ответом на сообщение или в подписи к медиа):\n" +
			"/distort — случайный набор эффектов\n" +
			"/wobble — классическое искажение, сила от %[1]d до %[2]d: <code>/wobble 70</code>\n" +
			"/fry — прожарка: выжженные цвета, тряска и earrape, сила от %[1]d до %[2]d: <code>/fry 70</code>\n" +
			"/fx — меню эффектов, как в личке\n\n" +
			"В личке просто пришли медиа: я спрошу, что сделать, а <b>✨ Выбрать</b> даст собрать эффекты по одному. " +
			"Те же кнопки есть под каждым результатом, так что эффекты можно применять цепочкой.\n\n" +
			"Лимиты: до %[3]d МБ, видео до %[4]d с.",

		"progress.downloading":  "⏬ Скачиваю…",
		"progress.queued":       "⏳ В очереди: #%d",
		"progress.queued_eta":   "⏳ В очереди: #%d, примерно %s",
		"progress.extracting":   "🎞 Достаю кадры…",
		"progress.distorting":   "🌀 Искажаю кадры…\n<code>%s</code> %d%%",
		"progress.encoding":     "📼 Собираю видео…",
		"progress.uploading":    "⏫ Отправляю…",
		"progress.encoding_pct": "📼 Собираю видео…\n<code>%s</code> %d%%",
		"progress.processing":   "⚙️ Обрабатываю…\n<code>%s</code> %d%%",

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
		"err.reply_needed":  "Ответь командой /%s на сообщение, которое нужно обработать, или пришли медиа с /%[1]s в подписи.",
		"err.reply_group":   "\nЕсли группа закрытая, может понадобиться открыть историю чата для новых участников.",
		"err.nothing":       "В этом сообщении нечего искажать.",
		"err.no_video":      "В этом файле нет видео.",
		"err.strength":      "Сила должна быть от %d до %d.",
		"err.admins_only":   "Менять настройки здесь могут только админы чата.",
		"err.not_yours":     "Отменить может только тот, кто это прислал.",

		"dur.sec": "%d с",
		"dur.min": "%d мин %d с",

		"btn.cancel":        "❌ Отмена",
		"ask.what":          "Что с этим сделать?",
		"ask.pick":          "Выбери эффекты и нажми «Применить»:",
		"btn.random":        "🎲 Случайно",
		"btn.random_again":  "🎲 Ещё случайно",
		"btn.wobble":        "🌀 Wobble",
		"btn.fry":           "🔥 Прожарка",
		"btn.pick":          "✨ Выбрать",
		"err.expired":       "Это уже устарело, пришли медиа ещё раз.",
		"picker.presets":    "пресеты",
		"picker.picture":    "картинка",
		"picker.time":       "время",
		"picker.sound":      "звук",
		"picker.apply":      "▶️ Применить (%d)",
		"picker.close":      "✖️",
		"picker.empty":      "Выбери хотя бы один эффект.",
		"fx.wobble":         "🌀 Wobble",
		"fx.fry":            "🔥 Прожарка",
		"fx.bulge":          "🫧 Раздуть",
		"fx.swirl":          "🌪 Водоворот",
		"fx.pixel":          "🟪 Пиксели",
		"fx.rainbow":        "🌈 Радуга",
		"fx.mirror":         "🪞 Зеркало",
		"fx.trails":         "👻 Шлейфы",
		"fx.drunk":          "🥴 Пьяный",
		"fx.shake":          "📳 Тряска",
		"fx.reverse":        "⏪ Реверс",
		"fx.fast":           "⏩ ×2",
		"fx.slow":           "🐌 ×0,5",
		"fx.chipmunk":       "🐿 Бурундук",
		"fx.demon":          "😈 Демон",
		"fx.earrape":        "🔊 Earrape",
		"fx.echo":           "🗣 Эхо",
		"fx.demotivator":    "🖼 Демотиватор",
		"fx.mock":           "〰️ пРиВеТ",
		"fx.baby":           "👶 Лепет",
		"fx.typos":          "🍺 Пьяная печать",
		"fx.caps":           "📢 КАПС!!1",
		"fx.spaced":         "↔️ Р а з р я д к а",
		"fx.emoji":          "😂 Эмодзи-паста",
		"picker.text":       "текст",
		"picker.caption":    "подпись",
		"settings.fry":      "Сила прожарки",
		"admin.effects":     "<b>Эффекты</b>",
		"admin.effect_line": "%s: %d (%d%%)",
		"admin.sources":     "Как просили: командой %d · случайно %d · из вопроса %d · выбором %d · кнопками %d",

		"settings.title":    "<b>Настройки этого чата</b>\nСила wobble: <b>%d%%</b>\nСила прожарки: <b>%d%%</b>\nWobble искажает звук в видео: <b>%s</b>\nЯзык: %s",
		"settings.strength": "Сила wobble",
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
		"limit.max_fps":             "Макс. fps видео",
		"limit.process_scale":       "Масштаб обработки видео, %",
		"limit.max_jobs_per_chat":   "Задач на чат",
		"limit.max_queue":           "Размер очереди",
		"limit.rate_limit":          "Запросов за окно",
		"limit.rate_window_seconds": "Окно лимита, с",
		"limit.default_strength":    "Сила по умолчанию, %",
	},
}
