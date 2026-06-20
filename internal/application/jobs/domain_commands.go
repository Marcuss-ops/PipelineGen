// Package jobs — cross-layer Worker command types. Every command struct here
// is a thin type alias pointing at the canonical definition in
// internal/domain/job (added in ondata-5 stage 10). This preserves package
// name `jobs` (`jobs.RegisterWorkerCommand`) for in-package callers while
// letting downstream consumers (`job.RegisterWorkerCommand` in internal/domain
// or internal/infrastructure) see the same struct.
package jobs

import (
	job "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// WorkerCapabilities is the worker registration payload stored by the
// authoritative API.
type WorkerCapabilities = job.WorkerCapabilities

type RegisterWorkerCommand = job.RegisterWorkerCommand
type ClaimCommand = job.ClaimCommand
type HeartbeatCommand = job.HeartbeatCommand
type RenewCommand = job.RenewCommand
type ProgressCommand = job.ProgressCommand
type CompleteCommand = job.CompleteCommand
type FailCommand = job.FailCommand
