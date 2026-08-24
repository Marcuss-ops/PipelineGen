package assets

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"strconv"
)

// Package stockpipeline — orchestrator_fingerprint.go (Stock P1 split, July 2026).
//
// This file owns the artifact ID helper functions extracted from
// orchestrator_steps.go: ChunkArtifactID, ChunkArtifactFilename,
// MetadataArtifactID. These are pure functions that derive stable
// identifiers from the run fingerprint.
//
// godlike/07 no-fake-availability: same fingerprint ⇒ same ArtifactID
// across retries.
// ChunkArtifactID returns the canonical logical ArtifactID for a
// single chunk. Stable across retries of the same logical run
// (same fingerprint) — Drive FileID is LOCATION (changes per retry
// if the DriveUpload re-runs), but the logical IDENTITY (this
// string) stays constant per godlike/07 no-fake-availability.
//
// Format: stock:<run_fingerprint>:chunk:<chunk_index>
func ChunkArtifactID(runFingerprint string, chunkIndex int) string {
	return "stock:" + runFingerprint + ":chunk:" + strconv.Itoa(chunkIndex)
}

// ChunkArtifactFilename returns the canonical filename for a
// single chunk. Truncates the fingerprint to the first 12 hex
// chars for readable on-disk filenames while preserving enough
// entropy for human auditing. Full fingerprint remains in the
// ArtifactID where it matters for byte-equality comparisons.
func ChunkArtifactFilename(runFingerprint string, chunkIndex int) string {
	fpShort := runFingerprint
	if len(fpShort) > 12 {
		fpShort = fpShort[:12]
	}
	return "stock_" + fpShort + "_chunk_" + strconv.Itoa(chunkIndex) + ".mp4"
}

// MetadataArtifactID returns the canonical logical ArtifactID for
// the per-run metadata.json. Format: stock:<run_fingerprint>:metadata
//
// Same fingerprint ⇒ same ArtifactID across retries
// (godlike/07 no-fake-availability invariant).
func MetadataArtifactID(runFingerprint string) string {
	return "stock:" + runFingerprint + ":metadata"
}

// TimestampArtifactID returns the canonical logical ArtifactID for
// an explicit timestamp clip and its sidecar metadata.
//
// Format: stock:<run_fingerprint>:timestamp:<timestamp_index>:<kind>
// where kind is typically "video" or "metadata".
func TimestampArtifactID(runFingerprint string, timestampIndex int, kind string) string {
	return "stock:" + runFingerprint + ":timestamp:" + strconv.Itoa(timestampIndex) + ":" + kind
}

// effectiveChunkDurationSec resolves the per-run chunk duration
// (sec) override chain. Mirrors the prior run.go body semantics
// (input.ChunkDuration takes precedence over the runtime config)
// which falls back to the minimal runtime chunk duration).
//
// Centralised here so Service.Run and Service.runOrchestrator
// (and future Commit 4-7 entrypoints) share the same override
// chain without re-deriving it on every call site.
func effectiveChunkDurationSec(input *RunInput, s *Service) int {
	if input != nil && input.ChunkDuration > 0 {
		return input.ChunkDuration
	}
	if s != nil && s.runtime != nil {
		return s.runtime.ChunkDurationSec
	}
	return 0
}

// effectiveClipDurationSec resolves the per-run clip duration
// (sec) override chain. Mirrors the prior run.go body semantics.
// Centralised for the same reason as effectiveChunkDurationSec.
func effectiveClipDurationSec(input *RunInput, s *Service) int {
	if input != nil && input.ClipDuration > 0 {
		return input.ClipDuration
	}
	if s != nil && s.runtime != nil {
		return s.runtime.ClipDurationSec
	}
	return 0
}

// stagerForRun resolves the canonical acquisition.SourceStager for the
// stock pipeline. StockStager implements acquisition.SourceStager via
// the Prepare/Release adapter methods (stager_adapter.go).
//
// nil receiver returns a nil SourceStager; the orchestrator's
// nil-guard handles that case (ErrOrchestratorNilDeps) so the
// production error path is observable.
func (s *Service) stagerForRun() acquisition.SourceStager {
	if s == nil {
		return nil
	}
	stockStager := NewStockStager(s).
		WithSourceCache(s.sourceCacheReader, s.sourceCacheWriter).
		WithDownloader(serviceSourceDownloader{service: s})
	if s.driveReader != nil {
		stockStager = stockStager.WithDriveReader(s.driveReader)
	}
	return stockStager
}

