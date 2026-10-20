//go:build integration

package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// These tests need ffmpeg and ImageMagick with liquid rescale; run them in
// the runtime image: make itest.

func engine(t *testing.T) *Engine {
	t.Helper()
	e := NewEngine(runtime.NumCPU(), "libx264")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := e.Check(ctx); err != nil {
		t.Fatal(err)
	}
	return e
}

// fixture renders a test file with ffmpeg's built-in sources.
func fixture(t *testing.T, e *Engine, name string, args ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	full := append([]string{"-nostdin", "-y", "-v", "error"}, args...)
	if _, err := run(context.Background(), nil, e.FFmpeg, append(full, path)...); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImage(t *testing.T) {
	e := engine(t)
	in := fixture(t, e, "in.jpg", "-f", "lavfi", "-i", "testsrc=size=1280x720", "-frames:v", "1")
	out := filepath.Join(t.TempDir(), "out.jpg")
	if err := e.DistortImage(context.Background(), in, out, 50, false); err != nil {
		t.Fatal(err)
	}
	info, err := e.Probe(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != 512 || info.Height != 288 {
		t.Fatalf("got %dx%d", info.Width, info.Height)
	}
}

func TestSticker(t *testing.T) {
	e := engine(t)
	in := fixture(t, e, "in.webp", "-f", "lavfi", "-i", "testsrc=size=300x200", "-frames:v", "1")
	out := filepath.Join(t.TempDir(), "out.webp")
	if err := e.DistortImage(context.Background(), in, out, 70, true); err != nil {
		t.Fatal(err)
	}
	info, err := e.Probe(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	if max(info.Width, info.Height) != 512 {
		t.Fatalf("sticker must have a 512 px side, got %dx%d", info.Width, info.Height)
	}
}

func TestVoice(t *testing.T) {
	e := engine(t)
	in := fixture(t, e, "in.ogg", "-f", "lavfi", "-i", "sine=frequency=440:duration=2", "-c:a", "libopus")
	out := filepath.Join(t.TempDir(), "out.ogg")
	if err := e.DistortAudio(context.Background(), in, out, 50); err != nil {
		t.Fatal(err)
	}
	info, err := e.Probe(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	if !info.HasAudio || info.Duration < 1.5 {
		t.Fatalf("%+v", info)
	}
}

func TestVideoWithSound(t *testing.T) {
	e := engine(t)
	in := fixture(t, e, "in.mp4",
		"-f", "lavfi", "-i", "testsrc=size=640x360:rate=60:duration=2",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "libx264", "-c:a", "aac", "-shortest")
	out := filepath.Join(t.TempDir(), "out.mp4")
	var distorted int
	err := e.DistortVideo(context.Background(), in, out, VideoOptions{
		Kind: KindVideo, Strength: 50, MaxSeconds: 60, DistortAudio: true, WorkDir: t.TempDir(),
		Progress: func(s Stage, done, total int) {
			if s == StageDistorting {
				distorted = max(distorted, done)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	info, err := e.Probe(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	if !info.HasVideo || !info.HasAudio || info.Width != 512 || info.Height != 288 {
		t.Fatalf("%+v", info)
	}
	if info.FPS > 30.01 || distorted < 55 || distorted > 62 {
		t.Fatalf("fps %v, distorted %d frames; want 30 fps × 2 s", info.FPS, distorted)
	}
}

func TestAnimationHasNoSound(t *testing.T) {
	e := engine(t)
	in := fixture(t, e, "in.gif", "-f", "lavfi", "-i", "testsrc=size=201x151:rate=10:duration=1")
	out := filepath.Join(t.TempDir(), "out.mp4")
	if err := e.DistortVideo(context.Background(), in, out, VideoOptions{
		Kind: KindAnimation, Strength: 50, MaxSeconds: 60, WorkDir: t.TempDir(),
	}); err != nil {
		t.Fatal(err)
	}
	info, err := e.Probe(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	if info.HasAudio || info.Width%2 != 0 || info.Height%2 != 0 {
		t.Fatalf("%+v", info)
	}
}

func TestVideoSticker(t *testing.T) {
	e := engine(t)
	in := fixture(t, e, "in.webm",
		"-f", "lavfi", "-i", "testsrc=size=512x512:rate=30:duration=5,format=yuva420p",
		"-c:v", "libvpx-vp9", "-pix_fmt", "yuva420p", "-auto-alt-ref", "0")
	out := filepath.Join(t.TempDir(), "out.webm")
	if err := e.DistortVideo(context.Background(), in, out, VideoOptions{
		Kind: KindSticker, Strength: 50, WorkDir: t.TempDir(),
	}); err != nil {
		t.Fatal(err)
	}
	info, err := e.Probe(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(out)
	if info.Width != 512 || info.Duration > 3.1 || st.Size() > 256<<10 {
		t.Fatalf("%+v size %d", info, st.Size())
	}
}

func TestTooLong(t *testing.T) {
	e := engine(t)
	in := fixture(t, e, "in.mp4", "-f", "lavfi", "-i", "testsrc=size=64x64:rate=5:duration=10")
	err := e.DistortVideo(context.Background(), in, filepath.Join(t.TempDir(), "out.mp4"),
		VideoOptions{Kind: KindVideo, Strength: 50, MaxSeconds: 5, WorkDir: t.TempDir()})
	var tooLong *TooLongError
	if !errors.As(err, &tooLong) || tooLong.Limit != 5 {
		t.Fatalf("got %v", err)
	}
}

func TestCancelKillsProcesses(t *testing.T) {
	e := engine(t)
	in := fixture(t, e, "in.mp4", "-f", "lavfi", "-i", "testsrc=size=640x360:rate=30:duration=20")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	err := e.DistortVideo(ctx, in, filepath.Join(t.TempDir(), "out.mp4"),
		VideoOptions{Kind: KindVideo, Strength: 50, MaxSeconds: 60, WorkDir: t.TempDir()})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("cancellation took %v", time.Since(start))
	}
	if e.CPUInUse() != 0 {
		t.Fatalf("%d cpu slots leaked", e.CPUInUse())
	}
}

// BenchmarkDistortVideo measures a typical phone clip: 10 s, 720p, 60 fps, with sound.
// Run with: make itest-bench
func BenchmarkDistortVideo(b *testing.B) {
	e := NewEngine(runtime.NumCPU(), "libx264")
	dir := b.TempDir()
	in := filepath.Join(dir, "in.mp4")
	if _, err := run(context.Background(), nil, e.FFmpeg, "-nostdin", "-y", "-v", "error",
		"-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=60:duration=10",
		"-f", "lavfi", "-i", "sine=duration=10", "-c:v", "libx264", "-c:a", "aac", "-shortest", in); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := range b.N {
		work := filepath.Join(dir, "work", strconv.Itoa(i))
		if err := os.MkdirAll(work, 0o700); err != nil {
			b.Fatal(err)
		}
		out := filepath.Join(dir, strconv.Itoa(i)+".mp4")
		if err := e.DistortVideo(context.Background(), in, out, VideoOptions{
			Kind: KindVideo, Strength: 50, MaxSeconds: 60, DistortAudio: true, WorkDir: work,
		}); err != nil {
			b.Fatal(err)
		}
	}
}
