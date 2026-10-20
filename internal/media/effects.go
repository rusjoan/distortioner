package media

import (
	"fmt"
	"slices"
	"strings"
)

// MediaKind is what the input is, which decides which effects apply and
// how the result is encoded.
type MediaKind int

const (
	MediaPhoto        MediaKind = iota // still picture, JPEG out
	MediaSticker                       // still sticker, WebP 512 px out
	MediaVideo                         // mp4 with sound
	MediaAnimation                     // mp4 without sound (Telegram "GIF")
	MediaVideoNote                     // square mp4 with sound
	MediaVideoSticker                  // VP9 WebM with alpha, ≤3 s, no sound
	MediaVoice                         // Opus voice message
	MediaAudio                         // audio file, AAC out
)

func (k MediaKind) still() bool { return k == MediaPhoto || k == MediaSticker }
func (k MediaKind) sound() bool { return k == MediaVoice || k == MediaAudio }

// audible reports whether the output carries sound.
func (k MediaKind) audible() bool { return k == MediaVideo || k == MediaVideoNote || k.sound() }

// Group orders effects: a recipe is always applied in this order, so the
// result does not depend on the order effects were picked in.
type Group int

const (
	GroupWhole    Group = iota // wobble, fry: complete looks on their own
	GroupFrames                // per-frame ImageMagick operators
	GroupTime                  // reverse and tempo
	GroupGeometry              // mirror, shake
	GroupColor                 // pixels, rainbow
	GroupTemporal              // trails, drunk
	GroupLayout                // demotivator: frames the finished picture
	GroupSound                 // audio-only effects
)

// Step is one effect of a recipe. Strength matters for wobble and fry.
type Step struct {
	ID       string
	Strength Strength
}

// Recipe is the list of effects applied together.
type Recipe []Step

// IDs returns the effect ids in application order.
func (r Recipe) IDs() []string {
	ids := make([]string, len(r))
	for i, s := range r.sorted() {
		ids[i] = s.ID
	}
	return ids
}

// Emoji returns the effects as emoji, e.g. "🫧 + 🌈".
func (r Recipe) Emoji() string {
	parts := make([]string, 0, len(r))
	for _, s := range r.sorted() {
		parts = append(parts, catalog[s.ID].Emoji)
	}
	return strings.Join(parts, " + ")
}

// PerFrame reports whether the recipe processes video frame by frame,
// which is the expensive path.
func (r Recipe) PerFrame() bool {
	return slices.ContainsFunc(r, func(s Step) bool { return catalog[s.ID].frame != nil })
}

// Tempo is how the recipe changes the length: 0.5 when the result is twice
// as fast, 2 when twice as slow.
func (r Recipe) Tempo() float64 {
	f := 1.0
	for _, s := range r {
		switch s.ID {
		case "fast":
			f *= 0.5
		case "slow":
			f *= 2
		}
	}
	return f
}

// Has reports whether the recipe contains the effect.
func (r Recipe) Has(id string) bool {
	return slices.ContainsFunc(r, func(s Step) bool { return s.ID == id })
}

