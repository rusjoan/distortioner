package media

import (
	"math/rand/v2"
	"strings"
	"testing"
)

func TestRecipeOrderIsCanonical(t *testing.T) {
	a := Recipe{{ID: "earrape"}, {ID: "pixel"}, {ID: "bulge"}, {ID: "reverse"}}
	b := Recipe{{ID: "reverse"}, {ID: "bulge"}, {ID: "earrape"}, {ID: "pixel"}}
	if strings.Join(a.IDs(), ",") != "bulge,reverse,pixel,earrape" || strings.Join(b.IDs(), ",") != strings.Join(a.IDs(), ",") {
		t.Fatalf("%v %v", a.IDs(), b.IDs())
	}
	if a.Emoji() != "🫧 + ⏪ + 🟪 + 🔊" {
		t.Fatalf("%q", a.Emoji())
	}
}

func TestApplicability(t *testing.T) {
	cases := map[MediaKind][]string{
		MediaPhoto:        {"wobble", "fry", "bulge", "swirl", "pixel", "rainbow", "mirror", "demotivator"},
		MediaSticker:      {"wobble", "fry", "bulge", "swirl", "pixel", "rainbow", "mirror"},
		MediaVoice:        {"wobble", "fry", "earrape", "chipmunk", "demon", "echo", "reverse", "fast", "slow"},
		MediaAnimation:    {"wobble", "fry", "bulge", "swirl", "pixel", "rainbow", "mirror", "trails", "drunk", "shake", "reverse", "fast", "slow"},
		MediaVideo:        nil, // everything
		MediaVideoSticker: {"wobble", "fry", "bulge", "swirl", "pixel", "rainbow", "mirror", "trails", "drunk", "shake", "reverse", "fast", "slow"},
	}
	for kind, want := range cases {
		var got []string
		for _, e := range Catalog {
			if e.AppliesTo(kind) {
				got = append(got, e.ID)
			}
		}
		if want == nil { // everything but the photo-only demotivator
			for _, e := range Catalog {
				if e.ID != "demotivator" {
					want = append(want, e.ID)
				}
			}
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("kind %d: got %v, want %v", kind, got, want)
		}
	}
}

func TestFryStrengthScales(t *testing.T) {
	if shake(50) != "crop=w=iw*0.820:h=ih*0.820:x=(iw-ow)/2+(random(1)-0.5)*iw*0.160:y=(ih-oh)/2+(random(2)-0.5)*ih*0.160" {
		t.Fatalf("shake(50) changed: %s", shake(50))
	}
	if !strings.Contains(shake(80), "iw*0.256") || !strings.Contains(shake(20), "iw*0.064") {
		t.Fatalf("%s / %s", shake(20), shake(80))
	}
	for s, want := range map[Strength]string{
		50: "volume=10.0dB,asoftclip=type=hard:threshold=0.200",
		80: "volume=19.0dB,asoftclip=type=hard:threshold=0.200",
		20: "volume=1.0dB,asoftclip=type=hard:threshold=0.200",
	} {
		if !strings.Contains(earrape(s), want) {
			t.Errorf("earrape(%d) = %s", s, earrape(s))
		}
	}
}

func TestPitchKeepsLength(t *testing.T) {
	if got := pitch(1.6)(50, 44100); got != "asetrate=70560,aresample=44100,atempo=0.6250" {
		t.Fatal(got)
	}
	for _, id := range []string{"chipmunk", "demon"} {
		e := catalog[id]
		if e.Speed || e.video != nil || e.AppliesTo(MediaAnimation) || !e.AppliesTo(MediaVideo) || !e.AppliesTo(MediaVoice) {
			t.Errorf("%s must be sound only", id)
		}
	}
}

