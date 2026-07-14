// Package job — ExecutionResult alias (PR-KERNEL-JOB-POPULATE, July 2026).
//
// The canonical ExecutionResult type now lives in internal/kernel/job.
// This file re-exports it as a transparent alias so existing callers
// that import internal/domain/job continue to compile during the
// Wave 5 contraction window.
package job

import (
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ExecutionResult is the canonical dual-shape envelope for handler
// results that carry both a typed Result payload and an
// ArtifactManifest sidecar for the Sender-side upload cycle.
type ExecutionResult[T any] = kerneljob.ExecutionResult[T]