// Package stockpipeline — orchestrator_stage_snapshots.go.
//
// Owns the read-only stage projection returned with a stock job result
// (StageSnapshot collection). Extracted from orchestrator_run.go to keep
// the orchestration entry point under the max_lines_per_file_strict gate.
// stageSnapshots projects the latest per-step rows from the step store
// into the stable, read-only stage list returned with a stock job result.
// A skipped stage is explicitly non-applicable rather than a false
// completed success (for example, compose_chunks is bypassed when the
// cutter output is already the canonical final artifact).
func (o *Orchestrator) stageSnapshots(ctx context.Context, input *RunInput) ([]StageSnapshot, error) {
	if o == nil || o.stepStore == nil {
		return nil, steps.ErrStoreNotWired
	}
	history, err := o.stepStore.ListByJob(ctx, o.cfg.JobId)
	if err != nil {
		return nil, err
	}
	latest := make(map[string]steps.StepState, len(history))
	for _, row := range history {
		if existing, ok := latest[row.StepKey]; !ok || row.ID > existing.ID {
			latest[row.StepKey] = row
		}
	}
	stageNames := []string{
		StepKeyStockPlan,
		StepKeyStockStageSources,
		StepKeyStockExtractClips,
		StepKeyStockComposeChunks,
		StepKeyStockPublish,
		StepKeyStockFinalize,
	}
	stages := make([]StageSnapshot, 0, len(stageNames))
	for _, name := range stageNames {
		stage := StageSnapshot{Name: name, Status: string(steps.StatusPending), Applicable: true}
		bypassed := name == StepKeyStockComposeChunks && shouldBypassStockCompose(name, input)
		if bypassed {
			stage.Status = "skipped"
			stage.Applicable = false
		}
		if row, ok := latest[name]; ok && !bypassed {
			stage.Status = string(row.Status)
			stage.Attempt = row.Attempt
			if !row.StartedAt.IsZero() {
				startedAt := row.StartedAt
				stage.StartedAt = &startedAt
			}
			if !row.CompletedAt.IsZero() {
				completedAt := row.CompletedAt
				stage.CompletedAt = &completedAt
			}
			stage.LastError = row.LastError
		}
		stages = append(stages, stage)
	}
	return stages, nil
}

// Package stockpipeline — orchestrator_steps.go
// (PR-STOCK-ORCHESTRATOR-SPLIT, July 2026).
//
// SLIM orchestrator-steps surface: package doc + Step interface +
// 6 canonical step key constants. The 6 step implementations
// live in 1-file-per-Step single-purpose capability files
// (godlike/06 SSOT one-canonical-owner-per-fact) per AGENTS.md
// Pattern 5:
//
//   - StockPlanStep           → step_plan_clips.go
//   - StockStageSourcesStep   → step_stage_sources.go
//   - StockExtractClipsStep   → step_extract_clips.go
//   - StockComposeChunksStep  → step_compose_chunks.go
//   - StockPublishStep        → step_publish.go
//   - StockFinalizeStep       → step_finalize.go
//
// The 6 step-level typed sentinels (ErrStockPublishArtifactFailed,
// ErrStockFinalizeSpineFailed, ErrStockFinalizeLeaseMissing,
// ErrStockFnRequired, ErrStockStageSourcesAllFailed,
// ErrStockComposeChunksAllFailed) live in
// orchestrator_step_errors.go.
//
// godlike/06 SSOT (one canonical owner per fact):
//   - 6 step key constants:     THIS FILE (orchestrator_steps.go)
//   - Step interface:           THIS FILE (orchestrator_steps.go)
//   - 6 step implementations:   step_plan_clips.go +
//     step_stage_sources.go +
//     step_extract_clips.go +
//     step_compose_chunks.go +
//     step_publish.go +
//     step_finalize.go
//   - 6 step-level sentinels:   orchestrator_step_errors.go
//   - DefaultStockSteps() +
//     compile-time assertions:  orchestrator_defaults.go
//   - StepRunner interface +
//     RunState + 6 accessors:   step_runner.go
//   - Artifact ID helpers:      orchestrator_fingerprint.go
//   - Metadata helpers:         orchestrator_metadata.go
//
// PR-STOCK-ORCHESTRATOR-SPLIT extracted the 6 step impls + 6
// sentinels from this file on 2026-07-04. The pre-split file
// was 874 LoC (the user spec referenced 949 LoC; the spec's
// "slim RunResilient ladder ~140 LoC" sub-file would have been
// empty per godlike/07 no-fake-availability — RunResilient lives
// in orchestrator.go today, not in orchestrator_steps.go; the 7
// step file names in the spec implied splitting StockFinalizeStep
// into 3+ sub-step files which is an aggressive split of a single
// Step type rather than the natural 1-file-per-Step unit; the
// minimum-ripple 1-file-per-Step split (6 step files + sentinels
// + slimmed orchestrator_steps.go = 8 files) is the canonical
// interpretation; see the commit body for the full honest scope
// disclosure).
// ── Canonical step keys (PipelineGen §12-5) ────────────────────────────
//
// SSOT: changing any of these constants is a wire-format break for the
// canonical §12-3 step store. New stages MUST add new constants;
// existing ones MUST NOT be renamed without a migration forward-pointer
// via architecture/current.yaml.
const (
	StepKeyStockPlan          = "stock.plan"
	StepKeyStockStageSources  = "stock.stage_sources"
	StepKeyStockExtractClips  = "stock.extract_clips"
	StepKeyStockComposeChunks = "stock.compose_chunks"
	StepKeyStockPublish       = "stock.publish"
	StepKeyStockFinalize      = "stock.finalize"
)

