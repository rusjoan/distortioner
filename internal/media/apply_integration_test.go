//go:build integration

package media

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"slices"
	"sync"
	"testing"
)

// Every catalog effect on a video with sound: it must produce a playable
// video of the usual size, keep the sound, and change the length only when
// it changes the tempo.
func TestApplyEveryEffectOnVideo(t *testing.T) {
	e := engine(t)
	in := quietVideo(t, e, "640x360", 2)
	for _, fx := range Catalog {
		if !fx.AppliesTo(MediaVideo) {
			continue
		}
		t.Run(fx.ID, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out.mp4")
			err := e.Apply(context.Background(), in, out, Recipe{{ID: fx.ID, Strength: 50}}, ApplyOptions{
				Kind: MediaVideo, MaxSeconds: 60, DistortAudio: true, WorkDir: t.TempDir(),
			})
			if err != nil {
				t.Fatal(err)
			}
			info, err := e.Probe(context.Background(), out)
			if err != nil {
				t.Fatal(err)
			}
			if !info.HasVideo || !info.HasAudio || max(info.Width, info.Height) > 512 {
				t.Fatalf("%+v", info)
			}
			want := 2.0
			switch fx.ID {
			case "fast":
				want = 1
			case "slow":
				want = 4
			}
			if math.Abs(info.Duration-want) > 0.35 {
				t.Fatalf("duration %.2f, want %.2f", info.Duration, want)
			}
		})
	}
}

func TestApplyStillEffects(t *testing.T) {
	e := engine(t)
	in := fixture(t, e, "in.jpg", "-f", "lavfi", "-i", "testsrc=size=800x600", "-frames:v", "1")
	for _, fx := range Catalog {
		if !fx.AppliesTo(MediaPhoto) {
			continue
		}
		t.Run(fx.ID, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out.jpg")
			if err := e.Apply(context.Background(), in, out, Recipe{{ID: fx.ID, Strength: 50}}, ApplyOptions{Kind: MediaPhoto}); err != nil {
				t.Fatal(err)
			}
			if info, err := e.Probe(context.Background(), out); err != nil || max(info.Width, info.Height) > 1024 {
				t.Fatalf("%+v %v", info, err)
			}
		})
	}
}

