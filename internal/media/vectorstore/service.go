package vectorstore

import (
	"context"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

// Service provides high-level operations over the Store interface.
type Service struct {
	store            Store
	cfg              Config
	log              *zap.Logger
	enabled          bool
	retryAttempts    int
	retryInitialWait time.Duration
	retryMaxWait     time.Duration
}

// NewService creates a new vectorstore service backed by the given Store.
func NewService(store Store, cfg Config, log *zap.Logger) *Service {
	metrics.QdrantHealthStatus.Set(0)
	return &Service{
		store:            store,
		cfg:              cfg,
		log:              log,
		enabled:          true,
		retryAttempts:    3,
		retryInitialWait: 200 * time.Millisecond,
		retryMaxWait:     5 * time.Second,
	}
}

func (s *Service) SetRetryPolicy(attempts int, initialWait, maxWait time.Duration) {
	if attempts > 0 {
		s.retryAttempts = attempts
	}
	if initialWait > 0 {
		s.retryInitialWait = initialWait
	}
	if maxWait > 0 {
		s.retryMaxWait = maxWait
	}
}

var transientHTTPStatusRe = regexp.MustCompile(`status[\s:]*(5\d\d|429)`)

func isTransientQdrantErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if msg == "" {
		return false
	}
	if transientHTTPStatusRe.MatchString(msg) {
		return true
	}
	transientSubstrings := []string{
		"timeout", "timed out", "connection reset", "connection refused",
		"broken pipe", "no such host", "i/o timeout", "temporarily unavailable",
		"server error", "internal server", "bad gateway", "service unavailable",
		"gateway timeout", "rate limit", "too many requests",
		"collection not found", "not ready",
	}
	for _, sub := range transientSubstrings {
		if strings.Contains(msg, sub) {
			return true
		}
	}
	return false
}

func (s *Service) retryQdrantCall(ctx context.Context, op string, fn func() error) error {
	var lastErr error
	wait := s.retryInitialWait
	for attempt := 1; attempt <= s.retryAttempts; attempt++ {
		err := fn()
		if err == nil {
			return nil
		}
		lastErr = err
		if !isTransientQdrantErr(err) {
			return err
		}
		if attempt == s.retryAttempts {
			break
		}
		s.log.Warn("transient Qdrant error, retrying", zap.String("op", op), zap.Int("attempt", attempt), zap.Duration("backoff", wait), zap.Error(err))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		wait *= 2
		if wait > s.retryMaxWait {
			wait = s.retryMaxWait
		}
	}
	return lastErr
}

func (s *Service) retryQdrantCallValue(ctx context.Context, op string, fn func() ([]SearchResult, error)) ([]SearchResult, error) {
	var (
		results []SearchResult
		lastErr error
	)
	wait := s.retryInitialWait
	for attempt := 1; attempt <= s.retryAttempts; attempt++ {
		results, lastErr = fn()
		if lastErr == nil {
			return results, nil
		}
		if !isTransientQdrantErr(lastErr) {
			return results, lastErr
		}
		if attempt == s.retryAttempts {
			break
		}
		s.log.Warn("transient Qdrant error, retrying", zap.String("op", op), zap.Int("attempt", attempt), zap.Duration("backoff", wait), zap.Error(lastErr))
		select {
		case <-ctx.Done():
			return results, ctx.Err()
		case <-time.After(wait):
		}
		wait *= 2
		if wait > s.retryMaxWait {
			wait = s.retryMaxWait
		}
	}
	return results, lastErr
}

func (s *Service) Enabled() bool {
	return s.enabled
}

func (s *Service) Close() error {
	if s.store != nil {
		return s.store.Close()
	}
	return nil
}

const (
	CurrentEmbeddingVersion  = "e5-base-2026-06-03"
	CurrentSearchTextVersion = "youtube_v2_structured"
)
