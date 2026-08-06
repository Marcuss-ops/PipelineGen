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
	"crypto/sha256"
	"encoding/hex"
	"sync"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// StepRunner is the typed context each step body sees during execution.
type StepRunner interface {
	Cfg() OrchestratorConfig
	RunInput() *RunInput
	JobID() string
	PolicyVersion() string

	Planner() ClipPlanner
	SourceStager() assets.SourceStager
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
	orch                *Orchestrator
	in                  *RunInput
	state               *RunState
	log                 *zap.Logger
	artifactPreparation finalization.ArtifactPreparationService
	jobFinalizer        finalization.JobFinalizer
	fingerprintOnce     sync.Once
	cachedFingerprint   string
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
// Replaces the direct internal/infrastructure/files.SHA256String import
// (godlike/06 import-boundary discipline).
func sha256String(text string) string {
	h := sha256.Sum256([]byte(text))
	return hex.EncodeToString(h[:])
}

func defaultStepRunnerLog() *zap.Logger {
	return zap.NewNop()
}
