package bot

import (
	"context"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/graynk/distortioner/internal/i18n"
	"github.com/graynk/distortioner/internal/media"
	"github.com/graynk/distortioner/internal/store"
)

// Mode is what the user asked for.
type Mode int

const (
	ModeRandom Mode = iota // /distort, 🎲
	ModeWobble             // /wobble, 🌀: the original seam carving
	ModeFry                // /fry, 🔥
	ModeRecipe             // effects picked one by one
)

// pendingTTL bounds how long a question or an open picker is remembered.
const pendingTTL = time.Hour

// mediaKind maps what we received to what the media engine processes.
func (k Kind) mediaKind() media.MediaKind {
	switch k {
	case KindPhoto, KindImageFile:
		return media.MediaPhoto
	case KindSticker:
		return media.MediaSticker
	case KindAnimation:
		return media.MediaAnimation
	case KindVideoNote:
		return media.MediaVideoNote
	case KindVideoSticker:
		return media.MediaVideoSticker
	case KindVoice:
		return media.MediaVoice
	case KindAudio:
		return media.MediaAudio
	default:
		return media.MediaVideo
	}
}

// captionFor picks the text effects for captions, following the mode.
func (b *Bot) captionFor(r request) media.Recipe {
	switch r.mode {
	case ModeWobble:
		return media.Recipe{{ID: "mock"}}
	case ModeFry:
		return media.FriedText
	case ModeRecipe:
		return r.caption // only what was picked
	}
	b.rndMu.Lock()
	defer b.rndMu.Unlock()
	return media.RandomText(b.rnd)
}

// recipeFor turns the request into effects for this media.
func (b *Bot) recipeFor(r request, kind media.MediaKind, duration float64) media.Recipe {
	switch r.mode {
	case ModeWobble:
		return media.Recipe{{ID: "wobble", Strength: r.wobble}}
	case ModeFry:
		return media.Recipe{{ID: "fry", Strength: r.fry}}
	case ModeRecipe:
		out := make(media.Recipe, len(r.recipe))
		for i, s := range r.recipe {
			out[i] = media.Step{ID: s.ID, Strength: r.strengthOf(s.ID)}
		}
		return out
	}
	b.rndMu.Lock()
	defer b.rndMu.Unlock()
	return media.Random(b.rnd, kind, duration, r.wobble, r.fry)
}

func (r request) strengthOf(id string) media.Strength {
	switch id {
	case "wobble":
		return r.wobble
	case "fry":
		return r.fry
	}
	return 50
}

// source names how the request was made, for statistics.
func (r request) statSource() string {
	if r.via != "" {
		return r.via
	}
	switch r.mode {
	case ModeRandom:
		return store.SourceRandom
	case ModeRecipe:
		return store.SourcePicker
	}
	return store.SourceCommand
}

// Callback data of the choice buttons: "x:r" random, "x:w" wobble,
// "x:f" fry, "x:p" open the picker. Picker buttons: "p:<effect>",
// "p:!" apply, "p:x" close, "p:-" a row title.
func choiceMarkup(lang i18n.Lang, again bool) *models.InlineKeyboardMarkup {
	random := i18n.T(lang, "btn.random")
	if again {
		random = i18n.T(lang, "btn.random_again")
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: random, CallbackData: "x:r"}, {Text: i18n.T(lang, "btn.wobble"), CallbackData: "x:w"}},
		{{Text: i18n.T(lang, "btn.fry"), CallbackData: "x:f"}, {Text: i18n.T(lang, "btn.pick"), CallbackData: "x:p"}},
	}}
}

// textChoiceMarkup is the choice for text: wobble and fry make no sense
// as such, so only a new random pick or picking effects.
func textChoiceMarkup(lang i18n.Lang, again bool) *models.InlineKeyboardMarkup {
	random := i18n.T(lang, "btn.random")
	if again {
		random = i18n.T(lang, "btn.random_again")
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{
		{{Text: random, CallbackData: "x:r"}, {Text: i18n.T(lang, "btn.pick"), CallbackData: "x:p"}},
	}}
}

// pending is a question or an open picker waiting for a tap.
type pending struct {
	parts    []request // the media (several for an album)
	question bool      // the message is our "what to do?" question
	selected []string  // picker selection
	kind     media.MediaKind
	text     bool // the target is text: text effects instead of media ones
	caption  bool // media with a caption: text effects rewrite it
	expires  time.Time
}

// choices is the keyboard of a question or a result for this target.
func (p *pending) choices(lang i18n.Lang, again bool) *models.InlineKeyboardMarkup {
	if p.text {
		return textChoiceMarkup(lang, again)
	}
	return choiceMarkup(lang, again)
}

