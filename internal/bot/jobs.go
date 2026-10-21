package bot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/graynk/distortioner/internal/i18n"
	"github.com/graynk/distortioner/internal/jobs"
	"github.com/graynk/distortioner/internal/media"
	"github.com/graynk/distortioner/internal/settings"
	"github.com/graynk/distortioner/internal/store"
	"github.com/graynk/distortioner/internal/telegram"
)

// task is the state shared by a job's Run and Done callbacks.
type task struct {
	r         request
	kind      Kind
	items     []item
	recipe    media.Recipe
	caption   media.Recipe // text effects for the captions
	pool      string       // "video" or "image", for metrics
	submitted time.Time
	workDir   string
	inputs    []string
	progress  *telegram.Progress
	started   time.Time
}

// submit queues the items as one job: a single item or a whole album.
func (b *Bot) submit(ctx context.Context, r request, kind Kind, items []item) {
	if !b.admit(ctx, r, kind, items) {
		return
	}

	// One recipe for the whole job; an album gets effects that suit all parts.
	mkind := items[0].kind.mediaKind()
	if len(items) > 1 {
		mkind = media.MediaVideo
		for _, it := range items {
			if it.kind.mediaKind() == media.MediaPhoto {
				mkind = media.MediaPhoto
			}
		}
	}
	recipe := b.recipeFor(r, mkind, items[0].duration).Applicable(mkind)
	if len(recipe) == 0 {
		// A picked set may not suit this media at all: fall back to wobble.
		recipe = media.Recipe{{ID: "wobble", Strength: r.wobble}}
	}

	// Only frame-by-frame work on moving pictures is heavy: everything else
	// is one ffmpeg pass and never waits behind seam carving. Anything that
	// takes a while (video and sound) gets a status message either way.
	heavy, slow := false, false
	for _, it := range items {
		heavy = heavy || (recipe.PerFrame() && it.kind.heavy())
		slow = slow || it.kind.heavy() || it.kind == KindVoice || it.kind == KindAudio
	}
	pool, poolName := b.Image, "i"
	if heavy {
		pool, poolName = b.Video, "v"
	}

	t := &task{r: r, kind: kind, items: items, recipe: recipe, pool: "image", submitted: time.Now()}
	for _, it := range items {
		if it.caption != "" {
			t.caption = b.captionFor(r) // one pick for the whole album
			break
		}
	}
	if heavy {
		t.pool = "video"
	}
	if slow {
		// Show progress right away; the cancel button is added once the job has an id.
		p, err := b.TG.StartProgress(ctx, r.chatID, r.replyTo, i18n.T(r.lang, "progress.downloading"), nil)
		if err != nil {
			b.handleSendError(ctx, r, err)
			return
		}
		t.progress = p
	}

	// The closures read the id from the job: Submit sets it under the pool's
	// lock before any worker can see the job.
	// Estimates are per second of media: a minute-long video weighs sixty
	// times a photo.
	var cost float64
	for _, it := range items {
		cost += max(it.duration, 1)
	}
	var job *jobs.Job
	job = &jobs.Job{
		ChatID: r.chatID,
		Cost:   cost,
		Run: func(ctx context.Context) error {
			b.untrack(job.ID())
			return b.run(ctx, t, poolName, job.ID())
		},
		Done: func(err error) {
			b.untrack(job.ID())
			b.done(t, err)
		},
	}
	id, err := pool.Submit(job)
	if err != nil {
		text, reason := i18n.T(r.lang, "err.queue_full"), "queue_full"
		switch {
		case errors.Is(err, jobs.ErrChatLimit):
			text, reason = i18n.T(r.lang, "err.chat_limit", b.Settings.Int(settings.MaxJobsPerChat)), "chat_limit"
		case errors.Is(err, jobs.ErrClosed):
			text, reason = i18n.T(r.lang, "err.shutdown"), "shutdown"
		}
		b.Metrics.Rejection(reason)
		if t.progress != nil {
			t.progress.Finish(ctx, text)
		} else {
			b.reply(ctx, r, text)
		}
		b.record(r, kind, store.StatusRejected, 0, nil)
		return
	}

	if t.progress == nil {
		return
	}
	b.mu.Lock()
	b.tracked[id] = &tracked{pool: pool, poolName: poolName, progress: t.progress, lang: r.lang, userID: r.userID}
	b.mu.Unlock()
	// Only say "in queue" when the job really waits: with a free worker it
	// starts in a moment, and run shows the first stage itself.
	if pos, eta, waits := pool.Waits(id); waits {
		t.progress.Queued(ctx, queuedText(r.lang, pos, eta), cancelMarkup(r.lang, poolName, id))
	}
}

