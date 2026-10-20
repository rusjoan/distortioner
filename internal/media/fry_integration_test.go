//go:build integration

package media

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// meanVolume returns the mean volume of the first audio stream in dB.
func meanVolume(t *testing.T, path string) float64 {
	t.Helper()
	out, err := exec.Command("ffmpeg", "-hide_banner", "-nostats", "-i", path, "-vn", "-af", "volumedetect", "-f", "null", "-").CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	m := regexp.MustCompile(`mean_volume: (-?[0-9.]+) dB`).FindSubmatch(out)
	if m == nil {
		t.Fatalf("no volume in %s", out)
	}
	v, _ := strconv.ParseFloat(string(m[1]), 64)
	return v
}

// quietSpeechLike is a quiet, wobbling tone with sound and video.
func quietVideo(t *testing.T, e *Engine, size string, seconds int) string {
	return fixture(t, e, "in.mp4",
		"-f", "lavfi", "-i", "testsrc2=size="+size+":rate=30:duration="+strconv.Itoa(seconds),
		"-f", "lavfi", "-i", "sine=frequency=220:duration="+strconv.Itoa(seconds),
		"-af", "volume=-24dB", "-c:v", "libx264", "-c:a", "aac", "-shortest")
}

func TestFryVideo(t *testing.T) {
	e := engine(t)
	in := quietVideo(t, e, "1280x720", 3)
	out := filepath.Join(t.TempDir(), "out.mp4")
	if err := e.FryVideo(context.Background(), in, out, KindVideo, 60); err != nil {
		t.Fatal(err)
	}
	info, err := e.Probe(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	if !info.HasAudio || max(info.Width, info.Height) > 512 || info.Width%2 != 0 || info.Height%2 != 0 {
		t.Fatalf("%+v", info)
	}
	before, after := meanVolume(t, in), meanVolume(t, out)
	if after-before < 15 {
		t.Fatalf("earrape too quiet: %.1f dB → %.1f dB", before, after)
	}
}

func TestFryVideoNoteStaysSquare(t *testing.T) {
	e := engine(t)
	in := quietVideo(t, e, "384x384", 2)
	out := filepath.Join(t.TempDir(), "out.mp4")
	if err := e.FryVideo(context.Background(), in, out, KindVideoNote, 60); err != nil {
		t.Fatal(err)
	}
	info, err := e.Probe(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Width != info.Height || !info.HasAudio {
		t.Fatalf("%+v", info)
	}
}

func TestFryAnimationHasNoSound(t *testing.T) {
	e := engine(t)
	in := fixture(t, e, "in.gif", "-f", "lavfi", "-i", "testsrc=size=201x151:rate=10:duration=1")
	out := filepath.Join(t.TempDir(), "out.mp4")
	if err := e.FryVideo(context.Background(), in, out, KindAnimation, 60); err != nil {
		t.Fatal(err)
	}
	if info, _ := e.Probe(context.Background(), out); info.HasAudio || !info.HasVideo {
		t.Fatalf("%+v", info)
	}
}

func TestFryVideoSticker(t *testing.T) {
	e := engine(t)
	in := fixture(t, e, "in.webm",
		"-f", "lavfi", "-i", "testsrc=size=512x512:rate=30:duration=4,format=yuva420p",
		"-c:v", "libvpx-vp9", "-pix_fmt", "yuva420p", "-auto-alt-ref", "0")
	out := filepath.Join(t.TempDir(), "out.webm")
	if err := e.FryVideo(context.Background(), in, out, KindSticker, 0); err != nil {
		t.Fatal(err)
	}
	info, err := e.Probe(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(out)
	if max(info.Width, info.Height) != 512 || info.Duration > 3.1 || st.Size() > 256<<10 {
		t.Fatalf("%+v size %d", info, st.Size())
	}
}

func TestFryVideoTooLong(t *testing.T) {
	e := engine(t)
	in := fixture(t, e, "in.mp4", "-f", "lavfi", "-i", "testsrc=size=64x64:rate=5:duration=10")
	err := e.FryVideo(context.Background(), in, filepath.Join(t.TempDir(), "o.mp4"), KindVideo, 5)
	if _, ok := err.(*TooLongError); !ok {
		t.Fatalf("got %v", err)
	}
}

func TestFryImages(t *testing.T) {
	e := engine(t)
	in := fixture(t, e, "in.jpg", "-f", "lavfi", "-i", "testsrc=size=1024x768", "-frames:v", "1")
	photo := filepath.Join(t.TempDir(), "out.jpg")
	if err := e.FryImage(context.Background(), in, photo, false); err != nil {
		t.Fatal(err)
	}
	if info, _ := e.Probe(context.Background(), photo); max(info.Width, info.Height) > 512 {
		t.Fatalf("photo %+v", info)
	}
	sticker := filepath.Join(t.TempDir(), "out.webp")
	if err := e.FryImage(context.Background(), in, sticker, true); err != nil {
		t.Fatal(err)
	}
	info, err := e.Probe(context.Background(), sticker)
	if err != nil || max(info.Width, info.Height) != 512 {
		t.Fatalf("sticker %+v %v", info, err)
	}
	if _, err := os.Stat(sticker[:len(sticker)-len(".webp")] + ".fried.png"); !os.IsNotExist(err) {
		t.Fatal("temporary png left behind")
	}
}

func TestEarrapeVoice(t *testing.T) {
	e := engine(t)
	in := fixture(t, e, "in.ogg", "-f", "lavfi", "-i", "sine=frequency=220:duration=2", "-af", "volume=-24dB", "-c:a", "libopus")
	out := filepath.Join(t.TempDir(), "out.ogg")
	if err := e.Earrape(context.Background(), in, out); err != nil {
		t.Fatal(err)
	}
	if before, after := meanVolume(t, in), meanVolume(t, out); after-before < 15 {
		t.Fatalf("earrape too quiet: %.1f dB → %.1f dB", before, after)
	}
}
