package telegram

import (
	"context"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// MinEditInterval keeps progress edits well below Telegram's per-chat limits.
const MinEditInterval = 3 * time.Second

// Progress is a status message that is edited as a job advances.
// Intermediate updates are throttled and best effort; final ones are not.
type Progress struct {
	c      *Client
	chatID int64
	msgID  int

	mu     sync.Mutex
	text   string
	editAt time.Time
	closed bool
}

// StartProgress sends the initial status message.
func (c *Client) StartProgress(ctx context.Context, chatID int64, replyTo int, text string, markup models.ReplyMarkup) (*Progress, error) {
	m, err := c.SendText(ctx, chatID, replyTo, text, markup)
	if err != nil {
		return nil, err
	}
	return &Progress{c: c, chatID: chatID, msgID: m.ID, text: text, editAt: time.Now()}, nil
}

// MessageID returns the id of the status message.
func (p *Progress) MessageID() int { return p.msgID }

// Update edits the message unless it was edited recently or the text is
// unchanged. Flood errors are swallowed: the next update will catch up.
func (p *Progress) Update(ctx context.Context, text string, markup models.ReplyMarkup) {
	p.mu.Lock()
	if p.closed || text == p.text || time.Since(p.editAt) < MinEditInterval {
		p.mu.Unlock()
		return
	}
	p.text, p.editAt = text, time.Now()
	p.mu.Unlock()

	_, _ = p.c.EditMessageText(ctx, &bot.EditMessageTextParams{
		ChatID: p.chatID, MessageID: p.msgID, Text: text,
		ParseMode: models.ParseModeHTML, ReplyMarkup: markup,
	})
}

// Finish replaces the message with a final text (and no buttons).
func (p *Progress) Finish(ctx context.Context, text string) {
	if !p.close() {
		return
	}
	_ = p.c.EditText(ctx, p.chatID, p.msgID, text, nil)
}

// Delete removes the status message.
func (p *Progress) Delete(ctx context.Context) {
	if !p.close() {
		return
	}
	p.c.Delete(ctx, p.chatID, p.msgID)
}

func (p *Progress) close() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false
	}
	p.closed = true
	return true
}