func cancelMarkup(lang i18n.Lang, pool string, id uint64) models.ReplyMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: i18n.T(lang, "btn.cancel"), CallbackData: fmt.Sprintf("c:%s:%d", pool, id)},
	}}}
}

// run downloads, distorts and sends the result. It runs on a pool worker.
func (b *Bot) run(ctx context.Context, t *task, poolName string, id uint64) error {
	t.started = time.Now()
	b.Metrics.QueueWait(t.pool, t.started.Sub(t.submitted))
	dir, err := os.MkdirTemp(b.Cfg.TempDir, "distortioner-")
	if err != nil {
		return err
	}
	t.workDir = dir

	var markup models.ReplyMarkup
	if t.progress != nil {
		markup = cancelMarkup(t.r.lang, poolName, id)
		// Not throttled, and "in queue" can no longer overwrite it.
		t.progress.Started(ctx, i18n.T(t.r.lang, "progress.downloading"), markup)
	}

	outputs := make([]string, len(t.items))
	for i, it := range t.items {
		in := filepath.Join(dir, fmt.Sprintf("in%d%s", i, it.ext))
		start := time.Now()
		if err := b.TG.Download(ctx, it.fileID, in); err != nil {
			return fmt.Errorf("download: %w", err)
		}
		b.Metrics.Stage("download", string(it.kind), time.Since(start))
		b.Metrics.Input(string(it.kind), fileSize(in), it.duration)
		t.inputs = append(t.inputs, in)

		start = time.Now()
		out, err := b.apply(ctx, t, it, in, i, markup)
		if err != nil {
			return err
		}
		b.Metrics.Stage("process", string(it.kind), time.Since(start))
		b.Metrics.Output(string(it.kind), fileSize(out))
		outputs[i] = out
	}

	if t.progress != nil {
		t.progress.Set(ctx, i18n.T(t.r.lang, "progress.uploading"), markup)
	}
	start := time.Now()
	defer func() { b.Metrics.Stage("upload", string(t.kind), time.Since(start)) }()
	if len(t.items) > 1 {
		return b.sendAlbum(ctx, t, outputs)
	}
	return b.sendResult(ctx, t, t.items[0], outputs[0])
}

func fileSize(path string) int64 {
	if st, err := os.Stat(path); err == nil {
		return st.Size()
	}
	return 0
}

// outcome names how a job ended, for metrics.
func outcome(err error) string {
	var tooLong *media.TooLongError
	var panicked *jobs.PanicError
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, jobs.ErrCanceled):
		return "canceled"
	case errors.Is(err, jobs.ErrShutdown):
		return "shutdown"
	case errors.As(err, &tooLong):
		return "too_long"
	case errors.Is(err, media.ErrNoVideo), errors.Is(err, media.ErrNothingToApply):
		return "unsupported"
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.As(err, &panicked):
		return "panic"
	case telegram.IsBlocked(err):
		return "blocked"
	}
	return "failed"
}

// stageReporter shows a new stage right away and throttles the percent
// within a stage, so short stages are seen and long ones do not flood.
type stageReporter struct {
	ctx      context.Context
	progress *telegram.Progress
	lang     i18n.Lang
	markup   models.ReplyMarkup

	mu    sync.Mutex
	stage media.Stage
	seen  bool
}

