package bot

import (
	"path/filepath"
	"strings"

	"github.com/go-telegram/bot/models"
)

// Kind is what we distort; it is also the label used in statistics.
type Kind string

const (
	KindPhoto        Kind = "photo"
	KindSticker      Kind = "sticker"
	KindVideoSticker Kind = "video_sticker"
	KindAnimation    Kind = "animation"
	KindVideo        Kind = "video"
	KindVideoNote    Kind = "video_note"
	KindVoice        Kind = "voice"
	KindAudio        Kind = "audio"
	KindImageFile    Kind = "image_file"
	KindVideoFile    Kind = "video_file"
	KindText         Kind = "text"
	KindAlbum        Kind = "album"
)

// Effect is what is done to the media.
type Effect int

const (
	EffectDistort Effect = iota // seam carving, the default
	EffectFry                   // deep fry, shake and earrape
)

// label prefixes the media kind in statistics: "video", "fry_video".
func (e Effect) label(k Kind) string {
	if e == EffectFry {
		return "fry_" + string(k)
	}
	return string(k)
}

// heavy kinds go through the video pool and get a progress message.
func (k Kind) heavy() bool {
	switch k {
	case KindVideoSticker, KindAnimation, KindVideo, KindVideoNote, KindVideoFile:
		return true
	}
	return false
}

// item is a piece of media extracted from a message.
type item struct {
	kind    Kind
	fileID  string
	size    int64
	ext     string // extension for the downloaded file
	caption string
}

// classify finds the media in a message. unsupported is set for media we
// recognise but cannot process (animated Lottie stickers).
func classify(m *models.Message) (it item, ok, unsupported bool) {
	it, ok, unsupported = classifyMedia(m)
	if strings.HasPrefix(it.caption, "/") {
		it.caption = "" // the caption was a command such as /fry, not text to keep
	}
	return it, ok, unsupported
}

func classifyMedia(m *models.Message) (it item, ok, unsupported bool) {
	it.caption = m.Caption
	switch {
	case len(m.Photo) > 0:
		p := m.Photo[len(m.Photo)-1] // sizes are sorted, the last is the largest
		return item{kind: KindPhoto, fileID: p.FileID, size: int64(p.FileSize), ext: ".jpg", caption: m.Caption}, true, false
	case m.Sticker != nil:
		s := m.Sticker
		switch {
		case s.IsAnimated:
			return it, false, true
		case s.IsVideo:
			return item{kind: KindVideoSticker, fileID: s.FileID, size: int64(s.FileSize), ext: ".webm"}, true, false
		default:
			return item{kind: KindSticker, fileID: s.FileID, size: int64(s.FileSize), ext: ".webp"}, true, false
		}
	case m.Animation != nil:
		a := m.Animation
		return item{kind: KindAnimation, fileID: a.FileID, size: a.FileSize, ext: extOr(a.FileName, ".mp4"), caption: m.Caption}, true, false
	case m.Video != nil:
		v := m.Video
		return item{kind: KindVideo, fileID: v.FileID, size: v.FileSize, ext: extOr(v.FileName, ".mp4"), caption: m.Caption}, true, false
	case m.VideoNote != nil:
		v := m.VideoNote
		return item{kind: KindVideoNote, fileID: v.FileID, size: int64(v.FileSize), ext: ".mp4"}, true, false
	case m.Voice != nil:
		v := m.Voice
		return item{kind: KindVoice, fileID: v.FileID, size: v.FileSize, ext: ".ogg", caption: m.Caption}, true, false
	case m.Audio != nil:
		a := m.Audio
		return item{kind: KindAudio, fileID: a.FileID, size: a.FileSize, ext: extOr(a.FileName, ".mp3"), caption: m.Caption}, true, false
	case m.Document != nil:
		d := m.Document
		mime := strings.ToLower(d.MimeType)
		it = item{fileID: d.FileID, size: d.FileSize, caption: m.Caption}
		switch {
		case mime == "image/gif":
			it.kind, it.ext = KindAnimation, ".gif"
		case strings.HasPrefix(mime, "image/") && mime != "image/svg+xml":
			it.kind, it.ext = KindImageFile, extOr(d.FileName, ".png")
		case strings.HasPrefix(mime, "video/"):
			it.kind, it.ext = KindVideoFile, extOr(d.FileName, ".mp4")
		case strings.HasPrefix(mime, "audio/"):
			it.kind, it.ext = KindAudio, extOr(d.FileName, ".mp3")
		default:
			return item{}, false, false
		}
		return it, true, false
	case m.Text != "":
		return item{kind: KindText}, true, false
	}
	return item{}, false, false
}

// extOr returns the lowercase extension of name, or def when it has none
// or it does not look like a sane extension.
func extOr(name, def string) string {
	ext := strings.ToLower(filepath.Ext(name))
	if len(ext) < 2 || len(ext) > 6 {
		return def
	}
	for _, r := range ext[1:] {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return def
		}
	}
	return ext
}

// command is a parsed bot command.
type command struct {
	name string
	args string
}

// parseCommand extracts "/name@bot args". Commands addressed to another bot
// are reported with foreign=true so they are ignored rather than distorted.
func parseCommand(text, botUsername string) (cmd command, ok, foreign bool) {
	if !strings.HasPrefix(text, "/") {
		return cmd, false, false
	}
	head, args, _ := strings.Cut(text, " ")
	name, target, addressed := strings.Cut(head[1:], "@")
	if addressed && !strings.EqualFold(target, botUsername) {
		return cmd, false, true
	}
	return command{name: strings.ToLower(name), args: strings.TrimSpace(args)}, name != "", false
}
