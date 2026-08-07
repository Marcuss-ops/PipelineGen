// Package stockpipeline — upload_orchestration.go
// (Stock Cutover Commit 4-expanded, July 2026).
//
// Defines the canonical port surface for the orchestrator's
// resilience flow (post-emission outbox dispatch + Qdrant projection),
// and the typed envelope that threads the per-run FinalStatus through to
// the JobFinalizer. Fixture defaults are owned by the explicit test
// constructor; production wiring uses NewProductionStockOrchestrator.
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
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
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
	Counts      RunCounts
	Stages      []StageSnapshot `json:"stages"`
}

// StageSnapshot is the stable, read-only stage projection returned with a
// stock job result. It deliberately omits checkpoint fingerprints and raw
// artifact references: those are internal resume details, not an API
// contract. A skipped stage is explicitly non-applicable rather than a
// false completed success (for example, compose_chunks is bypassed when
// the cutter output is already the canonical final artifact).
type StageSnapshot struct {
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	Attempt     int        `json:"attempt"`
	Applicable  bool       `json:"applicable"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	LastError   string     `json:"last_error,omitempty"`
}

// RunCounts is the auditable outcome of one stock run. Values are derived
// from completed stages, never copied from requested values.
type RunCounts struct {
	RequestedVideoCount  int `json:"requested_video_count"`
	DiscoveredVideoCount int `json:"discovered_video_count"`
	SelectedVideoCount   int `json:"selected_video_count"`
	DownloadedVideoCount int `json:"downloaded_video_count"`
	ProcessedVideoCount  int `json:"processed_video_count"`
	PlannedClipCount     int `json:"planned_clip_count"`
	CreatedClipCount     int `json:"created_clip_count"`
	PublishedClipCount   int `json:"published_clip_count"`
	PersistedClipCount   int `json:"persisted_clip_count"`
	IndexedClipCount     int `json:"indexed_clip_count"`
	FailedVideoCount     int `json:"failed_video_count"`
	FailedClipCount      int `json:"failed_clip_count"`
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

// noopWriter is a fixture-only TransactionalAssetWriter. Production
// wiring must inject the SQLite-backed outbox adapter.
type noopWriter struct{}

// WriteAndEnqueue is a no-op; the canonical test (a) in
// run_upload_indexing_test.go injects a different writer to verify
// the atomic-rollback contract.
func (noopWriter) WriteAndEnqueue(_ context.Context, _ *asset.Asset, _ string) error {
	return nil
}

// noopProjection is a fixture-only ProjectionPort. Production wiring
// must inject the Qdrant-backed adapter; the canonical test (c) injects
// a Qdrant-offline stub to verify the StatusIndexPending fallback.
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

// ErrNoProducedChunk surfaces the §12-1 P0 #1 (July 2026) orchestrator-
// level fail-closed gate: every Orchestrator.Run success path MUST
// carry at least one Required:true chunk entry in the manifest. The
// gate fires when zero Artifact entries with (Required:true AND Kind ==
// ArtifactKindVideo) are present in RunSummary.Manifest.Artifacts.
//
// This is the manifest-level analog of the post-publish gate-level
// ErrStockNoChunksFinalized (in finalizer_gates.go — fired inside
// BuildFinalizationRequest after Drive upload). The orchestrator-level
// gate fires EARLIER: closing the verdict's P0 #1 false-success class
// where Orchestrator.Run declared success without producing any real
// chunk. Per godlike/07 typed-error contract, callers can errors.Is
// from any seam.
var ErrNoProducedChunk = errors.New("stock: orchestrator produced no Required chunk entry (P0 #1 fail-closed — manifest must declare ≥1 chunk before Run returns nil)")

// ErrMetadataMissing surfaces the §12-1 P0 #1 (July 2026) orchestrator-
// level fail-closed gate: every Orchestrator.Run success path MUST
// carry exactly one Required:true metadata.json entry (canonical
// stable ID = StockArtifactIdMetadata). The gate fires when zero
// Artifact entries with (Required:true AND Kind == ArtifactKindMetadata)
// are present in RunSummary.Manifest.Artifacts.
//
// This is the manifest-level analog of the post-publish gate-level
// ErrStockMetadataNotPublished (in finalizer_gates.go). The
// orchestrator-level gate fires EARLIER: closing the verdict's
// silent-success class where the run declares success without
// declaring the metadata.json envelope needed for downstream
// reconstruction. Per godlike/07 typed-error contract, callers can
// errors.Is from any seam.
var ErrMetadataMissing = errors.New("stock: orchestrator manifest is missing the Required metadata.json entry (P0 #1 fail-closed — must declare metadata before Run returns nil)")

// ErrStockProductionArtifactPrepMissing surfaces a composition-root wiring
// gap: the orchestrator's JobFinalizer is wired (production path) but
// ArtifactPreparation is nil. The stock.publish step cannot upload chunks
// or metadata.json without a concrete ArtifactPreparation adapter —
// returning nil error in this state is a silent-success false-positive.
//
// Gate fires at RunResilient entry (before any step) so composition
// roots that forget to call WithAssetPreparation surface the gap
// immediately rather than at publish time (cf. godlike/07 fail-closed).
// Test-fixture mode (both nil) is unaffected.
var ErrStockProductionArtifactPrepMissing = errors.New("stock: production gate — ArtifactPreparation nil while JobFinalizer wired (call WithAssetPreparation before RunResilient)")

// ErrStockProductionJobFinalizerMissing surfaces the symmetric wiring gap:
// ArtifactPreparation is wired but JobFinalizer is nil. The stock.finalize
// step cannot execute the single-TX spine write without a concrete
// JobFinalizer — returning nil error in this state abandons the
// CompleteWithArtifacts contract silently.
//
// Gate fires at RunResilient entry. Test-fixture mode (both nil) is
// unaffected.
var ErrStockProductionJobFinalizerMissing = errors.New("stock: production gate — JobFinalizer nil while ArtifactPreparation wired (call WithJobFinalizer before RunResilient)")

// AssertRunSummaryArtifactsRequired is the §12-1 P0 #1 (July 2026)
// orchestrator-level fail-closed gate. Pure function: easy TDD,
// zero side-effects on RunSummary. It is the SINGLE owner (godlike/06
// SSOT) of the "manifest declares ≥1 Required:true chunk AND the
// Required:true metadata.json entry" fact at the orchestrator layer.
//
// Composition (priority-ordered, fail-fast):
//
//  1. nil RunSummary OR nil RunSummary.Manifest → ErrMetadataMissing
//     (cannot assess chunk presence without a manifest).
//  2. zero Required:true ArtifactKindMetadata entries → ErrMetadataMissing.
//  3. zero Required:true ArtifactKindVideo entries → ErrNoProducedChunk.
//
// Pre-Commit-4-7 (the chunk-rendering ladder not shipped yet) every
// stock run hits (2) — all 5 entries in buildStockManifest have
// Required:false, so the gate fires ErrMetadataMissing on every run,
// closing the P0 #1 false-success class. Post-Commit-4-7 the chunk
// ladder flips entries to Required:true once their LocalPath is
// hydrated, so the gate starts passing.
//
// Wired into Orchestrator.RunResilient BEFORE the
// `return &RunSummary{...}, nil` line — wrapping the typed error via
// fmt.Errorf("%w: orchestrator success gate", sentinel) so the caller
// can errors.Is(sentinel) AND retain the human-readable prefix for
// log scanners.
//
// godlike/06 SSOT: this gate is the typed-package entry-point of
// "did the orchestrator produce canonical artifacts?" — paired with
// the post-publish gate-level layers in finalizer_gates.go. The
// orchestrator-level gate declares (Required:true) artefact presence;
// the post-publish gates declare populate-and-validate completeness.
// Failing the orchestrator-level gate short-circuits before any
// publisher/upload/indexer work happens.
// godlike/07 typed-error: ErrMetadataMissing and ErrNoProducedChunk
// are exported errors.New sentinels, reachable via errors.Is from any
// caller + test seam.
//
// Layering note (condition-3 delegation): the verdict lists 3
// conditions for Orchestrator.Run success: (1) ≥1 Required chunk
// finalized AND (2) metadata.json Required finalized AND (3)
// CompleteWithArtifacts finalized. This gate enforces conditions
// 1+2 at the orchestrator layer. Condition 3 is enforced at the
// post-publish gate in finalizer_gates.go::BuildFinalizationRequest
// (called by Service.HandleJob after the orchestrator returns),
// where the orchestrator's pre-publication manifest is the input to
// the spine-finalizer CompositionRequest. The orchestrator does
// not call CompleteWithArtifacts itself (architecturally separate
// concerns: orchestrator owns the prepare/build pipeline; HandleJob
// owns the spine finalization), so condition 3 cannot be enforced
// at the orchestrator level without restructuring the layer split.
// The two gates together close the verdict's success-class fully.
func AssertRunSummaryArtifactsRequired(summary *RunSummary) error {
	if summary == nil || summary.Manifest == nil {
		return fmt.Errorf("%w: RunSummary or RunSummary.Manifest nil", ErrMetadataMissing)
	}
	var hasMetadata, hasChunk bool
	for _, a := range summary.Manifest.Artifacts {
		if !a.Required {
			continue
		}
		// Kind comparison note (typed-string bridge):
		//   job.Artifact.Kind is typed `string`. The kind-typed
		//   constants in this package come from two distinct
		//   packages — job (untyped-string constants: ArtifactKindMetadata)
		//   and finalization (typed-string constants of type
		//   finalization.ArtifactKind: KindVideo). The
		//   untyped-const case label auto-converts to string
		//   on switch-tag match; the typed-const requires an
		//   explicit `string(...)` conversion to align with the
		//   switch tag's `string` type. The conversion is
		//   compile-time (string-of-typed-string is zero-cost)
		//   so the gate's runtime cost is unchanged.
		if a.Kind == job.ArtifactKindMetadata {
			hasMetadata = true
		}
		if a.Kind == string(finalization.KindVideo) {
			hasChunk = true
		}
	}
	if !hasMetadata {
		return ErrMetadataMissing
	}
	if !hasChunk {
		return ErrNoProducedChunk
	}
	return nil
}