func (s *stageReporter) report(stage media.Stage, done, total int) {
	if s.progress == nil {
		return
	}
	text := stageText(s.lang, stage, done, total)
	s.mu.Lock()
	changed := !s.seen || stage != s.stage
	s.stage, s.seen = stage, true
	s.mu.Unlock()
	if changed {
		s.progress.Set(s.ctx, text, s.markup)
		return
	}
	s.progress.Update(s.ctx, text, s.markup)
}

// outExt is the extension of the result for this kind of media.
func outExt(it item) string {
	switch it.kind {
	case KindPhoto:
		return ".jpg"
	case KindImageFile:
		if it.ext == ".png" || it.ext == ".webp" || it.ext == ".jpg" || it.ext == ".jpeg" {
			return it.ext
		}
		return ".png"
	case KindSticker:
		return ".webp"
	case KindVoice, KindAudio:
		// Telegram takes M4A for voice messages too, and every player opens it.
		return ".m4a"
	case KindVideoSticker:
		return ".webm"
	}
	return ".mp4"
}

// apply runs the task's recipe on one item and returns the output path.
func (b *Bot) apply(ctx context.Context, t *task, it item, in string, i int, markup models.ReplyMarkup) (string, error) {
	// The file name is what the user sees for audio and documents.
	outDir := filepath.Join(t.workDir, fmt.Sprintf("out%d", i))
	work := filepath.Join(t.workDir, fmt.Sprintf("work%d", i))
	for _, d := range []string{outDir, work} {
		if err := os.Mkdir(d, 0o700); err != nil {
			return "", err
		}
	}
	out := filepath.Join(outDir, resultName(it, t.recipe)+outExt(it))
	stages := &stageReporter{ctx: ctx, progress: t.progress, lang: t.r.lang, markup: markup}
	var poster media.Demotivator
	if t.recipe.Has("demotivator") {
		poster = b.demotivatorText(b.captionText(t.caption, it.caption), t.r.lang)
	}
	err := b.Engine.Apply(ctx, in, out, t.recipe, media.ApplyOptions{
		Kind:         it.kind.mediaKind(),
		MaxSeconds:   b.Settings.Int(settings.MaxVideoSeconds),
		MaxFPS:       b.Settings.Int(settings.MaxFPS),
		ProcessScale: b.Settings.Int(settings.ProcessScale),
		DistortAudio: t.r.prefs.DistortAudio,
		Demotivator:  poster,
		WorkDir:      work,
		Progress:     stages.report,
	})
	// Frames can take hundreds of megabytes; free them before uploading.
	_ = os.RemoveAll(work)
	return out, err
}

func stageText(lang i18n.Lang, stage media.Stage, done, total int) string {
	pct := 0
	if total > 0 {
		pct = done * 100 / total
	}
	switch stage {
	case media.StageExtracting:
		return i18n.T(lang, "progress.extracting")
	case media.StageDistorting:
		return i18n.T(lang, "progress.distorting", progressBar(pct), pct)
	case media.StageProcessing:
		return i18n.T(lang, "progress.processing", progressBar(pct), pct)
	default:
		if total > 0 {
			return i18n.T(lang, "progress.encoding_pct", progressBar(pct), pct)
		}
		return i18n.T(lang, "progress.encoding")
	}
}

func progressBar(pct int) string {
	filled := min(max(pct/10, 0), 10)
	return "[" + strings.Repeat("■", filled) + strings.Repeat("□", 10-filled) + "]"
}

func (b *Bot) sendResult(ctx context.Context, t *task, it item, out string) error {
	r := t.r
	caption := ""
	if !t.recipe.Has("demotivator") { // the poster already shows it
		caption = b.captionText(t.caption, it.caption)
	}
	caption = resultCaption(r, t.applied(), caption)
	reply := telegram.ReplyTo(r.replyTo)
	markup := choiceMarkup(r.lang, true)

	send := func(kind Kind) error {
		_, err := telegram.SendFile(ctx, out, func(ctx context.Context, f *models.InputFileUpload) (*models.Message, error) {
			return b.sendAs(ctx, kind, f, r.chatID, caption, reply, markup)
		})
		return err
	}
	err := send(it.kind)
	// Users can forbid voice and video messages in their privacy settings
	// (Premium); deliver the result as a regular video or a file instead.
	if telegram.IsVoiceForbidden(err) {
		switch it.kind {
		case KindVideoNote:
			err = send(KindVideo)
		case KindVoice:
			err = send(KindAudio) // an audio track still plays in the chat
		}
	}
	return err
}

