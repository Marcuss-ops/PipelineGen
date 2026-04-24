package channelmonitor

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"velox/go-master/pkg/config"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

type ErrorKind int

const (
	KindRetryable ErrorKind = iota
	KindFatal
)

func (k ErrorKind) String() string {
	switch k {
	case KindRetryable:
		return "retryable"
	case KindFatal:
		return "fatal"
	default:
		return "unknown"
	}
}

type RetryableError struct {
	Err  error
	Kind ErrorKind
}

func (e *RetryableError) Error() string {
	return e.Err.Error()
}

func (e *RetryableError) Unwrap() error {
	return e.Err
}

func ClassifyError(err error) ErrorKind {
	if err == nil {
		return KindFatal
	}

	errMsg := err.Error()

	if isHTTPError(err) {
		code := extractHTTPStatus(err)
		switch code {
		case http.StatusTooManyRequests, http.StatusForbidden:
			return KindRetryable
		case http.StatusNotFound, http.StatusGone:
			return KindFatal
		case http.StatusInternalServerError, http.StatusBadGateway,
			http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return KindRetryable
		}
	}

	if isTimeout(err) || isRateLimit(err) || isNetwork(err) {
		return KindRetryable
	}

	fatalKeywords := []string{"copyright", "blocked", "unavailable", "private video"}
	for _, kw := range fatalKeywords {
		if strings.Contains(errMsg, kw) {
			return KindFatal
		}
	}

	return KindRetryable
}

func isHTTPError(err error) bool {
	return contains(err.Error(), "status") && contains(err.Error(), "400|401|403|404|429|500|502|503|504")
}

func isTimeout(err error) bool {
	return contains(err.Error(), "timeout")
}

func isRateLimit(err error) bool {
	return contains(err.Error(), "rate limit") || contains(err.Error(), "429")
}

func isNetwork(err error) bool {
	return contains(err.Error(), "connection refused") ||
		contains(err.Error(), "network") ||
		contains(err.Error(), "temporary")
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func extractHTTPStatus(err error) int {
	s := err.Error()
	for i := 0; i < len(s)-3; i++ {
		if s[i] >= '4' && s[i] <= '5' && s[i+1] >= '0' && s[i+1] <= '9' {
			return int((s[i]-'0')*100 + (s[i+1]-'0')*10 + (s[i+2] - '0'))
		}
	}
	return 0
}

type RetryConfig struct {
	MaxRetries int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
	Jitter     bool
}

func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries: 3,
		BaseDelay:  2 * time.Second,
		MaxDelay:   32 * time.Second,
		Jitter:     true,
	}
}

func WithRetry(ctx context.Context, cfg RetryConfig, fn func() error) error {
	var lastErr error
	attempt := 0

	for {
		err := fn()
		if err == nil {
			return nil
		}

		kind := ClassifyError(err)
		if kind == KindFatal {
			return err
		}

		attempt++
		if attempt > cfg.MaxRetries {
			logger.Warn("Max retries exceeded",
				zap.Int("attempts", attempt),
				zap.Error(lastErr),
			)
			return lastErr
		}

		delay := cfg.BaseDelay * time.Duration(1<<(attempt-1))
		if delay > cfg.MaxDelay {
			delay = cfg.MaxDelay
		}

		if cfg.Jitter {
			jitter := time.Duration(rand.Int63n(int64(delay / 2)))
			delay = delay/2 + jitter
		}

		logger.Info("Retrying after error",
			zap.Int("attempt", attempt),
			zap.Duration("delay", delay),
			zap.String("kind", kind.String()),
			zap.Error(err),
		)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}

		lastErr = err
	}
}

type FailedClipRecord struct {
	VideoID    string    `json:"video_id"`
	Title      string    `json:"title"`
	ChannelURL string    `json:"channel_url"`
	Error      string    `json:"error"`
	ErrorKind  string    `json:"error_kind"`
	Attempts   int       `json:"attempts"`
	FailedAt   time.Time `json:"failed_at"`
	VideoURL   string    `json:"video_url,omitempty"`
	StartSec   int       `json:"start_sec,omitempty"`
	Duration   int       `json:"duration,omitempty"`
}