func TestApplyMixOnSticker(t *testing.T) {
	e := engine(t)
	in := fixture(t, e, "in.webp", "-f", "lavfi", "-i", "testsrc=size=300x200", "-frames:v", "1")
	out := filepath.Join(t.TempDir(), "out.webp")
	if err := e.Apply(context.Background(), in, out, Recipe{{ID: "bulge"}, {ID: "rainbow"}, {ID: "pixel"}},
		ApplyOptions{Kind: MediaSticker, WorkDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if info, err := e.Probe(context.Background(), out); err != nil || max(info.Width, info.Height) != 512 {
		t.Fatalf("%+v %v", info, err)
	}
}

func TestApplySoundEffectsOnVoice(t *testing.T) {
	e := engine(t)
	in := fixture(t, e, "in.ogg", "-f", "lavfi", "-i", "sine=frequency=220:duration=2", "-c:a", "libopus")
	for _, fx := range Catalog {
		if !fx.AppliesTo(MediaVoice) {
			continue
		}
		t.Run(fx.ID, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out.ogg")
			if err := e.Apply(context.Background(), in, out, Recipe{{ID: fx.ID, Strength: 50}}, ApplyOptions{Kind: MediaVoice}); err != nil {
				t.Fatal(err)
			}
			if info, err := e.Probe(context.Background(), out); err != nil || !info.HasAudio || info.Duration < 0.9 {
				t.Fatalf("%+v %v", info, err)
			}
		})
	}
}

func TestApplyMixWithFramesTimeAndSound(t *testing.T) {
	e := engine(t)
	in := quietVideo(t, e, "640x360", 2)
	out := filepath.Join(t.TempDir(), "out.mp4")
	recipe := Recipe{{ID: "earrape"}, {ID: "swirl"}, {ID: "reverse"}, {ID: "mirror"}}
	if err := e.Apply(context.Background(), in, out, recipe, ApplyOptions{Kind: MediaVideo, MaxSeconds: 60, WorkDir: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	info, err := e.Probe(context.Background(), out)
	if err != nil || !info.HasAudio || info.Width != 512 {
		t.Fatalf("%+v %v", info, err)
	}
	if v := meanVolume(t, out); v < -15 {
		t.Fatalf("earrape missing: %.1f dB", v)
	}
}

// A stronger fry drives the clipper harder: the sound gets more squashed,
// its peaks closer to its average.
func TestFryStrengthSquashesMore(t *testing.T) {
	e := engine(t)
	in := quietVideo(t, e, "320x240", 2)
	crest := map[Strength]float64{}
	for _, s := range []Strength{20, 80} {
		out := filepath.Join(t.TempDir(), "out.mp4")
		if err := e.Apply(context.Background(), in, out, Recipe{{ID: "fry", Strength: s}}, ApplyOptions{Kind: MediaVideo, MaxSeconds: 60}); err != nil {
			t.Fatal(err)
		}
		crest[s] = maxVolume(t, out) - meanVolume(t, out)
	}
	if crest[20]-crest[80] < 1 {
		t.Fatalf("peak-to-mean at fry 20: %.1f dB, at fry 80: %.1f dB", crest[20], crest[80])
	}
}

func TestNothingApplies(t *testing.T) {
	e := engine(t)
	in := fixture(t, e, "in.jpg", "-f", "lavfi", "-i", "testsrc=size=64x64", "-frames:v", "1")
	err := e.Apply(context.Background(), in, filepath.Join(t.TempDir(), "o.jpg"), Recipe{{ID: "earrape"}}, ApplyOptions{Kind: MediaPhoto})
	if !errors.Is(err, ErrNothingToApply) {
		t.Fatalf("got %v", err)
	}
}

// Progress grows and reaches 100% on every path: one pass, frames, sound.
func TestApplyReportsProgress(t *testing.T) {
	e := engine(t)
	video := quietVideo(t, e, "320x240", 3)
	voice := fixture(t, e, "in.ogg", "-f", "lavfi", "-i", "sine=duration=3", "-c:a", "libopus")
	cases := []struct {
		name   string
		in     string
		kind   MediaKind
		recipe Recipe
		stage  Stage
	}{
		{"one pass", video, MediaVideo, Recipe{{ID: "fry", Strength: 50}}, StageProcessing},
		{"slow one pass", video, MediaVideo, Recipe{{ID: "slow"}}, StageProcessing},
		{"frames", video, MediaVideo, Recipe{{ID: "bulge"}}, StageEncoding},
		{"sound", voice, MediaVoice, Recipe{{ID: "echo"}}, StageProcessing},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var mu sync.Mutex
			var seen []int
			out := filepath.Join(t.TempDir(), "out.mp4")
			if c.kind == MediaVoice {
				out = filepath.Join(t.TempDir(), "out.ogg")
			}
			err := e.Apply(context.Background(), c.in, out, c.recipe, ApplyOptions{
				Kind: c.kind, MaxSeconds: 60, WorkDir: t.TempDir(),
				Progress: func(s Stage, done, total int) {
					if s == c.stage && total == 1000 {
						mu.Lock()
						seen = append(seen, done)
						mu.Unlock()
					}
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(seen) < 2 || seen[len(seen)-1] != 1000 || !slices.IsSorted(seen) {
				t.Fatalf("progress %v", seen)
			}
		})
	}
}

func TestDemotivator(t *testing.T) {
	e := engine(t)
	in := fixture(t, e, "in.jpg", "-f", "lavfi", "-i", "testsrc=size=640x480", "-frames:v", "1")
	for name, c := range map[string]struct {
		recipe Recipe
		poster Demotivator
	}{
		"cyrillic":       {Recipe{{ID: "demotivator"}}, Demotivator{Title: "МОЙ КОТ", Subtitle: "он спит, и это 100% правда"}},
		"tricky text":    {Recipe{{ID: "demotivator"}}, Demotivator{Title: "@/etc/passwd", Subtitle: `%[w] \n`}},
		"fried poster":   {Recipe{{ID: "fry", Strength: 50}, {ID: "demotivator"}}, Demotivator{Title: "ЖИЗНЬ"}},
		"with an effect": {Recipe{{ID: "bulge"}, {ID: "demotivator"}}, Demotivator{Title: "WHY?", Subtitle: "just why"}},
	} {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out.jpg")
			if err := e.Apply(context.Background(), in, out, c.recipe, ApplyOptions{Kind: MediaPhoto, Demotivator: c.poster, WorkDir: t.TempDir()}); err != nil {
				t.Fatal(err)
			}
			info, err := e.Probe(context.Background(), out)
			if err != nil {
				t.Fatal(err)
			}
			// 512 px picture + frame + padding, and a text block below.
			if info.Width <= 512 || info.Height <= info.Width*288/512 {
				t.Fatalf("%dx%d", info.Width, info.Height)
			}
			corner, err := run(context.Background(), e.magickEn, e.Magick, out, "-format", "%[fx:int(255*mean)]", "-crop", "4x4+0+0", "info:")
			if err != nil || string(corner) > "40" && len(corner) >= 2 {
				t.Fatalf("poster corner is not black: %s %v", corner, err)
			}
		})
	}
}

func TestDemotivatorNotForVideos(t *testing.T) {
	if (Effect{}).AppliesTo(MediaVideo) {
		t.Skip()
	}
	e, _ := Lookup("demotivator")
	if e.AppliesTo(MediaVideo) || e.AppliesTo(MediaSticker) || !e.AppliesTo(MediaPhoto) {
		t.Fatal("demotivator is for photos only")
	}
}