// sendAs uploads f as the given kind of message.
func (b *Bot) sendAs(ctx context.Context, kind Kind, f *models.InputFileUpload, chatID int64, caption string,
	reply *models.ReplyParameters, markup models.ReplyMarkup) (*models.Message, error) {
	switch kind {
	case KindPhoto:
		return b.TG.SendPhoto(ctx, &tgbot.SendPhotoParams{ChatID: chatID, Photo: f, Caption: caption, ReplyParameters: reply, ReplyMarkup: markup})
	case KindSticker, KindVideoSticker:
		return b.TG.SendSticker(ctx, &tgbot.SendStickerParams{ChatID: chatID, Sticker: f, ReplyParameters: reply, ReplyMarkup: markup})
	case KindAnimation:
		return b.TG.SendAnimation(ctx, &tgbot.SendAnimationParams{ChatID: chatID, Animation: f, Caption: caption, ReplyParameters: reply, ReplyMarkup: markup})
	case KindVideo, KindVideoFile:
		return b.TG.SendVideo(ctx, &tgbot.SendVideoParams{ChatID: chatID, Video: f, Caption: caption, SupportsStreaming: true, ReplyParameters: reply, ReplyMarkup: markup})
	case KindVideoNote:
		return b.TG.SendVideoNote(ctx, &tgbot.SendVideoNoteParams{ChatID: chatID, VideoNote: f, ReplyParameters: reply, ReplyMarkup: markup})
	case KindVoice:
		return b.TG.SendVoice(ctx, &tgbot.SendVoiceParams{ChatID: chatID, Voice: f, Caption: caption, ReplyParameters: reply, ReplyMarkup: markup})
	case KindAudio:
		return b.TG.SendAudio(ctx, &tgbot.SendAudioParams{ChatID: chatID, Audio: f, Caption: caption, ReplyParameters: reply, ReplyMarkup: markup})
	default: // KindImageFile and fallbacks: a plain document
		return b.TG.SendDocument(ctx, &tgbot.SendDocumentParams{ChatID: chatID, Document: f, Caption: caption, ReplyParameters: reply, ReplyMarkup: markup})
	}
}

func (b *Bot) sendAlbum(ctx context.Context, t *task, outputs []string) error {
	r := t.r
	_, err := telegram.Retry(ctx, func(ctx context.Context) ([]*models.Message, error) {
		group := make([]models.InputMedia, len(outputs))
		for i, out := range outputs {
			f, err := os.Open(out) //nolint:gosec // our own output file
			if err != nil {
				return nil, err
			}
			defer f.Close()
			name := fmt.Sprintf("f%d", i) // attachment field names stay ASCII
			caption := b.captionText(t.caption, t.items[i].caption)
			if i == 0 {
				caption = resultCaption(r, t.applied(), caption)
			}
			if t.items[i].kind == KindPhoto {
				group[i] = &models.InputMediaPhoto{Media: "attach://" + name, MediaAttachment: f, Caption: caption}
			} else {
				group[i] = &models.InputMediaVideo{Media: "attach://" + name, MediaAttachment: f, Caption: caption, SupportsStreaming: true}
			}
		}
		return b.TG.SendMediaGroup(ctx, &tgbot.SendMediaGroupParams{
			ChatID: r.chatID, Media: group, ReplyParameters: telegram.ReplyTo(r.replyTo),
		})
	})
	return err
}

