package media

import "math/rand/v2"

// maxReverseSeconds: reverse buffers the whole stream in memory.
const maxReverseSeconds = 20

// Random picks a recipe for /distort with three independent dice, each
// choosing one option with equal odds:
//   - picture: any effect that changes the picture, wobble and fry included;
//   - sound: nothing or a sound effect; skipped after wobble and fry, which
//     bring their own sound. Without a picture (voice, audio) wobble and fry
//     are sound options and "nothing" is not;
//   - time: nothing, reverse or a tempo change.
//
// wobble and fry use the given strengths. duration is the input length in
// seconds (0 if unknown).
func Random(rnd *rand.Rand, kind MediaKind, duration float64, wobble, fry Strength) Recipe {
	var picture, sound, timing []string
	for _, e := range Catalog {
		if e.NoRandom || !e.AppliesTo(kind) {
			continue
		}
		switch {
		case e.Group == GroupTime:
			if e.ID != "reverse" || duration <= maxReverseSeconds {
				timing = append(timing, e.ID)
			}
		case kind.sound():
			sound = append(sound, e.ID) // wobble, fry and the sound effects
		case e.Visual():
			picture = append(picture, e.ID)
		case e.Group == GroupSound:
			sound = append(sound, e.ID)
		}
	}
	if !kind.sound() && len(sound) > 0 {
		sound = append(sound, "") // a picture is already changed: sound may stay
	}
	if len(timing) > 0 {
		timing = append(timing, "")
	}

	var recipe Recipe
	add := func(id string) {
		if id == "" {
			return
		}
		s := Step{ID: id, Strength: 50}
		switch id {
		case "wobble":
			s.Strength = wobble
		case "fry":
			s.Strength = fry
		}
		recipe = append(recipe, s)
	}
	roll := func(options []string) string {
		if len(options) == 0 {
			return ""
		}
		return options[rnd.IntN(len(options))]
	}

	look := roll(picture)
	add(look)
	if look != "wobble" && look != "fry" {
		add(roll(sound))
	}
	add(roll(timing))
	return recipe
}
