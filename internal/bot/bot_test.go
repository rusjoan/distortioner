package bot

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/graynk/distortioner/internal/media"
	"github.com/graynk/distortioner/internal/settings"
	"github.com/graynk/distortioner/internal/store"
)

func TestPrivateTextIsDistorted(t *testing.T) {
	h := newHarness(t)
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Text: "hello <world>"})
	c := h.api.waitFor("sendMessage", nil)
	// A random text effect, HTML-escaped, with the text choice buttons.
	if c.params["text"] == "hello &lt;world&gt;" || strings.Contains(c.params["text"], "<world>") || c.params["chat_id"] != "1" {
		t.Fatalf("%+v", c.params)
	}
	if !strings.Contains(c.params["reply_markup"], `"x:r"`) || !strings.Contains(c.params["reply_markup"], `"x:p"`) ||
		strings.Contains(c.params["reply_markup"], `"x:f"`) {
		t.Fatalf("%s", c.params["reply_markup"])
	}
}

func TestWobbleOnTextIsTheClassic(t *testing.T) {
	h := newHarness(t)
	h.command(user(1, "en"), private(1), "/wobble", &models.Message{ID: 7, Chat: private(1), Text: "hello <world>"})
	if c := h.api.waitFor("sendMessage", nil); c.params["text"] != "hElLo &lt;WoRlD&gt;" {
		t.Fatalf("%q", c.params["text"])
	}
}

func TestStartIsLocalized(t *testing.T) {
	h := newHarness(t)
	h.send(&models.Message{From: user(1, "ru"), Chat: private(1), Text: "/start"})
	c := h.api.waitFor("sendMessage", nil)
	if !strings.Contains(c.params["text"], "Пришли картинку") {
		t.Fatalf("%q", c.params["text"])
	}
}

func TestGroupIgnoresPlainMessagesAndForeignCommands(t *testing.T) {
	h := newHarness(t)
	h.send(&models.Message{From: user(1, "en"), Chat: group(-5), Text: "just chatting"})
	h.send(&models.Message{From: user(1, "en"), Chat: group(-5), Text: "/distort@other_bot"})
	time.Sleep(50 * time.Millisecond)
	if calls := h.api.methods(); len(calls) != 0 {
		t.Fatalf("unexpected calls %v", calls)
	}
}

func TestGroupDistortReplyToText(t *testing.T) {
	h := newHarness(t)
	original := &models.Message{ID: 7, From: user(2, "en"), Chat: group(-5), Text: "abc"}
	h.send(&models.Message{From: user(1, "en"), Chat: group(-5), Text: "/wobble@distortioner_bot", ReplyToMessage: original})
	c := h.api.waitFor("sendMessage", nil)
	if c.params["text"] != "aBc" {
		t.Fatalf("%q", c.params["text"])
	}
}

func TestDistortWithoutReplyExplains(t *testing.T) {
	h := newHarness(t)
	h.send(&models.Message{From: user(1, "en"), Chat: group(-5), Text: "/distort"})
	c := h.api.waitFor("sendMessage", nil)
	if !strings.Contains(c.params["text"], "Reply with /distort") || !strings.Contains(c.params["text"], "chat history") {
		t.Fatalf("%q", c.params["text"])
	}
}

func TestDistortRejectsBadStrength(t *testing.T) {
	h := newHarness(t)
	original := &models.Message{ID: 7, Chat: group(-5), Text: "abc"}
	h.send(&models.Message{From: user(1, "en"), Chat: group(-5), Text: "/distort 99", ReplyToMessage: original})
	c := h.api.waitFor("sendMessage", nil)
	if !strings.Contains(c.params["text"], "between 20 and 80") {
		t.Fatalf("%q", c.params["text"])
	}
}

func TestAnimatedStickerIsNotSupported(t *testing.T) {
	h := newHarness(t)
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Sticker: &models.Sticker{IsAnimated: true}})
	c := h.api.waitFor("sendMessage", nil)
	if !strings.Contains(c.params["text"], "not supported") {
		t.Fatalf("%q", c.params["text"])
	}
}

func TestTooBigFile(t *testing.T) {
	h := newHarness(t)
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Caption: "/wobble", Video: &models.Video{FileID: "v", FileSize: 50 << 20}})
	c := h.api.waitFor("sendMessage", nil)
	if !strings.Contains(c.params["text"], "too big") || !strings.Contains(c.params["text"], "20 MB") {
		t.Fatalf("%q", c.params["text"])
	}
}

