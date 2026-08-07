package stockpipeline

import (
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
)

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
	stockStager := NewStockStager(s).
		WithSourceCache(s.sourceCacheReader, s.sourceCacheWriter).
		WithDownloader(serviceSourceDownloader{service: s})
	if s.driveReader != nil {
		stockStager = stockStager.WithDriveReader(s.driveReader)
	}
	if err := reg.Register(assets.SourceKindExistingCatalog, stockStager); err != nil {
		// godlike/07 typed-error path: log+drop for production;
		// tests assert via the registry's own error sentinels.
		return nil
	}
	resolvedStager, err := reg.Resolve(assets.SourceKindExistingCatalog)
	if err != nil {
		return nil
	}
	return resolvedStager
}
