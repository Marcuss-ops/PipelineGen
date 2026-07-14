// Package job — worker_commands.go (Card 9 Phase 2 SSOT shim, July 2026).
//
// Back-compat type aliases to the canonical kernel/job declarations,
// preserving identity so callers across application/infrastructure
// layers can use either `kerneljob.X` or `job.X` interchangeably.
//
// godlike/06 SSOT: kerneljob.X is canonical; domain/job.X is the shim
// that preserves the previous import-path surface. Identity is
// preserved by Go type aliases — a runtime switch on either name
// type-switches to the same underlying struct.
package job

import kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

type (
	// WorkerCapabilities aliases kerneljob.WorkerCapabilities.
	WorkerCapabilities = kerneljob.WorkerCapabilities
	// RegisterWorkerCommand aliases kerneljob.RegisterWorkerCommand.
	RegisterWorkerCommand = kerneljob.RegisterWorkerCommand
	// ClaimCommand aliases kerneljob.ClaimCommand.
	ClaimCommand = kerneljob.ClaimCommand
	// HeartbeatCommand aliases kerneljob.HeartbeatCommand.
	HeartbeatCommand = kerneljob.HeartbeatCommand
	// RenewCommand aliases kerneljob.RenewCommand.
	RenewCommand = kerneljob.RenewCommand
	// ProgressCommand aliases kerneljob.ProgressCommand.
	ProgressCommand = kerneljob.ProgressCommand
	// CompleteCommand aliases kerneljob.CompleteCommand.
	CompleteCommand = kerneljob.CompleteCommand
	// FailCommand aliases kerneljob.FailCommand.
	FailCommand = kerneljob.FailCommand
)
