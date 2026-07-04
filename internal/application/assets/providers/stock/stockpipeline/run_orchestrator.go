// Package stockpipeline — run_orchestrator.go (Stock Cutover, July 2026).
//
// STATO ATTUALE: Service.runOrchestratorResilient è il canonical
// entrypoint per traffico produzione (Service.HandleJob).
// Service.runOrchestrator è la versione manifest-only usata dal
// path legacy Service.Run (ServiceRunner interface back-compat).
//
// PROSSIMO STEP: migrare Service.runOrchestrator →
// runOrchestratorResilient e collassare Run in RunResilient.
//
// DEPRECATO: projectManifestToPipelineResult proietta il manifesto
// nel legacy *PipelineResult per il ServiceRunner interface.
package stockpipeline

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
)

// runOrchestrator is the manifest-only entry point for legacy
// Service.Run callers (ServiceRunner interface back-compat).
// Production traffic uses runOrchestratorResilient instead.
//
// PROSSIMO STEP: migrate all callers to runOrchestratorResilient
// and collapse this function.
//
// Configuration precedence (preserves the legacy run.go override chain;
// see effectiveChunkDurationSec / effectiveClipDurationSec below).
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

// runOrchestratorResilient is the canonical production entry point.
// Calls Orchestrator.RunResilient to obtain the *RunSummary that pairs
// the typed *job.ArtifactManifest with the per-run FinalStatus.
//
// STATO ATTUALE: Service.HandleJob (production broker traffic) uses
// this variant so FinalStatus surfaces in the result map.
// Service.runOrchestrator (manifest-only) remains for legacy callers.
//
// Resilience contract: artifacts on Drive + Qdrant OK ⇒ SUCCEEDED;
// artifacts on Drive + Qdrant failed ⇒ INDEX_PENDING;
// manifest-gate failed ⇒ typed sentinel ⇒ JobFailed.
func (s *Service) runOrchestratorResilient(ctx context.Context, input *RunInput, jobID string) (*RunSummary, error) {
	if s == nil {
		return nil, fmt.Errorf("stockpipeline.Service.runOrchestratorResilient: nil receiver")
	}
	if input == nil {
		return nil, fmt.Errorf("stockpipeline.Service.runOrchestratorResilient: nil *RunInput")
	}
	cfg := OrchestratorConfig{
		JobId:            jobID,
		Lease:            input.FinalizationLease,
		PolicyVersion:    "v1",
		ChunkDurationSec: effectiveChunkDurationSec(input, s),
		ClipDurationSec:  effectiveClipDurationSec(input, s),
	}
	// Phase 2 (July 2026): wire SQLite-backed step store for
	// crash-resume across process restarts. When db is nil (stock
	// Service routed via imageSvc, WireStockPipeline stubbed), the
	// orchestrator falls back to in-memory (NewOrchestrator default).
	// PROSSIMO STEP: make DB required when WireStockPipeline is
	// re-enabled.
	if s.db != nil {
		cfg.StepStore = steps.NewSQLiteStore(s.db)
	}
	o := NewOrchestrator(
		cfg,
		NewDeterministicPlanner(),
		NewInMemoryStepStore(),
		s.stagerForRun(),
		s.cutter,
		s.renderer,
	)
	// §12-7 (July 2026): thread the Finalizer-side ports via the
	// orchestrator's fluent setters. AssetPreparation wraps the
	// canonical delivery.Publisher so StockPublishStep's per-chunk
	// + per-metadata.json ArtifactPreparation.Prepare calls land on
	// the single canonical upload surface (godlike/06 SSOT one-owner-
	// per-fact). JobFinalizer is the canonical single-TX spine-write
	// service that StockFinalizeStep.CompleteWithArtifacts invokes.
	if s.publisher != nil {
		o.WithAssetPreparation(finalizer.NewArtifactPreparation(
			drive.NewArtifactPublisherAdapter(s.publisher, s.log), s.log))
	}
	o.WithJobFinalizer(s.finalizer)
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
// *job.ArtifactManifest into the legacy *PipelineResult used by
// pre-cutover callers via the ServiceRunner interface.
//
// STATO ATTUALE: la proiezione è identity-shaped (campi zero)
// perché il ServiceRunner interface non è ancora stato migrato.
//
// PROSSIMO STEP: quando ServiceRunner viene ritirato (post-Commit-9
// cleanup wave), questa funzione e *PipelineResult possono essere
// rimossi.
//
// DEPRECATO: tenere solo per back-compat ServiceRunner.
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
