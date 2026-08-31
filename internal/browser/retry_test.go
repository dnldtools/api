package browser

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRetrySucceedsFirstAttempt(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 3, Backoff: time.Millisecond}
	calls := 0

	err := Retry(context.Background(), cfg, func(attempt int) error {
		calls++
		return nil
	})

	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestRetryRetriesUntilSuccess(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 5, Backoff: time.Millisecond}
	calls := 0

	err := Retry(context.Background(), cfg, func(attempt int) error {
		calls++
		if attempt < 3 {
			return errors.New("boom")
		}
		return nil
	})

	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestRetryStopsAfterMaxAttempts(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 3, Backoff: time.Millisecond}
	calls := 0

	err := Retry(context.Background(), cfg, func(attempt int) error {
		calls++
		return errors.New("boom")
	})

	if err == nil {
		t.Fatal("expected error after max attempts")
	}
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestRetryRespectsContextCancellation(t *testing.T) {
	cfg := RetryConfig{MaxAttempts: 100, Backoff: 50 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	err := Retry(ctx, cfg, func(attempt int) error {
		return errors.New("boom")
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
