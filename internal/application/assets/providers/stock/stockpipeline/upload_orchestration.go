// Package stockpipeline — upload_orchestration.go
// (Stock Cutover Commit 4-expanded, July 2026).
//
// Defines the canonical port surface for the orchestrator's
// resilience flow (post-emission outbox dispatch + Qdrant projection),
// the typed envelope that threads the per-run FinalStatus through to
// the JobFinalizer, and the default no-op implementations wired by
// NewOrchestrator when the caller does not inject custom ports.
//
// The 3 ports declared here are:
//
//   - ManifestBuilder:     builds the run's *job.ArtifactManifest
//     from (workflowID, jobID). The default stockManifestBuilder
//     delegates to the canonical 5-entry buildStockManifest().
//   - TransactionalAssetWriter: atomic UPSERT + outbox enqueue per
//     planned clip. The contract — and the canonical test (a) in
//     run_upload_indexing_test.go — state that on WriteAndEnqueue
//     failure, the upstream DB state is ROLLED BACK so partial
//     writes do not leak.
//   - ProjectionPort: post-emission best-effort projection to the
//     Qdrant side index. Failure flips FinalStatus to
//     job.StatusIndexPending (rather than job.StatusFailed); see
//     canonical test (c) for the contract.
//
// RunSummary is the typed envelope Orchestrator.Run returns. It carries
// the typed manifest + the FinalStatus the JobFinalizer should stamp
// on the job row (the per-run projection outcome surfaces here so the
// broker runner can persist the post-indexing state machine without
// re-inferring it from the manifest alone).
//
// godlike/06 SSOT: each port declares exactly one owning capability.
// godlike/07 typed-error contract: the 4 sentinel errors below are
// exported + reachable via errors.Is from any test seam.
package stockpipeline

