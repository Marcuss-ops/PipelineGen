// Package stockpipeline — run_orchestrator.go (Stock Cutover Commit 2, July 2026).
//
// Service.runOrchestrator is the canonical entrypoint for the
// new Orchestrator-driven pipeline. It supersedes the legacy
// Service.Run body for production traffic:
//
//   - Service.HandleJob → s.runOrchestrator(ctx, input, job.ID)
//     (broker-driven; the typed manifest is exposed via the
//     __artifact_manifest map key for the worker runner).
//   - Service.Run → s.runOrchestrator(ctx, input, "")
//     (legacy-signature shim for ServiceRunner interface callers;
//     the empty JobId falls back to DefaultOrchestratorJobId).
//
// projectManifestToPipelineResult projects the typed
// *job.ArtifactManifest back into the legacy *PipelineResult
// shape (zero-fields today — Commit 4-7 hydrates once chunk
// rendering is wired).
package stockpipeline

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// runOrchestrator is the Stock Cutover Commit 2 canonical entry
// point. Constructs a fresh *Orchestrator per call (the planner +
// step store are cheap; per-call construction sidesteps the
// thread-safety concern of caching stateful components).
//
// jobID is stamped on the returned ArtifactManifest.JobID.
// Service.HandleJob passes the broker-assigned job.ID; Service.Run
// passes "" (the placeholder DefaultOrchestratorJobId kicks in via
// NewOrchestrator).
//
// Configuration precedence (preserves the legacy run.go override
// chain):
//   - ChunkDurationSec: input.ChunkDuration → s.pcfg.ChunkDuration →
//     s.cfg.Video.WithDefaults().ChunkDuration
//   - ClipDurationSec:  input.ClipDuration  → s.cfg.Video.WithDefaults().ClipDuration
//
// The orchestrator's chunk ladder (resolve_sources → plan_clips →
// stage_sources) runs through the planner + steps + noop-stager
// today. The cutter + renderer ports are wired but not yet
// invoked — Commit 4-7 (Cut → Render → Stage → Publish ladder)
// connects them to the orchestrator's step ladder; the orchestrator
// already accepts them as Orchestrator fields so production wiring
// in Commit 4-7 is type-stable.
func (s *Service) runOrchestrator(ctx context.Context, input *RunInput, jobID string) (*job.ArtifactManifest, error) {
	if s == nil {
		return nil, fmt.Errorf("stockpipeline.Service.runOrchestrator: nil receiver")
	}
	if input == nil {
		return nil, fmt.Errorf("stockpipeline.Service.runOrchestrator: nil *RunInput")
	}
	cfg := OrchestratorConfig{
		JobId:            jobID,
		PolicyVersion:    "v1",
		ChunkDurationSec: effectiveChunkDurationSec(input, s),
		ClipDurationSec:  effectiveClipDurationSec(input, s),
	}
	o := NewOrchestrator(
		cfg,
		NewDeterministicPlanner(),
		NewInMemoryStepStore(),
		s.stagerForRun(),
		s.cutter,
		s.renderer,
	)
	manifest, err := o.Run(ctx, input)
	if err != nil {
		// Preserve the orchestrator's signal class via wrap so callers
		// can errors.Is(orchestrator-senterr). The Service.Run shim
		// wraps once more so the double-wrap is observable in tests
		// — service callers that want the inner error unwrap should
		// use runOrchestrator directly (HandleJob does).
		return nil, fmt.Errorf("stockpipeline.Service.runOrchestrator: orchestrator.Run: %w", err)
	}
	if s.log != nil {
		s.log.Info("stock orchestrator run succeeded",
			zap.String("job_id", manifest.JobID),
			zap.String("workflow_id", manifest.WorkflowID),
			zap.Int("artifact_count", len(manifest.Artifacts)),
		)
	}
	return manifest, nil
}

// runOrchestratorResilient is the Stock Cutover Commit 4-expanded sibling
// of runOrchestrator. It calls Orchestrator.RunResilient (not
// Orchestrator.Run) to obtain the *RunSummary that pairs the typed
// *job.ArtifactManifest with the per-run FinalStatus the broker
// JobFinalizer stamps on the job row (canonical resilience contract:
// artifacts on Drive + Qdrant projection failed ⇒ job.StatusIndexPending;
// artifacts on Drive + Qdrant projection OK ⇒ job.StatusSucceeded;
// signature/manifest-gate failed ⇒ typed sentinel ⇒ JobFailed).
//
// Surface NON-BREAKING: runOrchestrator (manifest-only) remains active
// for the existing run_orchestrator_test.go tests + the legacy
// ServiceRunner interface (stock -> usecase). Only HandleJob
// (production broker traffic) uses this variant so FinalStatus surfaces
// in the result map under "final_status".
func (s *Service) runOrchestratorResilient(ctx context.Context, input *RunInput, jobID string) (*RunSummary, error) {
	if s == nil {
		return nil, fmt.Errorf("stockpipeline.Service.runOrchestratorResilient: nil receiver")
	}
	if input == nil {
		return nil, fmt.Errorf("stockpipeline.Service.runOrchestratorResilient: nil *RunInput")
	}
	cfg := OrchestratorConfig{
		JobId:            jobID,
		PolicyVersion:    "v1",
		ChunkDurationSec: effectiveChunkDurationSec(input, s),
		ClipDurationSec:  effectiveClipDurationSec(input, s),
	}
	o := NewOrchestrator(
		cfg,
		NewDeterministicPlanner(),
		NewInMemoryStepStore(),
		s.stagerForRun(),
		s.cutter,
		s.renderer,
	)
	summary, err := o.RunResilient(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("stockpipeline.Service.runOrchestratorResilient: orchestrator.RunResilient: %w", err)
	}
	if s.log != nil {
		s.log.Info("stock orchestrator resilient run succeeded",
			zap.String("job_id", summary.Manifest.JobID),
			zap.String("final_status", string(summary.FinalStatus)),
			zap.Int("artifact_count", len(summary.Manifest.Artifacts)),
		)
	}
	return summary, nil
}

