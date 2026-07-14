// Package job — finalize_aliases.go (July 2026).
//
// Re-export aliases for the canonical kernel/job finalize + lease
// types. These live in the domain/job package so callers that need
// both domain-specific constants and kernel-defined finalize/lease
// shapes can import a single package.
//
// Future code SHOULD import internal/kernel/job directly; these aliases
// are preserved for back-compat during the Wave 5 contraction window.
package job

import (
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── FinalizeAttempt type aliases ─────────────────────────────────────

type (
	// FinalizeAttemptOutcome is the canonical outcome enum for the
	// consolidated terminal-decision primitive.
	FinalizeAttemptOutcome = kerneljob.FinalizeAttemptOutcome

	// ArtifactStatePatch is the canonical artifact-state patch shape.
	ArtifactStatePatch = kerneljob.ArtifactStatePatch

	// OutboxEventSpec is the canonical outbox event shape passed to
	// FinalizeAttempt.
	OutboxEventSpec = kerneljob.OutboxEventSpec

	// FinalizeAttemptCommand is the canonical consolidated terminal-
	// decision command envelope.
	FinalizeAttemptCommand = kerneljob.FinalizeAttemptCommand

	// FinalizeAttemptResult is the canonical post-commit projection
	// returned by FinalizeAttempt.
	FinalizeAttemptResult = kerneljob.FinalizeAttemptResult

	// FinalizeAttemptFn is the canonical function signature for a
	// FinalizeAttempt implementation.
	FinalizeAttemptFn = kerneljob.FinalizeAttemptFn
)

// Outcome constants are re-exported so existing domain/job callers
// continue to compile.
const (
	OutcomeSucceeded       = kerneljob.OutcomeSucceeded
	OutcomeFailedPermanent = kerneljob.OutcomeFailedPermanent
	OutcomeScheduleRetry   = kerneljob.OutcomeScheduleRetry
)

// ── Lease state aliases ──────────────────────────────────────────────

type (
	// LeaseState is the canonical post-renewal lease state enum.
	LeaseState = kerneljob.LeaseState

	// RenewLeaseResult is the canonical post-renewal lease result
	// envelope.
	RenewLeaseResult = kerneljob.RenewLeaseResult
)

// LeaseState constants are re-exported for back-compat.
const (
	LeaseStateContinue        = kerneljob.LeaseStateContinue
	LeaseStateCancelRequested = kerneljob.LeaseStateCancelRequested
	LeaseStateLeaseLost       = kerneljob.LeaseStateLeaseLost
)
