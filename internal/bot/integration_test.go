//go:build integration

package bot

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"
)

// render produces a fixture with ffmpeg's test sources.
func render(t *testing.T, name string, args ...string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("ffmpeg", append(append([]string{"-nostdin", "-y", "-v", "error"}, args...), path)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestE2EPhoto(t *testing.T) {
	h := newHarness(t)
	h.api.addFile("photo", render(t, "p.jpg", "-f", "lavfi", "-i", "testsrc=size=640x480", "-frames:v", "1"))
	h.command(user(1, "en"), private(1), "/wobble", &models.Message{ID: 30, Chat: private(1), Caption: "nice cat",
		Photo: []models.PhotoSize{{FileID: "photo"}}})

	c := h.api.waitFor("sendPhoto", nil)
	if len(c.files["photo"]) == 0 || c.params["caption"] != "nIcE cAt" {
		t.Fatalf("%+v", c.params)
	}
	if !strings.Contains(c.params["reply_markup"], `"x:r"`) || !strings.Contains(c.params["reply_markup"], `"x:p"`) {
		t.Fatalf("no choice buttons: %s", c.params["reply_markup"])
	}
}

func TestE2EVideoWithProgress(t *testing.T) {
	h := newHarness(t)
	h.api.addFile("video", render(t, "v.mp4",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=25:duration=2",
		"-f", "lavfi", "-i", "sine=duration=2", "-c:v", "libx264", "-c:a", "aac", "-shortest"))
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Caption: "/wobble", Video: &models.Video{FileID: "video"}})

	c := h.api.waitFor("sendVideo", nil)
	if len(c.files["video"]) == 0 {
		t.Fatal("empty video")
	}
	h.api.waitFor("deleteMessage", nil) // the status message is removed
	if first := h.api.find("sendMessage"); len(first) == 0 || !strings.Contains(first[0].params["text"], "Downloading") {
		t.Fatalf("no progress message: %v", h.api.methods())
	}
}

func TestE2EMoreButtonDistortsAgain(t *testing.T) {
	h := newHarness(t)
	h.api.addFile("sticker", render(t, "s.webp", "-f", "lavfi", "-i", "testsrc=size=512x512", "-frames:v", "1"))
	result := &models.Message{ID: 77, Chat: private(1), Sticker: &models.Sticker{FileID: "sticker"}}
	h.press(*user(1, "en"), result, "r:70")
	c := h.api.waitFor("sendSticker", nil)
	if !strings.Contains(c.params["reply_markup"], `"x:w"`) || !strings.Contains(c.params["reply_parameters"], `"message_id":77`) {
		t.Fatalf("%+v", c.params)
	}
}

func TestE2EAlbum(t *testing.T) {
	h := newHarness(t)
	img := render(t, "a.jpg", "-f", "lavfi", "-i", "testsrc=size=320x240", "-frames:v", "1")
	h.api.addFile("a1", img)
	h.api.addFile("a2", img)
	for i, id := range []string{"a1", "a2"} {
		h.send(&models.Message{ID: 300 + i, From: user(1, "en"), Chat: private(1), MediaGroupID: "g1",
			Photo: []models.PhotoSize{{FileID: id}}})
	}
	question := h.question(1)
	h.press(*user(1, "en"), &models.Message{ID: question.resultID, Chat: private(1)}, "x:w")
	c := h.api.waitFor("sendMediaGroup", nil)
	if len(c.files) != 2 || !strings.Contains(c.params["media"], "attach://") {
		t.Fatalf("files %d media %s", len(c.files), c.params["media"])
	}
}

