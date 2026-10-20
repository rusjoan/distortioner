// Package media distorts images, audio and video with ImageMagick's
// liquid rescale (seam carving) and ffmpeg.
package media

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode"
)

// Strength is the percentage of the picture removed by seam carving
// before it is scaled back up. 50 matches the original bot.
type Strength int

// keep returns the share of the image kept by liquid rescale, in percent.
func (s Strength) keep() int {
	k := 100 - int(s)
	return min(max(k, 10), 90)
}

// Engine runs the external tools. A single semaphore bounds how many
// ImageMagick processes run at once across all jobs.
type Engine struct {
	FFmpeg  string
	FFprobe string
	Magick  string
	Codec   string // ffmpeg video encoder for mp4 output

	cpu      chan struct{}
	magickEn []string
}

// NewEngine creates an engine allowing cpuLimit concurrent ImageMagick processes.
func NewEngine(cpuLimit int, codec string) *Engine {
	return &Engine{
		FFmpeg:  "ffmpeg",
		FFprobe: "ffprobe",
		Magick:  "magick",
		Codec:   codec,
		cpu:     make(chan struct{}, cpuLimit),
		// Each process gets a single thread: parallelism comes from running
		// one process per core, and OpenMP inside would oversubscribe the CPU.
		magickEn: append(os.Environ(), "MAGICK_THREAD_LIMIT=1", "OMP_NUM_THREADS=1"),
	}
}

// CPUInUse reports how many ImageMagick slots are taken.
func (e *Engine) CPUInUse() int { return len(e.cpu) }

// CPULimit reports the size of the ImageMagick pool.
func (e *Engine) CPULimit() int { return cap(e.cpu) }

// Check verifies that all tools are installed and that ImageMagick has
// liquid rescale support (it is an optional delegate).
func (e *Engine) Check(ctx context.Context) error {
	for _, tool := range []string{e.FFmpeg, e.FFprobe} {
		if _, err := run(ctx, nil, tool, "-version"); err != nil {
			return err
		}
	}
	out, err := run(ctx, e.magickEn, e.Magick,
		"-size", "8x8", "xc:white", "-liquid-rescale", "50%", "-format", "%w", "info:")
	if err != nil {
		return fmt.Errorf("imagemagick without liquid rescale support: %w", err)
	}
	if strings.TrimSpace(string(out)) != "4" {
		return fmt.Errorf("imagemagick liquid rescale returned %q", out)
	}
	return nil
}

func (e *Engine) acquire(ctx context.Context) error {
	select {
	case e.cpu <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *Engine) release() { <-e.cpu }

// DistortImage applies wobble to a still image. Stickers are fitted into
// 512×512 as Telegram requires.
func (e *Engine) DistortImage(ctx context.Context, in, out string, strength Strength, sticker bool) error {
	kind := MediaPhoto
	if sticker {
		kind = MediaSticker
	}
	return e.Apply(ctx, in, out, Recipe{{ID: "wobble", Strength: strength}}, ApplyOptions{Kind: kind})
}

// vibrato builds the audio filter; stronger distortion means deeper vibrato.
func vibrato(strength Strength) string {
	depth := min(max(0.2+float64(strength)/100, 0.3), 1.0)
	return fmt.Sprintf("vibrato=f=6:d=%.2f", depth)
}

// DistortAudio applies wobble's vibrato; .ogg output is Opus.
func (e *Engine) DistortAudio(ctx context.Context, in, out string, strength Strength) error {
	return e.Apply(ctx, in, out, Recipe{{ID: "wobble", Strength: strength}}, ApplyOptions{Kind: soundKind(out)})
}

// DistortText alternates letter case: "hello" → "hElLo".
func DistortText(text string) string {
	i := 0
	return strings.Map(func(r rune) rune {
		if !unicode.IsLetter(r) {
			return r
		}
		i++
		if i%2 == 0 {
			return unicode.ToUpper(r)
		}
		return unicode.ToLower(r)
	}, text)
}