// projectManifestToPipelineResult converts the typed
// *job.ArtifactManifest (canonical post-cutover shape, C12
// 5-artifact envelope) into the legacy *PipelineResult used by
// pre-cutover callers via the ServiceRunner interface
// (stockpipeline.StockUseCase.Submit sync path) and stock.Adapter
// via stockRunner.
//
// Today the projection is identity-shaped: the orchestrator's
// 5 fixed C12 envelope entries have empty Paths (Required:false)
// so PipelineResult.Chunks/MetadataLink/MetadataFileID stay at
// their zero values. Commit 4-7 (chunk ladder) hydrates these
// fields once the orchestrator emits per-chunk Artifact entries
// with real drive links / metadata paths from the binder run.
//
// Why keep this shim rather than changing Service.Run's signature?
// The ServiceRunner interface compile-time assertion
// (var _ ServiceRunner = (*Service)(nil) in stockpipeline/usecase.go)
// locks Service.Run's return type to *PipelineResult. Changing it
// would force a ServiceRunner + stockRunner + StockUseCase +
// Adapter + Submit sync-path rewrite, which is out of scope for
// Commit 2. Post-cutover (Commit 9 cleanup wave), the legacy shape
// can be retired and the projection collapses.
func projectManifestToPipelineResult(manifest *job.ArtifactManifest) *PipelineResult {
	if manifest == nil {
		return &PipelineResult{}
	}
	return &PipelineResult{
		SearchTerms:    nil, // manifest does not carry SearchTerms in C12 envelope
		TotalClips:     0,   // Commit 4-7 hydrates from Manifest.Artifacts (chunk entries → chunk videos → clip count)
		TotalChunks:    0,   // Commit 4-7 hydrates from Manifest.Artifacts (chunk entries count)
		Chunks:         nil, // Commit 4-7 hydrates from Manifest.Artifacts (each Artifact entry → ChunkResult)
		MetadataLink:   "",  // Commit 4-7 hydrates from Manifest.Artifacts[0].RemoteAssetID (metadata Artifact)
		MetadataFileID: "",  // Commit 4-7 hydrates from Manifest.Artifacts[0].RemoteAssetID (metadata Artifact)
	}
}

// effectiveChunkDurationSec resolves the per-run chunk duration
// (sec) override chain. Mirrors the prior run.go body semantics
// (input.ChunkDuration takes precedence over s.pcfg.ChunkDuration
// which falls back to s.cfg.Video.WithDefaults().ChunkDuration).
//
// Centralised here so Service.Run and Service.runOrchestrator
// (and future Commit 4-7 entrypoints) share the same override
// chain without re-deriving it on every call site.
func effectiveChunkDurationSec(input *RunInput, s *Service) int {
	if input != nil && input.ChunkDuration > 0 {
		return input.ChunkDuration
	}
	if s != nil && s.pcfg.ChunkDuration > 0 {
		return s.pcfg.ChunkDuration
	}
	if s != nil && s.cfg != nil {
		return s.cfg.Video.WithDefaults().ChunkDuration
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
	if s != nil && s.cfg != nil {
		return s.cfg.Video.WithDefaults().ClipDuration
	}
	return 0
}

// stagerForRun resolves the canonical assets.SourceStager for the
// stock pipeline (Commit 1.2 — Stock Cutover, July 2026).
//
// godlike/06 SSOT: this helper centralises registry construction so
// production wiring has one canonical entry point per run. Today
// the registry carries a single SourceKindExistingCatalog entry
// (StockStager wrapping Service.StageSource — the only SourceStager
// adapter the stock pipeline actually invokes at runtime). Future
// commit waves add YouTube / Artlist / Drive / HTTP / per-source-kind
// dispatch when the orchestrator's stage_sources step gains real
// Stage invocations (currently Begin/Complete only).
//
// nil receiver returns a nil SourceStager; the orchestrator's
// nil-guard handles that case (ErrOrchestratorNilDeps) so the
// production error path is observable.
func (s *Service) stagerForRun() assets.SourceStager {
	if s == nil {
		return nil
	}
	reg := assets.NewSourceStagerRegistry()
	// Existing-catalog path is the only kind the stock pipeline
	// dispatches today. StockStager wraps Service.StageSource
	// (the canonical yt-dlp-backed download path) and satisfies
	// assets.SourceStager via the compile-time assertion at
	// stager_adapter.go:18.
	if err := reg.Register(assets.SourceKindExistingCatalog, NewStockStager(s)); err != nil {
		// godlike/07 typed-error path: log+drop for production;
		// tests assert via the registry's own error sentinels.
		return nil
	}
	stager, err := reg.Resolve(assets.SourceKindExistingCatalog)
	if err != nil {
		return nil
	}
	return stager
}