// done reports the outcome and cleans up. It runs exactly once per job.
func (b *Bot) done(t *task, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	b.Metrics.Outcome(t.pool, outcome(err))
	r := t.r
	defer func() {
		if t.workDir != "" {
			_ = os.RemoveAll(t.workDir)
		}
	}()

	if err == nil {
		if t.progress != nil {
			t.progress.Delete(ctx)
		}
		b.record(r, t.kind, store.StatusOK, time.Since(t.started), t.applied())
		return
	}

	if telegram.IsBlocked(err) || telegram.IsNoRights(err) {
		b.handleSendError(ctx, r, err)
		if t.progress != nil {
			t.progress.Delete(ctx)
		}
		b.record(r, t.kind, store.StatusFailed, 0, nil)
		return
	}

	text := errorText(r.lang, err)
	if t.progress != nil {
		t.progress.Finish(ctx, text)
	} else {
		b.reply(ctx, r, text)
	}

	if userError(err) {
		b.record(r, t.kind, store.StatusRejected, 0, nil)
		return
	}
	b.record(r, t.kind, store.StatusFailed, 0, nil)
	b.Log.Error("job failed", "chat", r.chatID, "kind", t.kind, "err", err)

	f := store.Failure{ChatID: r.chatID, Kind: string(t.kind), Error: err.Error(), At: time.Now()}
	if b.Cfg.KeepFailedInputs && len(t.inputs) > 0 {
		f.InputPath = b.keepInput(t.inputs[len(t.inputs)-1])
	}
	id, serr := b.Store.AddFailure(ctx, f)
	if serr != nil {
		b.Log.Warn("store failure", "err", serr)
	}
	f.ID = id
	b.Admin.NotifyFailure(f)
}

// keepInput moves a failed input out of the work dir for later inspection.
func (b *Bot) keepInput(path string) string {
	dir := filepath.Join(b.Cfg.DataDir, "failed")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		b.Log.Warn("keep failed input", "err", err)
		return ""
	}
	dst := filepath.Join(dir, fmt.Sprintf("%d-%s", time.Now().UnixNano(), filepath.Base(path)))
	if err := os.Rename(path, dst); err != nil {
		// Different filesystems (tmpfs → volume): copy instead.
		if err := copyFile(path, dst); err != nil {
			b.Log.Warn("keep failed input", "err", err)
			return ""
		}
	}
	return dst
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src) //nolint:gosec // our own work file
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o600) //nolint:gosec // dst is built by keepInput
}

// housekeeping prunes old statistics and kept inputs.
func (b *Bot) housekeeping(ctx context.Context) {
	now := time.Now()
	if n, err := b.Store.PruneEvents(ctx, now.Add(-b.Cfg.StatsRetention)); err != nil {
		b.Log.Warn("prune events", "err", err)
	} else if n > 0 {
		b.Log.Info("pruned old events", "count", n)
	}

	inputs, err := b.Store.FailureInputs(ctx)
	if err != nil {
		b.Log.Warn("list failure inputs", "err", err)
		return
	}
	var total int64
	sizes := make([]int64, len(inputs))
	for i, f := range inputs {
		if st, err := os.Stat(f.InputPath); err == nil {
			sizes[i] = st.Size()
			total += st.Size()
		}
	}
	for i, f := range inputs { // oldest first
		if now.Sub(f.At) < b.Cfg.FailedTTL && total <= b.Cfg.FailedMaxBytes {
			break
		}
		_ = os.Remove(f.InputPath)
		total -= sizes[i]
		if err := b.Store.ForgetFailureInput(ctx, f.ID); err != nil {
			b.Log.Warn("forget failure input", "err", err)
		}
	}
	if err := b.Store.PruneFailures(ctx, now.Add(-30*24*time.Hour)); err != nil {
		b.Log.Warn("prune failures", "err", err)
	}
}

// kindLabels name results that came without a file name.
var kindLabels = map[Kind]string{
	KindPhoto: "photo", KindSticker: "sticker", KindVideoSticker: "sticker", KindAnimation: "gif",
	KindVideo: "video", KindVideoNote: "note", KindVoice: "voice", KindAudio: "audio",
	KindImageFile: "image", KindVideoFile: "video",
}

