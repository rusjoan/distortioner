package bot

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/graynk/distortioner/internal/admin"
	"github.com/graynk/distortioner/internal/config"
	"github.com/graynk/distortioner/internal/jobs"
	"github.com/graynk/distortioner/internal/limiter"
	"github.com/graynk/distortioner/internal/media"
	"github.com/graynk/distortioner/internal/metrics"
	"github.com/graynk/distortioner/internal/settings"
	"github.com/graynk/distortioner/internal/store"
	"github.com/graynk/distortioner/internal/telegram"
)

const testToken = "123:secret"

// call is one Bot API request seen by the fake server.
type call struct {
	method   string
	params   map[string]string
	files    map[string][]byte
	names    map[string]string // uploaded file names by field
	resultID int               // message_id returned for send* calls
}

// fakeAPI is a minimal Bot API server that records requests.
type fakeAPI struct {
	t   *testing.T
	srv *httptest.Server

	mu     sync.Mutex
	calls  []call
	nextID int
	files  map[string][]byte // file_id → content served by getFile
	fail   map[string]string // method → error description returned once
	member string            // status returned by getChatMember
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{t: t, nextID: 1000, files: map[string][]byte{}, fail: map[string]string{}, member: "member"}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/file/bot"+testToken+"/") {
		id := strings.TrimPrefix(r.URL.Path, "/file/bot"+testToken+"/")
		f.mu.Lock()
		data, ok := f.files[id]
		f.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
		return
	}

	method := strings.TrimPrefix(r.URL.Path, "/bot"+testToken+"/")
	c := call{method: method, params: map[string]string{}, files: map[string][]byte{}, names: map[string]string{}}
	if err := r.ParseMultipartForm(32 << 20); err == nil {
		for k, v := range r.MultipartForm.Value {
			c.params[k] = v[0]
		}
		for k, fhs := range r.MultipartForm.File {
			file, _ := fhs[0].Open()
			data, _ := io.ReadAll(file)
			_ = file.Close()
			c.files[k] = data
			c.names[k] = fhs[0].Filename
		}
	}

	f.mu.Lock()
	f.nextID++
	id := f.nextID
	c.resultID = id
	f.calls = append(f.calls, c)
	failure, failing := f.fail[method]
	delete(f.fail, method)
	member := f.member
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if failing {
		code := 400
		if strings.HasPrefix(failure, "Forbidden") {
			code = 403
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": code, "description": failure})
		return
	}

	var result any = true
	chatID := c.params["chat_id"]
	switch method {
	case "getMe":
		result = map[string]any{"id": 123, "is_bot": true, "first_name": "Distortioner", "username": "distortioner_bot"}
	case "getFile":
		result = map[string]any{"file_id": c.params["file_id"], "file_unique_id": "u", "file_path": c.params["file_id"]}
	case "getChatMember":
		result = map[string]any{"status": member, "user": map[string]any{"id": 1, "is_bot": false, "first_name": "x"}}
	case "copyMessage":
		result = map[string]any{"message_id": id}
	case "sendMediaGroup":
		result = []any{map[string]any{"message_id": id, "date": time.Now().Unix(), "chat": map[string]any{"id": json.Number(chatID), "type": "private"}}}
	default:
		if strings.HasPrefix(method, "send") || method == "editMessageText" {
			result = map[string]any{"message_id": id, "date": time.Now().Unix(),
				"chat": map[string]any{"id": json.Number(chatID), "type": "private"}, "text": c.params["text"]}
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

func (f *fakeAPI) failNext(method, description string) {
	f.mu.Lock()
	f.fail[method] = description
	f.mu.Unlock()
}

func (f *fakeAPI) addFile(id string, data []byte) {
	f.mu.Lock()
	f.files[id] = data
	f.mu.Unlock()
}

// find returns calls of a method, in order.
func (f *fakeAPI) find(method string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if c.method == method {
			out = append(out, c)
		}
	}
	return out
}

// waitFor polls until a call matching the predicate shows up.
func (f *fakeAPI) waitFor(method string, match func(call) bool) call {
	f.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, c := range f.find(method) {
			if match == nil || match(c) {
				return c
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.t.Fatalf("no %s call matched; calls: %v", method, f.methods())
	return call{}
}

func (f *fakeAPI) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, c.method)
	}
	return out
}

type harness struct {
	*Bot
	api *fakeAPI
	cfg *config.Config
}

const adminID = 42

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	api := newFakeAPI(t)

	dir := t.TempDir()
	cfg := &config.Config{
		Token: testToken, AdminIDs: []int64{adminID}, DataDir: dir, TempDir: t.TempDir(),
		VideoWorkers: 1, ImageWorkers: 2, CPULimit: 2, Codec: "libx264", MaxFileMB: 20, MaxFileLimitMB: 20,
		JobTimeout: time.Minute, KeepFailedInputs: true, FailedTTL: time.Hour, FailedMaxBytes: 1 << 20,
		StatsRetention: time.Hour,
	}
	st, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sets, err := settings.Load(ctx, st, settings.Options{MaxFileMB: cfg.MaxFileMB, MaxFileLimitMB: cfg.MaxFileLimitMB})
	if err != nil {
		t.Fatal(err)
	}
	limits := jobs.Limits{
		MaxQueued:  func() int { return sets.Int(settings.MaxQueue) },
		MaxPerChat: func() int { return sets.Int(settings.MaxJobsPerChat) },
		IsPriority: sets.IsPriority,
	}
	video := jobs.New(cfg.VideoWorkers, cfg.JobTimeout, limits)
	image := jobs.New(cfg.ImageWorkers, cfg.JobTimeout, limits)
	t.Cleanup(func() {
		video.Shutdown(context.Background())
		image.Shutdown(context.Background())
	})

	tb, err := tgbot.New(testToken, tgbot.WithServerURL(api.srv.URL), tgbot.WithSkipGetMe())
	if err != nil {
		t.Fatal(err)
	}
	tg := telegram.New(tb, nil)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if testing.Verbose() {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	engine := media.NewEngine(cfg.CPULimit, cfg.Codec)
	panel := admin.New(admin.Deps{Cfg: cfg, TG: tg, Store: st, Settings: sets, Video: video, Image: image, Engine: engine, Log: log})
	b := New(Deps{
		Cfg: cfg, TG: tg, Store: st, Settings: sets, Engine: engine, Video: video, Image: image,
		Limiter: limiter.New(func() int { return sets.Int(settings.RateLimit) },
			func() time.Duration { return time.Duration(sets.Int(settings.RateWindow)) * time.Second }),
		Metrics: metrics.New("test"), Admin: panel, Log: log,
		Me: &models.User{ID: 123, IsBot: true, Username: "distortioner_bot"},
	})
	b.rnd = rand.New(rand.NewPCG(7, 7)) // deterministic random picks
	return &harness{Bot: b, api: api, cfg: cfg}
}

var nextMsgID = 1

func (h *harness) send(m *models.Message) {
	if m.ID == 0 {
		nextMsgID++
		m.ID = nextMsgID
	}
	if m.Date == 0 {
		m.Date = int(time.Now().Unix())
	}
	h.Handle(context.Background(), nil, &models.Update{Message: m})
}

// question waits for our "what to do?" question and until the bot
// remembers it: the bot stores it right after Telegram accepts it, which a
// test can outrun, a human cannot.
func (h *harness) question(chatID int64) call {
	h.api.t.Helper()
	c := h.api.waitFor("sendMessage", func(c call) bool {
		return c.params["chat_id"] == fmt.Sprint(chatID) &&
			(c.params["text"] == "What should I do with it?" || c.params["text"] == "Что с этим сделать?")
	})
	deadline := time.Now().Add(5 * time.Second)
	for h.pending.get(chatID, c.resultID) == nil {
		if time.Now().After(deadline) {
			h.api.t.Fatal("question not registered")
		}
		time.Sleep(time.Millisecond)
	}
	return c
}

// command sends "/<cmd>" in reply to target, as a user would in a group.
func (h *harness) command(from *models.User, chat models.Chat, cmd string, target *models.Message) {
	h.send(&models.Message{From: from, Chat: chat, Text: cmd, ReplyToMessage: target})
}

func (h *harness) press(from models.User, msg *models.Message, data string) {
	h.Handle(context.Background(), nil, &models.Update{CallbackQuery: &models.CallbackQuery{
		ID: fmt.Sprint(time.Now().UnixNano()), From: from, Data: data,
		Message: models.MaybeInaccessibleMessage{Type: models.MaybeInaccessibleMessageTypeMessage, Message: msg},
	}})
}

func user(id int64, lang string) *models.User {
	return &models.User{ID: id, FirstName: "u", LanguageCode: lang}
}

func private(id int64) models.Chat { return models.Chat{ID: id, Type: models.ChatTypePrivate} }

func group(id int64) models.Chat { return models.Chat{ID: id, Type: models.ChatTypeSupergroup} }