func TestMaintenanceRejectsUsersButNotAdmins(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.Settings.SetMaintenance(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := h.Settings.SetMaintenanceText(ctx, "back at 5 <pm>"); err != nil {
		t.Fatal(err)
	}
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Caption: "/wobble", Voice: &models.Voice{FileID: "v"}})
	c := h.api.waitFor("sendMessage", nil)
	if c.params["text"] != "back at 5 &lt;pm&gt;" {
		t.Fatalf("%q", c.params["text"])
	}
}

func TestBannedChatsAreIgnored(t *testing.T) {
	h := newHarness(t)
	if err := h.Settings.Add(context.Background(), store.Bans, 1); err != nil {
		t.Fatal(err)
	}
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Text: "hello"})
	time.Sleep(50 * time.Millisecond)
	if calls := h.api.methods(); len(calls) != 0 {
		t.Fatalf("unexpected calls %v", calls)
	}
}

func TestRateLimit(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.Settings.SetInt(ctx, settings.RateLimit, 1); err != nil {
		t.Fatal(err)
	}
	// The download fails, which is fine: we only look at admission.
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Caption: "/wobble", Voice: &models.Voice{FileID: "missing"}})
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Caption: "/wobble", Voice: &models.Voice{FileID: "missing"}})
	h.api.waitFor("sendMessage", func(c call) bool { return strings.Contains(c.params["text"], "not so often") })
}

func TestFailedJobIsReportedAndKept(t *testing.T) {
	h := newHarness(t)
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Caption: "/wobble", Voice: &models.Voice{FileID: "missing"}})
	h.api.waitFor("editMessageText", func(c call) bool { return strings.Contains(c.params["text"], "Failed to distort") })

	deadline := time.Now().Add(5 * time.Second)
	for {
		fs, err := h.Store.Failures(context.Background(), 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(fs) == 1 && fs[0].Kind == "voice" && strings.Contains(fs[0].Error, "download") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("failure not stored: %+v", fs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBlockedUserJobsAreCanceled(t *testing.T) {
	h := newHarness(t)
	h.api.failNext("sendMessage", "Forbidden: bot was blocked by the user")
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Text: "hi"})
	h.api.waitFor("sendMessage", nil)
	// No retries and no "no rights" message after a block.
	time.Sleep(50 * time.Millisecond)
	if n := len(h.api.find("sendMessage")); n != 1 {
		t.Fatalf("%d sendMessage calls", n)
	}
}

func TestNoRightsIsRemembered(t *testing.T) {
	h := newHarness(t)
	h.api.failNext("sendMessage", "Bad Request: not enough rights to send text messages to the chat")
	original := &models.Message{ID: 7, Chat: group(-5), Text: "abc"}
	h.send(&models.Message{From: user(1, "en"), Chat: group(-5), Text: "/distort", ReplyToMessage: original})
	h.api.waitFor("sendMessage", func(c call) bool { return strings.Contains(c.params["text"], "rights") })
	if !h.blockedByRights(-5) {
		t.Fatal("chat not remembered")
	}
}

func TestSettingsFlow(t *testing.T) {
	h := newHarness(t)
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Text: "/settings"})
	c := h.api.waitFor("sendMessage", nil)
	if !strings.Contains(c.params["text"], "Wobble strength: <b>50%</b>") {
		t.Fatalf("%q", c.params["text"])
	}
	panel := &models.Message{ID: 500, Chat: private(1)}
	h.press(*user(1, "en"), panel, "s:str:70")
	h.press(*user(1, "en"), panel, "s:lang:ru")
	h.press(*user(1, "en"), panel, "s:audio")

	h.api.waitFor("editMessageText", func(c call) bool { return strings.Contains(c.params["text"], "Сила wobble: <b>70%</b>") })
	got, ok, err := h.Store.ChatSettings(context.Background(), 1)
	if err != nil || !ok || got.Strength != 70 || got.Lang != "ru" || got.DistortAudio {
		t.Fatalf("%+v ok=%v err=%v", got, ok, err)
	}
}

func TestGroupSettingsNeedChatAdmin(t *testing.T) {
	h := newHarness(t)
	h.send(&models.Message{From: user(1, "en"), Chat: group(-5), Text: "/settings"})
	c := h.api.waitFor("sendMessage", nil)
	if !strings.Contains(c.params["text"], "Only chat admins") {
		t.Fatalf("%q", c.params["text"])
	}
	h.api.member = "administrator"
	h.send(&models.Message{From: user(1, "en"), Chat: group(-5), Text: "/settings"})
	h.api.waitFor("sendMessage", func(c call) bool { return strings.Contains(c.params["text"], "Settings for this chat") })
}

