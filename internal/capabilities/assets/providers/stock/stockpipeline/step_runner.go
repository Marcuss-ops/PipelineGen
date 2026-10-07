// Package stockpipeline — step_runner.go (Stock P1 split, July 2026).
//
// This file owns the StepRunner interface, the RunState accumulator,
// the orchestratorRunner concrete implementation, its accessor methods,
// and the RunFingerprint computation. Extracted from orchestrator_steps.go
// (Stock P0 action plan).
//
// godlike/06 SSOT: StepRunner is the single seam between per-step
// bodies and the Orchestrator. Steps MUST NOT access Orchestrator
// fields directly — they go through the StepRunner accessors.
package stockpipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	assets "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// StepRunner is the typed context each step body sees during execution.
type StepRunner interface {
	Cfg() OrchestratorConfig
	RunInput() *RunInput
	JobID() string
	PolicyVersion() string

	Planner() ClipPlanner
	SourceStager() acquisition.SourceStager
	Cutter() VideoCutter
	Renderer() StockRenderer
	Builder() ManifestBuilder
	Writer() TransactionalAssetWriter
	Projection() ProjectionPort
	SourceDurationProbe() SourceDurationProbe
	BatchRepository() StockBatchRepository
	LocalFS() LocalFSPort

	ArtifactPreparation() finalization.ArtifactPreparationService
	JobFinalizer() finalization.JobFinalizer
	RunFingerprint() string

	// DestinationReconciler returns the post-publish hygiene pass that removes
	// stale artifacts left by earlier plans in the destination folder. Nil is
	// the supported "not wired" value (test fixtures, un-wired roots).
	DestinationReconciler() DestinationReconciler

	Log() *zap.Logger
	State() *RunState
}

// RunState is the mutable per-call accumulator shared by the ordered steps.
type RunState struct {
	Plan               []ClipPlan
	StagedAssets       []*assets.StagedAsset
	CutPaths           []string
	ComposedPaths      []string
	Published          []ChunkState
	MetadataPublished  MetadataState
	Manifest           *job.ArtifactManifest
	FinalStatus        job.Status
	FinalizationResult *finalization.FinalizationResult
	Counts             RunCounts
	// SourceErrors preserves per-source staging failures so partial
	// work is observable in checkpoints, diagnostics, and the wrapped
	// incomplete-source error; failures must not exist only in logs.
	SourceErrors map[string]string
}

// orchestratorRunner is the canonical StepRunner implementation.
type orchestratorRunner struct {
	orch                  *Orchestrator
	in                    *RunInput
	state                 *RunState
	log                   *zap.Logger
	artifactPreparation   finalization.ArtifactPreparationService
	jobFinalizer          finalization.JobFinalizer
	destinationReconciler DestinationReconciler
	fingerprintOnce       sync.Once
	cachedFingerprint     string
}

// BatchRepository returns the durable stock batch repository
// wired by the composition root. nil means test/backcompat mode.
func (a *orchestratorRunner) BatchRepository() StockBatchRepository {
	if a == nil || a.orch == nil {
		return nil
	}
	return a.orch.batchRepository
}

var _ StepRunner = (*orchestratorRunner)(nil)

func (a *orchestratorRunner) Cfg() OrchestratorConfig                { return a.orch.cfg }
func (a *orchestratorRunner) RunInput() *RunInput                    { return a.in }
func (a *orchestratorRunner) JobID() string                          { return a.orch.cfg.JobId }
func (a *orchestratorRunner) PolicyVersion() string                  { return a.orch.cfg.PolicyVersion }
func (a *orchestratorRunner) Planner() ClipPlanner                   { return a.orch.planner }
func (a *orchestratorRunner) SourceStager() acquisition.SourceStager { return a.orch.stager }
func (a *orchestratorRunner) Cutter() VideoCutter                    { return a.orch.cutter }
func (a *orchestratorRunner) Renderer() StockRenderer                { return a.orch.renderer }
func (a *orchestratorRunner) Builder() ManifestBuilder               { return a.orch.builder }
func (a *orchestratorRunner) Writer() TransactionalAssetWriter       { return a.orch.writer }
func (a *orchestratorRunner) Projection() ProjectionPort             { return a.orch.projection }
func (a *orchestratorRunner) SourceDurationProbe() SourceDurationProbe {
	return a.orch.sourceProbe
}
func (a *orchestratorRunner) LocalFS() LocalFSPort {
	if a == nil || a.orch == nil {
		return nil
	}
	return a.orch.localFS
}
func (a *orchestratorRunner) ArtifactPreparation() finalization.ArtifactPreparationService {
	return a.artifactPreparation
}
func (a *orchestratorRunner) JobFinalizer() finalization.JobFinalizer {
	return a.jobFinalizer
}

func (a *orchestratorRunner) DestinationReconciler() DestinationReconciler {
	return a.destinationReconciler
}

func (a *orchestratorRunner) Log() *zap.Logger { return a.log }
func (a *orchestratorRunner) State() *RunState { return a.state }

