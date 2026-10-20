package media

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync/atomic"

	"golang.org/x/sync/errgroup"
)

// VideoKind selects the output container and constraints.
type VideoKind int

const (
	KindVideo     VideoKind = iota // mp4 with sound
	KindAnimation                  // mp4 without sound (Telegram "GIF")
	KindVideoNote                  // square mp4 with sound
	KindSticker                    // VP9 webm with alpha, ≤3 s, no sound
)

const (
	maxSide       = 512
	defaultMaxFPS = 30
	defaultFPS    = 25 // when the input does not report a rate
	stickerMaxSec = 3
	// framesPerProcess is the largest batch handed to one ImageMagick process.
	framesPerProcess = 30
)

// ErrNoVideo is returned for files without a video stream.
var ErrNoVideo = errors.New("no video stream")

// TooLongError is returned when the input exceeds the duration limit.
type TooLongError struct{ Limit int }

func (e *TooLongError) Error() string { return fmt.Sprintf("video is longer than %d seconds", e.Limit) }

// Stage is a step of video processing, reported to the progress callback.
type Stage int

const (
	StageExtracting Stage = iota
	StageDistorting
	StageEncoding
)

// VideoOptions configures DistortVideo.
type VideoOptions struct {
	Kind         VideoKind
	Strength     Strength
	MaxSeconds   int
	DistortAudio bool
	// MaxFPS caps the frame rate; fewer frames mean proportionally less work.
	// Zero means 30.
	MaxFPS int
	// ProcessScale shrinks frames, in percent, just for seam carving; they
	// are scaled back afterwards, so the output size does not change.
	// Lower is faster and blurrier. Zero means 100.
	ProcessScale int
	// WorkDir holds intermediate frames; it must exist and be private to the job.
	WorkDir string
	// Progress, if set, is called from several goroutines.
	Progress func(stage Stage, done, total int)
}

// DistortVideo distorts every frame of in and writes the result to out.
func (e *Engine) DistortVideo(ctx context.Context, in, out string, opt VideoOptions) error {
	report := opt.Progress
	if report == nil {
		report = func(Stage, int, int) {}
	}

	info, err := e.Probe(ctx, in)
	if err != nil {
		return err
	}
	if !info.HasVideo {
		return ErrNoVideo
	}
	maxSec := opt.MaxSeconds
	if opt.Kind == KindSticker {
		maxSec = stickerMaxSec
	}
	// Stickers are trimmed rather than rejected; Telegram caps them anyway.
	if opt.Kind != KindSticker && maxSec > 0 && info.Duration > float64(maxSec)+0.5 {
		return &TooLongError{Limit: maxSec}
	}

	maxFPS := opt.MaxFPS
	if maxFPS <= 0 {
		maxFPS = defaultMaxFPS
	}
	fps := info.FPS
	if fps <= 0 || math.IsInf(fps, 0) {
		fps = defaultFPS
	}
	fps = min(fps, float64(maxFPS))
	rate := strconv.FormatFloat(fps, 'f', 3, 64)

	framesDir := filepath.Join(opt.WorkDir, "frames")
	if err := os.MkdirAll(framesDir, 0o700); err != nil {
		return err
	}
	pattern := filepath.Join(framesDir, "%06d.pam")

	report(StageExtracting, 0, 0)
	if err := e.extractFrames(ctx, in, pattern, info, rate, maxSec); err != nil {
		return fmt.Errorf("extract frames: %w", err)
	}

	frames, err := filepath.Glob(filepath.Join(framesDir, "*.pam"))
	if err != nil {
		return err
	}
	if len(frames) == 0 {
		return errors.New("extract frames: no frames produced")
	}
	sort.Strings(frames)

	scale := opt.ProcessScale
	if scale <= 0 || scale > 100 {
		scale = 100
	}
	if err := e.distortFrames(ctx, frames, opt.Strength, scale, report); err != nil {
		return fmt.Errorf("distort frames: %w", err)
	}

	report(StageEncoding, 0, 0)
	if err := e.encode(ctx, in, pattern, out, info, rate, opt); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	return nil
}

