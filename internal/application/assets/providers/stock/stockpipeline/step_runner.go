// Package stockpipeline — step_runner.go (Stock P1 split, July 2026).
//
// This file owns the StepRunner interface, the runState accumulator,
// the orchestratorRunner concrete implementation, its accessor methods,
// and the RunFingerprint computation. Extracted from orchestrator_steps.go
// (Stock P0 action plan).
//
// godlike/06 SSOT: StepRunner is the single seam between per-step
// bodies and the Orchestrator. Steps MUST NOT access Orchestrator
// fields directly — they go through the StepRunner accessors.
package stockpipeline

import (
	"strconv"
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	kerneljob "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// StepRunner is the typed context each step body sees during
// execution. The Orchestrator constructs an *orchestratorRunner
// per RunResilient call and threads it through each Step.Run.
//
// godlike/06 SSOT: StepRunner is the single seam between per-step
// bodies and the Orchestrator. Steps MUST NOT access Orchestrator
// fields directly — they go through the StepRunner accessors so
// test fakes can implement StepRunner without dragging the
// Orchestrator's full surface into the test fixture.
type StepRunner interface {
	// Immutable per-call inputs.
	Cfg() OrchestratorConfig
	RunInput() *RunInput
	JobID() string
	PolicyVersion() string

	// Port dependencies (read-only accessors).
	Planner() ClipPlanner
	SourceStager() assets.SourceStager
	Cutter() VideoCutter
	Renderer() StockRenderer
	Builder() ManifestBuilder
	Writer() TransactionalAssetWriter
	Projection() ProjectionPort
	// SourceDurationProbe is the optional ffprobe-backed port
	// (PR-STOCK-TIMESTAMP-CLIPS Front 5, July 2026) that
	// step_extract_clips uses to validate ClipPlan.EndSec against
	// the source video duration BEFORE invoking VideoCutter.Cut.
	// nil is the canonical backward-compat value: validation is
	// skipped (godlike/07 fail-open), and the step falls through
	// to the legacy unvalidated path. The composition root wires
	// the concrete via orchestrator.WithSourceProbe(probe) (forward-
	// pointer PR-STOCK-SOURCE-DURATION-WIRE for production wiring).
	SourceDurationProbe() SourceDurationProbe

	// §12-7 extensions: Finalizer-side ports + run fingerprint.
	ArtifactPreparation() finalization.ArtifactPreparationService
	JobFinalizer() finalization.JobFinalizer
	RunFingerprint() string

	Log() *zap.Logger
	State() *runState
}

// runState is the mutable per-call accumulator phases write to
// and read from. Each step writes to ITS designated field(s) and
// reads from its predecessor's field(s); the typed []Step slice
// encodes the canonical ordering.
//
// SSOT: this is the ONLY state shared across steps. Steps MUST NOT
// cross-pad via package-level globals or external state.
type runState struct {
	// Plan is the output of stock.plan and the input of stock.extract_clips.
	Plan []ClipPlan

	// StagedAssets is the output of stock.stage_sources. Today (Commit 5)
	// it's nil — staging is a Begin/Complete stub.
	StagedAssets []*assets.StagedAsset

	// CutPaths is the output of stock.extract_clips.
	CutPaths []string

	// ComposedPaths is the output of stock.compose_chunks.
	ComposedPaths []string

	// Published (§12-7 replaces the §12-5 stub []job.Artifact): the
	// per-chunk ChunkState slice populated by StockPublishStep after
	// AssetPreparation.Prepare per chunk. RemoteFileID / WebViewLink /
	// DownloadLink are populated from the Publisher response. LocalPath
	// is the chunk's on-disk render output (still a placeholder ID
	// per ComposeChunks stub — real paths wire in Commit 7).
	Published []ChunkState

	// MetadataPublished (§12-7 NEW): the per-run metadata.json state
	// after AssetPreparation.Prepare. StockFinalizeStep reads this
	// for BuildFinalizationRequest so the per-run metadata Artifact
	// is included in the spine write.
	MetadataPublished MetadataState

	// Manifest is the output of stock.finalize.
	Manifest *job.ArtifactManifest

	// FinalStatus is the orchestrator's per-run job status stamp.
	FinalStatus job.Status

	// FinalizationResult (§12-7 NEW) is the JobFinalizer response after
	// stock.finalize calls CompleteWithArtifacts. Surfaced via result-map
	// __finalization_status key for dashboards. Nil when JobFinalizer
	// is unwired (test fixture / §F.1 OPTIONAL back-compat).
	FinalizationResult *finalization.FinalizationResult
}

// ── orchestratorRunner — StepRunner impl backed by Orchestrator ────────

// orchestratorRunner is the canonical StepRunner implementation.
// One is constructed per RunResilient call so the per-call (in,
// state) pair survives across steps without leaking into
// Orchestrator fields.
//
// §12-7 adds ArtifactPreparation + JobFinalizer + RunFingerprint
// fields. The fingerprint is computed once on construction
// (lazy) so it's stable across all 6 steps in the run —
// drift-free even if cfg.PolicyVersion is mutated mid-run.
type orchestratorRunner struct {
	orch                *Orchestrator
	in                  *RunInput
	state               *runState
	log                 *zap.Logger
	artifactPreparation finalization.ArtifactPreparationService
	jobFinalizer        finalization.JobFinalizer
	fingerprintOnce     sync.Once
	cachedFingerprint   string
}

// Compile-time assertion: *orchestratorRunner satisfies StepRunner.
var _ StepRunner = (*orchestratorRunner)(nil)

func (a *orchestratorRunner) Cfg() OrchestratorConfig           { return a.orch.cfg }
func (a *orchestratorRunner) RunInput() *RunInput               { return a.in }
func (a *orchestratorRunner) JobID() string                     { return a.orch.cfg.JobId }
func (a *orchestratorRunner) PolicyVersion() string             { return a.orch.cfg.PolicyVersion }
func (a *orchestratorRunner) Planner() ClipPlanner              { return a.orch.planner }
func (a *orchestratorRunner) SourceStager() assets.SourceStager { return a.orch.stager }
func (a *orchestratorRunner) Cutter() VideoCutter               { return a.orch.cutter }
func (a *orchestratorRunner) Renderer() StockRenderer           { return a.orch.renderer }
func (a *orchestratorRunner) Builder() ManifestBuilder          { return a.orch.builder }
func (a *orchestratorRunner) Writer() TransactionalAssetWriter  { return a.orch.writer }
func (a *orchestratorRunner) Projection() ProjectionPort        { return a.orch.projection }
func (a *orchestratorRunner) SourceDurationProbe() SourceDurationProbe {
	return a.orch.sourceProbe
}
func (a *orchestratorRunner) ArtifactPreparation() finalization.ArtifactPreparationService {
	return a.artifactPreparation
}
func (a *orchestratorRunner) JobFinalizer() finalization.JobFinalizer {
	return a.jobFinalizer
}
func (a *orchestratorRunner) Log() *zap.Logger { return a.log }
func (a *orchestratorRunner) State() *runState { return a.state }

// RunFingerprint returns the canonical run fingerprint for the
// current call. Byte-stable across retries of the same logical
// run (same jobID + same input + same policyVersion) so the
// per-chunk ArtifactID derivation (ChunkArtifactID) and the
// metadata ArtifactID (MetadataArtifactID) are deterministic
// per godlike/07 no-fake-availability.
//
// Composition: SHA256(PolicyVersion | FolderID | Subfolder |
// FolderName | joined DirectURLs | joined SearchQueries |
// strconv(TotalMinutes, ChunkDuration, ClipDuration, MaxVideos) |
// strconv Bool(NoAudio, NoEffects, NoTransitions)).
//
// Same inputs ⇒ same fingerprint ⇒ same chunk ArtifactIDs ⇒
// no-fake-availability: a retry with the same logical run cannot
// produce a different AssetID, so the JobFinalizer's
// UNIQUE(?job_id, attempt, result_hash) collapse to a single row.
func (a *orchestratorRunner) RunFingerprint() string {
	if a == nil || a.orch == nil {
		return ""
	}
	in := a.in
	if in == nil {
		return ""
	}
	parts := []string{
		a.orch.cfg.PolicyVersion,
		in.FolderID,
		in.Subfolder,
		in.FolderName,
		strings.Join(in.DirectURLs, ","),
		strings.Join(in.SearchQueries, ","),
		strconv.Itoa(in.TotalMinutes),
		strconv.Itoa(in.ChunkDuration),
		strconv.Itoa(in.ClipDuration),
		strconv.Itoa(in.MaxVideos),
		strconv.FormatBool(in.NoAudio),
		strconv.FormatBool(in.NoEffects),
		strconv.FormatBool(in.NoTransitions),
	}
	return files.SHA256String(strings.Join(parts, "|"))
}

func defaultStepRunnerLog() *zap.Logger {
	return zap.NewNop()
}
