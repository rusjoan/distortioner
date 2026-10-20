package media

import "math/rand/v2"

// Chances of a random pick: wobble or fry on their own, otherwise a mix.
const (
	randomWobble = 0.35
	randomFry    = 0.35
	// maxReverseSeconds: reverse buffers the whole stream in memory.
	maxReverseSeconds = 20
)

// Random picks a recipe for /distort. wobble and fry use the given
// strengths. duration is the input length in seconds (0 if unknown).
func Random(rnd *rand.Rand, kind MediaKind, duration float64, wobble, fry Strength) Recipe {
	switch p := rnd.Float64(); {
	case p < randomWobble:
		return Recipe{{ID: "wobble", Strength: wobble}}
	case p < randomWobble+randomFry:
		return Recipe{{ID: "fry", Strength: fry}}
	}
	return randomMix(rnd, kind, duration)
}

// randomMix picks 1–3 compatible effects from the rest of the catalog:
// at most one tempo change and one per-frame effect (they are expensive),
// and at least one visual effect for anything with a picture.
func randomMix(rnd *rand.Rand, kind MediaKind, duration float64) Recipe {
	var pool []Effect
	for _, e := range Catalog {
		if e.Group == GroupWhole || e.NoRandom || !e.AppliesTo(kind) {
			continue
		}
		if e.ID == "reverse" && duration > maxReverseSeconds {
			continue
		}
		pool = append(pool, e)
	}
	if len(pool) == 0 {
		return nil
	}

	want := 1 + rnd.IntN(3)
	var (
		recipe       Recipe
		speed, frame bool
		visual       bool
	)
	add := func(e Effect) bool {
		if recipe.Has(e.ID) || (e.Speed && speed) || (e.frame != nil && frame) {
			return false
		}
		recipe = append(recipe, Step{ID: e.ID, Strength: 50})
		speed = speed || e.Speed
		frame = frame || e.frame != nil
		visual = visual || e.Visual()
		return true
	}
	for _, i := range rnd.Perm(len(pool)) {
		if len(recipe) == want {
			break
		}
		add(pool[i])
	}
	if !kind.sound() && !visual {
		// A sound-only mix on a video would look untouched: swap one in.
		for _, i := range rnd.Perm(len(pool)) {
			if pool[i].Visual() && !pool[i].Speed && pool[i].frame == nil {
				if len(recipe) == want && len(recipe) > 0 {
					recipe = recipe[:len(recipe)-1]
				}
				add(pool[i])
				break
			}
		}
	}
	return recipe
}
