package media

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// "Deep fry": oversaturated, oversharpened, noisy and badly compressed
// picture, a shaking frame and earrape sound. Everything is a single ffmpeg
// pass, so it costs a fraction of seam carving.
const (
	// fryFilter cranks colour and sharpness, adds noise and fakes heavy
	// compression by going down to a third of the size and back up.
	fryFilter = "eq=saturation=3.2:contrast=1.7:brightness=0.06:gamma_r=1.25," +
		"unsharp=7:7:2.5,noise=alls=25:allf=t," +
		"scale=iw/3:-2,scale=iw*3:-2:flags=neighbor"
	// shakeFilter crops a randomly moving window, like an earthquake.
	shakeFilter = "crop=w=iw*0.82:h=ih*0.82:" +
		"x=(iw-ow)/2+(random(1)-0.5)*iw*0.16:y=(ih-oh)/2+(random(2)-0.5)*ih*0.16"
	// earrapeFilter (level 2): mono, no rumble, big bass and treble, then
	// loudness normalisation so that quiet and loud inputs end up alike, and
	// only then gain, bit crushing and hard clipping.
	earrapeFilter = "aformat=channel_layouts=mono,highpass=f=35," +
		"bass=g=36:f=80:w=0.8,treble=g=8," +
		"loudnorm=I=-20:TP=-1:LRA=7,aresample=48000," +
		"volume=16dB,asoftclip=type=hard:threshold=0.6," +
		"acrusher=bits=4:mode=lin:aa=0,volume=4dB,asoftclip=type=hard,volume=-0.5dB"
	// fryCRF is deliberately bad: compression artifacts are part of the look.
	fryCRF = "34"
)

// fitFilter shrinks to at most maxSide, as for distortion.
var fitFilter = fmt.Sprintf("scale=w=min(%d\\,iw):h=min(%d\\,ih):force_original_aspect_ratio=decrease", maxSide, maxSide)

// FryVideo deep-fries a video, GIF, video note or video sticker.
func (e *Engine) FryVideo(ctx context.Context, in, out string, kind VideoKind, maxSeconds int) error {
	info, err := e.Probe(ctx, in)
	if err != nil {
		return err
	}
	if !info.HasVideo {
		return ErrNoVideo
	}
	if kind != KindSticker && maxSeconds > 0 && info.Duration > float64(maxSeconds)+0.5 {
		return &TooLongError{Limit: maxSeconds}
	}

	// One CPU slot, one encoder thread: frying runs next to seam carving
	// and must not take the whole machine.
	if err := e.acquire(ctx); err != nil {
		return err
	}
	defer e.release()

	args := []string{"-nostdin", "-y", "-v", "error", "-threads", "1"}
	if info.Alpha && info.VideoCodec == "vp9" {
		args = append(args, "-c:v", "libvpx-vp9")
	}
	args = append(args, "-i", in, "-map", "0:v:0", "-filter_threads", "1")
	vf := fitFilter + "," + fryFilter + "," + shakeFilter

	if kind == KindSticker {
		args = append(args,
			"-vf", vf+fmt.Sprintf(",scale=%d:%d:force_original_aspect_ratio=decrease", maxSide, maxSide),
			// Noise compresses badly: a hard rate cap keeps the sticker under
			// Telegram's 256 KB.
			"-c:v", "libvpx-vp9", "-pix_fmt", "yuva420p", "-b:v", "250k", "-maxrate", "300k", "-bufsize", "300k",
			"-deadline", "good", "-cpu-used", "4", "-an", "-t", strconv.Itoa(stickerMaxSec),
			"-f", "webm", out)
		_, err := run(ctx, nil, e.FFmpeg, args...)
		return err
	}

	if info.HasAudio && kind != KindAnimation {
		args = append(args, "-map", "0:a:0", "-af", earrapeFilter, "-c:a", "aac", "-b:a", "128k")
	} else {
		args = append(args, "-an")
	}
	args = append(args, "-vf", vf+",scale=trunc(iw/2)*2:trunc(ih/2)*2", "-c:v", e.Codec)
	if e.Codec == "libx264" {
		args = append(args, "-preset", "veryfast", "-crf", fryCRF)
	}
	args = append(args, "-pix_fmt", "yuv420p", "-movflags", "+faststart", "-f", "mp4", out)
	_, err = run(ctx, nil, e.FFmpeg, args...)
	return err
}

// FryImage deep-fries a picture. Photos come out as low-quality JPEG;
// stickers as WebP fitted into 512×512.
func (e *Engine) FryImage(ctx context.Context, in, out string, sticker bool) error {
	if err := e.acquire(ctx); err != nil {
		return err
	}
	defer e.release()

	fried := out
	if sticker {
		fried = strings.TrimSuffix(out, filepath.Ext(out)) + ".fried.png"
		defer os.Remove(fried)
	}
	args := []string{"-nostdin", "-y", "-v", "error", "-threads", "1", "-i", in,
		"-frames:v", "1", "-vf", fitFilter + "," + fryFilter}
	if !sticker {
		args = append(args, "-q:v", "28") // JPEG: 2 is best, 31 is worst
	}
	if _, err := run(ctx, nil, e.FFmpeg, append(args, fried)...); err != nil {
		return err
	}
	if !sticker {
		return nil
	}
	// ffmpeg here has no WebP encoder; ImageMagick does the sticker.
	_, err := run(ctx, e.magickEn, e.Magick, fried, "-resize", "512x512", out)
	return err
}

// Earrape applies the earrape effect to sound. The codec follows out's
// extension: .ogg gives Opus (voice messages), anything else AAC.
func (e *Engine) Earrape(ctx context.Context, in, out string) error {
	codec := []string{"-c:a", "aac", "-b:a", "128k"}
	if strings.EqualFold(filepath.Ext(out), ".ogg") {
		codec = []string{"-c:a", "libopus", "-b:a", "64k"}
	}
	args := append([]string{"-nostdin", "-y", "-v", "error", "-i", in, "-vn", "-map", "0:a:0", "-af", earrapeFilter}, codec...)
	_, err := run(ctx, nil, e.FFmpeg, append(args, out)...)
	return err
}