func TestAdminPanel(t *testing.T) {
	h := newHarness(t)
	// Not an admin: silence.
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Text: "/admin"})
	time.Sleep(50 * time.Millisecond)
	if n := len(h.api.find("sendMessage")); n != 0 {
		t.Fatalf("panel shown to a non-admin")
	}

	h.send(&models.Message{From: user(adminID, "en"), Chat: private(adminID), Text: "/admin"})
	h.api.waitFor("sendMessage", func(c call) bool { return strings.Contains(c.params["text"], "Admin panel") })
	panel := &models.Message{ID: 900, Chat: private(adminID)}
	admin := *user(adminID, "en")

	// Ban via input + confirmation.
	h.press(admin, panel, "a:add:bans")
	h.api.waitFor("editMessageText", func(c call) bool { return strings.Contains(c.params["text"], "Send the chat id") })
	h.send(&models.Message{From: user(adminID, "en"), Chat: private(adminID), Text: "not a number"})
	h.api.waitFor("editMessageText", func(c call) bool { return strings.Contains(c.params["text"], "That is not a chat id") })
	h.send(&models.Message{From: user(adminID, "en"), Chat: private(adminID), Text: "-100777"})
	h.api.waitFor("editMessageText", func(c call) bool { return strings.Contains(c.params["text"], "Ban chat <code>-100777</code>?") })
	if h.Settings.IsBanned(-100777) {
		t.Fatal("banned before confirmation")
	}
	h.press(admin, panel, "a:add!:bans:-100777")
	if !h.Settings.IsBanned(-100777) {
		t.Fatal("not banned")
	}

	// Change a limit.
	h.press(admin, panel, "a:set:max_video_seconds")
	h.send(&models.Message{From: user(adminID, "en"), Chat: private(adminID), Text: "1"})
	h.api.waitFor("editMessageText", func(c call) bool { return strings.Contains(c.params["text"], "from 5 to 600") })
	h.send(&models.Message{From: user(adminID, "en"), Chat: private(adminID), Text: "120"})
	h.api.waitFor("editMessageText", func(c call) bool { return strings.Contains(c.params["text"], "Saved") })
	if h.Settings.Int(settings.MaxVideoSeconds) != 120 {
		t.Fatal("limit not saved")
	}

	// Maintenance toggle requires confirmation.
	h.press(admin, panel, "a:maint:on")
	if on, _ := h.Settings.Maintenance(); on {
		t.Fatal("toggled without confirmation")
	}
	h.press(admin, panel, "a:maint!:on")
	if on, _ := h.Settings.Maintenance(); !on {
		t.Fatal("maintenance not enabled")
	}

	// Stats and queue views render.
	h.press(admin, panel, "a:stats:week")
	h.api.waitFor("editMessageText", func(c call) bool { return strings.Contains(c.params["text"], "Statistics, Week") })
	h.press(admin, panel, "a:queue")
	h.api.waitFor("editMessageText", func(c call) bool { return strings.Contains(c.params["text"], "CPU slots") })

	// Non-admins pressing admin buttons get nothing.
	before := len(h.api.find("editMessageText"))
	h.press(*user(1, "en"), panel, "a:maint!:off")
	if on, _ := h.Settings.Maintenance(); !on || len(h.api.find("editMessageText")) != before {
		t.Fatal("non-admin changed state")
	}
}

func TestOldUpdatesAreDropped(t *testing.T) {
	h := newHarness(t)
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Text: "hi", Date: int(time.Now().Add(-3 * time.Hour).Unix())})
	time.Sleep(50 * time.Millisecond)
	if calls := h.api.methods(); len(calls) != 0 {
		t.Fatalf("unexpected calls %v", calls)
	}
}

