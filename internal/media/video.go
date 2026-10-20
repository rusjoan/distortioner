package media

import (
	"context"
	"errors"
	"fmt"
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
	StageProcessing // a single ffmpeg pass: effects without per-frame work
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

// mediaKind maps the video kinds of the wobble/fry API to media kinds.
func (k VideoKind) mediaKind() MediaKind {
	switch k {
	case KindAnimation:
		return MediaAnimation
	case KindVideoNote:
		return MediaVideoNote
	case KindSticker:
		return MediaVideoSticker
	default:
		return MediaVideo
	}
}

// DistortVideo applies wobble (seam carving on every frame) to a video.
func (e *Engine) DistortVideo(ctx context.Context, in, out string, opt VideoOptions) error {
	return e.Apply(ctx, in, out, Recipe{{ID: "wobble", Strength: opt.Strength}}, ApplyOptions{
		Kind: opt.Kind.mediaKind(), MaxSeconds: opt.MaxSeconds, MaxFPS: opt.MaxFPS,
		ProcessScale: opt.ProcessScale, DistortAudio: opt.DistortAudio,
		WorkDir: opt.WorkDir, Progress: opt.Progress,
	})
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

// processFrames runs ImageMagick operators on frames in place, in
// batches: one process per batch saves start-up and initialisation per
// frame. Batches stay small enough to keep every CPU slot busy.
func (e *Engine) processFrames(ctx context.Context, frames, ops []string, report func(Stage, int, int)) error {
	total := len(frames)
	report(StageDistorting, 0, total)

	batch := min(framesPerProcess, max(1, total/(e.CPULimit()*4)))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(e.CPULimit())
	var done atomic.Int64
	for start := 0; start < total; start += batch {
		files := frames[start:min(start+batch, total)]
		g.Go(func() error {
			if err := e.acquire(ctx); err != nil {
				return err
			}
			defer e.release()
			args := append(append([]string{"mogrify"}, ops...), files...)
			if _, err := run(ctx, e.magickEn, e.Magick, args...); err != nil {
				return err
			}
			report(StageDistorting, int(done.Add(int64(len(files)))), total)
			return nil
		})
	}
	return g.Wait()
}