import (
	"context"
	"errors"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// RunSummary is the canonical envelope returned by Orchestrator.Run
// post-Commit-4-expanded. It pairs the typed *job.ArtifactManifest
// (still used by the wire-format Decode + Validate of the broker
// worker runner) with a FinalStatus the JobFinalizer stamps on the
// job row directly. The FinalStatus is one of:
//
//   - job.StatusSucceeded   — happy path (artifacts emitted AND
//     Qdrant projection completed without error).
//   - job.StatusIndexPending — artifacts emitted, but the
//     post-emission Qdrant projection failed (best-effort,
//     reconciled by the Qdrant-reconciler task per domain/asset/
//     index_state.go).
//   - job.StatusFailed     — non-transient failure surfaced by the
//     orchestrator (typically via ErrManifestIncomplete or
//     ErrAtomicDispatchFailed; the broker orchestrator surfaces
//     these as JobFailed).
type RunSummary struct {
	Manifest    *job.ArtifactManifest
	FinalStatus job.Status
}

// TransactionalAssetWriter is the canonical port for atomic
// UPSERT-write + outbox-enqueue per planned clip. The contract
// requires single-TX semantics: a non-nil error from
// WriteAndEnqueue MUST leave the upstream DB in the pre-call state
// (zero partial writes). The default noopWriter trivially satisfies
// the contract; the production outbox-backed adapter wraps SQLite's
// BEGIN IMMEDIATE + outbox.Dispatcher.EnqueueAndIndex.
//
// Hooked into Orchestrator.Run between plan_clips and project_manifest;
// a returned error aborts the run with ErrAtomicDispatchFailed.
type TransactionalAssetWriter interface {
	WriteAndEnqueue(ctx context.Context, clip *asset.Asset, fileHash string) error
}

// ProjectionPort is the canonical port for the post-emission
// Qdrant projection step. Failure does NOT abort the run —
// orchestrator flips FinalStatus to job.StatusIndexPending and
// returns a nil error so the broker runner persists the index-pending
// state and the Qdrant-reconciler task can retry asynchronously.
//
// The default noopProjection trivially returns nil (=> SUCCEEDED).
type ProjectionPort interface {
	Project(ctx context.Context, manifest *job.ArtifactManifest) error
}

// ManifestBuilder is the canonical port for building a run's
// *job.ArtifactManifest from the per-run (workflowID, jobID) tuple.
// The default stockManifestBuilder delegates to the canonical
// buildStockManifest() helper (5 fixed C12 entries with
// Required:false; post-Commit-4-expanded, Commit-2's emission
// contract).
//
// The port is exposed so tests can inject manifests containing
// Required:true entries with empty Path values to verify the
// orchestrator's manifest-completeness gate (test (b) in
// run_upload_indexing_test.go).
type ManifestBuilder interface {
	Build(workflowID, jobID string) (*job.ArtifactManifest, error)
}

// stockManifestBuilder is the canonical default ManifestBuilder
// implementation. It returns the hard-coded 5-entry C12 manifest
// produced by buildStockManifest (the Commit 2 wire-format anchor).
type stockManifestBuilder struct{}

// Build invokes the package-level buildStockManifest helper. It
// returns a nil error unconditionally because buildStockManifest is
// a pure-data constructor (no I/O, no failure modes).
func (stockManifestBuilder) Build(workflowID, jobID string) (*job.ArtifactManifest, error) {
	return buildStockManifest(workflowID, jobID), nil
}

// noopWriter is the default TransactionalAssetWriter. It returns
// nil unconditionally — production wiring injects the SQLite-backed
// outbox adapter in NewOrchestratorWithResilience.
type noopWriter struct{}

// WriteAndEnqueue is a no-op; the canonical test (a) in
// run_upload_indexing_test.go injects a different writer to verify
// the atomic-rollback contract.
func (noopWriter) WriteAndEnqueue(_ context.Context, _ *asset.Asset, _ string) error {
	return nil
}

// noopProjection is the default ProjectionPort. It returns nil
// unconditionally — production wiring injects the Qdrant-backed
// adapter in NewOrchestratorWithResilience; the canonical test (c)
// injects a Qdrant-offline stub to verify the StatusIndexPending
// fallback.
type noopProjection struct{}

// Project is a no-op; the canonical test (c) substitutes a
// Qdrant-offline stub to verify the index-pending fallback contract.
func (noopProjection) Project(_ context.Context, _ *job.ArtifactManifest) error {
	return nil
}

// ── Typed sentinel errors (godlike/07 typed-error contract) ────────
//
// All four errors are typed errors.New(...) so callers can probe via
// errors.Is(...) from any seam. The orchestrator wraps these via
// fmt.Errorf("stage: %w", <sentinel>) (or, for test (b), surfaces the
// sentinel verbatim against the typed-error chain).

// ErrManifestIncomplete surfaces a failed manifest-completeness
// gate: the built manifest has at least one Required:true artifact
// with an empty Path (or another Validate()-invariant violation).
// This is Test (b)'s canonical surface — callers should retry only
// after fixing the manifest builder / chunk-emission pipeline that
// produced the empty Path.
var ErrManifestIncomplete = errors.New("manifest-completeness gate: required artifact missing path")

// ErrAtomicDispatchFailed surfaces a TransactionalAssetWriter
// mid-flight failure during the orchestrator's atomic UPSERT+enqueue
// loop. The orchestrator surfaces this as a typed JobFailed outcome;
// the test (a) verifies that a writer error propagates verbatim and
// that the orchestrator's RunSummary.{Manifest, FinalStatus} can
// not reach the broker runner in a half-written state.
var ErrAtomicDispatchFailed = errors.New("atomic dispatch failed: outbox write aborted mid-transaction (DB rolled back)")

// ErrProjectionResilience is documented but NOT surfaced as an
// orchestrator-level error — Qdrant projection failures flip
// FinalStatus to job.StatusIndexPending and return nil from Run.
// The sentinel is exported for log scanners that want to keyword-
// search for the resilience path; the orchestrator never wraps it.
var ErrProjectionResilience = errors.New("projection-resilience path: Qdrant projection unavailable — status flipped to INDEX_PENDING")

// ErrResilienceNotWired surfaces a missing required resilience
// port at construction time. NewOrchestratorWithResilience rejects
// nil builder / writer / projection; the canonical guard mirrors
// the existing ErrOrchestratorNilDeps pattern (compose-time
// fail-closed, NOT a panic at runtime).
var ErrResilienceNotWired = errors.New("orchestrator: resilience ports (builder, writer, projection) must be non-nil; NewOrchestratorWithResilience requires explicit port injection")