func TestFileLimitFromAdminPanel(t *testing.T) {
	h := newHarness(t)
	panel := &models.Message{ID: 900, Chat: private(adminID)}
	h.press(*user(adminID, "en"), panel, "a:limits")
	h.api.waitFor("editMessageText", func(c call) bool { return strings.Contains(c.params["reply_markup"], "Max file size, MB: 20") })

	h.press(*user(adminID, "en"), panel, "a:set:max_file_mb")
	h.send(&models.Message{From: user(adminID, "en"), Chat: private(adminID), Text: "21"})
	h.api.waitFor("editMessageText", func(c call) bool { return strings.Contains(c.params["text"], "from 1 to 20") })
	h.send(&models.Message{From: user(adminID, "en"), Chat: private(adminID), Text: "5"})
	h.api.waitFor("editMessageText", func(c call) bool { return strings.Contains(c.params["text"], "Saved") })

	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Caption: "/wobble", Video: &models.Video{FileID: "v", FileSize: 6 << 20}})
	c := h.api.waitFor("sendMessage", func(c call) bool { return c.params["chat_id"] == "1" })
	if !strings.Contains(c.params["text"], "max 5 MB") {
		t.Fatalf("%q", c.params["text"])
	}
}

func TestFriedText(t *testing.T) {
	h := newHarness(t)
	original := &models.Message{ID: 7, Chat: group(-5), Text: "abc"}
	h.send(&models.Message{From: user(1, "en"), Chat: group(-5), Text: "/fry", ReplyToMessage: original})
	c := h.api.waitFor("sendMessage", nil)
	if !strings.HasPrefix(c.params["text"], "ABC") || !strings.Contains(c.params["text"], "!!!11one") {
		t.Fatalf("%q", c.params["text"])
	}
}

func TestTextPicker(t *testing.T) {
	h := newHarness(t)
	original := &models.Message{ID: 7, From: user(1, "ru"), Chat: private(1), Text: "шар"}
	h.command(user(1, "ru"), private(1), "/wobble", original)
	first := h.api.waitFor("sendMessage", nil)
	// Buttons on the result work on the result's own text ("шАр").
	result := &models.Message{ID: first.resultID, Chat: private(1), Text: first.params["text"], ReplyToMessage: original}
	u := *user(1, "ru")
	h.press(u, result, "x:p")
	m := h.api.waitFor("editMessageReplyMarkup", nil).params["reply_markup"]
	if !strings.Contains(m, `"p:typos"`) || strings.Contains(m, `"p:bulge"`) {
		t.Fatalf("%s", m)
	}
	h.press(u, result, "p:mock")
	h.press(u, result, "p:caps") // replaces mock: both rewrite case
	h.press(u, result, "p:baby")
	h.press(u, result, "p:!")
	c := h.api.waitFor("sendMessage", func(c call) bool { return c.resultID != first.resultID })
	if c.params["text"] != "САЛ!!!1!11один" {
		t.Fatalf("%q", c.params["text"])
	}
	// Picked effects are counted.
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, _ := h.Store.EffectStats(context.Background(), time.Now().Add(-time.Hour))
		if st.Effects["caps"] == 1 && st.Effects["baby"] == 1 && st.Effects["mock"] == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stats %+v", st)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestFxOnTextAsksWithTextChoices(t *testing.T) {
	h := newHarness(t)
	h.command(user(1, "en"), group(-5), "/fx", &models.Message{ID: 7, Chat: group(-5), Text: "abc"})
	c := h.api.waitFor("sendMessage", nil)
	if c.params["text"] != "What should I do with it?" || strings.Contains(c.params["reply_markup"], `"x:w"`) {
		t.Fatalf("%+v", c.params)
	}
}

func TestFryWithoutReplyExplains(t *testing.T) {
	h := newHarness(t)
	h.send(&models.Message{From: user(1, "ru"), Chat: private(1), Text: "/fry"})
	c := h.api.waitFor("sendMessage", nil)
	if !strings.Contains(c.params["text"], "/fry") || !strings.Contains(c.params["text"], "в подписи") {
		t.Fatalf("%q", c.params["text"])
	}
}

func TestCaptionCommandForAnotherBotIsIgnored(t *testing.T) {
	h := newHarness(t)
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Caption: "/fry@other_bot",
		Photo: []models.PhotoSize{{FileID: "p"}}})
	time.Sleep(50 * time.Millisecond)
	if calls := h.api.methods(); len(calls) != 0 {
		t.Fatalf("unexpected calls %v", calls)
	}
}

func TestPromptForMediaWithoutCommand(t *testing.T) {
	h := newHarness(t)
	h.send(&models.Message{ID: 500, From: user(1, "en"), Chat: private(1), Photo: []models.PhotoSize{{FileID: "p"}}})
	c := h.api.waitFor("sendMessage", nil)
	if c.params["text"] != "What should I do with it?" || !strings.Contains(c.params["reply_parameters"], `"message_id":500`) {
		t.Fatalf("%+v", c.params)
	}
	for _, data := range []string{"x:r", "x:w", "x:f", "x:p"} {
		if !strings.Contains(c.params["reply_markup"], `"`+data+`"`) {
			t.Fatalf("no %s in %s", data, c.params["reply_markup"])
		}
	}
	if n := len(h.api.find("getFile")); n != 0 {
		t.Fatal("downloaded before the user chose")
	}
}

