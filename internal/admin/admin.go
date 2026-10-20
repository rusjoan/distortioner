// Package admin implements the admin panel: a single message navigated
// with inline buttons, plus free-text input for ids and values.
package admin

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/graynk/distortioner/internal/config"
	"github.com/graynk/distortioner/internal/i18n"
	"github.com/graynk/distortioner/internal/jobs"
	"github.com/graynk/distortioner/internal/media"
	"github.com/graynk/distortioner/internal/settings"
	"github.com/graynk/distortioner/internal/store"
	"github.com/graynk/distortioner/internal/telegram"
)

const (
	inputTTL      = 5 * time.Minute
	pageSize      = 8
	errorsShown   = 10
	alertInterval = 10 * time.Minute
)

// Deps are the services the panel controls.
type Deps struct {
	Cfg      *config.Config
	TG       *telegram.Client
	Store    *store.Store
	Settings *settings.Service
	Video    *jobs.Pool
	Image    *jobs.Pool
	Engine   *media.Engine
	Log      *slog.Logger
}

type inputKind int

const (
	inputChat  inputKind = iota // a chat id for a ban/priority set
	inputValue                  // an integer setting
	inputText                   // the maintenance message
)

type pendingInput struct {
	kind    inputKind
	set     string // "bans" or "prio" for inputChat
	key     settings.Key
	chatID  int64 // where the panel message is
	panelID int
	expires time.Time
}

// Panel is safe for concurrent use.
type Panel struct {
	Deps

	mu     sync.Mutex
	inputs map[int64]*pendingInput
	langs  map[int64]i18n.Lang

	alertMu    sync.Mutex
	alertCount int
	alertLast  store.Failure
}

// New creates the panel.
func New(d Deps) *Panel {
	return &Panel{Deps: d, inputs: map[int64]*pendingInput{}, langs: map[int64]i18n.Lang{}}
}

func (p *Panel) lang(u *models.User) i18n.Lang {
	l := i18n.Detect(u.LanguageCode)
	p.mu.Lock()
	p.langs[u.ID] = l
	p.mu.Unlock()
	return l
}

// Open sends a fresh panel message.
func (p *Panel) Open(ctx context.Context, m *models.Message) {
	l := p.lang(m.From)
	p.clearInput(m.From.ID)
	text, kb := p.home(l)
	if _, err := p.TG.SendText(ctx, m.Chat.ID, 0, text, kb); err != nil {
		p.Log.Warn("send admin panel", "err", err)
	}
}

// Awaiting reports whether the admin was asked to type something.
func (p *Panel) Awaiting(userID int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	in, ok := p.inputs[userID]
	return ok && time.Now().Before(in.expires)
}

func (p *Panel) clearInput(userID int64) *pendingInput {
	p.mu.Lock()
	defer p.mu.Unlock()
	in := p.inputs[userID]
	delete(p.inputs, userID)
	return in
}

// HandleInput consumes a text message typed in response to a prompt.
func (p *Panel) HandleInput(ctx context.Context, m *models.Message) {
	l := p.lang(m.From)
	in := p.clearInput(m.From.ID)
	if in == nil {
		return
	}
	p.TG.Delete(ctx, m.Chat.ID, m.ID) // keep the chat tidy: the panel shows the result
	if time.Now().After(in.expires) {
		p.edit(ctx, in.chatID, in.panelID, i18n.T(l, "admin.input_expired"), p.backTo(l, "home"))
		return
	}
	text := strings.TrimSpace(m.Text)

	var (
		view string
		kb   *models.InlineKeyboardMarkup
	)
	switch in.kind {
	case inputChat:
		id, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			view, kb = p.prompt(l, i18n.T(l, "admin.bad_chat")+"\n\n"+i18n.T(l, "admin.ask_chat"))
			p.await(m.From.ID, in)
			break
		}
		view, kb = p.confirmAdd(l, in.set, id)
	case inputValue:
		spec, _ := p.Settings.Spec(in.key)
		n, err := strconv.Atoi(text)
		if err == nil {
			err = p.Settings.SetInt(ctx, in.key, n)
		}
		if err != nil {
			view, kb = p.prompt(l, i18n.T(l, "admin.bad_value", spec.Min, spec.Max)+"\n\n"+
				i18n.T(l, "admin.ask_value", i18n.T(l, "limit."+string(in.key)), spec.Min, spec.Max))
			p.await(m.From.ID, in)
			break
		}
		view, kb = p.limits(l)
		view = i18n.T(l, "admin.saved") + "\n\n" + view
	case inputText:
		if text == "-" {
			text = ""
		}
		if err := p.Settings.SetMaintenanceText(ctx, text); err != nil {
			p.Log.Error("save maintenance text", "err", err)
		}
		view, kb = p.maintenance(l)
	}
	p.edit(ctx, in.chatID, in.panelID, view, kb)
}

