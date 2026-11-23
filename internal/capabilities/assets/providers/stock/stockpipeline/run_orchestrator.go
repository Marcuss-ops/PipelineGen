// Package stockpipeline — run_orchestrator.go (Stock Cutover, July 2026).
//
// STATO ATTUALE: Service.runOrchestratorResilient è il canonical
// entrypoint per traffico produzione (Service.HandleJob e
// Service.Run via runSyncPersist).
//
// DEPRECATO: projectManifestToPipelineResult proietta il manifesto
// nel legacy *PipelineResult per il ServiceRunner interface
// (vedi manifest_projection.go).
package stockpipeline

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline/core"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
)

// runOrchestratorResilient is the canonical production entry point.
// Calls Orchestrator.RunResilient to obtain the *RunSummary that pairs
// the typed *job.ArtifactManifest with the per-run FinalStatus.
//
// STATO ATTUALE: Service.HandleJob (production broker traffic) uses
// this variant so FinalStatus surfaces in the result map.
// Service.runOrchestrator (manifest-only) remains for legacy callers.
//
// Resilience contract: artifacts on Drive + pgvector OK ⇒ SUCCEEDED;
// artifacts on Drive + pgvector failed ⇒ INDEX_PENDING;
// manifest-gate failed ⇒ typed sentinel ⇒ JobFailed.
func (s *Service) runOrchestratorResilient(ctx context.Context, input *RunInput, jobID string) (summary *RunSummary, err error) {
	var ownedRun *kernobs.Run
	defer func() {
		if ownedRun != nil {
			ownedRun.FinishWithError(err)
		}
	}()
	if s == nil {
		return nil, fmt.Errorf("Service.runOrchestratorResilient: nil receiver")
	}
	if input == nil {
		return nil, fmt.Errorf("Service.runOrchestratorResilient: nil *RunInput")
	}
	if kernobs.FromContext(ctx) == nil {
		// The job runtime normally binds the canonical Run before entering
		// the stock pipeline. Keep direct callers observable without creating
		// a second timer owner when a run is already present.
		ownedRun = kernobs.NewRunObserver(nil).StartRun(ctx, kernobs.RunInfo{JobID: jobID, AttemptID: kernobs.NewAttemptID()})
		ctx = kernobs.WithRun(ctx, ownedRun)
	}
	// drive_folder_id is the operator-selected parent. Resolve the readable
	// folder_name below it once, then publish round subfolders below that
	// resolved folder. This keeps Drive hierarchy creation inside stock.
	if strings.TrimSpace(input.DriveFolderID) != "" && strings.TrimSpace(input.FolderName) != "" {
		if s.folderCreator == nil {
			return nil, fmt.Errorf("Service.runOrchestratorResilient: folder creator is not wired")
		}
		folderID, folderErr := s.folderCreator.GetOrCreateFolder(ctx, strings.TrimSpace(input.FolderName), strings.TrimSpace(input.DriveFolderID))
		if folderErr != nil {
			return nil, fmt.Errorf("Service.runOrchestratorResilient: create stock root folder: %w", folderErr)
		}
		input.DriveFolderID = folderID
		input.DriveFolderResolved = true
	} else if strings.TrimSpace(input.DriveFolderID) != "" {
		// An already-resolved destination ID is authoritative. Per-clip
		// naming must not create nested folders below it.
		input.DriveFolderResolved = true
	}

	// Resolve text search queries to YouTube URLs before passing to
	// the orchestrator, which only understands DirectURLs.
	searchInputCount := len(input.SearchQueries)
	searchURLCount := len(input.DirectURLs)
	searchMetric := core.StartServiceStockPhase(ctx, "stock.search", jobID)
	searchErr := s.resolveInputQueries(ctx, input)
	if searchMetric != nil {
		searchMetric.SetItems(int64(searchInputCount), int64(len(input.DirectURLs)-searchURLCount))
		core.FinishServiceStockPhase(s.log, searchMetric, searchErr)
	}
	if searchErr != nil {
		return nil, fmt.Errorf("Service.runOrchestratorResilient: %w", searchErr)
	}

	// Direct URLs arrive without provider duration metadata (unlike
	// search-resolved sources). Resolve it before planning so the
	// deterministic planner distributes clip windows inside the REAL source
	// length instead of over-planning past the end of the file. Non-fatal:
	// a failed probe leaves the planner's conservative fallback in place.
	s.enrichDirectURLDurations(ctx, input)

	cfg := OrchestratorConfig{
		JobId:            jobID,
		Lease:            input.FinalizationLease,
		PolicyVersion:    "v1",
		ChunkDurationSec: effectiveChunkDurationSec(input, s),
		ClipDurationSec:  effectiveClipDurationSec(input, s),
	}
	// Execution bounds come from concurrency.max_concurrent_stock_downloads /
	// max_concurrent_stock_cuts. Zeros (operator never set them, or a test/CLI
	// Service with no RuntimeConfig) leave the orchestrator's own defaults in
	// place via the <=0 normalization in the constructors.
	cfg.MaxConcurrentDownloads, cfg.MaxConcurrentJobs = effectiveExecutionBounds(s)
	// Phase 2 (July 2026): wire SQLite-backed step store for
	// crash-resume across process restarts. When db is nil (stock
	// Service routed via imageSvc, WireStockPipeline stubbed), the
	// orchestrator falls back to in-memory (test orchestrator default).
	// PROSSIMO STEP: make DB required when WireStockPipeline is
	// re-enabled.
	if s.stepStore != nil {
		cfg.StepStore = s.stepStore
	}
	planner := NewDeterministicPlanner()
	// Resolve the acquisition.SourceStager for the orchestrator.
	// StockStager implements acquisition.SourceStager via Prepare/Release
	// adapter methods (stager_adapter.go).
	stager := s.stagerForRun()
	writer := TransactionalAssetWriter(nil)
	if s.dispatcher != nil {
		writer = core.NewStockDispatcherWriter(s.dispatcher)
	}
	artifactPreparation := finalization.ArtifactPreparationService(nil)
	if s.publisher != nil {
		artifactPreparation = finalizer.NewArtifactPreparation(s.publisherPort, s.log)
	}
	var o *Orchestrator
	if s.runtimeMode == stockPipelineTestMode {
		// Fixture services are intentionally routed through the fixture
		// constructor. Its in-memory step store and noop resilience ports
		// cannot leak into production because production services take the
		// strict branch below.
		o = NewTestStockOrchestrator(cfg, planner, stager, s.cutter)
	} else {
		var constructErr error
		o, constructErr = NewProductionStockOrchestrator(cfg, ProductionStockPipelineDeps{
			Pipeline: ProductionPipelineDeps{
				Planner: planner, Stager: stager, Cutter: s.cutter,
				Builder: stockManifestBuilder{},
			},
			Persistence: ProductionPersistenceDeps{
				Writer: writer, Projection: s.projection, StepStore: s.stepStore,
				ArtifactPreparation: artifactPreparation, JobFinalizer: s.finalizer,
				BatchRepository: s.batchRepo,
			},
			Runtime: ProductionRuntimeDeps{
				SourceProbe: s.sourceProbe, LocalFS: s.localFS, Logger: s.log,
			},
		})
		if constructErr != nil {
			return nil, fmt.Errorf("Service.runOrchestratorResilient: construct production pipeline: %w", constructErr)
		}
	}
	summary, err = o.RunResilient(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("Service.runOrchestratorResilient: orchestrator.RunResilient: %w", err)
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

// runSyncPersist (July 2026) routes ALL sync paths through the
// resilient orchestrator (RunResilient) with a synthetic broker
// lease, so StockFinalizeStep writes to media_assets via the
// single-TX spine. This is the canonical path for both
// persist=true and persist=false sync-mode stock pipeline requests.
//
// godlike/07 no-fake-availability: the synthetic lease uses
// deterministic identifiers (sync-stock-<nanos>) so every call
// produces a distinct lease — the finalizer's CAS-fence won't
// conflate two sync-mode calls that happen to share a jobID.
//
// The §12-1 P0 #2 gate (in Orchestrator.RunResilient) fires
// typed errors when either publisher or finalizer is nil — the
// caller converts those to the ServiceRunner error surface without
// special-casing.
func (s *Service) runSyncPersist(ctx context.Context, input *RunInput) (*PipelineResult, error) {
	// Generate synthetic identifiers for sync-mode persistence.
	// The lease uses deterministic JobID/WorkerID so the finalizer's
	// CAS-fence (revision-match on jobs table) is still meaningful
	// even without a real broker — the sync mode holds the "lease"
	// for the duration of this call; concurrent sync requests get
	// distinct leases and won't CAS-fence each other.
	jobID := fmt.Sprintf("sync-stock-%d", time.Now().UnixNano())
	input.FinalizationLease = finalization.Lease{
		LeaseID:   jobID + "-lease",
		JobID:     jobID,
		WorkerID:  "sync-mode",
		Attempt:   1,
		ExpiresAt: time.Now().Add(1 * time.Hour),
	}

	if s.jobCreator == nil {
		return nil, fmt.Errorf("Service.runSyncPersist: durable job creator is not wired")
	}
	now := time.Now().UTC()
	if err := s.jobCreator.Create(ctx, &job.Job{
		ID: jobID, Type: "media.stock", Status: job.StatusRunning,
		WorkerID:    input.FinalizationLease.WorkerID,
		LeaseID:     input.FinalizationLease.LeaseID,
		LeaseExpiry: &input.FinalizationLease.ExpiresAt,
		CreatedAt:   now, UpdatedAt: now, Revision: 1,
	}); err != nil {
		return nil, fmt.Errorf("Service.runSyncPersist: insert synthetic job: %w", err)
	}

	// Delegate to the canonical resilient path — runOrchestratorResilient
	// resolves queries, builds the orchestrator with finalizer + asset
	// preparation, and invokes RunResilient. godlike/06 SSOT: the
	// orchestrator construction lives in exactly one method.
	summary, err := s.runOrchestratorResilient(ctx, input, jobID)
	if err != nil {
		return nil, fmt.Errorf("Service.runSyncPersist: %w", err)
	}

	projected, err := projectManifestToPipelineResult(summary.Manifest)
	if err != nil {
		return nil, fmt.Errorf("Service.runSyncPersist: %w", err)
	}
	return projected, nil
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

// effectiveExecutionBounds resolves the stock run's two fan-out bounds from the
// Service's operator config (concurrency.max_concurrent_stock_downloads /
// max_concurrent_stock_cuts, flattened by wiring.stockRuntimeConfig).
//
// Both are returned as 0 when the Service has no RuntimeConfig (test/CLI
// callers) or the operator left them unset, and 0 means "unset" all the way
// down: OrchestratorConfig normalizes <=0 to DefaultMaxConcurrentDownloads and
// DefaultMaxConcurrentJobs. This site therefore must NOT substitute a value the
// operator did not ask for — same contract as the youtube runtime flattening.
//
// Downloads and cuts stay separate because their bottlenecks differ: staging
// pays yt-dlp's fixed per-invocation cost (latency-bound), cutting is CPU-bound.
func effectiveExecutionBounds(s *Service) (downloads, cuts int) {
	if s == nil || s.runtime == nil {
		return 0, 0
	}
	return s.runtime.MaxConcurrentDownloads, s.runtime.MaxConcurrentCuts
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

// Run is the Stock Cutover thin Orchestrator delegate.
//
// Signature preserved at (*PipelineResult, error) for backwards
// compat with the ServiceRunner interface
// (var _ ServiceRunner = (*Service)(nil) in stockpipeline/usecase.go)
// and stock.Adapter's stockRunner interface.
//
// All sync paths now route through runSyncPersist →
// runOrchestratorResilient with a synthetic broker lease, so
// StockFinalizeStep writes to media_assets via the single-TX spine
// (same contract as production broker jobs).
//
// On error the underlying orchestrator error is wrapped via %w
// so callers can errors.Is/As inspect the orchestrator's signal
// class (ErrOrchestratorNilDeps etc.).
func (s *Service) Run(ctx context.Context, input *RunInput) (*PipelineResult, error) {
	if s == nil {
		return nil, fmt.Errorf("Service.Run: nil receiver")
	}
	if input == nil {
		return nil, fmt.Errorf("Service.Run: nil *RunInput")
	}

	// All sync paths route through the resilient orchestrator
	// via runSyncPersist, which provides a synthetic lease and
	// delegates to runOrchestratorResilient.
	return s.runSyncPersist(ctx, input)
}
