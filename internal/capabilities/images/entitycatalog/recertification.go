package entitycatalog

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"
)

const (
	DefaultRecertificationBatchSize      = 100
	DefaultRecertificationMaxAttempts    = 5
	DefaultRecertificationInitialBackoff = time.Hour
	DefaultRecertificationMaxBackoff     = 24 * time.Hour
)

var ErrRecertificationRepositoryUnavailable = errors.New("entity image catalog: recertification repository unavailable")

// RecertificationCandidate is a candidate plus durable retry metadata. The
// Materialization field is informational: recertification never updates it.
type RecertificationCandidate struct {
	Candidate
	FailureCount        int
	LastValidationAt    time.Time
	NextRetryAt         time.Time
	LastValidationError string
	Materialization     *Materialization
}

// RecertificationRepository is optional so existing catalog consumers and
// test repositories remain source-compatible. SQLite implements this port
// after migration 228.
type RecertificationRepository interface {
	ListCandidatesForRecertification(context.Context, time.Time, int, int) ([]RecertificationCandidate, error)
	RecordCandidateValidation(context.Context, int64, ValidationResult) error
}

// ValidationResult is the durable outcome of one remote URL validation.
type ValidationResult struct {
	CheckedAt    time.Time
	Success      bool
	Error        string
	FailureCount int
	NextRetryAt  time.Time
}

// ImageCandidateValidator performs only remote technical validation. It does
// not download an asset into the materialization pipeline and therefore cannot
// invalidate an existing Drive asset.
type ImageCandidateValidator interface {
	Validate(context.Context, string) error
}

type RecertificationConfig struct {
	BatchSize      int
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	Interval       time.Duration
	InitialDelay   time.Duration
}

func DefaultRecertificationConfig() RecertificationConfig {
	return RecertificationConfig{
		BatchSize:      DefaultRecertificationBatchSize,
		MaxAttempts:    DefaultRecertificationMaxAttempts,
		InitialBackoff: DefaultRecertificationInitialBackoff,
		MaxBackoff:     DefaultRecertificationMaxBackoff,
		Interval:       24 * time.Hour,
		InitialDelay:   time.Minute,
	}
}

func (c RecertificationConfig) normalized() RecertificationConfig {
	defaults := DefaultRecertificationConfig()
	if c.BatchSize <= 0 {
		c.BatchSize = defaults.BatchSize
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = defaults.MaxAttempts
	}
	if c.InitialBackoff <= 0 {
		c.InitialBackoff = defaults.InitialBackoff
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = defaults.MaxBackoff
	}
	if c.MaxBackoff < c.InitialBackoff {
		c.MaxBackoff = c.InitialBackoff
	}
	if c.Interval <= 0 {
		c.Interval = defaults.Interval
	}
	if c.InitialDelay < 0 {
		c.InitialDelay = defaults.InitialDelay
	}
	return c
}

// RecertificationReport describes one bounded run.
type RecertificationReport struct {
	Selected  int
	Succeeded int
	Failed    int
	Skipped   int
}

type RecertificationService struct {
	repo      Repository
	validator ImageCandidateValidator
	config    RecertificationConfig
	now       func() time.Time
}

func NewRecertificationService(repo Repository, validator ImageCandidateValidator, config RecertificationConfig) *RecertificationService {
	return &RecertificationService{repo: repo, validator: validator, config: config.normalized(), now: func() time.Time { return time.Now().UTC() }}
}

// RunOnce validates at most BatchSize candidates selected by the repository.
// A successful validation refreshes only the remote candidate timestamp. A
// failure records bounded retry metadata and marks the URL broken. No call to
// UpsertMaterialization or deletion of Drive metadata occurs here.
func (s *RecertificationService) RunOnce(ctx context.Context) (RecertificationReport, error) {
	if s == nil || s.repo == nil || s.validator == nil {
		return RecertificationReport{}, ErrRecertificationRepositoryUnavailable
	}
	repo, ok := s.repo.(RecertificationRepository)
	if !ok {
		return RecertificationReport{}, ErrRecertificationRepositoryUnavailable
	}
	now := s.now().UTC()
	candidates, err := repo.ListCandidatesForRecertification(ctx, now, s.config.BatchSize, s.config.MaxAttempts)
	if err != nil {
		return RecertificationReport{}, err
	}
	report := RecertificationReport{Selected: len(candidates)}
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		result := ValidationResult{CheckedAt: now, Success: false, FailureCount: candidate.FailureCount}
		err := s.validator.Validate(ctx, candidate.SourceURL)
		if err == nil {
			result.Success = true
			result.FailureCount = 0
		} else {
			result.FailureCount = candidate.FailureCount + 1
			result.Error = truncateValidationError(err)
			if result.FailureCount < s.config.MaxAttempts {
				result.NextRetryAt = now.Add(s.retryBackoff(result.FailureCount))
			}
		}
		if err := repo.RecordCandidateValidation(ctx, candidate.ID, result); err != nil {
			return report, fmt.Errorf("record candidate %d validation: %w", candidate.ID, err)
		}
		if result.Success {
			report.Succeeded++
		} else {
			report.Failed++
		}
	}
	return report, nil
}

// Run starts the periodic maintenance loop. It performs no work before the
// configured initial delay, then executes bounded runs at Interval until ctx
// is cancelled. RunOnce remains the deterministic operator/test entry point.
func (s *RecertificationService) Run(ctx context.Context, onRun func(RecertificationReport, error)) {
	if s == nil {
		return
	}
	config := s.config.normalized()
	timer := time.NewTimer(config.InitialDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}
	ticker := time.NewTicker(config.Interval)
	defer ticker.Stop()
	run := func() {
		report, err := s.RunOnce(ctx)
		if onRun != nil {
			onRun(report, err)
		}
	}
	run()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func (s *RecertificationService) retryBackoff(failureCount int) time.Duration {
	if failureCount <= 1 {
		return s.config.InitialBackoff
	}
	power := math.Pow(2, float64(failureCount-1))
	backoff := time.Duration(float64(s.config.InitialBackoff) * power)
	if backoff > s.config.MaxBackoff || backoff < s.config.InitialBackoff {
		return s.config.MaxBackoff
	}
	return backoff
}

func truncateValidationError(err error) string {
	if err == nil {
		return ""
	}
	value := strings.TrimSpace(err.Error())
	if len(value) > 1000 {
		return value[:1000]
	}
	return value
}

// HTTPImageCandidateValidator validates status, decodability and minimum
// dimensions. The standard Go decoder set intentionally rejects unsupported
// formats such as WebP until an explicit decoder is installed.
type HTTPImageCandidateValidator struct {
	client *http.Client
}

func NewHTTPImageCandidateValidator(client *http.Client) *HTTPImageCandidateValidator {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &HTTPImageCandidateValidator{client: client}
}

func (v *HTTPImageCandidateValidator) Validate(ctx context.Context, sourceURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	config, _, err := decodeImageConfig(resp.Body)
	if err != nil {
		return fmt.Errorf("decode image: %w", err)
	}
	longSide := config.Width
	if config.Height > longSide {
		longSide = config.Height
	}
	if longSide < 800 || config.Height < 1 || config.Width*config.Height < 400000 {
		return fmt.Errorf("image dimensions %dx%d below catalog minimum", config.Width, config.Height)
	}
	return nil
}
