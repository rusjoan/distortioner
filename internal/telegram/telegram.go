// Package telegram wraps the Bot API client with retries, error
// classification, file downloads and throttled progress messages.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// Client is a thin helper around *bot.Bot.
type Client struct {
	*bot.Bot
	http *http.Client
}

// New wraps b; downloads go through httpClient (nil means a default one).
func New(b *bot.Bot, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Minute}
	}
	return &Client{Bot: b, http: httpClient}
}

const (
	maxRetries    = 3
	maxRetryAfter = time.Minute
)

// Retry calls fn until it succeeds, retrying on flood control (honouring
// retry_after) and on transport errors. API errors are returned as is.
func Retry[T any](ctx context.Context, fn func(context.Context) (T, error)) (T, error) {
	var (
		res T
		err error
	)
	for attempt := 0; ; attempt++ {
		res, err = fn(ctx)
		if err == nil || attempt >= maxRetries {
			return res, err
		}
		var wait time.Duration
		var flood *bot.TooManyRequestsError
		switch {
		case errors.As(err, &flood):
			wait = time.Duration(flood.RetryAfter) * time.Second
			if wait > maxRetryAfter {
				return res, err
			}
		case isTransport(err):
			wait = time.Duration(attempt+1) * time.Second
		default:
			return res, err
		}
		select {
		case <-ctx.Done():
			return res, errors.Join(err, ctx.Err())
		case <-time.After(wait):
		}
	}
}

func isTransport(err error) bool {
	// go-telegram/bot flattens transport errors into strings.
	return strings.Contains(err.Error(), "error do request")
}

// IsBlocked reports that the bot can no longer write to the chat at all:
// the user blocked it, deleted their account or kicked it from the group.
func IsBlocked(err error) bool {
	if !errors.Is(err, bot.ErrorForbidden) {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "blocked") || strings.Contains(msg, "deactivated") ||
		strings.Contains(msg, "kicked") || strings.Contains(msg, "not a member")
}

// IsNoRights reports that the bot is in the chat but may not send this kind of message.
func IsNoRights(err error) bool {
	return err != nil && strings.Contains(err.Error(), "not enough rights")
}

// IsVoiceForbidden reports that the user does not accept voice and video
// messages (a privacy setting), so voices and video notes cannot be sent.
func IsVoiceForbidden(err error) bool {
	return err != nil && strings.Contains(err.Error(), "VOICE_MESSAGES_FORBIDDEN")
}

// IsNotModified reports an edit that did not change anything.
func IsNotModified(err error) bool {
	return err != nil && strings.Contains(err.Error(), "message is not modified")
}

// ReplyTo builds reply parameters that do not fail when the original is gone.
func ReplyTo(messageID int) *models.ReplyParameters {
	if messageID == 0 {
		return nil
	}
	return &models.ReplyParameters{MessageID: messageID, AllowSendingWithoutReply: true}
}

// Upload opens path for an upload. Callers close the file after the request.
func Upload(path string) (*models.InputFileUpload, *os.File, error) {
	f, err := os.Open(path) //nolint:gosec // path is produced by the bot itself
	if err != nil {
		return nil, nil, err
	}
	return &models.InputFileUpload{Filename: filepath.Base(path), Data: f}, f, nil
}

// SendFile retries an upload, reopening the file for every attempt.
func SendFile(ctx context.Context, path string, send func(context.Context, *models.InputFileUpload) (*models.Message, error)) (*models.Message, error) {
	return Retry(ctx, func(ctx context.Context) (*models.Message, error) {
		in, f, err := Upload(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return send(ctx, in)
	})
}

// SendText sends a plain or HTML message.
func (c *Client) SendText(ctx context.Context, chatID int64, replyTo int, text string, markup models.ReplyMarkup) (*models.Message, error) {
	return Retry(ctx, func(ctx context.Context) (*models.Message, error) {
		return c.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:          chatID,
			Text:            text,
			ParseMode:       models.ParseModeHTML,
			ReplyParameters: ReplyTo(replyTo),
			ReplyMarkup:     markup,
			LinkPreviewOptions: &models.LinkPreviewOptions{
				IsDisabled: bot.True(),
			},
		})
	})
}

// Copy sends a copy of a message with a new caption.
func (c *Client) Copy(ctx context.Context, chatID, fromChatID int64, messageID, replyTo int, caption string, markup models.ReplyMarkup) error {
	_, err := Retry(ctx, func(ctx context.Context) (*models.MessageID, error) {
		return c.CopyMessage(ctx, &bot.CopyMessageParams{
			ChatID: chatID, FromChatID: fromChatID, MessageID: messageID, Caption: caption,
			ReplyParameters: ReplyTo(replyTo), ReplyMarkup: markup,
		})
	})
	return err
}

// EditText edits an HTML message; "not modified" is not an error.
func (c *Client) EditText(ctx context.Context, chatID int64, messageID int, text string, markup models.ReplyMarkup) error {
	_, err := Retry(ctx, func(ctx context.Context) (*models.Message, error) {
		return c.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:      chatID,
			MessageID:   messageID,
			Text:        text,
			ParseMode:   models.ParseModeHTML,
			ReplyMarkup: markup,
		})
	})
	if IsNotModified(err) {
		return nil
	}
	return err
}

// EditMarkup replaces the buttons of a message; "not modified" is ignored.
func (c *Client) EditMarkup(ctx context.Context, chatID int64, messageID int, markup models.ReplyMarkup) {
	_, _ = Retry(ctx, func(ctx context.Context) (*models.Message, error) {
		return c.EditMessageReplyMarkup(ctx, &bot.EditMessageReplyMarkupParams{
			ChatID: chatID, MessageID: messageID, ReplyMarkup: markup,
		})
	})
}

// Delete deletes a message, ignoring the result.
func (c *Client) Delete(ctx context.Context, chatID int64, messageID int) {
	_, _ = c.DeleteMessage(ctx, &bot.DeleteMessageParams{ChatID: chatID, MessageID: messageID})
}

// Answer answers a callback query; failures are irrelevant to the user.
func (c *Client) Answer(ctx context.Context, id, text string, alert bool) {
	_, _ = c.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: id, Text: text, ShowAlert: alert,
	})
}

// Download fetches a file by id into dst. It supports both the public Bot
// API and a self-hosted server running with --local, which returns absolute
// file paths instead of serving the file over HTTP.
func (c *Client) Download(ctx context.Context, fileID, dst string) error {
	f, err := Retry(ctx, func(ctx context.Context) (*models.File, error) {
		return c.GetFile(ctx, &bot.GetFileParams{FileID: fileID})
	})
	if err != nil {
		return fmt.Errorf("get file: %w", err)
	}

	out, err := os.Create(dst) //nolint:gosec // dst is inside the job's work dir
	if err != nil {
		return err
	}
	defer out.Close()

	if filepath.IsAbs(f.FilePath) {
		in, err := os.Open(f.FilePath)
		if err != nil {
			return fmt.Errorf("open local file: %w", err)
		}
		defer in.Close()
		_, err = io.Copy(out, in)
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.FileDownloadLink(f), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// The URL contains the token; never let it reach the logs.
		return errors.New("download file: request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download file: status %d", resp.StatusCode)
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("download file: %w", err)
	}
	return out.Close()
}