func TestRandom(t *testing.T) {
	rnd := rand.New(rand.NewPCG(1, 2))
	const n = 10000
	counts := map[string]int{}
	whole := map[string]int{}
	for range n {
		r := Random(rnd, MediaVideo, 10, 50, 50)
		if len(r) == 0 {
			t.Fatal("empty recipe")
		}
		if len(r) == 1 && (r[0].ID == "wobble" || r[0].ID == "fry") {
			whole[r[0].ID]++
			continue
		}
		if len(r) > 3 {
			t.Fatalf("too many effects: %v", r.IDs())
		}
		speed, frame, visual := 0, 0, false
		for _, s := range r {
			e := catalog[s.ID]
			if e.Group == GroupWhole {
				t.Fatalf("whole look in a mix: %v", r.IDs())
			}
			if e.Speed {
				speed++
			}
			if e.frame != nil {
				frame++
			}
			visual = visual || e.Visual()
			counts[s.ID]++
		}
		if speed > 1 || frame > 1 || !visual {
			t.Fatalf("bad mix %v", r.IDs())
		}
	}
	for id, share := range map[string]float64{"wobble": randomWobble, "fry": randomFry} {
		if got := float64(whole[id]) / n; got < share-0.02 || got > share+0.02 {
			t.Errorf("%s share %.3f, want ≈%.2f", id, got, share)
		}
	}
	for _, e := range Catalog {
		switch {
		case e.NoRandom && counts[e.ID] > 0:
			t.Errorf("%s picked at random", e.ID)
		case !e.NoRandom && e.Group != GroupWhole && e.AppliesTo(MediaVideo) && counts[e.ID] == 0:
			t.Errorf("%s never picked", e.ID)
		}
	}
}

func TestRandomRespectsMedia(t *testing.T) {
	rnd := rand.New(rand.NewPCG(3, 4))
	for range 2000 {
		for _, kind := range []MediaKind{MediaPhoto, MediaVoice, MediaAnimation} {
			for _, s := range Random(rnd, kind, 30, 50, 50) {
				if !catalog[s.ID].AppliesTo(kind) {
					t.Fatalf("%s on kind %d", s.ID, kind)
				}
				if s.ID == "reverse" {
					t.Fatal("reverse picked for a 30 s input")
				}
			}
		}
	}
}

// The exact filters of the bot's fry at strength 50.
func TestFryDefaults(t *testing.T) {
	p := FryDefaults(50)
	wantVideo := "eq=saturation=3.2:contrast=1.7:brightness=0.06:gamma_r=1.25,unsharp=7:7:2.5,noise=alls=25:allf=t," +
		"scale=iw/1.5:-2,scale=iw*1.5:-2:flags=neighbor," + shake(50)
	if got := p.Video(false); got != wantVideo {
		t.Errorf("video\n got %s\nwant %s", got, wantVideo)
	}
	wantAudio := "aformat=channel_layouts=mono,bass=g=48:f=40:w=0.8,treble=g=10," +
		"loudnorm=I=-10:TP=-1:LRA=7,aresample=48000,volume=10.0dB,asoftclip=type=hard:threshold=0.200," +
		"acrusher=bits=14:mode=lin:aa=0,volume=1dB,asoftclip=type=hard,volume=-0.5dB"
	if got := p.Audio(); got != wantAudio {
		t.Errorf("audio\n got %s\nwant %s", got, wantAudio)
	}
	if strings.Contains(p.Video(true), "crop") {
		t.Error("stills must not shake")
	}
}

func TestFryZeroKnobsDoNothing(t *testing.T) {
	zero := FryParams{BassFreq: 80}
	if zero.Video(false) != "null" || zero.Audio() != "anull" {
		t.Fatalf("%q %q", zero.Video(false), zero.Audio())
	}
	// Each knob alone turns on only its own part.
	if got := (FryParams{Crush: 12}).Audio(); got != "aformat=channel_layouts=mono,acrusher=bits=4:mode=lin:aa=0" {
		t.Fatal(got)
	}
	if got := (FryParams{Shrink: 2}).Video(true); got != "scale=iw/3:-2,scale=iw*3:-2:flags=neighbor" {
		t.Fatal(got)
	}
}

func TestIMTextEscaping(t *testing.T) {
	for in, want := range map[string]string{
		"@/etc/passwd": `\@/etc/passwd`,
		"100% жизнь":   "100%% жизнь",
		`a\b`:          `a\\b`,
	} {
		if got := imText(in); got != want {
			t.Errorf("imText(%q) = %q, want %q", in, got, want)
		}
	}
}