type pendingStore struct {
	mu sync.Mutex
	m  map[[2]int64]*pending
}

func newPendingStore() *pendingStore { return &pendingStore{m: map[[2]int64]*pending{}} }

func pendingKey(chatID int64, msgID int) [2]int64 { return [2]int64{chatID, int64(msgID)} }

func (s *pendingStore) put(chatID int64, msgID int, p *pending) {
	p.expires = time.Now().Add(pendingTTL)
	s.mu.Lock()
	s.m[pendingKey(chatID, msgID)] = p
	s.mu.Unlock()
}

func (s *pendingStore) get(chatID int64, msgID int) *pending {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.m[pendingKey(chatID, msgID)]
	if p != nil && time.Now().After(p.expires) {
		delete(s.m, pendingKey(chatID, msgID))
		return nil
	}
	return p
}

func (s *pendingStore) drop(chatID int64, msgID int) {
	s.mu.Lock()
	delete(s.m, pendingKey(chatID, msgID))
	s.mu.Unlock()
}

func (s *pendingStore) sweep(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, p := range s.m {
		if now.After(p.expires) {
			delete(s.m, k)
		}
	}
}

// ask replies to media sent without a command with the choice buttons.
func (b *Bot) ask(ctx context.Context, parts []request) {
	r := parts[0]
	if b.Settings.IsBanned(r.chatID) {
		return
	}
	p := &pending{parts: parts, question: true, kind: media.MediaVideo}
	if it, ok, _ := classify(r.source); ok {
		p.kind, p.text = it.kind.mediaKind(), it.kind == KindText
	}
	p.kind = albumKind(parts, p.kind)
	p.caption = !p.text && hasCaption(parts)
	m, err := b.TG.SendText(ctx, r.chatID, r.source.ID, i18n.T(r.lang, "ask.what"), p.choices(r.lang, false))
	if err != nil {
		b.handleSendError(ctx, r, err)
		return
	}
	b.pending.put(r.chatID, m.ID, p)
}

// albumKind is the most restrictive kind among the parts: a photo in an
// album limits the effects to those that work on stills.
func albumKind(parts []request, first media.MediaKind) media.MediaKind {
	for _, p := range parts {
		if it, ok, _ := classify(p.source); ok && it.kind.mediaKind() == media.MediaPhoto {
			return media.MediaPhoto
		}
	}
	return first
}

// hasCaption reports whether any part has a caption to rewrite.
func hasCaption(parts []request) bool {
	for _, r := range parts {
		if it, ok, _ := classify(r.source); ok && it.caption != "" {
			return true
		}
	}
	return false
}

// target finds what the tapped buttons apply to: the media behind our
// question, or the result the buttons are attached to.
func (b *Bot) target(ctx context.Context, cq *models.CallbackQuery) (*pending, bool) {
	msg := cq.Message.Message
	if msg == nil {
		return nil, false
	}
	if p := b.pending.get(msg.Chat.ID, msg.ID); p != nil {
		return p, true
	}
	if it, ok, _ := classify(msg); ok && it.kind != KindText {
		// A media result: the buttons apply to the message itself, replying to it.
		r := b.newRequest(ctx, msg, &cq.From, msg.ID)
		return &pending{parts: []request{r}, kind: it.kind.mediaKind(), caption: it.caption != ""}, true
	}
	if orig := msg.ReplyToMessage; orig != nil {
		if it, ok, _ := classify(orig); ok && it.kind != KindText {
			// Our question after a restart: the media is what it replies to.
			r := b.newRequest(ctx, orig, &cq.From, orig.ID)
			return &pending{parts: []request{r}, question: true, kind: it.kind.mediaKind(), caption: it.caption != ""}, true
		}
	}
	if msg.Text != "" && !isOurQuestion(msg.Text) {
		// A text result: like media results, the buttons apply to the text of
		// this very message. What it replies to may be the command, not the text.
		r := b.newRequest(ctx, msg, &cq.From, msg.ID)
		return &pending{parts: []request{r}, text: true}, true
	}
	return nil, false
}

// isOurQuestion reports whether a text is one of our prompts, which must
// never be distorted itself.
func isOurQuestion(text string) bool {
	for _, l := range i18n.Langs {
		if text == i18n.T(l, "ask.what") || text == i18n.T(l, "ask.pick") {
			return true
		}
	}
	return false
}