func TestE2ECancelQueuedVideo(t *testing.T) {
	h := newHarness(t)
	long := render(t, "long.mp4", "-f", "lavfi", "-i", "testsrc=size=640x480:rate=30:duration=20", "-c:v", "libx264")
	h.api.addFile("long", long)
	h.api.addFile("short", long)
	// The single video worker is busy with the first one; the second waits.
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Caption: "/wobble", Video: &models.Video{FileID: "long"}})
	h.send(&models.Message{From: user(2, "en"), Chat: private(2), Caption: "/wobble", Video: &models.Video{FileID: "short"}})

	queued := h.api.waitFor("editMessageText", func(c call) bool {
		return c.params["chat_id"] == "2" && strings.Contains(c.params["text"], "In queue: #1")
	})
	data := regexp.MustCompile(`c:v:\d+`).FindString(queued.params["reply_markup"])
	if data == "" {
		t.Fatalf("no cancel button: %s", queued.params["reply_markup"])
	}

	// Someone else cannot cancel it.
	h.press(*user(3, "en"), &models.Message{ID: 1, Chat: private(2)}, data)
	h.api.waitFor("answerCallbackQuery", func(c call) bool { return strings.Contains(c.params["text"], "Only the person") })

	h.press(*user(2, "en"), &models.Message{ID: 1, Chat: private(2)}, data)
	h.api.waitFor("editMessageText", func(c call) bool {
		return c.params["chat_id"] == "2" && strings.Contains(c.params["text"], "Canceled")
	})
	h.Video.CancelChat(1)
}

func TestE2EVideoNote(t *testing.T) {
	h := newHarness(t)
	h.api.addFile("note", render(t, "n.mp4",
		"-f", "lavfi", "-i", "testsrc=size=384x384:rate=30:duration=1",
		"-f", "lavfi", "-i", "sine=duration=1", "-c:v", "libx264", "-c:a", "aac", "-shortest"))
	h.command(user(1, "en"), private(1), "/wobble", &models.Message{ID: 40, Chat: private(1), VideoNote: &models.VideoNote{FileID: "note", Length: 384}})
	if c := h.api.waitFor("sendVideoNote", nil); len(c.files["video_note"]) == 0 {
		t.Fatal("empty video note")
	}
}

// Users may forbid voice and video messages; the result then arrives as a
// regular video (video notes) or a document (voice messages).
func TestE2EVoiceResultIsM4A(t *testing.T) {
	h := newHarness(t)
	h.api.addFile("v", render(t, "v.ogg", "-f", "lavfi", "-i", "sine=duration=1", "-c:a", "libopus"))
	h.command(user(1, "en"), private(1), "/fry", &models.Message{ID: 43, Chat: private(1), Voice: &models.Voice{FileID: "v"}})
	c := h.api.waitFor("sendVoice", nil)
	if data := c.files["voice"]; len(data) < 12 || string(data[4:8]) != "ftyp" {
		t.Fatalf("voice is not an MP4/M4A file: % x", c.files["voice"][:min(12, len(c.files["voice"]))])
	}
	if c.names["voice"] != "voice_fry.m4a" {
		t.Fatalf("file name %q", c.names["voice"])
	}
	// Short stages are still shown: processing and uploading.
	var stages []string
	for _, e := range h.api.find("editMessageText") {
		stages = append(stages, e.params["text"])
	}
	all := strings.Join(stages, " | ")
	if !strings.Contains(all, "Processing") || !strings.Contains(all, "Uploading") {
		t.Fatalf("stages: %s", all)
	}
}

func TestE2EVoiceMessagesForbidden(t *testing.T) {
	h := newHarness(t)
	h.api.addFile("note", render(t, "n.mp4",
		"-f", "lavfi", "-i", "testsrc=size=384x384:rate=30:duration=1", "-c:v", "libx264"))
	h.api.addFile("voice", render(t, "v.ogg", "-f", "lavfi", "-i", "sine=duration=1", "-c:a", "libopus"))

	h.api.failNext("sendVideoNote", "Bad Request: VOICE_MESSAGES_FORBIDDEN")
	h.command(user(1, "en"), private(1), "/wobble", &models.Message{ID: 41, Chat: private(1), VideoNote: &models.VideoNote{FileID: "note"}})
	if c := h.api.waitFor("sendVideo", nil); len(c.files["video"]) == 0 {
		t.Fatal("no fallback video")
	}

	h.api.failNext("sendVoice", "Bad Request: VOICE_MESSAGES_FORBIDDEN")
	h.command(user(1, "en"), private(1), "/wobble", &models.Message{ID: 42, Chat: private(1), Voice: &models.Voice{FileID: "voice"}})
	c := h.api.waitFor("sendAudio", nil)
	if len(c.files["audio"]) == 0 {
		t.Fatal("no fallback audio")
	}
	// Only the two status messages: no error was shown to the user.
	for _, c := range h.api.find("sendMessage") {
		if !strings.Contains(c.params["text"], "Downloading") {
			t.Fatalf("unexpected message: %v", c.params)
		}
	}
}