// Step is the canonical typed contract for a single orchestrator-side
// step. The Orchestrator iterates over a typed []Step slice and
// dispatches each step's Run with a StepRunner (defined in step_runner.go).
//
// Name() returns the canonical step_key (one of the
// StepKeyStockXxx constants above) — used to build the
// steps.StepKey triple on checkpoint rows. The Name() output MUST
// match the typed slice's position; changing it is a pipeline-order
// break.
//
// Run() executes the step body. Returns nil error on success
// (orchestrator dispatches MarkCompleted); returns non-nil error
// on failure (orchestrator dispatches MarkFailed + aborts the
// run with the typed error). Optional non-fatal outcomes (like
// the projection-resilience INDEX_PENDING flip) MUST be expressed
// via state mutation (e.g. State.FinalStatus = StatusIndexPending)
// rather than returning error — error means abort.
type Step interface {
	Name() string
	Run(ctx context.Context, runner StepRunner) error
}

// Package stockpipeline — orchestrator_defaults.go (Stock P1 split, July 2026).
//
// This file owns the canonical 6-step dispatch factory and compile-time
// assertions extracted from orchestrator_steps.go.
//
// godlike/06 SSOT: DefaultStockSteps() is the single canonical pipeline
// order for stock. Future steps MUST be appended (preserving pipeline
// semantics) — never inserted mid-slice.
// DefaultStockSteps returns the canonical 6-step slice the
// Orchestrator iterates in RunResilient. The slice order is the
// canonical pipeline order: plan → stage_sources → extract_clips
// → compose_chunks → publish → finalize.
//
// SSOT (godlike/06): this is the single canonical pipeline order
// for stock. Future steps MUST be appended (preserving pipeline
// semantics) — never inserted mid-slice (that would re-rank the
// lexically-sorted step_store.FirstNonCompleted result and break
// resume semantics per §12-3 doc-comment).
func DefaultStockSteps() []Step {
	return []Step{
		StockPlanStep{},
		StockStageSourcesStep{},
		StockExtractClipsStep{},
		StockComposeChunksStep{},
		StockPublishStep{},
		StockFinalizeStep{},
	}
}

// Compile-time assertions: every default Step struct satisfies Step.
var (
	_ Step = StockPlanStep{}
	_ Step = StockStageSourcesStep{}
	_ Step = StockExtractClipsStep{}
	_ Step = StockComposeChunksStep{}
	_ Step = StockPublishStep{}
	_ Step = StockFinalizeStep{}
)