// onChoice handles "x:" buttons.
func (b *Bot) onChoice(ctx context.Context, cq *models.CallbackQuery) {
	lang := i18n.Detect(cq.From.LanguageCode)
	msg := cq.Message.Message
	p, ok := b.target(ctx, cq)
	if !ok {
		b.TG.Answer(ctx, cq.ID, i18n.T(lang, "err.expired"), true)
		return
	}
	choice := strings.TrimPrefix(cq.Data, "x:")
	if choice == "p" {
		b.TG.Answer(ctx, cq.ID, "", false)
		b.openPicker(ctx, msg, p, b.newRequest(ctx, msg, &cq.From, 0).lang)
		return
	}
	b.TG.Answer(ctx, cq.ID, "", false)
	mode := map[string]Mode{"r": ModeRandom, "w": ModeWobble, "f": ModeFry}[choice]
	b.start(ctx, msg, p, mode, nil, &cq.From)
}

// start runs the choice on the pending media.
func (b *Bot) start(ctx context.Context, msg *models.Message, p *pending, mode Mode, recipe media.Recipe, from *models.User) {
	via := store.SourceButton
	if p.question {
		via = store.SourcePrompt
		b.pending.drop(msg.Chat.ID, msg.ID)
		b.TG.Delete(ctx, msg.Chat.ID, msg.ID)
	}
	if mode == ModeRecipe {
		via = store.SourcePicker
	}
	var caption media.Recipe
	if !p.text {
		recipe, caption = recipe.SplitText()
	}
	parts := make([]request, len(p.parts))
	for i, part := range p.parts {
		// Settings and language follow whoever tapped the button.
		r := b.newRequest(ctx, part.source, from, part.replyTo)
		r.mode, r.recipe, r.caption, r.via = mode, recipe, caption, via
		parts[i] = r
	}
	if mode == ModeRecipe && len(recipe) == 0 && len(caption) > 0 {
		b.recaption(ctx, parts)
		return
	}
	if len(parts) > 1 {
		b.processAlbum(ctx, parts)
		return
	}
	b.process(ctx, parts[0])
}

// pickerRow is a titled row of effects in the picker.
type pickerRow struct {
	title string
	ids   []string
}

// Picker rows, in display order.
var pickerRows = []pickerRow{
	{"picker.presets", []string{"wobble", "fry"}},
	{"picker.picture", []string{"bulge", "swirl", "pixel", "rainbow", "mirror", "trails", "drunk", "shake", "demotivator"}},
	{"picker.time", []string{"reverse", "fast", "slow"}},
	{"picker.sound", []string{"earrape", "chipmunk", "demon", "echo"}},
}

func (b *Bot) openPicker(ctx context.Context, msg *models.Message, p *pending, lang i18n.Lang) {
	p.selected = nil
	b.pending.put(msg.Chat.ID, msg.ID, p)
	b.showPicker(ctx, msg, p, lang)
}

func (b *Bot) showPicker(ctx context.Context, msg *models.Message, p *pending, lang i18n.Lang) {
	markup := pickerMarkup(lang, p)
	if p.question {
		_ = b.TG.EditText(ctx, msg.Chat.ID, msg.ID, i18n.T(lang, "ask.pick"), markup)
		return
	}
	b.TG.EditMarkup(ctx, msg.Chat.ID, msg.ID, markup)
}

// textPickerRows is the picker for text.
var textPickerRows = func() []pickerRow {
	ids := make([]string, len(media.TextCatalog))
	for i, e := range media.TextCatalog {
		ids[i] = e.ID
	}
	return []pickerRow{{"picker.text", ids}}
}()

// captionPickerRows rewrite the caption of media.
var captionPickerRows = []pickerRow{{"picker.caption", textPickerRows[0].ids}}

// applies reports whether an effect can be picked for the target.
func (p *pending) applies(id string) bool {
	if _, ok := media.LookupText(id); ok || p.text {
		return ok && (p.text || p.caption)
	}
	e, ok := media.Lookup(id)
	return ok && e.AppliesTo(p.kind)
}

const pickerPerRow = 5

// effectEmoji is the button face of an effect.
func effectEmoji(id string) string {
	if e, ok := media.LookupText(id); ok {
		return e.Emoji
	}
	e, _ := media.Lookup(id)
	return e.Emoji
}