func TestE2EFryReplyToVideo(t *testing.T) {
	h := newHarness(t)
	h.api.addFile("vid", render(t, "v.mp4",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=30:duration=2",
		"-f", "lavfi", "-i", "sine=duration=2", "-c:v", "libx264", "-c:a", "aac", "-shortest"))
	original := &models.Message{ID: 70, Chat: group(-5), Video: &models.Video{FileID: "vid"}}
	h.send(&models.Message{From: user(1, "en"), Chat: group(-5), Text: "/fry@distortioner_bot", ReplyToMessage: original})
	c := h.api.waitFor("sendVideo", nil)
	if len(c.files["video"]) == 0 || !strings.Contains(c.params["reply_markup"], `"x:f"`) {
		t.Fatalf("%+v", c.params)
	}
	// A status while it works, removed when the result is sent.
	if n := len(h.api.find("sendMessage")); n != 1 || !strings.Contains(h.api.find("sendMessage")[0].params["text"], "Downloading") {
		t.Fatalf("status messages: %v", h.api.find("sendMessage"))
	}
	h.api.waitFor("deleteMessage", nil)
}

func TestE2EFryCaptionAndMoreButton(t *testing.T) {
	h := newHarness(t)
	h.api.addFile("photo", render(t, "p.jpg", "-f", "lavfi", "-i", "testsrc=size=640x480", "-frames:v", "1"))
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Caption: "/fry", Photo: []models.PhotoSize{{FileID: "photo"}}})
	c := h.api.waitFor("sendPhoto", nil)
	if len(c.files["photo"]) == 0 || c.params["caption"] != "" {
		t.Fatalf("%+v", c.params)
	}

	h.api.addFile("st", render(t, "s.webp", "-f", "lavfi", "-i", "testsrc=size=512x512", "-frames:v", "1"))
	h.press(*user(1, "en"), &models.Message{ID: 80, Chat: private(1), Sticker: &models.Sticker{FileID: "st"}}, "f")
	if c := h.api.waitFor("sendSticker", nil); !strings.Contains(c.params["reply_markup"], `"x:f"`) {
		t.Fatalf("%+v", c.params)
	}
}

func TestE2EQuestionThenRandomOnResult(t *testing.T) {
	h := newHarness(t)
	h.api.addFile("photo", render(t, "p.jpg", "-f", "lavfi", "-i", "testsrc=size=640x480", "-frames:v", "1"))
	h.send(&models.Message{ID: 900, From: user(1, "en"), Chat: private(1), Photo: []models.PhotoSize{{FileID: "photo"}}})
	question := h.question(1)
	h.press(*user(1, "en"), &models.Message{ID: question.resultID, Chat: private(1),
		ReplyToMessage: &models.Message{ID: 900, Chat: private(1), Photo: []models.PhotoSize{{FileID: "photo"}}}}, "x:w")
	first := h.api.waitFor("sendPhoto", nil)
	if first.params["caption"] != "" || len(h.api.find("deleteMessage")) != 1 {
		t.Fatalf("wobble result %+v, question not deleted", first.params)
	}

	// 🎲 on the result: a random mix, listed in the caption.
	h.press(*user(1, "en"), &models.Message{ID: first.resultID, Chat: private(1), Photo: []models.PhotoSize{{FileID: "photo"}}}, "x:r")
	second := h.api.waitFor("sendPhoto", func(c call) bool { return c.resultID != first.resultID })
	if !strings.HasPrefix(second.params["caption"], "🎲 ") {
		t.Fatalf("%+v", second.params)
	}
}