func (p *Panel) await(userID int64, in *pendingInput) {
	in.expires = time.Now().Add(inputTTL)
	p.mu.Lock()
	p.inputs[userID] = in
	p.mu.Unlock()
}

// HandleCallback handles "a:…" buttons.
func (p *Panel) HandleCallback(ctx context.Context, cq *models.CallbackQuery) {
	msg := cq.Message.Message
	if msg == nil {
		p.TG.Answer(ctx, cq.ID, "", false)
		return
	}
	l := p.lang(&cq.From)
	parts := strings.Split(strings.TrimPrefix(cq.Data, "a:"), ":")
	action, args := parts[0], parts[1:]
	arg := func(i int) string {
		if i < len(args) {
			return args[i]
		}
		return ""
	}
	chatArg := func(i int) int64 {
		n, _ := strconv.ParseInt(arg(i), 10, 64)
		return n
	}

	if action != "file" {
		p.clearInput(cq.From.ID)
	}
	alert := ""
	var (
		text string
		kb   *models.InlineKeyboardMarkup
	)

	switch action {
	case "home":
		text, kb = p.home(l)
	case "stats":
		text, kb = p.stats(ctx, l, arg(0))
	case "queue":
		text, kb = p.queue(l)
	case "drop":
		text, kb = p.confirm(l, i18n.T(l, "admin.confirm_drop", chatArg(0)), "drop!:"+arg(0), "queue")
	case "drop!":
		n := p.Video.CancelChat(chatArg(0)) + p.Image.CancelChat(chatArg(0))
		alert = i18n.T(l, "admin.dropped", n)
		text, kb = p.queue(l)
	case "maint":
		if arg(0) == "" {
			text, kb = p.maintenance(l)
			break
		}
		text, kb = p.confirm(l, i18n.T(l, "admin.confirm_maint", onOff(l, arg(0) == "on")), "maint!:"+arg(0), "maint")
	case "maint!":
		if err := p.Settings.SetMaintenance(ctx, arg(0) == "on"); err != nil {
			alert = err.Error()
		}
		text, kb = p.maintenance(l)
	case "mtext":
		p.await(cq.From.ID, &pendingInput{kind: inputText, chatID: msg.Chat.ID, panelID: msg.ID})
		text, kb = p.prompt(l, i18n.T(l, "admin.ask_text"))
	case "list":
		page, _ := strconv.Atoi(arg(1))
		text, kb = p.list(l, arg(0), page)
	case "add":
		p.await(cq.From.ID, &pendingInput{kind: inputChat, set: arg(0), chatID: msg.Chat.ID, panelID: msg.ID})
		text, kb = p.prompt(l, i18n.T(l, "admin.ask_chat"))
	case "add!":
		if err := p.addChat(ctx, arg(0), chatArg(1)); err != nil {
			alert = err.Error()
		}
		text, kb = p.list(l, arg(0), 0)
	case "rm":
		key := "admin.confirm_unban"
		if arg(0) == "prio" {
			key = "admin.confirm_unprio"
		}
		text, kb = p.confirm(l, i18n.T(l, key, chatArg(1)), "rm!:"+arg(0)+":"+arg(1), "list:"+arg(0))
	case "rm!":
		if err := p.Settings.Remove(ctx, chatSet(arg(0)), chatArg(1)); err != nil {
			alert = err.Error()
		}
		text, kb = p.list(l, arg(0), 0)
	case "limits":
		text, kb = p.limits(l)
	case "set":
		spec, ok := p.Settings.Spec(settings.Key(arg(0)))
		if !ok {
			text, kb = p.limits(l)
			break
		}
		p.await(cq.From.ID, &pendingInput{kind: inputValue, key: spec.Key, chatID: msg.Chat.ID, panelID: msg.ID})
		text, kb = p.prompt(l, i18n.T(l, "admin.ask_value", i18n.T(l, "limit."+string(spec.Key)), spec.Min, spec.Max))
	case "errors":
		text, kb = p.failures(ctx, l)
	case "file":
		if err := p.sendFailureInput(ctx, msg.Chat.ID, chatArg(0)); err != nil {
			p.TG.Answer(ctx, cq.ID, i18n.T(l, "admin.file_gone"), true)
		} else {
			p.TG.Answer(ctx, cq.ID, "", false)
		}
		return
	default:
		text, kb = p.home(l)
	}

	p.TG.Answer(ctx, cq.ID, alert, alert != "")
	p.edit(ctx, msg.Chat.ID, msg.ID, text, kb)
}

