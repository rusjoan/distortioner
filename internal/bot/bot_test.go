package bot

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/graynk/distortioner/internal/settings"
	"github.com/graynk/distortioner/internal/store"
)

func TestPrivateTextIsDistorted(t *testing.T) {
	h := newHarness(t)
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Text: "hello <world>"})
	c := h.api.waitFor("sendMessage", nil)
	if c.params["text"] != "hElLo &lt;WoRlD&gt;" || c.params["chat_id"] != "1" {
		t.Fatalf("%+v", c.params)
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
	h.send(&models.Message{From: user(1, "en"), Chat: group(-5), Text: "/distort@distortioner_bot", ReplyToMessage: original})
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
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Video: &models.Video{FileID: "v", FileSize: 50 << 20}})
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
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Voice: &models.Voice{FileID: "v"}})
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
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Voice: &models.Voice{FileID: "missing"}})
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Voice: &models.Voice{FileID: "missing"}})
	h.api.waitFor("sendMessage", func(c call) bool { return strings.Contains(c.params["text"], "not so often") })
}

func TestFailedJobIsReportedAndKept(t *testing.T) {
	h := newHarness(t)
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Voice: &models.Voice{FileID: "missing"}})
	h.api.waitFor("sendMessage", func(c call) bool { return strings.Contains(c.params["text"], "Failed to distort") })

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
	if !strings.Contains(c.params["text"], "Strength: <b>50%</b>") {
		t.Fatalf("%q", c.params["text"])
	}
	panel := &models.Message{ID: 500, Chat: private(1)}
	h.press(*user(1, "en"), panel, "s:str:70")
	h.press(*user(1, "en"), panel, "s:lang:ru")
	h.press(*user(1, "en"), panel, "s:audio")

	h.api.waitFor("editMessageText", func(c call) bool { return strings.Contains(c.params["text"], "Сила: <b>70%</b>") })
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

	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Video: &models.Video{FileID: "v", FileSize: 6 << 20}})
	c := h.api.waitFor("sendMessage", func(c call) bool { return c.params["chat_id"] == "1" })
	if !strings.Contains(c.params["text"], "max 5 MB") {
		t.Fatalf("%q", c.params["text"])
	}
}

func TestFryNeedsMediaNotText(t *testing.T) {
	h := newHarness(t)
	original := &models.Message{ID: 7, Chat: group(-5), Text: "abc"}
	h.send(&models.Message{From: user(1, "en"), Chat: group(-5), Text: "/fry", ReplyToMessage: original})
	c := h.api.waitFor("sendMessage", nil)
	if !strings.Contains(c.params["text"], "Text can't be fried") {
		t.Fatalf("%q", c.params["text"])
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

func TestEffectLabel(t *testing.T) {
	if EffectFry.label(KindVideo) != "fry_video" || EffectDistort.label(KindVideo) != "video" {
		t.Fatal("labels")
	}
}
