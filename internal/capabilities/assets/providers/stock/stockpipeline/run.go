// Package stockpipeline — run.go (Stock Cutover Commit 2, July 2026).
//
// Commit 2 DUAL-WRITE retired: Service.Run body now delegates to
// the new Orchestrator (per user-spec literal "Set stock.Service.Run
// = new orchestrator only"). The legacy ~280-line body that
// called resolveQuery / processSingleVideo / renderChunk /
// / InterleaveClips has been MOVED OUT of this file. The legacy
// helpers themselves (run_upload.go, query.go, download_helpers.go,
// etc.) STAY ON DISK as part of the package — they remain
// compiled, but Service.Run no longer invokes any of them.
//
// Pre-cutover callers (StockUseCase via ServiceRunner,
// stock.Adapter via stockRunner) continue to call Service.Run with
// the legacy signature; the body's job is to project the typed
// *job.ArtifactManifest back into the legacy *PipelineResult so
// these callers remain source-stable.
//
// Production HandleJob traffic uses Service.runOrchestrator directly
// (see service.go::HandleJob) and emits the typed manifest via
// the broker's __artifact_manifest key. See run_orchestrator.go
// for the canonical Orchestrator-construction + projection helpers.
//
//nolint:audit-pin:gdl-07-14 stock-cutover-commit4-expanded
package stockpipeline

import (
	"context"
	"fmt"
)

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