type DeadLetterQueue struct {
	mu    sync.Mutex
	file  *os.File
	path  string
	count int
}

func NewDeadLetterQueue(path string) (*DeadLetterQueue, error) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	return &DeadLetterQueue{file: f, path: path}, nil
}

func (q *DeadLetterQueue) Write(record FailedClipRecord) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	data, err := json.Marshal(record)
	if err != nil {
		return err
	}

	if _, err := q.file.Write(append(data, '\n')); err != nil {
		return err
	}

	q.count++
	return nil
}

func (q *DeadLetterQueue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.file.Close()
}

func (q *DeadLetterQueue) Count() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.count
}

func (q *DeadLetterQueue) ReadAll() ([]FailedClipRecord, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if _, err := q.file.Seek(0, 0); err != nil {
		return nil, err
	}

	var records []FailedClipRecord
	dec := json.NewDecoder(q.file)
	for dec.More() {
		var rec FailedClipRecord
		if err := dec.Decode(&rec); err != nil {
			continue
		}
		records = append(records, rec)
	}

	return records, nil
}

func WriteFailedClip(path string, record FailedClipRecord) error {
	q, err := NewDeadLetterQueue(path)
	if err != nil {
		return err
	}
	defer q.Close()
	return q.Write(record)
}

type RetryCtx struct {
	Config   RetryConfig
	DLQPath  string
	VideoID  string
	Title    string
	Channel  string
	VideoURL string
	StartSec int
	Duration int
}

func (m *Monitor) RetryDownload(ctx context.Context, r RetryCtx, fn func() error) error {
	var attempts int
	var lastErr error

	for {
		attempts++
		err := fn()
		if err == nil {
			return nil
		}

		kind := ClassifyError(err)
		if kind == KindFatal {
			m.writeDLQ(r, lastErr, attempts)
			return fmt.Errorf("fatal error after %d attempts: %w", attempts, err)
		}

		if attempts > r.Config.MaxRetries {
			logger.Warn("Max retries exceeded for clip",
				zap.String("video_id", r.VideoID),
				zap.Int("attempts", attempts),
				zap.Error(lastErr),
			)
			m.writeDLQ(r, lastErr, attempts)
			return lastErr
		}

		delay := r.Config.BaseDelay * time.Duration(1<<(attempts-1))
		if delay > r.Config.MaxDelay {
			delay = r.Config.MaxDelay
		}

		logger.Info("Retrying clip download",
			zap.String("video_id", r.VideoID),
			zap.Int("attempt", attempts),
			zap.Duration("delay", delay),
			zap.Error(err),
		)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}

		lastErr = err
	}
}

func (m *Monitor) writeDLQ(r RetryCtx, err error, attempts int) {
	if r.DLQPath == "" {
		r.DLQPath = config.ResolveDataPath("failed_clips.jsonl")
	}

	record := FailedClipRecord{
		VideoID:    r.VideoID,
		Title:      r.Title,
		ChannelURL: r.Channel,
		VideoURL:   r.VideoURL,
		StartSec:   r.StartSec,
		Duration:   r.Duration,
		Error:      err.Error(),
		ErrorKind:  ClassifyError(err).String(),
		Attempts:   attempts,
		FailedAt:   time.Now(),
	}

	if dlqErr := WriteFailedClip(r.DLQPath, record); dlqErr != nil {
		logger.Warn("Failed to write DLQ record",
			zap.String("video_id", r.VideoID),
			zap.Error(dlqErr),
		)
	} else {
		logger.Info("Failed clip written to DLQ",
			zap.String("video_id", r.VideoID),
			zap.Int("attempts", attempts),
		)
	}
}
