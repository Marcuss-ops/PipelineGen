// Package queue owns the dependency-light enqueue policies shared by job
// producers. It depends on kernel job contracts only and never imports the
// root jobs package, which keeps the queue layer below worker orchestration.
package queue

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// MaxPayloadSize is the maximum allowed serialized job payload size.
const MaxPayloadSize = 1 << 20 // 1 MB

// ValidateEnqueueRequest checks the request boundary before idempotency or
// persistence logic runs.
func ValidateEnqueueRequest(req *job.EnqueueRequest) error {
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

// GenerateJobID creates a unique job ID using the existing timestamp-plus-
// random-suffix format. The timestamp fallback keeps ID generation available
// even if the system random source is temporarily unavailable.
func GenerateJobID() string {
	const suffixBytes = 4
	var suffix [suffixBytes]byte
	if _, err := rand.Read(suffix[:]); err == nil {
		return fmt.Sprintf("job_%d_%s", time.Now().UnixNano(), hex.EncodeToString(suffix[:]))
	}
	return fmt.Sprintf("job_%d_%016x", time.Now().UnixNano(), time.Now().UnixNano())
}