func (p *Panel) edit(ctx context.Context, chatID int64, msgID int, text string, kb *models.InlineKeyboardMarkup) {
	var markup models.ReplyMarkup
	if kb != nil {
		markup = kb
	}
	if err := p.TG.EditText(ctx, chatID, msgID, text, markup); err != nil {
		p.Log.Warn("edit admin panel", "err", err)
	}
}

func btn(text, data string) models.InlineKeyboardButton {
	return models.InlineKeyboardButton{Text: text, CallbackData: "a:" + data}
}

func keyboard(rows ...[]models.InlineKeyboardButton) *models.InlineKeyboardMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (p *Panel) backTo(l i18n.Lang, section string) *models.InlineKeyboardMarkup {
	return keyboard([]models.InlineKeyboardButton{btn(i18n.T(l, "admin.back"), section)})
}

func (p *Panel) prompt(l i18n.Lang, text string) (string, *models.InlineKeyboardMarkup) {
	return text, keyboard([]models.InlineKeyboardButton{btn(i18n.T(l, "admin.cancel_input"), "home")})
}

func (p *Panel) confirm(l i18n.Lang, question, yes, no string) (string, *models.InlineKeyboardMarkup) {
	return question, keyboard([]models.InlineKeyboardButton{
		btn(i18n.T(l, "admin.yes"), yes), btn(i18n.T(l, "admin.no"), no),
	})
}

func onOff(l i18n.Lang, on bool) string {
	if on {
		return i18n.T(l, "on")
	}
	return i18n.T(l, "off")
}

func (p *Panel) home(l i18n.Lang) (string, *models.InlineKeyboardMarkup) {
	v, i := p.Video.Snapshot(), p.Image.Snapshot()
	on, _ := p.Settings.Maintenance()
	text := i18n.T(l, "admin.title", v.Queued+i.Queued, v.Running+i.Running, onOff(l, on))
	return text, keyboard(
		[]models.InlineKeyboardButton{btn(i18n.T(l, "admin.stats"), "stats:day"), btn(i18n.T(l, "admin.queue"), "queue")},
		[]models.InlineKeyboardButton{btn(i18n.T(l, "admin.maintenance"), "maint"), btn(i18n.T(l, "admin.limits"), "limits")},
		[]models.InlineKeyboardButton{btn(i18n.T(l, "admin.bans"), "list:bans:0"), btn(i18n.T(l, "admin.priority"), "list:prio:0")},
		[]models.InlineKeyboardButton{btn(i18n.T(l, "admin.errors"), "errors"), btn(i18n.T(l, "admin.refresh"), "home")},
	)
}

