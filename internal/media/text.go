package media

import (
	"math/rand/v2"
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"
)

// TextEffect is a way to mangle text. Text effects are pure Go and instant.
type TextEffect struct {
	ID    string
	Emoji string
	order int  // application order inside a recipe
	cased bool // changes letter case: mock and caps exclude each other
	apply func(rnd *rand.Rand, s string) string
}

// TextCatalog lists the text effects in display order.
var TextCatalog = []TextEffect{
	{ID: "mock", Emoji: "〰️", order: 30, cased: true, apply: func(_ *rand.Rand, s string) string { return DistortText(s) }},
	{ID: "baby", Emoji: "👶", order: 10, apply: func(_ *rand.Rand, s string) string { return babyTalk(s) }},
	{ID: "typos", Emoji: "🍺", order: 20, apply: drunkTyping},
	{ID: "caps", Emoji: "📢", order: 30, cased: true, apply: func(_ *rand.Rand, s string) string { return capsHysteria(s) }},
	{ID: "spaced", Emoji: "↔️", order: 40, apply: func(_ *rand.Rand, s string) string { return spaced(s) }},
	{ID: "emoji", Emoji: "😂", order: 50, apply: emojiPasta},
}

var textCatalog = func() map[string]TextEffect {
	m := map[string]TextEffect{}
	for _, e := range TextCatalog {
		m[e.ID] = e
	}
	return m
}()

// Cased reports whether the effect rewrites letter case.
func (e TextEffect) Cased() bool { return e.cased }

// LookupText returns a text effect.
func LookupText(id string) (TextEffect, bool) {
	e, ok := textCatalog[id]
	return e, ok
}

// SplitText separates text effects from media effects.
func (r Recipe) SplitText() (steps, text Recipe) {
	for _, s := range r {
		if _, ok := textCatalog[s.ID]; ok {
			text = append(text, s)
		} else {
			steps = append(steps, s)
		}
	}
	return steps, text
}

// maxMessage is Telegram's limit for a text message.
const maxMessage = 4096

// ApplyText runs text effects in their fixed order.
func ApplyText(rnd *rand.Rand, recipe Recipe, s string) string {
	steps := slices.Clone(recipe)
	slices.SortStableFunc(steps, func(a, b Step) int { return textCatalog[a.ID].order - textCatalog[b.ID].order })
	for _, step := range steps {
		if e, ok := textCatalog[step.ID]; ok {
			s = e.apply(rnd, s)
		}
	}
	return Clip(s, maxMessage)
}

// Clip cuts s to at most n UTF-16 code units, the way Telegram counts
// message length: most emoji take two.
func Clip(s string, n int) string {
	units := 0
	for i, r := range s {
		units += utf16.RuneLen(r)
		if units > n {
			return s[:i]
		}
	}
	return s
}

// RandomText picks one text effect, all with equal odds.
func RandomText(rnd *rand.Rand) Recipe {
	return Recipe{{ID: TextCatalog[rnd.IntN(len(TextCatalog))].ID}}
}

// FriedText is what /fry does to text: shouting with emoji.
var FriedText = Recipe{{ID: "caps"}, {ID: "emoji"}}

// babyTalk lisps like a toddler: р→л, ш/щ→с, ж→з; r/l→w in English.
func babyTalk(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case 'р':
			return 'л'
		case 'Р':
			return 'Л'
		case 'ш', 'щ':
			return 'с'
		case 'Ш', 'Щ':
			return 'С'
		case 'ж':
			return 'з'
		case 'Ж':
			return 'З'
		case 'r', 'l':
			return 'w'
		case 'R', 'L':
			return 'W'
		}
		return r
	}, s)
}

// spaced puts a space between letters: "п р и в е т".
func spaced(s string) string {
	var b strings.Builder
	prev := rune(0)
	for _, r := range s {
		if prev != 0 && prev != '\n' && r != '\n' {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}

// capsHysteria shouts: capitals and broken exclamation marks.
func capsHysteria(s string) string {
	up := strings.ToUpper(strings.TrimRight(s, " .!?\n"))
	tail := "!!!11one"
	if strings.IndexFunc(up, func(r rune) bool { return unicode.Is(unicode.Cyrillic, r) }) >= 0 {
		tail = "!!!1!11один"
	}
	up = strings.NewReplacer(". ", "!!1 ", "? ", "?!?! ", "\n", "!!!\n").Replace(up)
	return up + tail
}

// Keyboard neighbours for drunk typing, by layout.
var neighbours = func() map[rune][]rune {
	rows := []string{
		"йцукенгшщзхъ", "фывапролджэ", "ячсмитьбю",
		"qwertyuiop", "asdfghjkl", "zxcvbnm",
	}
	m := map[rune][]rune{}
	for _, row := range rows {
		r := []rune(row)
		for i, c := range r {
			if i > 0 {
				m[c] = append(m[c], r[i-1])
			}
			if i < len(r)-1 {
				m[c] = append(m[c], r[i+1])
			}
		}
	}
	return m
}()

// drunkTyping hits neighbouring keys, drops, doubles and swaps letters.
func drunkTyping(rnd *rand.Rand, s string) string {
	in := []rune(s)
	var out []rune
	for i := 0; i < len(in); i++ {
		r := in[i]
		lower := unicode.ToLower(r)
		n, letter := neighbours[lower]
		if !letter {
			out = append(out, r)
			continue
		}
		switch p := rnd.Float64(); {
		case p < 0.08: // a neighbouring key
			miss := n[rnd.IntN(len(n))]
			if unicode.IsUpper(r) {
				miss = unicode.ToUpper(miss)
			}
			out = append(out, miss)
		case p < 0.12: // dropped
		case p < 0.16: // doubled
			out = append(out, r, r)
		case p < 0.20 && i+1 < len(in) && unicode.IsLetter(in[i+1]): // swapped with the next one
			out = append(out, in[i+1], r)
			i++
		default:
			out = append(out, r)
		}
	}
	return string(out)
}

var pasta = []string{"😂", "👌", "💯", "🔥", "😳", "🤣", "👀", "🥵", "😩", "🤡", "💀", "😭", "🙏", "✨", "😎", "🤪"}

// emojiPasta sprinkles emoji after words, like a fried copypasta.
func emojiPasta(rnd *rand.Rand, s string) string {
	words := strings.Split(s, " ")
	for i, w := range words {
		if strings.TrimSpace(w) == "" || rnd.Float64() > 0.4 {
			continue
		}
		var b strings.Builder
		b.WriteString(w)
		b.WriteByte(' ')
		for range 1 + rnd.IntN(3) {
			b.WriteString(pasta[rnd.IntN(len(pasta))])
		}
		words[i] = b.String()
	}
	out := strings.Join(words, " ")
	return out + " " + pasta[rnd.IntN(len(pasta))] + pasta[rnd.IntN(len(pasta))]
}