func TestPicker(t *testing.T) {
	h := newHarness(t)
	h.send(&models.Message{ID: 500, From: user(1, "en"), Chat: private(1), Video: &models.Video{FileID: "v"}})
	prompt := &models.Message{ID: h.question(1).resultID, Chat: private(1)}
	u := *user(1, "en")

	h.press(u, prompt, "x:p")
	c := h.api.waitFor("editMessageText", nil)
	if !strings.Contains(c.params["text"], "Pick effects") || !strings.Contains(c.params["reply_markup"], `"p:bulge"`) {
		t.Fatalf("%+v", c.params)
	}
	edits := func() int { return len(h.api.find("editMessageText")) }
	last := func() string { e := h.api.find("editMessageText"); return e[len(e)-1].params["reply_markup"] }

	// Tempo changes exclude each other.
	h.press(u, prompt, "p:fast")
	h.press(u, prompt, "p:slow")
	if m := last(); !strings.Contains(m, "✅ 🐌") || strings.Contains(m, "✅ ⏩") || !strings.Contains(m, "Apply (1)") {
		t.Fatalf("%s", m)
	}
	// wobble and fry go alone.
	h.press(u, prompt, "p:bulge")
	h.press(u, prompt, "p:fry")
	if m := last(); !strings.Contains(m, "✅ 🔥") || strings.Contains(m, "✅ 🫧") || !strings.Contains(m, "Apply (1)") {
		t.Fatalf("%s", m)
	}
	h.press(u, prompt, "p:fry") // unselect
	before := edits()
	h.press(u, prompt, "p:!")
	if c := h.api.waitFor("answerCallbackQuery", func(c call) bool { return c.params["text"] != "" }); !strings.Contains(c.params["text"], "at least one") {
		t.Fatalf("%+v", c.params)
	}
	if edits() != before {
		t.Fatal("applied an empty selection")
	}
	// Closing returns to the question.
	h.press(u, prompt, "p:x")
	if e := h.api.find("editMessageText"); e[len(e)-1].params["text"] != "What should I do with it?" {
		t.Fatalf("%+v", e[len(e)-1].params)
	}
}

func TestAlbumGetsOneQuestion(t *testing.T) {
	h := newHarness(t)
	for i := range 3 {
		h.send(&models.Message{ID: 600 + i, From: user(1, "en"), Chat: private(1), MediaGroupID: "g",
			Photo: []models.PhotoSize{{FileID: "p"}}})
	}
	h.api.waitFor("sendMessage", nil)
	time.Sleep(albumWait + 200*time.Millisecond)
	if n := len(h.api.find("sendMessage")); n != 1 {
		t.Fatalf("%d questions", n)
	}
}

func TestStaleButtons(t *testing.T) {
	h := newHarness(t)
	// A text message from us without any media behind it.
	h.press(*user(1, "en"), &models.Message{ID: 9, Chat: private(1), Text: "What should I do with it?"}, "x:w")
	if c := h.api.waitFor("answerCallbackQuery", nil); !strings.Contains(c.params["text"], "too old") {
		t.Fatalf("%+v", c.params)
	}
	h.press(*user(1, "en"), &models.Message{ID: 9, Chat: private(1)}, "p:bulge")
	h.api.waitFor("answerCallbackQuery", func(c call) bool {
		return strings.Count(c.params["text"], "too old") == 1 && len(h.api.find("answerCallbackQuery")) == 2
	})
}

func TestFryStrengthSetting(t *testing.T) {
	h := newHarness(t)
	panel := &models.Message{ID: 500, Chat: private(1)}
	h.press(*user(1, "en"), panel, "s:fry:80")
	h.api.waitFor("editMessageText", func(c call) bool { return strings.Contains(c.params["text"], "Fry strength: <b>80%</b>") })
	got, ok, err := h.Store.ChatSettings(context.Background(), 1)
	if err != nil || !ok || got.FryStrength != 80 || got.Strength != 50 {
		t.Fatalf("%+v ok=%v err=%v", got, ok, err)
	}
}