func (p *Panel) stats(ctx context.Context, l i18n.Lang, period string) (string, *models.InlineKeyboardMarkup) {
	since, label := time.Now().Add(-24*time.Hour), i18n.T(l, "admin.day")
	switch period {
	case "week":
		since, label = time.Now().Add(-7*24*time.Hour), i18n.T(l, "admin.week")
	case "month":
		since, label = time.Now().AddDate(0, -1, 0), i18n.T(l, "admin.month")
	default:
		period = "day"
	}
	st, err := p.Store.Stats(ctx, since)
	if err != nil {
		return html.EscapeString(err.Error()), p.backTo(l, "home")
	}

	kinds := make([]string, 0, len(st.ByKind))
	for k := range st.ByKind {
		kinds = append(kinds, k)
	}
	slices.SortFunc(kinds, func(a, b string) int { return st.ByKind[b] - st.ByKind[a] })
	var lines strings.Builder
	for _, k := range kinds {
		fmt.Fprintf(&lines, "%s: %d\n", k, st.ByKind[k])
	}

	text := i18n.T(l, "admin.stats_body", label, st.Requests, st.Chats, st.Groups, st.Failed, st.Rejected,
		fmtDuration(st.AvgDuration), lines.String())
	periods := []models.InlineKeyboardButton{}
	for _, pr := range []string{"day", "week", "month"} {
		name := i18n.T(l, "admin."+pr)
		if pr == period {
			name = "• " + name
		}
		periods = append(periods, btn(name, "stats:"+pr))
	}
	return text, keyboard(periods, []models.InlineKeyboardButton{btn(i18n.T(l, "admin.back"), "home")})
}

func (p *Panel) queue(l i18n.Lang) (string, *models.InlineKeyboardMarkup) {
	v, i := p.Video.Snapshot(), p.Image.Snapshot()
	var b strings.Builder
	b.WriteString(i18n.T(l, "admin.queue_body", v.Queued, v.Running, v.Workers, fmtDuration(v.AvgRun),
		i.Queued, i.Running, p.Engine.CPUInUse(), p.Engine.CPULimit()))

	// Merge both pools per chat; show the busiest.
	loads := map[int64]*jobs.ChatLoad{}
	for _, c := range append(v.Chats, i.Chats...) {
		if loads[c.ChatID] == nil {
			loads[c.ChatID] = &jobs.ChatLoad{ChatID: c.ChatID}
		}
		loads[c.ChatID].Queued += c.Queued
		loads[c.ChatID].Running += c.Running
	}
	chats := make([]jobs.ChatLoad, 0, len(loads))
	for _, c := range loads {
		chats = append(chats, *c)
	}
	slices.SortFunc(chats, func(a, b jobs.ChatLoad) int { return (b.Queued + b.Running) - (a.Queued + a.Running) })
	if len(chats) > pageSize {
		chats = chats[:pageSize]
	}

	var rows [][]models.InlineKeyboardButton
	if len(chats) > 0 {
		b.WriteString("\n")
	}
	for _, c := range chats {
		b.WriteString("\n" + i18n.T(l, "admin.queue_chat", c.ChatID, c.Queued, c.Running))
		rows = append(rows, []models.InlineKeyboardButton{btn(i18n.T(l, "admin.drop_chat", c.ChatID), "drop:"+strconv.FormatInt(c.ChatID, 10))})
	}
	rows = append(rows, []models.InlineKeyboardButton{btn(i18n.T(l, "admin.refresh"), "queue"), btn(i18n.T(l, "admin.back"), "home")})
	return b.String(), keyboard(rows...)
}

