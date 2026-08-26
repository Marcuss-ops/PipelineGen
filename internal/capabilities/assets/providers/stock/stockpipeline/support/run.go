// Package stockpipeline — run.go (Stock Cutover Commit 2, July 2026).
//
// Commit 2 DUAL-WRITE retired: stockpipeline.Service.Run body now delegates to
// the new stockpipeline.Orchestrator (per user-spec literal "Set stock.Service.Run
// = new orchestrator only"). The legacy ~280-line body that
// called resolveQuery / processSingleVideo / renderChunk /
// / InterleaveClips has been MOVED OUT of this file. The legacy
// helpers themselves (run_upload.go, query.go, download_helpers.go,
// etc.) STAY ON DISK as part of the package — they remain
// compiled, but stockpipeline.Service.Run no longer invokes any of them.
//
// Pre-cutover callers (stockpipeline.StockUseCase via stockpipeline.ServiceRunner,
// stock.Adapter via stockRunner) continue to call stockpipeline.Service.Run with
// the legacy signature; the body's job is to project the typed
// *job.ArtifactManifest back into the legacy *stockpipeline.PipelineResult so
// these callers remain source-stable.
//
// Production HandleJob traffic uses stockpipeline.Service.runOrchestrator directly
// (see service.go::HandleJob) and emits the typed manifest via
// the broker's __artifact_manifest key. See run_orchestrator.go
// for the canonical stockpipeline.Orchestrator-construction + projection helpers.
//
//nolint:audit-pin:gdl-07-14 stock-cutover-commit4-expanded
package support

import (
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	"context"
	"fmt"
)

// Run is the Stock Cutover thin stockpipeline.Orchestrator delegate.
//
// Signature preserved at (*stockpipeline.PipelineResult, error) for backwards
// compat with the stockpipeline.ServiceRunner interface
// (var _ stockpipeline.ServiceRunner = (*stockpipeline.Service)(nil) in stockpipeline/usecase.go)
// and stock.Adapter's stockRunner interface.
//
// All sync paths now route through runSyncPersist →
// runOrchestratorResilient with a synthetic broker lease, so
// stockpipeline.StockFinalizeStep writes to media_assets via the single-TX spine
// (same contract as production broker jobs).
//
// On error the underlying orchestrator error is wrapped via %w
// so callers can errors.Is/As inspect the orchestrator's signal
// class (stockpipeline.ErrOrchestratorNilDeps etc.).
func (s *stockpipeline.Service) Run(ctx context.Context, input *stockpipeline.RunInput) (*stockpipeline.PipelineResult, error) {
	if s == nil {
		return nil, fmt.Errorf("stockpipeline.Service.Run: nil receiver")
	}
	if input == nil {
		return nil, fmt.Errorf("stockpipeline.Service.Run: nil *stockpipeline.RunInput")
	}

	// All sync paths route through the resilient orchestrator
	// via runSyncPersist, which provides a synthetic lease and
	// delegates to runOrchestratorResilient.
	return s.runSyncPersist(ctx, input)
}
