// Package completion — complete_job_ports.go (split July 2026).
//
// This file owns the canonical port interfaces and domain row types
// for the completion service. Extracted from complete_job_service.go
// per AGENTS.md Pattern 5.
//
// godlike/06 SSOT: each port interface is the single canonical owner
// of its contract. TxContext is the single in-transaction port surface.
package completion

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/remote"
)

// ── JobTypeRegistry & CompleteWithArtifactsSender ───────────────────

// CompleteWithArtifactsSender is the Pattern 0 port (godlike/06 SSOT)
// for the Sender-side atomic terminal surface consumed by
// PublishAndCompleteUseCase. The single method mirrors
// WithArtifactsService.CompleteWithArtifacts — the canonical
// post-P0-COMPL-4 dedup-closure surface.
type CompleteWithArtifactsSender interface {
	CompleteWithArtifacts(ctx context.Context, req *remote.CompleteWithArtifactsRequest, published []*finalization.PublishedArtifact) (*remote.CompleteWithArtifactsResponse, error)
}

// Compile-time pin (AGENTS.md Pattern 0): drift in the concrete
// WithArtifactsService.CompleteWithArtifacts signature is a build
// failure (the interface anchor catches signature drift at compile).
var _ CompleteWithArtifactsSender = (*WithArtifactsService)(nil)

// JobTypeRegistry is the typed port for "does this job type produce
// artifacts". godlike/06 SSOT: the application-layer JobRegistry
// (`internal/capabilities/jobs/queue/registry.go::Registry`) is the SINGLE
// canonical owner of this fact — NOT the request envelope, NOT the
// SQL column.
type JobTypeRegistry interface {
	// ProducesArtifacts returns true if jobs of the given type MUST
	// route through CompleteWithArtifacts (and may NOT route through
	// the legacy Complete path).
	ProducesArtifacts(jobType string) bool
}
