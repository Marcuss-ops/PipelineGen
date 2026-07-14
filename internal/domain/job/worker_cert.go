// Package job — worker cert aliases (PR-KERNEL-JOB-POPULATE, July 2026).
//
// The canonical WorkerCertIdentity type now lives in internal/kernel/job.
// This file re-exports it as a transparent alias so existing callers
// that import internal/domain/job continue to compile during the
// Wave 5 contraction window.
package job

import (
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// WorkerCertIdentity is the mTLS certificate identity associated with
// a worker registration.
type WorkerCertIdentity = kerneljob.WorkerCertIdentity
