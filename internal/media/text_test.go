package media

import (
	"math/rand/v2"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTextEffects(t *testing.T) {
	rnd := rand.New(rand.NewPCG(1, 1))
	cases := map[string]struct{ in, want string }{
		"mock":   {"привет мир", "пРиВеТ мИр"},
		"baby":   {"Рыжая шапка жрёт", "Лызая сапка злёт"},
		"spaced": {"при\nвет", "п р и\nв е т"},
		"caps":   {"привет. как дела?", "ПРИВЕТ!!1 КАК ДЕЛА!!!1!11один"},
	}
	for id, c := range cases {
		if got := ApplyText(rnd, Recipe{{ID: id}}, c.in); got != c.want {
			t.Errorf("%s(%q) = %q, want %q", id, c.in, got, c.want)
		}
	}
	if got := capsHysteria("hello"); got != "HELLO!!!11one" {
		t.Errorf("english caps: %q", got)
	}
	if got := babyTalk("really"); got != "weawwy" {
		t.Errorf("english baby: %q", got)
	}
}

func TestDrunkAndEmojiAreRandomButBounded(t *testing.T) {
	rnd := rand.New(rand.NewPCG(2, 2))
	in := "привет как дела у тебя сегодня вечером"
	changed := 0
	for range 100 {
		d := drunkTyping(rnd, in)
		if d != in {
			changed++
		}
		if n := utf8.RuneCountInString(d); n < len([]rune(in))/2 || n > len([]rune(in))*2 {
			t.Fatalf("drunk went too far: %q", d)
		}
		e := emojiPasta(rnd, in)
		if !strings.HasPrefix(e, "привет") || utf8.RuneCountInString(e) <= utf8.RuneCountInString(in) {
			t.Fatalf("emoji pasta: %q", e)
		}
	}
	if changed < 80 {
		t.Fatalf("drunk typing changed only %d of 100", changed)
	}
}

func TestRandomTextEqualOdds(t *testing.T) {
	rnd := rand.New(rand.NewPCG(3, 3))
	counts := map[string]int{}
	const n = 30000
	for range n {
		r := RandomText(rnd)
		if len(r) != 1 {
			t.Fatalf("%v", r)
		}
		counts[r[0].ID]++
	}
	for _, e := range TextCatalog {
		if share := float64(counts[e.ID]) / n; share < 1.0/6-0.015 || share > 1.0/6+0.015 {
			t.Errorf("%s share %.3f", e.ID, share)
		}
	}
}

func TestApplyTextOrderAndLimit(t *testing.T) {
	rnd := rand.New(rand.NewPCG(4, 4))
	// Baby talk runs before shouting regardless of the picked order.
	if got := ApplyText(rnd, Recipe{{ID: "caps"}, {ID: "baby"}}, "шар"); got != "САЛ!!!1!11один" {
		t.Fatalf("%q", got)
	}
	long := strings.Repeat("я", 3000)
	if got := ApplyText(rnd, Recipe{{ID: "spaced"}}, long); utf8.RuneCountInString(got) != maxMessage {
		t.Fatalf("not cut to the message limit: %d", utf8.RuneCountInString(got))
	}
}