func pickerMarkup(lang i18n.Lang, p *pending) *models.InlineKeyboardMarkup {
	var rows [][]models.InlineKeyboardButton
	groups := pickerRows
	switch {
	case p.text:
		groups = textPickerRows
	case p.caption:
		groups = append(slices.Clone(pickerRows), captionPickerRows...)
	}
	for _, group := range groups {
		var row []models.InlineKeyboardButton
		for _, id := range group.ids {
			if !p.applies(id) {
				continue
			}
			// Names do not fit: the buttons show emoji, a tap shows the name.
			label := effectEmoji(id)
			if slices.Contains(p.selected, id) {
				label = "✅" + label
			}
			row = append(row, models.InlineKeyboardButton{Text: label, CallbackData: "p:" + id})
		}
		if len(row) == 0 {
			continue
		}
		rows = append(rows, []models.InlineKeyboardButton{{Text: "— " + i18n.T(lang, group.title) + " —", CallbackData: "p:-"}})
		// Up to five a row, split evenly: 9 → 5 + 4, 6 → 3 + 3.
		lines := (len(row) + pickerPerRow - 1) / pickerPerRow
		per := (len(row) + lines - 1) / lines
		for len(row) > 0 {
			n := min(per, len(row))
			rows, row = append(rows, row[:n]), row[n:]
		}
	}
	rows = append(rows, []models.InlineKeyboardButton{
		{Text: i18n.T(lang, "picker.apply", len(p.selected)), CallbackData: "p:!"},
		{Text: i18n.T(lang, "picker.close"), CallbackData: "p:x"},
	})
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// toggle selects or unselects an effect. wobble and fry are complete looks
// and go alone; tempo changes exclude each other.
func toggle(selected []string, id string) []string {
	if i := slices.Index(selected, id); i >= 0 {
		return slices.Delete(selected, i, i+1)
	}
	if te, ok := media.LookupText(id); ok {
		// Mocking case and shouting both rewrite letter case.
		out := selected[:0:0]
		for _, s := range selected {
			if other, _ := media.LookupText(s); te.Cased() && other.Cased() {
				continue
			}
			out = append(out, s)
		}
		return append(out, id)
	}
	e, _ := media.Lookup(id)
	out := selected[:0:0]
	for _, s := range selected {
		if _, text := media.LookupText(s); text {
			out = append(out, s) // caption effects go with anything
			continue
		}
		other, _ := media.Lookup(s)
		if e.Group == media.GroupWhole || other.Group == media.GroupWhole || (e.Speed && other.Speed) {
			continue
		}
		out = append(out, s)
	}
	return append(out, id)
}

// onPicker handles "p:" buttons.
func (b *Bot) onPicker(ctx context.Context, cq *models.CallbackQuery) {
	lang := i18n.Detect(cq.From.LanguageCode)
	msg := cq.Message.Message
	if msg == nil {
		b.TG.Answer(ctx, cq.ID, "", false)
		return
	}
	p := b.pending.get(msg.Chat.ID, msg.ID)
	if p == nil {
		b.TG.Answer(ctx, cq.ID, i18n.T(lang, "err.expired"), true)
		return
	}
	lang = b.newRequest(ctx, msg, &cq.From, 0).lang
	switch arg := strings.TrimPrefix(cq.Data, "p:"); arg {
	case "-":
		b.TG.Answer(ctx, cq.ID, "", false)
	case "x":
		b.TG.Answer(ctx, cq.ID, "", false)
		if p.question {
			p.selected = nil
			_ = b.TG.EditText(ctx, msg.Chat.ID, msg.ID, i18n.T(lang, "ask.what"), p.choices(lang, false))
			return
		}
		b.pending.drop(msg.Chat.ID, msg.ID)
		b.TG.EditMarkup(ctx, msg.Chat.ID, msg.ID, p.choices(lang, true))
	case "!":
		if len(p.selected) == 0 {
			b.TG.Answer(ctx, cq.ID, i18n.T(lang, "picker.empty"), true)
			return
		}
		b.TG.Answer(ctx, cq.ID, "", false)
		recipe := make(media.Recipe, len(p.selected))
		for i, id := range p.selected {
			recipe[i] = media.Step{ID: id}
		}
		if !p.question {
			b.pending.drop(msg.Chat.ID, msg.ID)
			b.TG.EditMarkup(ctx, msg.Chat.ID, msg.ID, p.choices(lang, true))
		}
		b.start(ctx, msg, p, ModeRecipe, recipe, &cq.From)
	default:
		if !p.applies(arg) {
			b.TG.Answer(ctx, cq.ID, "", false)
			return
		}
		p.selected = toggle(p.selected, arg)
		b.TG.Answer(ctx, cq.ID, i18n.T(lang, "fx."+arg), false)
		b.showPicker(ctx, msg, p, lang)
	}
}

// resultCaption adds what was applied, for random picks and picked effects.
func resultCaption(r request, recipe media.Recipe, caption string) string {
	var tag string
	switch r.mode {
	case ModeRandom:
		tag = "🎲 " + recipe.Emoji()
	case ModeRecipe:
		tag = "✨ " + recipe.Emoji()
	default:
		return caption
	}
	if caption == "" {
		return tag
	}
	return caption + "\n\n" + tag
}

// newRand seeds the random picks.
func newRand() *rand.Rand {
	return rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 0x5eed)) //nolint:gosec // picking meme effects
}
