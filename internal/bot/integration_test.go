//go:build integration

package bot

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

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
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Caption: "nice cat",
		Photo: []models.PhotoSize{{FileID: "photo"}}})

	c := h.api.waitFor("sendPhoto", nil)
	if len(c.files["photo"]) == 0 || c.params["caption"] != "nIcE cAt" {
		t.Fatalf("%+v", c.params)
	}
	if !strings.Contains(c.params["reply_markup"], `"r:50"`) {
		t.Fatalf("no More button: %s", c.params["reply_markup"])
	}
}

func TestE2EVideoWithProgress(t *testing.T) {
	h := newHarness(t)
	h.api.addFile("video", render(t, "v.mp4",
		"-f", "lavfi", "-i", "testsrc=size=320x240:rate=25:duration=2",
		"-f", "lavfi", "-i", "sine=duration=2", "-c:v", "libx264", "-c:a", "aac", "-shortest"))
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Video: &models.Video{FileID: "video"}})

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
	if !strings.Contains(c.params["reply_markup"], `"r:70"`) || !strings.Contains(c.params["reply_parameters"], `"message_id":77`) {
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
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Video: &models.Video{FileID: "long"}})
	h.send(&models.Message{From: user(2, "en"), Chat: private(2), Video: &models.Video{FileID: "short"}})

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
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), VideoNote: &models.VideoNote{FileID: "note", Length: 384}})
	if c := h.api.waitFor("sendVideoNote", nil); len(c.files["video_note"]) == 0 {
		t.Fatal("empty video note")
	}
}

// Users may forbid voice and video messages; the result then arrives as a
// regular video (video notes) or a document (voice messages).
func TestE2EVoiceMessagesForbidden(t *testing.T) {
	h := newHarness(t)
	h.api.addFile("note", render(t, "n.mp4",
		"-f", "lavfi", "-i", "testsrc=size=384x384:rate=30:duration=1", "-c:v", "libx264"))
	h.api.addFile("voice", render(t, "v.ogg", "-f", "lavfi", "-i", "sine=duration=1", "-c:a", "libopus"))

	h.api.failNext("sendVideoNote", "Bad Request: VOICE_MESSAGES_FORBIDDEN")
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), VideoNote: &models.VideoNote{FileID: "note"}})
	if c := h.api.waitFor("sendVideo", nil); len(c.files["video"]) == 0 {
		t.Fatal("no fallback video")
	}

	h.api.failNext("sendVoice", "Bad Request: VOICE_MESSAGES_FORBIDDEN")
	h.send(&models.Message{From: user(1, "en"), Chat: private(1), Voice: &models.Voice{FileID: "voice"}})
	if c := h.api.waitFor("sendDocument", nil); len(c.files["document"]) == 0 {
		t.Fatal("no fallback document")
	}
	if n := len(h.api.find("sendMessage")); n != 1 { // only the video note's progress message
		t.Fatalf("unexpected messages: %v", h.api.find("sendMessage"))
	}
}