// RunFingerprint returns the canonical content-addressed identity for this
// run. It deliberately reuses the same structured payload projection as the
// per-step checkpoint fingerprint instead of maintaining a second delimiter-
// joined list of selected fields. This keeps artifact IDs, batch IDs, and
// checkpoint keys sensitive to the same relevant inputs (including explicit
// clips, Drive sources, metadata, and duration policy).
func (a *orchestratorRunner) RunFingerprint() string {
	if a == nil || a.orch == nil || a.in == nil {
		return ""
	}
	a.fingerprintOnce.Do(func() {
		a.cachedFingerprint = stepInputFingerprint(
			a.orch.cfg.JobId,
			"stock.run",
			a.orch.cfg,
			a.in,
			nil,
		)
	})
	return a.cachedFingerprint
}

// sha256String returns the lowercase hex-encoded SHA-256 digest of text.
// Replaces the direct internal/platform/filesystem.SHA256String import
// (godlike/06 import-boundary discipline).
func sha256String(text string) string {
	h := digest.SHA256Bytes([]byte(text))
	return h
}

func defaultStepRunnerLog() *zap.Logger {
	return zap.NewNop()
}

// ── Durable step history + RunState checkpoint codec ────────────────
// loadCompletedStepRows returns only the latest row per stage when that row is
// completed. A newer failed row supersedes an older completed row, allowing a
// retry to execute instead of skipping stale work.
func (o *Orchestrator) loadCompletedStepRows(ctx context.Context, jobID string) (map[string]steps.StepState, error) {
	if o.stepStore == nil {
		return nil, steps.ErrStoreNotWired
	}
	history, err := o.stepStore.ListByJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	latest := make(map[string]steps.StepState)
	for _, row := range history {
		if existing, ok := latest[row.StepKey]; !ok || row.ID > existing.ID {
			latest[row.StepKey] = row
		}
	}
	completed := make(map[string]steps.StepState, len(latest))
	for stepKey, row := range latest {
		if row.Status == steps.StatusCompleted {
			completed[stepKey] = row
		}
	}
	return completed, nil
}

const currentRunStateCheckpointVersion = 1

// runStateCheckpoint is a flat, versioned envelope. Embedding RunState keeps
// historical top-level fields such as "Plan" readable in v0 checkpoints.
type runStateCheckpoint struct {
	CheckpointVersion int `json:"checkpoint_version"`
	RunState
}

func marshalRunStateCheckpoint(state *RunState) ([]byte, error) {
	if state == nil {
		return nil, fmt.Errorf("nil RunState")
	}
	return json.Marshal(runStateCheckpoint{
		CheckpointVersion: currentRunStateCheckpointVersion,
		RunState:          *state,
	})
}

// rehydrateRunState validates and decodes both historical flat checkpoints
// (v0) and the current flat envelope (v1). Future or malformed versions fail
// closed instead of silently restoring an empty state.
func rehydrateRunState(result json.RawMessage) (RunState, error) {
	if len(result) == 0 {
		return RunState{}, fmt.Errorf("rehydrateRunState: empty checkpoint result")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(result, &fields); err != nil {
		return RunState{}, fmt.Errorf("rehydrateRunState: unmarshal: %w", err)
	}
	if len(fields) == 0 {
		return RunState{}, fmt.Errorf("rehydrateRunState: checkpoint has no RunState fields")
	}

	versionRaw, versioned := fields["checkpoint_version"]
	if !versioned {
		var legacy RunState
		if err := json.Unmarshal(result, &legacy); err != nil {
			return RunState{}, fmt.Errorf("rehydrateRunState: legacy unmarshal: %w", err)
		}
		return legacy, nil
	}

	var version int
	if string(versionRaw) == "null" {
		return RunState{}, fmt.Errorf("rehydrateRunState: checkpoint_version is null")
	}
	if err := json.Unmarshal(versionRaw, &version); err != nil {
		return RunState{}, fmt.Errorf("rehydrateRunState: checkpoint_version: %w", err)
	}
	if version != currentRunStateCheckpointVersion {
		return RunState{}, fmt.Errorf("rehydrateRunState: unsupported checkpoint_version=%d (want %d)", version, currentRunStateCheckpointVersion)
	}
	if !hasRunStateField(fields) {
		return RunState{}, fmt.Errorf("rehydrateRunState: versioned checkpoint has no RunState fields")
	}

	var checkpoint runStateCheckpoint
	if err := json.Unmarshal(result, &checkpoint); err != nil {
		return RunState{}, fmt.Errorf("rehydrateRunState: versioned unmarshal: %w", err)
	}
	return checkpoint.RunState, nil
}

// hasRunStateField allows additive unknown fields within v1, while refusing a
// versioned payload that contains no recognized RunState data at all.
func hasRunStateField(fields map[string]json.RawMessage) bool {
	for key := range fields {
		switch key {
		case "Plan", "StagedAssets", "CutPaths", "ComposedPaths", "Published",
			"MetadataPublished", "Manifest", "FinalStatus", "FinalizationResult",
			"Counts", "SourceErrors":
			return true
		}
	}
	return false
}