func (p *Panel) maintenance(l i18n.Lang) (string, *models.InlineKeyboardMarkup) {
	on, msg := p.Settings.Maintenance()
	shown := i18n.T(l, "admin.maint_default")
	if msg != "" {
		shown = html.EscapeString(msg)
	}
	toggle := btn(i18n.T(l, "admin.maint_on"), "maint:on")
	if on {
		toggle = btn(i18n.T(l, "admin.maint_off"), "maint:off")
	}
	return i18n.T(l, "admin.maint_body", onOff(l, on), shown), keyboard(
		[]models.InlineKeyboardButton{toggle, btn(i18n.T(l, "admin.maint_text"), "mtext")},
		[]models.InlineKeyboardButton{btn(i18n.T(l, "admin.back"), "home")},
	)
}

func chatSet(name string) store.ChatSet {
	if name == "prio" {
		return store.PriorityChats
	}
	return store.Bans
}

func (p *Panel) list(l i18n.Lang, set string, page int) (string, *models.InlineKeyboardMarkup) {
	if set != "prio" {
		set = "bans"
	}
	title := i18n.T(l, "admin.bans")
	if set == "prio" {
		title = i18n.T(l, "admin.priority")
	}
	ids := p.Settings.List(chatSet(set))
	pages := max((len(ids)+pageSize-1)/pageSize, 1)
	page = min(max(page, 0), pages-1)

	text := i18n.T(l, "admin.list_empty", title)
	if len(ids) > 0 {
		text = i18n.T(l, "admin.list_body", title, len(ids))
	}
	var rows [][]models.InlineKeyboardButton
	var row []models.InlineKeyboardButton
	for _, id := range ids[min(page*pageSize, len(ids)):min((page+1)*pageSize, len(ids))] {
		row = append(row, btn(i18n.T(l, "admin.remove", id), fmt.Sprintf("rm:%s:%d", set, id)))
		if len(row) == 2 {
			rows, row = append(rows, row), nil
		}
	}
	if row != nil {
		rows = append(rows, row)
	}
	if pages > 1 {
		rows = append(rows, []models.InlineKeyboardButton{
			btn("«", fmt.Sprintf("list:%s:%d", set, (page+pages-1)%pages)),
			btn(fmt.Sprintf("%d/%d", page+1, pages), fmt.Sprintf("list:%s:%d", set, page)),
			btn("»", fmt.Sprintf("list:%s:%d", set, (page+1)%pages)),
		})
	}
	rows = append(rows, []models.InlineKeyboardButton{btn(i18n.T(l, "admin.add"), "add:"+set), btn(i18n.T(l, "admin.back"), "home")})
	return text, keyboard(rows...)
}

func (p *Panel) confirmAdd(l i18n.Lang, set string, id int64) (string, *models.InlineKeyboardMarkup) {
	key := "admin.confirm_ban"
	if set == "prio" {
		key = "admin.confirm_prio"
	}
	return p.confirm(l, i18n.T(l, key, id), fmt.Sprintf("add!:%s:%d", set, id), "list:"+set+":0")
}

func (p *Panel) addChat(ctx context.Context, set string, id int64) error {
	if err := p.Settings.Add(ctx, chatSet(set), id); err != nil {
		return err
	}
	if set != "prio" {
		p.Video.CancelChat(id)
		p.Image.CancelChat(id)
	}
	return nil
}

func (p *Panel) limits(l i18n.Lang) (string, *models.InlineKeyboardMarkup) {
	var rows [][]models.InlineKeyboardButton
	for _, spec := range p.Settings.Specs() {
		label := i18n.T(l, "admin.limit_button", i18n.T(l, "limit."+string(spec.Key)), p.Settings.Int(spec.Key))
		rows = append(rows, []models.InlineKeyboardButton{btn(label, "set:"+string(spec.Key))})
	}
	rows = append(rows, []models.InlineKeyboardButton{btn(i18n.T(l, "admin.back"), "home")})
	return i18n.T(l, "admin.limits_body"), keyboard(rows...)
}