func (r Recipe) sorted() Recipe {
	out := slices.Clone(r)
	slices.SortStableFunc(out, func(a, b Step) int {
		if ga, gb := catalog[a.ID].Group, catalog[b.ID].Group; ga != gb {
			return int(ga) - int(gb)
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

// Effect describes one entry of the catalog. Filter builders take the step
// strength and what is being processed.
type Effect struct {
	ID    string
	Emoji string
	Group Group
	// Speed effects change the tempo; a recipe holds at most one.
	Speed bool
	// Motion effects only make sense on moving pictures.
	Motion bool
	// NoRandom effects need input (a caption) and are only picked by hand.
	NoRandom bool

	photosOnly bool // stills except stickers

	stills, moving, sound bool // where the effect applies

	frame func(s Strength) []string           // ImageMagick operators per frame
	video func(s Strength, still bool) string // -vf fragment
	audio func(s Strength, rate int) string   // -af fragment
}

// AppliesTo reports whether the effect does anything to this kind of media.
func (e Effect) AppliesTo(k MediaKind) bool {
	if e.photosOnly {
		return k == MediaPhoto
	}
	switch {
	case k.still():
		return e.stills
	case k.sound():
		return e.sound
	case k.audible():
		return e.moving || e.sound
	default: // silent moving pictures
		return e.moving
	}
}

// Visual reports whether the effect changes the picture.
func (e Effect) Visual() bool { return e.frame != nil || e.video != nil || e.Group == GroupLayout }

func fixed(f string) func(Strength, bool) string { return func(Strength, bool) string { return f } }

func fixedAudio(f string) func(Strength, int) string { return func(Strength, int) string { return f } }

func ops(o ...string) func(Strength) []string { return func(Strength) []string { return o } }

// Catalog lists the effects in display order.
var Catalog = []Effect{
	{ID: "wobble", Emoji: "🌀", Group: GroupWhole, stills: true, moving: true, sound: true,
		frame: wobbleOps, audio: func(s Strength, _ int) string { return vibrato(s) }},
	{ID: "fry", Emoji: "🔥", Group: GroupWhole, stills: true, moving: true, sound: true,
		video: func(s Strength, still bool) string { return FryDefaults(s).Video(still) },
		audio: func(s Strength, _ int) string { return FryDefaults(s).Audio() }},

	{ID: "bulge", Emoji: "🫧", Group: GroupFrames, stills: true, moving: true, frame: ops("-implode", "-1.1")},
	{ID: "swirl", Emoji: "🌪", Group: GroupFrames, stills: true, moving: true, frame: ops("-swirl", "110")},
	{ID: "pixel", Emoji: "🟪", Group: GroupColor, stills: true, moving: true, video: fixed("pixelize=width=14:height=14")},
	{ID: "rainbow", Emoji: "🌈", Group: GroupColor, stills: true, moving: true,
		video: func(_ Strength, still bool) string {
			if still {
				return "hue=h=150:s=2.2" // no time on a still: a fixed shift
			}
			return "hue=h=t*360:s=2.2"
		}},
	{ID: "mirror", Emoji: "🪞", Group: GroupGeometry, stills: true, moving: true,
		video: fixed("crop=iw/2:ih:0:0,split[l][r];[r]hflip[rr];[l][rr]hstack")},
	{ID: "trails", Emoji: "👻", Group: GroupTemporal, moving: true, Motion: true, video: fixed("lagfun=decay=0.96")},
	{ID: "drunk", Emoji: "🥴", Group: GroupTemporal, moving: true, Motion: true,
		video: fixed("tmix=frames=10,rotate=0.06*sin(2*PI*t*0.7):fillcolor=black")},
	{ID: "shake", Emoji: "📳", Group: GroupGeometry, moving: true, Motion: true,
		video: func(Strength, bool) string { return shake(50) }},

	{ID: "demotivator", Emoji: "🖼", Group: GroupLayout, stills: true, photosOnly: true, NoRandom: true},

	{ID: "earrape", Emoji: "🔊", Group: GroupSound, sound: true, audio: func(Strength, int) string { return earrape(50) }},
	{ID: "chipmunk", Emoji: "🐿", Group: GroupSound, sound: true, audio: pitch(1.6)},
	{ID: "demon", Emoji: "😈", Group: GroupSound, sound: true, audio: pitch(0.7)},
	{ID: "echo", Emoji: "🗣", Group: GroupSound, sound: true, audio: fixedAudio("aecho=0.8:0.88:450|900:0.5|0.3")},

	{ID: "reverse", Emoji: "⏪", Group: GroupTime, moving: true, sound: true,
		video: fixed("reverse"), audio: fixedAudio("areverse")},
	{ID: "fast", Emoji: "⏩", Group: GroupTime, Speed: true, moving: true, sound: true,
		video: fixed("setpts=PTS/2"), audio: fixedAudio("atempo=2")},
	{ID: "slow", Emoji: "🐌", Group: GroupTime, Speed: true, moving: true, sound: true,
		video: fixed("setpts=PTS*2"), audio: fixedAudio("atempo=0.5")},
}

var catalog = func() map[string]Effect {
	m := make(map[string]Effect, len(Catalog))
	for _, e := range Catalog {
		m[e.ID] = e
	}
	return m
}()

// Lookup returns a catalog entry.
func Lookup(id string) (Effect, bool) {
	e, ok := catalog[id]
	return e, ok
}

// pitch shifts the voice up or down and keeps the length: the tape trick
// (play faster, which raises the pitch) followed by a tempo correction.
func pitch(factor float64) func(Strength, int) string {
	return func(_ Strength, rate int) string {
		if rate <= 0 {
			rate = 48000
		}
		return fmt.Sprintf("asetrate=%d,aresample=%d,atempo=%.4f", int(float64(rate)*factor), rate, 1/factor)
	}
}

// wobbleOps is the seam carving of the original bot.
func wobbleOps(s Strength) []string {
	keep := s.keep()
	return []string{
		"-liquid-rescale", fmt.Sprintf("%d%%", keep),
		"-resize", fmt.Sprintf("%.4f%%", 100*100/float64(keep)),
	}
}

// shake and earrape are the parts of fry that are also effects of their own.
func shake(s Strength) string { return shakeFilter(FryDefaults(s).Shake) }

func earrape(s Strength) string { return FryDefaults(s).Audio() }
