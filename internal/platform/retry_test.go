package platform

import (
	"context"
	"errors"
	"testing"
	"time"
)

var errFake = errors.New("fake error")

func TestDo_Success(t *testing.T) {
	attempts := 0
	err := Do(context.Background(), func() error {
		attempts++
		return nil
	}, DefaultRetryOptions())
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", attempts)
	}
}

func TestDo_FailureThenSuccess(t *testing.T) {
	attempts := 0
	err := Do(context.Background(), func() error {
		attempts++
		if attempts < 3 {
			return errFake
		}
		return nil
	}, RetryOptions{MaxAttempts: 5, InitialBackoff: 1 * time.Millisecond})
	if err != nil {
		t.Errorf("expected nil after retries, got %v", err)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestDo_AllFailures(t *testing.T) {
	attempts := 0
	err := Do(context.Background(), func() error {
		attempts++
		return errFake
	}, RetryOptions{MaxAttempts: 3, InitialBackoff: 1 * time.Millisecond})
	if err == nil {
		t.Error("expected error after all retries exhausted")
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestDo_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Do(ctx, func() error {
		return errFake
	}, RetryOptions{MaxAttempts: 5, InitialBackoff: 1 * time.Hour})
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

func TestDo_NonRetryableError(t *testing.T) {
	nonRetryable := errors.New("non-retryable")
	attempts := 0
	err := Do(context.Background(), func() error {
		attempts++
		return nonRetryable
	}, RetryOptions{
		MaxAttempts:    5,
		InitialBackoff: 1 * time.Millisecond,
		IsRetryable: func(err error) bool {
			return err == errFake
		},
	})
	if err != nonRetryable {
		t.Errorf("expected non-retryable error, got %v", err)
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt (non-retryable), got %d", attempts)
	}
}

func TestDo_ContextCancelledDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	t0 := time.Now()
	err := Do(ctx, func() error {
		return errFake
	}, RetryOptions{MaxAttempts: 10, InitialBackoff: 5 * time.Second})
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if time.Since(t0) > 500*time.Millisecond {
		t.Errorf("expected early cancellation, took %v", time.Since(t0))
	}
}

func TestDoWithValue_Success(t *testing.T) {
	val, err := DoWithValue(context.Background(), func() (string, error) {
		return "hello", nil
	}, DefaultRetryOptions())
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if val != "hello" {
		t.Errorf("expected 'hello', got %q", val)
	}
}

func TestDoWithValue_Retry(t *testing.T) {
	attempts := 0
	val, err := DoWithValue(context.Background(), func() (int, error) {
		attempts++
		if attempts < 3 {
			return 0, errFake
		}
		return 42, nil
	}, RetryOptions{MaxAttempts: 5, InitialBackoff: 1 * time.Millisecond})
	if err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if val != 42 {
		t.Errorf("expected 42, got %d", val)
	}
}

func TestDoWithValue_AllFailures(t *testing.T) {
	val, err := DoWithValue(context.Background(), func() (int, error) {
		return 0, errFake
	}, RetryOptions{MaxAttempts: 3, InitialBackoff: 1 * time.Millisecond})
	if err == nil {
		t.Error("expected error")
	}
	if val != 0 {
		t.Errorf("expected zero value, got %d", val)
	}
}

func TestDefaultRetryOptions(t *testing.T) {
	opts := DefaultRetryOptions()
	if opts.MaxAttempts != 3 {
		t.Errorf("expected MaxAttempts=3, got %d", opts.MaxAttempts)
	}
	if opts.InitialBackoff != 1*time.Second {
		t.Errorf("expected InitialBackoff=1s, got %v", opts.InitialBackoff)
	}
	if opts.MaxBackoff != 30*time.Second {
		t.Errorf("expected MaxBackoff=30s, got %v", opts.MaxBackoff)
	}
	if opts.BackoffFactor != 2.0 {
		t.Errorf("expected BackoffFactor=2.0, got %f", opts.BackoffFactor)
	}
}

func TestNorm_Defaults(t *testing.T) {
	opts := norm(RetryOptions{})
	if opts.MaxAttempts != 3 {
		t.Errorf("expected MaxAttempts=3, got %d", opts.MaxAttempts)
	}
	if opts.InitialBackoff != 1*time.Second {
		t.Errorf("expected InitialBackoff=1s, got %v", opts.InitialBackoff)
	}
	if opts.MaxBackoff != 30*time.Second {
		t.Errorf("expected MaxBackoff=30s, got %v", opts.MaxBackoff)
	}
	if opts.BackoffFactor != 2.0 {
		t.Errorf("expected BackoffFactor=2.0, got %f", opts.BackoffFactor)
	}
}

func TestNorm_PreservesValues(t *testing.T) {
	opts := norm(RetryOptions{
		MaxAttempts:    5,
		InitialBackoff: 5 * time.Second,
		MaxBackoff:     60 * time.Second,
		BackoffFactor:  1.5,
	})
	if opts.MaxAttempts != 5 {
		t.Errorf("expected MaxAttempts=5, got %d", opts.MaxAttempts)
	}
	if opts.InitialBackoff != 5*time.Second {
		t.Errorf("expected InitialBackoff=5s, got %v", opts.InitialBackoff)
	}
	if opts.MaxBackoff != 60*time.Second {
		t.Errorf("expected MaxBackoff=60s, got %v", opts.MaxBackoff)
	}
	if opts.BackoffFactor != 1.5 {
		t.Errorf("expected BackoffFactor=1.5, got %f", opts.BackoffFactor)
	}
}

func TestSleepDuration(t *testing.T) {
	opts := RetryOptions{
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     30 * time.Second,
		BackoffFactor:  2.0,
	}
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 1 * time.Second},
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 16 * time.Second},
		{5, 30 * time.Second},
		{10, 30 * time.Second},
	}
	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := sleepDuration(tt.attempt, opts)
			if got != tt.want {
				t.Errorf("sleepDuration(%d) = %v, want %v", tt.attempt, got, tt.want)
			}
		})
	}
}

func TestSleepDuration_CustomFactor(t *testing.T) {
	opts := RetryOptions{
		InitialBackoff: 1 * time.Second,
		MaxBackoff:     100 * time.Second,
		BackoffFactor:  3.0,
	}
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{0, 1 * time.Second},
		{1, 3 * time.Second},
		{2, 9 * time.Second},
		{3, 27 * time.Second},
		{4, 81 * time.Second},
		{5, 100 * time.Second},
	}
	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := sleepDuration(tt.attempt, opts)
			if got != tt.want {
				t.Errorf("sleepDuration(%d) = %v, want %v", tt.attempt, got, tt.want)
			}
		})
	}
}