func (e *Engine) extractFrames(ctx context.Context, in, pattern string, info Info, rate string, maxSec int) error {
	args := []string{"-nostdin", "-y", "-v", "error"}
	if info.Alpha && info.VideoCodec == "vp9" {
		// The native VP9 decoder drops the alpha channel.
		args = append(args, "-c:v", "libvpx-vp9")
	}
	pixFmt := "rgb24"
	if info.Alpha {
		pixFmt = "rgba"
	}
	args = append(args, "-i", in, "-map", "0:v:0", "-an")
	if maxSec > 0 {
		args = append(args, "-t", strconv.Itoa(maxSec))
	}
	// Shrink before seam carving: it is the expensive part, and its cost
	// grows with the pixel count. Scaling only ever goes down here.
	filter := fmt.Sprintf("fps=%s,scale=w=min(%d\\,iw):h=min(%d\\,ih):force_original_aspect_ratio=decrease", rate, maxSide, maxSide)
	args = append(args, "-vf", filter, "-pix_fmt", pixFmt, "-f", "image2", pattern)
	_, err := run(ctx, nil, e.FFmpeg, args...)
	return err
}

// distortFrames distorts frames in place, in batches: one ImageMagick
// process per batch saves process start-up and initialisation per frame.
// Batches stay small enough to keep every CPU slot busy.
func (e *Engine) distortFrames(ctx context.Context, frames []string, strength Strength, scale int, report func(Stage, int, int)) error {
	total := len(frames)
	report(StageDistorting, 0, total)

	batch := min(framesPerProcess, max(1, total/(e.CPULimit()*4)))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(e.CPULimit())
	var done atomic.Int64
	for start := 0; start < total; start += batch {
		files := frames[start:min(start+batch, total)]
		g.Go(func() error {
			if err := e.distortBatch(ctx, files, strength, scale); err != nil {
				return err
			}
			report(StageDistorting, int(done.Add(int64(len(files)))), total)
			return nil
		})
	}
	return g.Wait()
}

// distortBatch distorts frames in place with a single mogrify process.
// With scale below 100 the frame is shrunk for seam carving only and
// scaled back in the same step, so its size does not change.
func (e *Engine) distortBatch(ctx context.Context, files []string, strength Strength, scale int) error {
	if err := e.acquire(ctx); err != nil {
		return err
	}
	defer e.release()

	keep := strength.keep()
	args := []string{"mogrify"}
	if scale < 100 {
		args = append(args, "-resize", fmt.Sprintf("%d%%", scale))
	}
	back := 100 * (100 / float64(keep)) * (100 / float64(scale))
	args = append(args,
		"-liquid-rescale", fmt.Sprintf("%d%%", keep),
		"-resize", fmt.Sprintf("%.4f%%", back))
	_, err := run(ctx, e.magickEn, e.Magick, append(args, files...)...)
	return err
}

func (e *Engine) encode(ctx context.Context, in, pattern, out string, info Info, rate string, opt VideoOptions) error {
	args := []string{"-nostdin", "-y", "-v", "error", "-framerate", rate, "-i", pattern}

	if opt.Kind == KindSticker {
		args = append(args,
			"-map", "0:v",
			"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease", maxSide, maxSide),
			"-c:v", "libvpx-vp9", "-pix_fmt", "yuva420p",
			"-b:v", "400k", "-deadline", "good", "-cpu-used", "4", "-row-mt", "1", "-an",
			"-t", strconv.Itoa(stickerMaxSec),
			"-f", "webm", out)
		_, err := run(ctx, nil, e.FFmpeg, args...)
		return err
	}

	withAudio := info.HasAudio && (opt.Kind == KindVideo || opt.Kind == KindVideoNote)
	if withAudio {
		args = append(args, "-i", in, "-map", "0:v", "-map", "1:a:0")
		if opt.DistortAudio {
			args = append(args, "-af", vibrato(opt.Strength))
		}
		args = append(args, "-c:a", "aac", "-b:a", "128k", "-shortest")
	} else {
		args = append(args, "-map", "0:v", "-an")
	}

	// yuv420p needs even dimensions.
	args = append(args, "-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2", "-c:v", e.Codec)
	if e.Codec == "libx264" {
		args = append(args, "-preset", "veryfast", "-crf", "26")
	}
	args = append(args, "-pix_fmt", "yuv420p", "-movflags", "+faststart", "-f", "mp4", out)
	_, err := run(ctx, nil, e.FFmpeg, args...)
	return err
}
