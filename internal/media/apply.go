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
	"strings"
)

// ApplyOptions configures Apply.
type ApplyOptions struct {
	Kind MediaKind
	// MaxSeconds rejects longer inputs (video stickers are trimmed instead).
	MaxSeconds int
	// MaxFPS caps the frame rate when frames are processed one by one.
	// Zero means 30.
	MaxFPS int
	// ProcessScale shrinks frames, in percent, for the per-frame effects
	// only; they are scaled back, so the output size does not change.
	// Zero means 100.
	ProcessScale int
	// DistortAudio lets wobble distort the sound of videos (a chat setting).
	DistortAudio bool
	// Demotivator is the poster text when the recipe has a demotivator.
	Demotivator Demotivator
	// WorkDir holds intermediate files; it must exist and be private to the job.
	WorkDir string
	// Progress, if set, is called from several goroutines.
	Progress func(stage Stage, done, total int)
}

// ErrNothingToApply is returned when no effect of the recipe applies to the media.
var ErrNothingToApply = errors.New("no effect applies to this media")

// Applicable keeps the steps that do something to this kind of media.
func (r Recipe) Applicable(k MediaKind) Recipe {
	var out Recipe
	for _, s := range r {
		if e, ok := catalog[s.ID]; ok && e.AppliesTo(k) {
			out = append(out, s)
		}
	}
	return out
}

// Apply runs a recipe on in and writes the result to out.
func (e *Engine) Apply(ctx context.Context, in, out string, recipe Recipe, opt ApplyOptions) error {
	recipe = recipe.Applicable(opt.Kind).sorted()
	if len(recipe) == 0 {
		return ErrNothingToApply
	}
	switch {
	case opt.Kind.still():
		return e.applyStill(ctx, in, out, recipe, opt)
	case opt.Kind.sound():
		return e.applySound(ctx, in, out, recipe, opt)
	default:
		return e.applyVideo(ctx, in, out, recipe, opt)
	}
}

// chain collects the parts of a recipe in application order.
type chain struct {
	frame []string // ImageMagick operators, run on each frame
	video []string // ffmpeg video filters
	audio []string // ffmpeg audio filters
	fry   bool     // fried output is compressed harder on purpose
}

func (e *Engine) build(recipe Recipe, opt ApplyOptions, info Info) chain {
	var c chain
	still := opt.Kind.still()
	for _, s := range recipe {
		def := catalog[s.ID]
		c.fry = c.fry || s.ID == "fry"
		if def.frame != nil {
			c.frame = append(c.frame, def.frame(s.Strength)...)
		}
		if def.video != nil {
			c.video = append(c.video, def.video(s.Strength, still))
		}
		if def.audio != nil && opt.Kind.audible() {
			if s.ID == "wobble" && !opt.Kind.sound() && !opt.DistortAudio {
				continue // the chat turned off sound distortion in videos
			}
			c.audio = append(c.audio, def.audio(s.Strength, info.SampleRate))
		}
	}
	return c
}

func (e *Engine) applyStill(ctx context.Context, in, out string, recipe Recipe, opt ApplyOptions) error {
	c := e.build(recipe, opt, Info{})
	sticker := opt.Kind == MediaSticker

	if err := e.acquire(ctx); err != nil {
		return err
	}
	defer e.release()

	// The demotivator frames the finished picture: render the rest first.
	final := out
	if recipe.Has("demotivator") {
		final = e.stagePath(opt, out, "pre-demotivator.png")
		defer os.Remove(final)
	}

	if err := e.stillEffects(ctx, in, final, c, sticker, opt, out); err != nil {
		return err
	}
	if final == out {
		return nil
	}
	quality := 90
	if c.fry {
		quality = 20
	}
	return e.demotivate(ctx, final, out, opt.Demotivator, quality)
}

func (e *Engine) stagePath(opt ApplyOptions, out, name string) string {
	if opt.WorkDir != "" {
		return filepath.Join(opt.WorkDir, name)
	}
	return strings.TrimSuffix(out, filepath.Ext(out)) + "." + name
}