func TestStrengthArguments(t *testing.T) {
	h := newHarness(t)
	h.command(user(1, "en"), group(-5), "/fry 99", &models.Message{ID: 7, Chat: group(-5), Text: "x"})
	if c := h.api.waitFor("sendMessage", nil); !strings.Contains(c.params["text"], "between 20 and 80") {
		t.Fatalf("%q", c.params["text"])
	}
}

// With a free worker a job starts at once: its status must not say
// "in queue", and it gets a cancel button right away.
func TestNoQueuedStatusWhenWorkerIsFree(t *testing.T) {
	h := newHarness(t)
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Caption: "/wobble", Video: &models.Video{FileID: "missing"}})
	h.api.waitFor("editMessageText", func(c call) bool { return strings.Contains(c.params["text"], "Failed") })
	for _, c := range h.api.find("editMessageText") {
		if strings.Contains(c.params["text"], "In queue") {
			t.Fatalf("queued status with a free worker: %+v", c.params)
		}
	}
	if !slices.ContainsFunc(h.api.find("editMessageText"), func(c call) bool {
		return strings.Contains(c.params["text"], "Downloading") && strings.Contains(c.params["reply_markup"], `"c:v:`)
	}) {
		t.Fatalf("no cancel button: %v", h.api.methods())
	}
}

func TestFxCommandShowsMenuInGroup(t *testing.T) {
	h := newHarness(t)
	target := &models.Message{ID: 77, From: user(2, "en"), Chat: group(-5), Video: &models.Video{FileID: "v"}}
	h.command(user(1, "en"), group(-5), "/fx@distortioner_bot", target)
	c := h.question(-5)
	if c.params["text"] != "What should I do with it?" || !strings.Contains(c.params["reply_parameters"], `"message_id":77`) ||
		!strings.Contains(c.params["reply_markup"], `"x:p"`) {
		t.Fatalf("%+v", c.params)
	}
	// The picker works on the question in the group as in a private chat.
	h.press(*user(3, "en"), &models.Message{ID: c.resultID, Chat: group(-5)}, "x:p")
	h.api.waitFor("editMessageText", func(c call) bool { return strings.Contains(c.params["reply_markup"], `"p:bulge"`) })

	h.command(user(1, "en"), group(-5), "/fx", nil)
	h.api.waitFor("sendMessage", func(c call) bool { return strings.Contains(c.params["text"], "Reply with /fx") })
}

// Sound and single-pass video effects take a while too: they get a status.
func TestStatusForSoundJobs(t *testing.T) {
	h := newHarness(t)
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Caption: "/fry", Voice: &models.Voice{FileID: "missing"}})
	h.api.waitFor("sendMessage", func(c call) bool { return strings.Contains(c.params["text"], "Downloading") })
	h.api.waitFor("editMessageText", func(c call) bool { return strings.Contains(c.params["text"], "Failed") })
}

func TestStageTexts(t *testing.T) {
	if got := stageText("en", media.StageProcessing, 500, 1000); !strings.Contains(got, "Processing") || !strings.Contains(got, "50%") {
		t.Fatal(got)
	}
	if got := stageText("en", media.StageEncoding, 0, 0); strings.Contains(got, "%") {
		t.Fatal(got)
	}
}

// The result of "/fry" as a reply replies to the command: its buttons must
// still take the text of the result, never the command.
func TestButtonsOnTextResultUseItsText(t *testing.T) {
	h := newHarness(t)
	original := &models.Message{ID: 7, From: user(2, "en"), Chat: group(-5), Text: "abc"}
	cmd := &models.Message{ID: 8, From: user(1, "en"), Chat: group(-5), Text: "/fry", ReplyToMessage: original}
	h.send(cmd)
	first := h.api.waitFor("sendMessage", nil)
	if !strings.Contains(first.params["reply_parameters"], `"message_id":8`) {
		t.Fatalf("expected a reply to the command: %+v", first.params)
	}
	result := &models.Message{ID: first.resultID, Chat: group(-5), Text: first.params["text"],
		ReplyToMessage: &models.Message{ID: 8, Chat: group(-5), Text: "/fry"}}
	h.press(*user(1, "en"), result, "x:r")
	second := h.api.waitFor("sendMessage", func(c call) bool { return c.resultID != first.resultID })
	if got := strings.ToLower(second.params["text"]); strings.Contains(strings.ReplaceAll(got, " ", ""), "/fry") || !strings.Contains(strings.ReplaceAll(got, " ", ""), "abc") {
		t.Fatalf("distorted the wrong text: %q", second.params["text"])
	}
}