func TestE2EPickedEffectsOnVideo(t *testing.T) {
	h := newHarness(t)
	h.api.addFile("vid", render(t, "v.mp4",
		"-f", "lavfi", "-i", "testsrc2=size=320x240:rate=25:duration=2",
		"-f", "lavfi", "-i", "sine=duration=2", "-c:v", "libx264", "-c:a", "aac", "-shortest"))
	h.send(&models.Message{ID: 910, From: user(1, "en"), Chat: private(1), Video: &models.Video{FileID: "vid"}})
	q := h.question(1)
	prompt := &models.Message{ID: q.resultID, Chat: private(1)}
	for _, data := range []string{"x:p", "p:bulge", "p:earrape", "p:!"} {
		h.press(*user(1, "en"), prompt, data)
	}
	c := h.api.waitFor("sendVideo", nil)
	if c.params["caption"] != "✨ 🫧 + 🔊" || len(c.files["video"]) == 0 {
		t.Fatalf("%+v", c.params)
	}

	// The effects are counted for the admin statistics.
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, err := h.Store.EffectStats(context.Background(), time.Now().Add(-time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if st.Effects["bulge"] == 1 && st.Effects["earrape"] == 1 && st.Sources["picker"] == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stats %+v", st)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestE2EStrengthCommands(t *testing.T) {
	h := newHarness(t)
	h.api.addFile("st", render(t, "s.webp", "-f", "lavfi", "-i", "testsrc=size=512x512", "-frames:v", "1"))
	sticker := &models.Message{ID: 920, Chat: group(-5), Sticker: &models.Sticker{FileID: "st"}}
	h.command(user(1, "en"), group(-5), "/wobble 70", sticker)
	h.api.waitFor("sendSticker", nil)
	h.command(user(1, "en"), group(-5), "/fry 30", sticker)
	h.api.waitFor("sendSticker", func(c call) bool { return len(h.api.find("sendSticker")) == 2 })
}

func TestE2EDemotivatorFromCaption(t *testing.T) {
	h := newHarness(t)
	h.api.addFile("cat", render(t, "c.jpg", "-f", "lavfi", "-i", "testsrc=size=640x480", "-frames:v", "1"))
	h.send(&models.Message{ID: 950, From: user(1, "ru"), Chat: private(1), Caption: "Мой кот\nон спит",
		Photo: []models.PhotoSize{{FileID: "cat"}}})
	q := h.question(1)
	prompt := &models.Message{ID: q.resultID, Chat: private(1)}
	for _, data := range []string{"x:p", "p:demotivator", "p:!"} {
		h.press(*user(1, "ru"), prompt, data)
	}
	c := h.api.waitFor("sendPhoto", nil)
	// The caption is on the poster, not repeated under it.
	if c.params["caption"] != "✨ 🖼" || len(c.files["photo"]) == 0 {
		t.Fatalf("%+v", c.params)
	}
}

func TestE2EPickedEffectsKeepCaption(t *testing.T) {
	h := newHarness(t)
	h.api.addFile("photo", render(t, "p.jpg", "-f", "lavfi", "-i", "testsrc=size=640x480", "-frames:v", "1"))
	h.send(&models.Message{ID: 960, From: user(1, "ru"), Chat: private(1), Caption: "котик спит", Photo: []models.PhotoSize{{FileID: "photo"}}})
	prompt := &models.Message{ID: h.question(1).resultID, Chat: private(1)}
	for _, data := range []string{"x:p", "p:bulge", "p:rainbow", "p:!"} {
		h.press(*user(1, "ru"), prompt, data)
	}
	// Nothing picked for the caption: it stays as it was.
	if c := h.api.waitFor("sendPhoto", nil); c.params["caption"] != "котик спит\n\n✨ 🫧 + 🌈" {
		t.Fatalf("%q", c.params["caption"])
	}
}
