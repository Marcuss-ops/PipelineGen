// Package jobs — enqueue_validate.go: input validation + payload size limit.
//
// PR-jobs-retry-contract (July 2026): the strict typed-error contract on
// Enqueue's input boundary is the canonical SSOT for validation errors.
// validateEnqueueRequest rejects nil-request, empty-type, negative-priority,
// and sub--1 max-retries before any business logic runs — fail-closed at
// the request boundary per godlike/07.
//
// 2026-07-06 (Phase 1 decomposition): split from enqueue_service.go per
// the god-object decomposition plan. Zero behavior changes. Same-package
// visibility preserves all caller paths; Enqueue calls validateEnqueueRequest
// as a package function with no import changes.
package queue

import (
	"fmt"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// MaxPayloadSize is the maximum allowed size for a serialized job payload in bytes.
const MaxPayloadSize = 1 << 20 // 1 MB

// validateEnqueueRequest checks the domain EnqueueRequest for common errors.
func validateEnqueueRequest(req *job.EnqueueRequest) error {
	if req == nil {
		return fmt.Errorf("enqueue request is nil")
	}
	if req.Type == "" {
		return fmt.Errorf("job type is required")
	}
	if req.Priority < 0 {
		return fmt.Errorf("priority must be non-negative, got %d", req.Priority)
	}
	if req.MaxRetries < -1 {
		return fmt.Errorf("max_retries must be >= -1, got %d", req.MaxRetries)
	}
	return nil
}
