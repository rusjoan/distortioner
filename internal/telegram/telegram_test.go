package telegram

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/go-telegram/bot"
)

func TestRetryHonoursFloodControl(t *testing.T) {
	calls := 0
	got, err := Retry(context.Background(), func(context.Context) (int, error) {
		calls++
		if calls < 3 {
			return 0, &bot.TooManyRequestsError{RetryAfter: 0}
		}
		return 42, nil
	})
	if err != nil || got != 42 || calls != 3 {
		t.Fatalf("got %d, err %v after %d calls", got, err, calls)
	}
}

func TestRetryStopsOnAPIError(t *testing.T) {
	calls := 0
	_, err := Retry(context.Background(), func(context.Context) (int, error) {
		calls++
		return 0, fmt.Errorf("%w, Bad Request: chat not found", bot.ErrorBadRequest)
	})
	if err == nil || calls != 1 {
		t.Fatalf("calls %d err %v", calls, err)
	}
}

func TestRetryGivesUpOnLongFlood(t *testing.T) {
	calls := 0
	_, err := Retry(context.Background(), func(context.Context) (int, error) {
		calls++
		return 0, &bot.TooManyRequestsError{RetryAfter: 3600}
	})
	if err == nil || calls != 1 {
		t.Fatalf("calls %d err %v", calls, err)
	}
}

func TestClassification(t *testing.T) {
	blocked := fmt.Errorf("%w, Forbidden: bot was blocked by the user", bot.ErrorForbidden)
	kicked := fmt.Errorf("%w, Forbidden: bot was kicked from the supergroup chat", bot.ErrorForbidden)
	rights := fmt.Errorf("%w, Bad Request: not enough rights to send photos to the chat", bot.ErrorBadRequest)
	other := errors.New("boom")

	if !IsBlocked(blocked) || !IsBlocked(kicked) || IsBlocked(rights) || IsBlocked(other) {
		t.Fatal("IsBlocked misclassifies")
	}
	if !IsNoRights(rights) || IsNoRights(blocked) || IsNoRights(nil) {
		t.Fatal("IsNoRights misclassifies")
	}
}