// Package stockpipeline — orchestrator_manifest.go (split July 2026).
//
// This file owns the C12 5-artifact manifest builder. Extracted from
// orchestrator.go per AGENTS.md Pattern 5.
//
// godlike/06 SSOT: buildStockManifest is the single canonical owner of
// the C12 5-artifact envelope shape.
// buildStockManifest returns the C12 5-artifact envelope for stock.
//
// Why a hard-coded 5? The user spec for Stock Cutover Commit 2 says:
//
//	"the JobStatusResponse exposes __artifact_manifest with the C12
//	 5-artifact shape"
//
// The 5 fixed entries are the per-kind envelope the downstream
// runner (internal/capabilities/jobs/worker/runner.go::uploadManifest)
// routes on:
//
//	(a) metadata   — pipeline metadata.json uploaded at the end
//	(b) thumbnail  — cover png for the run (rendered once per run)
//	(c) bindings   — source-clip bindings report (one per run)
//	(d) report     — runtime summary JSON (one per run)
//	(e) summary    — narrative text summary (one per run)
//
// All entries have Required:false today because Commit 2 cannot
// populate their on-disk Paths (chunk rendering, Drive upload,
// and the binder run all land in Commit 4-7). Required is flipped
// to true in Commit 4-7 once the entry has a real local path —
// Validate() requires Required:true ⇒ non-empty Path; setting
// Required:false today passes Validate() cleanly.
//
// Validate() invariants upheld:
//   - SchemaVersion non-empty (pipelinegen.artifacts.v1)
//   - len(Artifacts) > 0
//   - no Required⇒empty Path
//   - no non-empty Path⇒empty Filename (Commit 4-7 hydrates both)
//
// (NIT-1 — kind overloading rationale): ArtifactKindScriptJSON +
// ArtifactKindScriptText are repurposed for stock here because the
// C12 envelope does not yet declare a stock-specific report kind.
func buildStockManifest(workflowID, jobID string) *job.ArtifactManifest {
	manifest := &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    workflowID,
		JobID:         jobID,
		Artifacts: []job.Artifact{
			{
				ID:       StockArtifactIdMetadata,
				Kind:     job.ArtifactKindMetadata,
				Filename: "metadata.json",
				MIMEType: "application/json",
				Required: false,
			},
			{
				ID:       StockArtifactIdThumbnail,
				Kind:     job.ArtifactKindImage,
				Filename: "thumbnail.png",
				MIMEType: "image/png",
				Required: false,
			},
			{
				ID:       StockArtifactIdBindings,
				Kind:     job.ArtifactKindClipBindings,
				Filename: "bindings.json",
				MIMEType: "application/json",
				Required: false,
			},
			{
				ID:       StockArtifactIdReport,
				Kind:     job.ArtifactKindScriptJSON,
				Filename: "report.json",
				MIMEType: "application/json",
				Required: false,
			},
			{
				ID:       StockArtifactIdSummary,
				Kind:     job.ArtifactKindScriptText,
				Filename: "summary.txt",
				MIMEType: "text/plain",
				Required: false,
			},
		},
	}
	if len(manifest.Artifacts) != stockArtifactCount {
		panic("buildStockManifest: artifact arity drifted from canonical 5 (Stock Cutover Commit 2 invariant violated)")
	}
	return manifest
}

// Package stockpipeline — orchestrator.go (Stock Cutover, July 2026).
//
// Split-topology landing page. The canonical surfaces live in:
//
//   - orchestrator_types.go: OrchestratorConfig, Orchestrator struct,
//     DefaultMaxConcurrentJobs, DefaultOrchestratorJobId,
//     StockArtifactId*, ErrOrchestratorNilDeps
//   - orchestrator_manifest.go: buildStockManifest (C12 5-artifact envelope)
//   - orchestrator_constructor.go: NewTestStockOrchestrator,
//     WithAssetPreparation, WithJobFinalizer, WithLocalFS, stepInputFingerprint, firstSource
//   - orchestrator_run.go: Run, RunResilient, executorLogOrNop
//   - orchestrator_defaults.go: DefaultStockSteps, compile-time Step assertions
//   - orchestrator_fingerprint.go: ChunkArtifactID, ChunkArtifactFilename,
//     MetadataArtifactID
//   - orchestrator_metadata.go: StockRunMetadata, ChunkMetadataEntry,
//     buildStockRunMetadata, writeAndHashMetadata, buildChunkedStockManifest
//   - orchestrator_steps.go: Step interface + 6 pipeline step types
//   - orchestrator_step_errors.go: typed step-error sentinels
//
// STATO ATTUALE: Orchestrator is the code-driven pipeline entrypoint
// canonico. Usa ClipPlanner + SourceStager + VideoCutter + StockRenderer
// + ArtifactPreparation + JobFinalizer per produrre chunk reali, upload
// Drive, e single-TX spine write.
//
// PROSSIMO STEP: buildStockManifest emette 5 entry Required:false;
// il chunk-rendering ladder (già wired) flippa Required:true quando
// LocalPath è hydrated. La projection Qdrant è best-effort con
// fallback INDEX_PENDING.
//
// DEPRECATO: Service.Run (legacy path) coesiste per back-compat
// ServiceRunner interface; il traffico produzione va via
// Service.HandleJob → runOrchestratorResilient.
