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
	kind     Kind
	fileID   string
	size     int64
	ext      string // extension for the downloaded file
	caption  string
	duration float64 // seconds, as reported by Telegram; 0 if unknown
	name     string  // original file name, if the media had one
}

// classify finds the media in a message. unsupported is set for media we
// recognise but cannot process (animated Lottie stickers).
func classify(m *models.Message) (it item, ok, unsupported bool) {
	it, ok, unsupported = classifyMedia(m)
	if strings.HasPrefix(it.caption, "/") {
		it.caption = "" // the caption was a command such as /fry, not text to keep
	}
	it.caption = stripTag(it.caption)
	return it, ok, unsupported
}

// stripTag drops the line naming the applied effects that results carry,
// so that chained results do not pile them up.
func stripTag(caption string) string {
	head, last := "", caption
	if i := strings.LastIndex(caption, "\n\n"); i >= 0 {
		head, last = caption[:i], caption[i+2:]
	}
	if strings.HasPrefix(last, "🎲 ") || strings.HasPrefix(last, "✨ ") {
		return head
	}
	return caption
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
		return item{kind: KindAnimation, fileID: a.FileID, size: a.FileSize, ext: extOr(a.FileName, ".mp4"), caption: m.Caption, duration: float64(a.Duration), name: a.FileName}, true, false
	case m.Video != nil:
		v := m.Video
		return item{kind: KindVideo, fileID: v.FileID, size: v.FileSize, ext: extOr(v.FileName, ".mp4"), caption: m.Caption, duration: float64(v.Duration), name: v.FileName}, true, false
	case m.VideoNote != nil:
		v := m.VideoNote
		return item{kind: KindVideoNote, fileID: v.FileID, size: int64(v.FileSize), ext: ".mp4", duration: float64(v.Duration)}, true, false
	case m.Voice != nil:
		v := m.Voice
		return item{kind: KindVoice, fileID: v.FileID, size: v.FileSize, ext: ".ogg", caption: m.Caption, duration: float64(v.Duration)}, true, false
	case m.Audio != nil:
		a := m.Audio
		return item{kind: KindAudio, fileID: a.FileID, size: a.FileSize, ext: extOr(a.FileName, ".mp3"), caption: m.Caption, duration: float64(a.Duration), name: a.FileName}, true, false
	case m.Document != nil:
		d := m.Document
		mime := strings.ToLower(d.MimeType)
		it = item{fileID: d.FileID, size: d.FileSize, caption: m.Caption, name: d.FileName}
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
