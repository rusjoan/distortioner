package i18n

import (
	"regexp"
	"testing"
)

var verb = regexp.MustCompile(`%(\[\d+\])?[a-z%]`)

// Every key must exist in every language with the same format verbs.
func TestCatalogsMatch(t *testing.T) {
	for key, en := range messages[EN] {
		for _, lang := range Langs {
			s, ok := messages[lang][key]
			if !ok {
				t.Errorf("%s: missing %q", lang, key)
				continue
			}
			if a, b := len(verb.FindAllString(en, -1)), len(verb.FindAllString(s, -1)); a != b {
				t.Errorf("%s: %q has %d verbs, English has %d", lang, key, b, a)
			}
		}
	}
	for _, lang := range Langs {
		for key := range messages[lang] {
			if _, ok := messages[EN][key]; !ok {
				t.Errorf("%s: %q is not in English", lang, key)
			}
		}
	}
}

func TestDetect(t *testing.T) {
	for code, want := range map[string]Lang{"ru": RU, "ru-RU": RU, "uk": RU, "en-US": EN, "": EN, "de": EN} {
		if got := Detect(code); got != want {
			t.Errorf("Detect(%q) = %s", code, got)
		}
	}
}

func TestT(t *testing.T) {
	if got := T(RU, "err.too_big", 20); got != "Семпай, он слишком большой… (максимум 20 МБ)" {
		t.Fatal(got)
	}
	if got := T(RU, "admin.alert", 3, "10m", "video", int64(5), "boom"); got == "" || !regexp.MustCompile(`10m: 3`).MatchString(got) {
		t.Fatal(got)
	}
	if T(EN, "missing.key") != "missing.key" {
		t.Fatal("missing key fallback")
	}
}