// stillEffects runs the ImageMagick operators and then the ffmpeg filters
// of a recipe on a still, writing to dst.
func (e *Engine) stillEffects(ctx context.Context, in, dst string, c chain, sticker bool, opt ApplyOptions, out string) error {
	// ImageMagick: orientation, the 512 px cap and per-frame operators. With
	// no ffmpeg filters this is the whole job, exactly as wobble always was.
	magickArgs := append([]string{in + "[0]", "-auto-orient", "-resize", "512x512>"}, c.frame...)
	if len(c.video) == 0 {
		if sticker {
			magickArgs = append(magickArgs, "-resize", "512x512")
		}
		_, err := run(ctx, e.magickEn, e.Magick, append(magickArgs, dst)...)
		return err
	}

	stage := e.stagePath(opt, out, "still.png")
	defer os.Remove(stage)
	if _, err := run(ctx, e.magickEn, e.Magick, append(magickArgs, stage)...); err != nil {
		return err
	}

	fried := dst
	if sticker {
		// ffmpeg here has no WebP encoder: filter to PNG, then ImageMagick.
		fried = strings.TrimSuffix(stage, ".png") + ".fx.png"
		defer os.Remove(fried)
	}
	args := []string{"-nostdin", "-y", "-v", "error", "-threads", "1", "-i", stage,
		"-frames:v", "1", "-vf", strings.Join(c.video, ",")}
	if !sticker && !strings.HasSuffix(fried, ".png") {
		q := "3"
		if c.fry {
			q = "28" // JPEG: 2 is best, 31 is worst
		}
		args = append(args, "-q:v", q)
	}
	if _, err := run(ctx, nil, e.FFmpeg, append(args, fried)...); err != nil {
		return err
	}
	if !sticker {
		return nil
	}
	_, err := run(ctx, e.magickEn, e.Magick, fried, "-resize", "512x512", dst)
	return err
}

func (e *Engine) applySound(ctx context.Context, in, out string, recipe Recipe, opt ApplyOptions) error {
	info, err := e.Probe(ctx, in)
	if err != nil {
		return err
	}
	c := e.build(recipe, opt, info)
	if len(c.audio) == 0 {
		return ErrNothingToApply
	}
	// The container follows out's extension: .ogg gives Opus, the rest AAC.
	codec := []string{"-c:a", "aac", "-b:a", "128k"}
	if strings.EqualFold(filepath.Ext(out), ".ogg") {
		codec = []string{"-c:a", "libopus", "-b:a", "64k"}
	}
	args := append([]string{"-nostdin", "-y", "-v", "error", "-i", in, "-vn", "-map", "0:a:0",
		"-af", strings.Join(c.audio, ",")}, codec...)
	if opt.Progress != nil {
		opt.Progress(StageProcessing, 0, 1000)
	}
	return runProgress(ctx, e.FFmpeg, append(args, out), info.Duration*recipe.Tempo(), progress(opt, StageProcessing))
}

// progress adapts the options' callback to one stage, or returns nil.
func progress(opt ApplyOptions, stage Stage) func(done, total int) {
	if opt.Progress == nil {
		return nil
	}
	return func(done, total int) { opt.Progress(stage, done, total) }
}

// expectedLength is the output length in seconds, for progress reporting.
func expectedLength(info Info, recipe Recipe, opt ApplyOptions, maxSec int) float64 {
	d := info.Duration
	if maxSec > 0 {
		d = min(d, float64(maxSec))
	}
	d *= recipe.Tempo()
	switch opt.Kind {
	case MediaVideoSticker:
		d = min(d, stickerMaxSec)
	case MediaVideoNote:
		d = min(d, 60)
	}
	return d
}

