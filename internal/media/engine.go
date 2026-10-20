// Package media distorts images, audio and video with ImageMagick's
// liquid rescale (seam carving) and ffmpeg.
package media

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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

// DistortImage distorts a still image. The output format follows out's
// extension. Stickers are fitted into 512×512 as Telegram requires.
func (e *Engine) DistortImage(ctx context.Context, in, out string, strength Strength, sticker bool) error {
	if err := e.acquire(ctx); err != nil {
		return err
	}
	defer e.release()

	keep := strength.keep()
	args := []string{
		in + "[0]", // first frame only, in case of animated webp/gif
		"-auto-orient",
		"-resize", "512x512>",
		"-liquid-rescale", fmt.Sprintf("%d%%", keep),
		"-resize", fmt.Sprintf("%.4f%%", 100*100/float64(keep)),
	}
	if sticker {
		args = append(args, "-resize", "512x512")
	}
	args = append(args, out)
	_, err := run(ctx, e.magickEn, e.Magick, args...)
	return err
}

// vibrato builds the audio filter; stronger distortion means deeper vibrato.
func vibrato(strength Strength) string {
	depth := min(max(0.2+float64(strength)/100, 0.3), 1.0)
	return fmt.Sprintf("vibrato=f=6:d=%.2f", depth)
}

// DistortAudio applies vibrato. The codec follows out's extension:
// .ogg gives Opus (voice messages), anything else AAC.
func (e *Engine) DistortAudio(ctx context.Context, in, out string, strength Strength) error {
	codec := []string{"-c:a", "aac", "-b:a", "128k"}
	if strings.EqualFold(filepath.Ext(out), ".ogg") {
		codec = []string{"-c:a", "libopus", "-b:a", "64k"}
	}
	args := append([]string{"-nostdin", "-y", "-i", in, "-vn", "-map", "0:a:0", "-af", vibrato(strength)}, codec...)
	_, err := run(ctx, nil, e.FFmpeg, append(args, out)...)
	return err
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
