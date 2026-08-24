// Package jobs — enqueue_id.go: job identity generation.
//
// 2026-07-06 (Phase 1 decomposition): split from enqueue_service.go per
// the god-object decomposition plan. generateJobID is the canonical SSOT
// for job ID derivation (timestamp + random suffix). Zero behavior changes.
// Same-package visibility preserves all caller paths; Enqueue calls
// generateJobID() as a package function with no import changes.
package jobs

import (
	"fmt"
	"time"

	hashutil "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// generateJobID creates a unique job ID based on timestamp + random suffix.
func generateJobID() string {
	return fmt.Sprintf("job_%d_%s", time.Now().UnixNano(), hashutil.RandomString(8))
}