func (e *Engine) applyVideo(ctx context.Context, in, out string, recipe Recipe, opt ApplyOptions) error {
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
	if opt.Kind == MediaVideoSticker {
		maxSec = stickerMaxSec
	}
	// Stickers are trimmed rather than rejected; Telegram caps them anyway.
	if opt.Kind != MediaVideoSticker && maxSec > 0 && info.Duration > float64(maxSec)+0.5 {
		return &TooLongError{Limit: maxSec}
	}
	c := e.build(recipe, opt, info)

	if len(c.frame) == 0 {
		// One ffmpeg pass on one CPU slot: cheap effects never take the machine.
		if err := e.acquire(ctx); err != nil {
			return err
		}
		defer e.release()
		report(StageProcessing, 0, 1000)
		args := []string{"-nostdin", "-y", "-v", "error", "-threads", "1"}
		if info.Alpha && info.VideoCodec == "vp9" {
			args = append(args, "-c:v", "libvpx-vp9")
		}
		args = append(args, "-i", in, "-map", "0:v:0", "-filter_threads", "1")
		vf := append([]string{fitFilter}, c.video...)
		return e.encodeVideo(ctx, args, vf, c, info, opt, in, out, false,
			expectedLength(info, recipe, opt, maxSec), progress(opt, StageProcessing))
	}

	// Per-frame path: extract, run the ImageMagick operators on every frame,
	// encode with the ffmpeg filters and the sound of the original.
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
	if err := e.processFrames(ctx, frames, frameOps(c.frame, scale), report); err != nil {
		return fmt.Errorf("process frames: %w", err)
	}

	report(StageEncoding, 0, 0)
	args := []string{"-nostdin", "-y", "-v", "error", "-framerate", rate, "-i", pattern}
	if err := e.encodeVideo(ctx, args, c.video, c, info, opt, in, out, true,
		expectedLength(info, recipe, opt, maxSec), progress(opt, StageEncoding)); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	return nil
}

// frameOps wraps the per-frame operators in the processing scale: shrink
// before, scale back after, so the frame size does not change.
func frameOps(o []string, scale int) []string {
	if scale >= 100 {
		return o
	}
	back := fmt.Sprintf("%.4f%%", 100*100/float64(scale))
	return append(append([]string{"-resize", fmt.Sprintf("%d%%", scale)}, o...), "-resize", back)
}

// encodeVideo finishes the ffmpeg command: video filters, sound, codec and
// Telegram's constraints for the media kind. With fromFrames the command
// reads the processed frames and takes the sound from in; otherwise it
// already reads in and maps its video.
func (e *Engine) encodeVideo(ctx context.Context, args, vf []string, c chain, info Info, opt ApplyOptions, in, out string, fromFrames bool,
	expected float64, onProgress func(done, total int)) error {
	withAudio := info.HasAudio && opt.Kind.audible()
	audioMap := "0:a:0"
	if fromFrames {
		if withAudio {
			args = append(args, "-i", in)
			audioMap = "1:a:0"
		}
		args = append(args, "-map", "0:v")
	}

	if opt.Kind == MediaVideoSticker {
		vf = append(vf, fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease", maxSide, maxSide))
		bitrate := []string{"-b:v", "400k"}
		if c.fry {
			// Noise compresses badly, and libvpx only holds the rate when it is
			// constant: 300 kbit/s × 3 s keeps the sticker well under 256 KB.
			bitrate = []string{"-b:v", "300k", "-minrate", "300k", "-maxrate", "300k", "-bufsize", "300k"}
		}
		args = append(args, "-vf", strings.Join(vf, ","), "-c:v", "libvpx-vp9", "-pix_fmt", "yuva420p")
		args = append(args, bitrate...)
		args = append(args, "-deadline", "good", "-cpu-used", "4", "-row-mt", "1", "-an",
			"-t", strconv.Itoa(stickerMaxSec), "-f", "webm", out)
		return runProgress(ctx, e.FFmpeg, args, expected, onProgress)
	}

	if withAudio {
		args = append(args, "-map", audioMap)
		if len(c.audio) > 0 {
			args = append(args, "-af", strings.Join(c.audio, ","))
		}
		args = append(args, "-c:a", "aac", "-b:a", "128k", "-shortest")
	} else {
		args = append(args, "-an")
	}
	// yuv420p needs even dimensions.
	vf = append(vf, "scale=trunc(iw/2)*2:trunc(ih/2)*2")
	args = append(args, "-vf", strings.Join(vf, ","), "-c:v", e.Codec)
	if e.Codec == "libx264" {
		crf := "26"
		if c.fry {
			crf = "34" // compression artifacts are part of the fried look
		}
		args = append(args, "-preset", "veryfast", "-crf", crf)
	}
	if opt.Kind == MediaVideoNote {
		args = append(args, "-t", "60") // Telegram: video notes are at most a minute
	}
	args = append(args, "-pix_fmt", "yuv420p", "-movflags", "+faststart", "-f", "mp4", out)
	return runProgress(ctx, e.FFmpeg, args, expected, onProgress)
}
