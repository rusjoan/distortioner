package bot

import (
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/graynk/distortioner/internal/media"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name        string
		msg         models.Message
		kind        Kind
		ext         string
		ok, unsupp  bool
		wantFile    string
		wantCaption string
	}{
		{"photo picks largest", models.Message{Photo: []models.PhotoSize{{FileID: "s"}, {FileID: "l"}}, Caption: "hi"},
			KindPhoto, ".jpg", true, false, "l", "hi"},
		{"static sticker", models.Message{Sticker: &models.Sticker{FileID: "st"}}, KindSticker, ".webp", true, false, "st", ""},
		{"video sticker", models.Message{Sticker: &models.Sticker{FileID: "vs", IsVideo: true}}, KindVideoSticker, ".webm", true, false, "vs", ""},
		{"animated sticker", models.Message{Sticker: &models.Sticker{IsAnimated: true}}, "", "", false, true, "", ""},
		{"gif document", models.Message{Document: &models.Document{FileID: "g", MimeType: "image/gif"}}, KindAnimation, ".gif", true, false, "g", ""},
		{"png document", models.Message{Document: &models.Document{FileID: "p", MimeType: "image/png", FileName: "a.PNG"}}, KindImageFile, ".png", true, false, "p", ""},
		{"svg document", models.Message{Document: &models.Document{MimeType: "image/svg+xml"}}, "", "", false, false, "", ""},
		{"video document", models.Message{Document: &models.Document{FileID: "v", MimeType: "video/quicktime", FileName: "clip.mov"}}, KindVideoFile, ".mov", true, false, "v", ""},
		{"pdf", models.Message{Document: &models.Document{MimeType: "application/pdf"}}, "", "", false, false, "", ""},
		{"voice", models.Message{Voice: &models.Voice{FileID: "vo"}}, KindVoice, ".ogg", true, false, "vo", ""},
		{"audio without name", models.Message{Audio: &models.Audio{FileID: "au"}}, KindAudio, ".mp3", true, false, "au", ""},
		{"text", models.Message{Text: "hello"}, KindText, "", true, false, "", ""},
		{"command caption dropped", models.Message{Photo: []models.PhotoSize{{FileID: "c"}}, Caption: "/fry"},
			KindPhoto, ".jpg", true, false, "c", ""},
		{"empty", models.Message{}, "", "", false, false, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			it, ok, unsupp := classify(&c.msg)
			if ok != c.ok || unsupp != c.unsupp || it.kind != c.kind || it.ext != c.ext || it.fileID != c.wantFile || it.caption != c.wantCaption {
				t.Fatalf("got %+v ok=%v unsupported=%v", it, ok, unsupp)
			}
		})
	}
}

func TestParseCommand(t *testing.T) {
	cases := []struct {
		text        string
		name, args  string
		ok, foreign bool
	}{
		{"/distort", "distort", "", true, false},
		{"/Distort@MyBot 70", "distort", "70", true, false},
		{"/distort@other_bot", "", "", false, true},
		{"/", "", "", false, false},
		{"hello", "", "", false, false},
		{"/start  ", "start", "", true, false},
	}
	for _, c := range cases {
		cmd, ok, foreign := parseCommand(c.text, "mybot")
		if ok != c.ok || foreign != c.foreign || cmd.name != c.name || cmd.args != c.args {
			t.Errorf("%q: got %+v ok=%v foreign=%v", c.text, cmd, ok, foreign)
		}
	}
}

func TestExtOr(t *testing.T) {
	for name, want := range map[string]string{"a.MP4": ".mp4", "noext": ".x", "weird.!!": ".x", "long.abcdefgh": ".x", "": ".x"} {
		if got := extOr(name, ".x"); got != want {
			t.Errorf("extOr(%q) = %q", name, got)
		}
	}
}

func TestResultName(t *testing.T) {
	cases := []struct {
		it     item
		recipe media.Recipe
		want   string
	}{
		{item{kind: KindVoice}, media.Recipe{{ID: "fry"}}, "voice_fry"},
		{item{kind: KindPhoto}, media.Recipe{{ID: "rainbow"}, {ID: "bulge"}}, "photo_bulge+rainbow"},
		{item{kind: KindAudio, name: "My Song (live).mp3"}, media.Recipe{{ID: "wobble"}}, "My_Song_live_wobble"},
		{item{kind: KindAudio, name: "моя песня.mp3"}, media.Recipe{{ID: "echo"}}, "моя_песня_echo"},
		{item{kind: KindImageFile, name: "...png"}, media.Recipe{{ID: "pixel"}}, "image_pixel"},
		{item{kind: KindVideoFile, name: strings.Repeat("a", 50) + ".mov"}, media.Recipe{{ID: "fry"}}, strings.Repeat("a", 32) + "_fry"},
	}
	for _, c := range cases {
		if got := resultName(c.it, c.recipe); got != c.want {
			t.Errorf("resultName(%+v) = %q, want %q", c.it, got, c.want)
		}
	}
}