func (p *Panel) failures(ctx context.Context, l i18n.Lang) (string, *models.InlineKeyboardMarkup) {
	failures, err := p.Store.Failures(ctx, errorsShown)
	if err != nil {
		return html.EscapeString(err.Error()), p.backTo(l, "home")
	}
	nav := []models.InlineKeyboardButton{btn(i18n.T(l, "admin.refresh"), "errors"), btn(i18n.T(l, "admin.back"), "home")}
	if len(failures) == 0 {
		return i18n.T(l, "admin.errors_empty"), keyboard(nav)
	}
	var b strings.Builder
	var rows [][]models.InlineKeyboardButton
	var row []models.InlineKeyboardButton
	for _, f := range failures {
		b.WriteString("\n" + i18n.T(l, "admin.error_line", f.ID, f.At.Format("01-02 15:04"), f.Kind, f.ChatID,
			html.EscapeString(truncate(f.Error, 160))) + "\n")
		if f.InputPath != "" {
			row = append(row, btn(i18n.T(l, "admin.send_file", f.ID), "file:"+strconv.FormatInt(f.ID, 10)))
			if len(row) == 4 {
				rows, row = append(rows, row), nil
			}
		}
	}
	if row != nil {
		rows = append(rows, row)
	}
	rows = append(rows, nav)
	return i18n.T(l, "admin.errors_body", b.String()), keyboard(rows...)
}

func (p *Panel) sendFailureInput(ctx context.Context, chatID, id int64) error {
	f, err := p.Store.Failure(ctx, id)
	if err != nil {
		return err
	}
	if f.InputPath == "" {
		return errors.New("input not kept")
	}
	if _, err := os.Stat(f.InputPath); err != nil {
		return err
	}
	_, err = telegram.SendFile(ctx, f.InputPath, func(ctx context.Context, in *models.InputFileUpload) (*models.Message, error) {
		return p.TG.SendDocument(ctx, &tgbot.SendDocumentParams{
			ChatID:   chatID,
			Document: in,
			Caption:  fmt.Sprintf("#%d · %s · %d", f.ID, f.Kind, f.ChatID),
		})
	})
	return err
}

// NotifyFailure queues an alert for the admins; alerts are batched.
func (p *Panel) NotifyFailure(f store.Failure) {
	p.alertMu.Lock()
	p.alertCount++
	p.alertLast = f
	p.alertMu.Unlock()
}

// Run sends batched failure alerts until ctx is done.
func (p *Panel) Run(ctx context.Context) {
	t := time.NewTicker(alertInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.flushAlerts(ctx)
		}
	}
}

func (p *Panel) flushAlerts(ctx context.Context) {
	p.alertMu.Lock()
	n, last := p.alertCount, p.alertLast
	p.alertCount = 0
	p.alertMu.Unlock()
	if n == 0 {
		return
	}
	for _, admin := range p.Cfg.AdminIDs {
		p.mu.Lock()
		l, ok := p.langs[admin]
		p.mu.Unlock()
		if !ok {
			l = i18n.EN
		}
		text := i18n.T(l, "admin.alert", n, fmtDuration(alertInterval), last.Kind, last.ChatID,
			html.EscapeString(truncate(last.Error, 300)))
		kb := keyboard([]models.InlineKeyboardButton{btn(i18n.T(l, "admin.open_errors"), "errors")})
		if _, err := p.TG.SendText(ctx, admin, 0, text, kb); err != nil {
			p.Log.Warn("send failure alert", "admin", admin, "err", err)
		}
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func fmtDuration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	if d < time.Minute {
		return d.Round(100 * time.Millisecond).String()
	}
	return d.Round(time.Second).String()
}
