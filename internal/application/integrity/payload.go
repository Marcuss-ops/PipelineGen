// Package integrity — IntegrityJobPayload + CleanupJobPayload
// (PR-INTEGRITY-VERIFIER-EXTRACT, July 2026).
//
// These payload structs are the canonical wire shapes exchanged
// between the coordinator (enqueue side) and the dispatcher
// (worker side) for the two extracted jobs:
//
//   - jobs.TypeIntegrityVerify → IntegrityJobPayload →
//     VerificationResult
//   - jobs.TypeAssetCleanup    ← CleanupJobPayload ←
//     VerificationResult
//
// godlike/06 SSOT: this file is the SOLE canonical owner of the
// payload shapes for the integrity + cleanup jobs. No other file
// may declare structs with these names. Future payload drift is
// caught by the canonical-schema-version assertion in the
// CodecDescriptor (jobs.CodecDescriptorMarker).
package integrity

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
)

// IntegrityJobPayload is the canonical payload for the
// `jobs.TypeIntegrityVerify` job. The coordinator enqueues it
// after the AssetCommitter.Commit step succeeds; the dispatcher
// deserialises it, calls IntegrityVerifier.Verify(ctx, asset,
// cleanupToken), and produces a VerificationResult that is
// threaded into the next job (`jobs.TypeAssetCleanup`).
//
// godlike/06 SSOT: the payloads are immutable from the enqueue
// side; the dispatcher treats them as read-only. A field
// mutation after enqueue would race the dispatcher-handoff. The
// coord/proc/ directory forbids field mutation on enqueued
// payloads (see forward-prevention notes in
// cmd/archcheck/scan/percheck_input_immutability.go).
type IntegrityJobPayload struct {
	// SchemaVersion tags the wire shape for forward-compat.
	// Bumped on additive changes only.
	SchemaVersion string
	// Committed is the persistence-layer CommittedAsset
	// produced by AssetCommitter.CommitAsset. It carries the
	// canonical asset_id + outbox_event_key for Qdrant
	// correlation downstream.
	Committed persistence.CommittedAsset
	// CleanupToken is the SourceStager token captured during
	// Stage. Threaded through to the gated cleanup job only
	// when VerificationResult.Status == VerificationPassed.
	CleanupToken string
}

// CleanupJobPayload is the canonical payload for the
// `jobs.TypeAssetCleanup` job. The integrity worker enqueues it
// AFTER producing a successful VerificationResult; the cleanup
// worker deserialises it, releases the staged source via
// SourceStager.Release, and (future work) deletes any per-step
// temp files.
//
// godlike/08 typed-sentinel rule: empty CleanupToken in a
// non-skipped result MUST surface as a hard error in the worker
// (not a silent no-op) so a future operator who bypasses the
// gate surfaces the misconfiguration instead of leaking staged
// bytes.
type CleanupJobPayload struct {
	// SchemaVersion tags the wire shape for forward-compat.
	SchemaVersion string
	// Verification is the canonical VerificationResult produced
	// by the IntegrityVerifier. The cleanup worker gates on
	// (Verification.Status == VerificationPassed &&
	// Verification.CleanupToken != "").
	Verification VerificationResult
}
