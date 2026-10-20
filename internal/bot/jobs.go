package bot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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
	r        request
	kind     Kind
	items    []item
	workDir  string
	inputs   []string
	progress *telegram.Progress
	started  time.Time
}

// submit queues the items as one job: a single item or a whole album.
func (b *Bot) submit(ctx context.Context, r request, kind Kind, items []item) {
	if !b.admit(ctx, r, kind, items) {
		return
	}

	heavy := false
	for _, it := range items {
		heavy = heavy || it.kind.heavy()
	}
	pool, poolName := b.Image, "i"
	if heavy {
		pool, poolName = b.Video, "v"
	}

	t := &task{r: r, kind: kind, items: items}
	if heavy {
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
	var job *jobs.Job
	job = &jobs.Job{
		ChatID: r.chatID,
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
		text := i18n.T(r.lang, "err.queue_full")
		switch {
		case errors.Is(err, jobs.ErrChatLimit):
			text = i18n.T(r.lang, "err.chat_limit", b.Settings.Int(settings.MaxJobsPerChat))
		case errors.Is(err, jobs.ErrClosed):
			text = i18n.T(r.lang, "err.shutdown")
		}
		if t.progress != nil {
			t.progress.Finish(ctx, text)
		} else {
			b.reply(ctx, r, text)
		}
		b.record(r, kind, store.StatusRejected, 0)
		return
	}

	if t.progress == nil {
		return
	}
	if pos, eta, ok := pool.Position(id); ok {
		b.mu.Lock()
		b.tracked[id] = &tracked{pool: pool, poolName: poolName, progress: t.progress, lang: r.lang, userID: r.userID}
		b.mu.Unlock()
		_ = b.TG.EditText(ctx, r.chatID, t.progress.MessageID(), queuedText(r.lang, pos, eta), cancelMarkup(r.lang, poolName, id))
	}
}

func cancelMarkup(lang i18n.Lang, pool string, id uint64) models.ReplyMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: i18n.T(lang, "btn.cancel"), CallbackData: fmt.Sprintf("c:%s:%d", pool, id)},
	}}}
}

func moreMarkup(lang i18n.Lang, strength media.Strength) models.ReplyMarkup {
	return &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: i18n.T(lang, "btn.more"), CallbackData: "r:" + strconv.Itoa(int(strength))},
	}}}
}

// run downloads, distorts and sends the result. It runs on a pool worker.
func (b *Bot) run(ctx context.Context, t *task, poolName string, id uint64) error {
	t.started = time.Now()
	dir, err := os.MkdirTemp(b.Cfg.TempDir, "distortioner-")
	if err != nil {
		return err
	}
	t.workDir = dir

	var markup models.ReplyMarkup
	if t.progress != nil {
		markup = cancelMarkup(t.r.lang, poolName, id)
		t.progress.Update(ctx, i18n.T(t.r.lang, "progress.downloading"), markup)
	}

	outputs := make([]string, len(t.items))
	for i, it := range t.items {
		in := filepath.Join(dir, fmt.Sprintf("in%d%s", i, it.ext))
		if err := b.TG.Download(ctx, it.fileID, in); err != nil {
			return fmt.Errorf("download: %w", err)
		}
		t.inputs = append(t.inputs, in)

		out, err := b.distort(ctx, t, it, in, i, markup)
		if err != nil {
			return err
		}
		outputs[i] = out
	}

	if len(t.items) > 1 {
		return b.sendAlbum(ctx, t, outputs)
	}
	return b.sendResult(ctx, t, t.items[0], outputs[0])
}

