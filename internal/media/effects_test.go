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

// Each die picks every option, "nothing" included, with equal odds.
func TestRandomDice(t *testing.T) {
	const n = 30000
	pictureOf := map[string]bool{}
	for _, tc := range []struct {
		kind                   MediaKind
		duration               float64
		picture, sound, timing []string
	}{
		{MediaVideo, 10, []string{"wobble", "fry", "bulge", "swirl", "pixel", "rainbow", "mirror", "trails", "drunk", "shake"},
			[]string{"", "earrape", "chipmunk", "demon", "echo"}, []string{"", "reverse", "fast", "slow"}},
		{MediaVideo, 60, nil, nil, []string{"", "fast", "slow"}}, // no reverse for long videos
		{MediaPhoto, 0, []string{"wobble", "fry", "bulge", "swirl", "pixel", "rainbow", "mirror"}, nil, nil},
		{MediaAnimation, 5, []string{"wobble", "fry", "bulge", "swirl", "pixel", "rainbow", "mirror", "trails", "drunk", "shake"},
			nil, []string{"", "reverse", "fast", "slow"}},
		{MediaVoice, 5, nil, []string{"wobble", "fry", "earrape", "chipmunk", "demon", "echo"}, []string{"", "reverse", "fast", "slow"}},
	} {
		rnd := rand.New(rand.NewPCG(1, uint64(tc.kind)))
		picture, sound, timing := map[string]int{}, map[string]int{}, map[string]int{}
		soundRolls := 0
		for range n {
			r := Random(rnd, tc.kind, tc.duration, 50, 50)
			look, heard, when := "", "", ""
			for _, s := range r {
				e := catalog[s.ID]
				switch {
				case e.Group == GroupTime:
					when = s.ID
				case tc.kind.sound() || e.Group == GroupSound:
					heard = s.ID
				default:
					look = s.ID
				}
			}
			if !tc.kind.sound() && look == "" {
				t.Fatalf("kind %d: nothing for the picture in %v", tc.kind, r.IDs())
			}
			picture[look]++
			timing[when]++
			if look == "wobble" || look == "fry" {
				if heard != "" {
					t.Fatalf("%s got a second sound: %v", look, r.IDs())
				}
				continue
			}
			soundRolls++
			sound[heard]++
		}
		check := func(die string, got map[string]int, want []string, rolls int) {
			if want == nil {
				return
			}
			if len(got) != len(want) {
				t.Errorf("kind %d %s: got %v, want %v", tc.kind, die, got, want)
			}
			for _, id := range want {
				share, expect := float64(got[id])/float64(rolls), 1/float64(len(want))
				if share < expect-0.015 || share > expect+0.015 {
					t.Errorf("kind %d %s %q: share %.3f, want %.3f", tc.kind, die, id, share, expect)
				}
			}
		}
		check("picture", picture, tc.picture, n)
		check("sound", sound, tc.sound, soundRolls)
		check("time", timing, tc.timing, n)
		for id := range picture {
			pictureOf[id] = true
		}
	}
	if pictureOf["demotivator"] {
		t.Error("demotivator picked at random")
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
