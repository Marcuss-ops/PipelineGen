package runner

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/contract"
)

// ErrMediaPreflight identifies a fail-closed media requirement failure.
var ErrMediaPreflight = errors.New("scriptgeneration: media preflight failed")

// MediaPreflightError carries the complete structured preflight result.
type MediaPreflightError struct {
	Result PreflightResult
}

func (e *MediaPreflightError) Error() string {
	if e == nil {
		return ErrMediaPreflight.Error()
	}
	if message := e.Result.Error(); message != "" {
		return fmt.Sprintf("%s: %s", ErrMediaPreflight, message)
	}
	return ErrMediaPreflight.Error()
}

func (e *MediaPreflightError) Unwrap() error { return ErrMediaPreflight }

// AsError converts a failed result to the canonical typed error.
func (r PreflightResult) AsError() error {
	if !r.HasFailures() {
		return nil
	}
	return &MediaPreflightError{Result: r}
}

// PreflightResult carries every failure found during the media preflight.
type PreflightResult struct {
	Failures []PreflightFailure
	WallMS   int64
}

// HasFailures returns true when at least one check failed.
func (r PreflightResult) HasFailures() bool { return len(r.Failures) > 0 }

// Error returns all failures joined by newlines.
func (r PreflightResult) Error() string {
	if len(r.Failures) == 0 {
		return ""
	}
	parts := make([]string, len(r.Failures))
	for i, f := range r.Failures {
		parts[i] = f.Error()
	}
	return strings.Join(parts, "\n")
}

// PreflightFailure is one discrete media requirement check failure.
type PreflightFailure struct {
	Category string
	AssetID  string
	Detail   string
}

func (f PreflightFailure) Error() string {
	if f.AssetID != "" {
		return fmt.Sprintf("[%s] %s: %s", f.Category, f.AssetID, f.Detail)
	}
	return fmt.Sprintf("[%s] %s", f.Category, f.Detail)
}

// MediaPreflight is the optional fail-fast media requirement verification
// port (P0.5). When wired, the runner executes it synchronously after
// normalization and before scene-text generation.
type MediaPreflight interface {
	Run(ctx context.Context, req contract.GenerateRequest) PreflightResult
}

// VidRushProviderAvailabilityPreflight rechecks the static provider registry
// when a durable run resumes after MEDIA_PREFLIGHT.
type VidRushProviderAvailabilityPreflight interface {
	RunVidRushProviderAvailability(ctx context.Context, req contract.GenerateRequest) PreflightResult
}