// resultName is a short file name for a result: the original name when
// there was one, otherwise the kind, then the effects: "voice_fry",
// "song_wobble", "photo_bulge+rainbow".
func resultName(it item, recipe media.Recipe) string {
	base := strings.TrimSuffix(it.name, filepath.Ext(it.name))
	base = cleanName(base, 32)
	if base == "" {
		base = kindLabels[it.kind]
	}
	return base + "_" + strings.Join(recipe.IDs(), "+")
}

// cleanName keeps letters, digits, '-' and '+', turns everything else into
// single underscores and cuts the result to n runes.
func cleanName(s string, n int) string {
	var b strings.Builder
	under := false
	count := 0
	for _, r := range s {
		if count == n {
			break
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '+' {
			b.WriteRune(r)
			under = false
			count++
			continue
		}
		if !under && b.Len() > 0 {
			b.WriteByte('_')
			under = true
			count++
		}
	}
	return strings.TrimRight(b.String(), "_")
}

// Classic demotivator lines for photos sent without a caption.
var posterLines = map[i18n.Lang][]media.Demotivator{
	i18n.RU: {
		{Title: "ОЖИДАНИЕ", Subtitle: "реальность оказалась иной"},
		{Title: "ЖИЗНЬ", Subtitle: "она такая"},
		{Title: "ЗАЧЕМ?", Subtitle: "просто зачем"},
		{Title: "МОТИВАЦИЯ", Subtitle: "её нет"},
		{Title: "ПОНЕДЕЛЬНИК", Subtitle: "опять"},
		{Title: "УСПЕХ", Subtitle: "когда-нибудь потом"},
	},
	i18n.EN: {
		{Title: "EXPECTATIONS", Subtitle: "reality had other plans"},
		{Title: "LIFE", Subtitle: "it is what it is"},
		{Title: "WHY?", Subtitle: "just why"},
		{Title: "MOTIVATION", Subtitle: "there is none"},
		{Title: "MONDAY", Subtitle: "again"},
		{Title: "SUCCESS", Subtitle: "maybe later"},
	},
}

// demotivatorText takes the poster text from the caption: the first line
// is the title, the rest the subtitle. Without a caption it picks a classic.
func (b *Bot) demotivatorText(caption string, lang i18n.Lang) media.Demotivator {
	var lines []string
	for _, l := range strings.Split(caption, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) > 0 {
		return media.Demotivator{Title: lines[0], Subtitle: strings.Join(lines[1:], " ")}
	}
	pool := posterLines[lang]
	if pool == nil {
		pool = posterLines[i18n.EN]
	}
	b.rndMu.Lock()
	defer b.rndMu.Unlock()
	return pool[b.rnd.IntN(len(pool))]
}

// maxCaption leaves room for the effects line within Telegram's 1024.
const maxCaption = 960

// applied is everything the result got: media and caption effects.
func (t *task) applied() media.Recipe { return append(slices.Clone(t.recipe), t.caption...) }

// captionText applies text effects to a caption; without any it stays.
func (b *Bot) captionText(fx media.Recipe, caption string) string {
	if caption == "" || len(fx) == 0 {
		return caption
	}
	b.rndMu.Lock()
	caption = media.ApplyText(b.rnd, fx, caption)
	b.rndMu.Unlock()
	if runes := []rune(caption); len(runes) > maxCaption {
		caption = string(runes[:maxCaption])
	}
	return caption
}

// recaption answers a pick of caption effects only: the media is sent
// back as is, with the new caption.
func (b *Bot) recaption(ctx context.Context, parts []request) {
	for _, r := range parts {
		it, _, _ := classify(r.source)
		kind := it.kind
		caption := resultCaption(r, r.caption, b.captionText(r.caption, it.caption))
		if err := b.TG.Copy(ctx, r.chatID, r.source.Chat.ID, r.source.ID, r.replyTo, caption, choiceMarkup(r.lang, true)); err != nil {
			b.handleSendError(ctx, r, err)
			b.record(r, kind, store.StatusFailed, 0, nil)
			continue
		}
		b.record(r, kind, store.StatusOK, 0, r.caption)
	}
}
