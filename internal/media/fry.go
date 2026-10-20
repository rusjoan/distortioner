package media

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// fitFilter shrinks to at most maxSide, as for distortion.
var fitFilter = fmt.Sprintf("scale=w=min(%d\\,iw):h=min(%d\\,ih):force_original_aspect_ratio=decrease", maxSide, maxSide)

// FryVideo deep-fries a video, GIF, video note or video sticker at the
// reference strength.
func (e *Engine) FryVideo(ctx context.Context, in, out string, kind VideoKind, maxSeconds int) error {
	return e.Apply(ctx, in, out, Recipe{{ID: "fry", Strength: 50}}, ApplyOptions{
		Kind: kind.mediaKind(), MaxSeconds: maxSeconds, WorkDir: filepath.Dir(out),
	})
}

// FryImage deep-fries a picture: low-quality JPEG, or a WebP sticker.
func (e *Engine) FryImage(ctx context.Context, in, out string, sticker bool) error {
	kind := MediaPhoto
	if sticker {
		kind = MediaSticker
	}
	return e.Apply(ctx, in, out, Recipe{{ID: "fry", Strength: 50}}, ApplyOptions{Kind: kind})
}

// Earrape applies the earrape effect to sound; .ogg output is Opus.
func (e *Engine) Earrape(ctx context.Context, in, out string) error {
	return e.Apply(ctx, in, out, Recipe{{ID: "earrape"}}, ApplyOptions{Kind: soundKind(out)})
}

func soundKind(out string) MediaKind {
	if strings.EqualFold(filepath.Ext(out), ".ogg") {
		return MediaVoice
	}
	return MediaAudio
}