func (b *Bot) distort(ctx context.Context, t *task, it item, in string, i int, markup models.ReplyMarkup) (string, error) {
	base := filepath.Join(t.workDir, fmt.Sprintf("out%d", i))
	strength := t.r.strength
	switch it.kind {
	case KindPhoto:
		return base + ".jpg", b.Engine.DistortImage(ctx, in, base+".jpg", strength, false)
	case KindImageFile:
		ext := it.ext
		if ext != ".png" && ext != ".webp" && ext != ".jpg" && ext != ".jpeg" {
			ext = ".png"
		}
		return base + ext, b.Engine.DistortImage(ctx, in, base+ext, strength, false)
	case KindSticker:
		return base + ".webp", b.Engine.DistortImage(ctx, in, base+".webp", strength, true)
	case KindVoice:
		return base + ".ogg", b.Engine.DistortAudio(ctx, in, base+".ogg", strength)
	case KindAudio:
		return base + ".m4a", b.Engine.DistortAudio(ctx, in, base+".m4a", strength)
	}

	vk := map[Kind]media.VideoKind{
		KindVideo: media.KindVideo, KindVideoFile: media.KindVideo, KindAnimation: media.KindAnimation,
		KindVideoNote: media.KindVideoNote, KindVideoSticker: media.KindSticker,
	}[it.kind]
	out := base + ".mp4"
	if vk == media.KindSticker {
		out = base + ".webm"
	}
	frames := filepath.Join(t.workDir, fmt.Sprintf("work%d", i))
	if err := os.Mkdir(frames, 0o700); err != nil {
		return "", err
	}
	err := b.Engine.DistortVideo(ctx, in, out, media.VideoOptions{
		Kind:         vk,
		Strength:     strength,
		MaxSeconds:   b.Settings.Int(settings.MaxVideoSeconds),
		DistortAudio: t.r.prefs.DistortAudio,
		WorkDir:      frames,
		Progress: func(stage media.Stage, done, total int) {
			if t.progress != nil {
				t.progress.Update(ctx, stageText(t.r.lang, stage, done, total), markup)
			}
		},
	})
	// Frames can take hundreds of megabytes; free them before uploading.
	_ = os.RemoveAll(frames)
	return out, err
}

func stageText(lang i18n.Lang, stage media.Stage, done, total int) string {
	switch stage {
	case media.StageExtracting:
		return i18n.T(lang, "progress.extracting")
	case media.StageDistorting:
		pct := 0
		if total > 0 {
			pct = done * 100 / total
		}
		return i18n.T(lang, "progress.distorting", progressBar(pct), pct)
	default:
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
	if it.caption != "" {
		caption = media.DistortText(it.caption)
	}
	reply := telegram.ReplyTo(r.replyTo)
	markup := moreMarkup(r.lang, r.strength)

	_, err := telegram.SendFile(ctx, out, func(ctx context.Context, f *models.InputFileUpload) (*models.Message, error) {
		switch it.kind {
		case KindPhoto:
			return b.TG.SendPhoto(ctx, &tgbot.SendPhotoParams{ChatID: r.chatID, Photo: f, Caption: caption, ReplyParameters: reply, ReplyMarkup: markup})
		case KindSticker, KindVideoSticker:
			return b.TG.SendSticker(ctx, &tgbot.SendStickerParams{ChatID: r.chatID, Sticker: f, ReplyParameters: reply, ReplyMarkup: markup})
		case KindAnimation:
			return b.TG.SendAnimation(ctx, &tgbot.SendAnimationParams{ChatID: r.chatID, Animation: f, Caption: caption, ReplyParameters: reply, ReplyMarkup: markup})
		case KindVideo, KindVideoFile:
			return b.TG.SendVideo(ctx, &tgbot.SendVideoParams{ChatID: r.chatID, Video: f, Caption: caption, SupportsStreaming: true, ReplyParameters: reply, ReplyMarkup: markup})
		case KindVideoNote:
			return b.TG.SendVideoNote(ctx, &tgbot.SendVideoNoteParams{ChatID: r.chatID, VideoNote: f, ReplyParameters: reply, ReplyMarkup: markup})
		case KindVoice:
			return b.TG.SendVoice(ctx, &tgbot.SendVoiceParams{ChatID: r.chatID, Voice: f, Caption: caption, ReplyParameters: reply, ReplyMarkup: markup})
		case KindAudio:
			return b.TG.SendAudio(ctx, &tgbot.SendAudioParams{ChatID: r.chatID, Audio: f, Caption: caption, ReplyParameters: reply, ReplyMarkup: markup})
		default: // KindImageFile
			return b.TG.SendDocument(ctx, &tgbot.SendDocumentParams{ChatID: r.chatID, Document: f, Caption: caption, ReplyParameters: reply, ReplyMarkup: markup})
		}
	})
	return err
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
			name := filepath.Base(out)
			caption := ""
			if t.items[i].caption != "" {
				caption = media.DistortText(t.items[i].caption)
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
		b.record(r, t.kind, store.StatusOK, time.Since(t.started))
		return
	}

	if telegram.IsBlocked(err) || telegram.IsNoRights(err) {
		b.handleSendError(ctx, r, err)
		if t.progress != nil {
			t.progress.Delete(ctx)
		}
		b.record(r, t.kind, store.StatusFailed, 0)
		return
	}

	text := errorText(r.lang, err)
	if t.progress != nil {
		t.progress.Finish(ctx, text)
	} else {
		b.reply(ctx, r, text)
	}

	if userError(err) {
		b.record(r, t.kind, store.StatusRejected, 0)
		return
	}
	b.record(r, t.kind, store.StatusFailed, 0)
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
