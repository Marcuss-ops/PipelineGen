// Package integrity — PR-INTEGRITY-VERIFIER-EXTRACT (July 2026).
//
// IntegrityVerifier is the canonical post-commit verification port.
// It is registered as a distinct job in jobs.Registry
// (`jobs.TypeIntegrityVerify`) and is invoked AFTER the AssetCommitter's
// Commit step — never by the coordinator itself. The coordinator
// (asset processing.Orchestrator) enqueues the verification job
// with a `IntegrityJobPayload` (carrying the CommittedAsset + the
// CleanupToken from the SourceStager) and returns immediately; the
// dispatcher executes Verify on a worker, which produces a
// VerificationResult that gates the next step (TypeAssetCleanup).
//
// godlike/06 SSOT: this package is the SOLE canonical owner of the
// IntegrityVerifier interface and the VerificationResult type.
// No other file may declare types with these names.
//
// godlike/07 NO-FAKE-AVAILABILITY: a wired IntegrityVerifier is a
// RUNTIME HARD REQUIREMENT for the post-commit pipeline. Callers
// that skip verification (e.g. fast-path admin tools) MUST do so
// explicitly — no silent fallback that would produce a
// VerificationResult.Passed=true without actually probing the asset.
package integrity

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
)

// IntegrityVerifier verifies that a CommittedAsset matches its
// physical/remote counterpart (size, hash, MIME type, Drive-side
// metadata). It is the SOLE canonical post-commit verifier.
//
// The CommittedAsset is the canonical input (the persistence-layer
// result of AssetCommitter.CommitAsset). The CleanupToken is the
// SourceStager token the coordinator captured during Stage; it is
// threaded through the verification result so the gated Cleanup
// job can release the staged source bytes AFTER verification
// confirms the asset is durable.
//
// Reference contract (godlike/06 SSOT, one canonical owner per
// fact):
//   - input:  CommittedAsset + CleanupToken
//   - output: VerificationResult (Passed bool + CleanupToken for
//     the gated cleanup step)
//   - error:  transient (network/IO) failures are returned; the
//     verification PASS/FAIL decision is encoded in
//     VerificationResult.Passed, NOT in the error return.
type IntegrityVerifier interface {
	// Verify drives the post-commit integrity check. The
	// implementation probes the Drive-side metadata, compares
	// sizes/hashes, and returns a VerificationResult that the
	// dispatcher inspects to gate the next step.
	Verify(ctx context.Context, asset persistence.CommittedAsset, cleanupToken string) (VerificationResult, error)
}

// VerificationStatus is the typed outcome of an integrity check.
// The 4-state lifecycle mirrors ProcessingStatus so a future
// outbox-consumer can route VerificationResults through the
// same lifecycle bookkeeping as the asset commit itself.
type VerificationStatus string

const (
	// VerificationPassed — the committed asset matches its
	// physical/remote counterpart. The Cleanup job is unblocked.
	VerificationPassed VerificationStatus = "passed"
	// VerificationFailed — the committed asset does NOT match
	// (size mismatch, hash drift, MIME drift, etc.). The
	// Cleanup job stays blocked; an operator is paged.
	VerificationFailed VerificationStatus = "failed"
	// VerificationSkipped — explicit opt-out (e.g. admin
	// fast-path). Distinct from Failed so dashboards can tell
	// a deliberate bypass from a real integrity failure.
	VerificationSkipped VerificationStatus = "skipped"
	// VerificationInconclusive — the verifier returned an error
	// (network/IO transient). The dispatcher will retry per
	// the canonical retry policy; after retries are exhausted
	// the job is routed to the DEAD-letter lane.
	VerificationInconclusive VerificationStatus = "inconclusive"
)

// VerificationResult is the canonical output of IntegrityVerifier
// and the canonical input of the gated cleanup job.
//
// godlike/06 SSOT: one canonical owner per fact. VerificationResult
// is the single source of truth for "is this asset durable"; no
// other type or field in the codebase may encode the same
// PASS/FAIL decision.
type VerificationResult struct {
	// AssetID is mirrored from CommittedAsset for dispatcher
	// routing (the dispatcher keys jobs by asset_id; threading
	// the field explicitly avoids a lookup round-trip).
	AssetID string
	// Status is the typed 4-state outcome (see VerificationStatus).
	Status VerificationStatus
	// CleanupToken is the SourceStager token captured during
	// Stage; it is the canonical input for the gated cleanup
	// job. Empty when the verifier could not perform the check
	// (network/IO error). The cleanup job is gated on
	// (Status == VerificationPassed && CleanupToken != "").
	CleanupToken string
	// DiagnosedAt is the timestamp the verifier stamped. The
	// dispatcher uses it to attach to the outbox event for
	// downstream consumers (dashboards, audit logs).
	DiagnosedAt string
	// Reason is the human-readable failure reason for
	// VerificationFailed / VerificationInconclusive. Empty on
	// success (godlike/08: empty Reason on success keeps the
	// happy-path log compact).
	Reason string
}
