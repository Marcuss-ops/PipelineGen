// Package stockpipeline — job_ports.go (PR-SPLIT-STOCK-PORTS, July 2026).
//
// Owns the job-side narrow infra ports used by the stock pipeline
// (2 narrow Pattern 0 interfaces scoped to the methods the pipeline
// actually invokes). Extracted from ports.go per godlike/06 SSOT
// one-canonical-owner-per-fact: this file is the SOLE canonical owner
// of stockChunkDispatcher.
//
// WAVE 9 (September 2026): the `clip_search_terms` updater port is GONE.
// The canonical commit already persists `media_assets.search_text` (the
// PostgreSQL search authority); a second inverted index maintained from
// an operational SQLite table was a shadow media index and had to be
// removed rather than synchronised.
//
// Each interface exposes only the methods the stock pipeline actually
// calls, so test fakes satisfy them via Go's structural subtyping without
// dragging the full concrete surface into test fixtures.
//
// Moved from service.go so infrastructure imports are confined to
// job_ports.go — service.go stays clean of internal/platform/
// imports (godlike/06 import-boundary discipline).
package stockpipeline

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// StockAssetUpsertRecord is the application-layer representation of an
// asset index record for upsert. Defined locally so the stock pipeline
// stays free of internal/platform/sqlite/assetindex imports
// (godlike/06 import-boundary discipline). The composition root adapts
// the concrete assetindex.AssetRecord to this type.
type StockAssetUpsertRecord struct {
	AssetID string
}

// stockAssetIndexUpserter is the narrow surface the stock pipeline
// uses from *assetindex.Service. Only Upsert is invoked.
//
//nolint:audit-pin:gdl-07-14 stock-cutover-commit4-expanded
type stockAssetIndexUpserter interface {
	Upsert(ctx context.Context, rec *StockAssetUpsertRecord) error
}

// stockChunkDispatcher is the narrow surface the stock pipeline
// uses from *outbox.Dispatcher. Only EnqueueAndIndex is invoked.
//
//nolint:audit-pin:gdl-07-14 stock-cutover-commit4-expanded
type stockChunkDispatcher interface {
	EnqueueAndIndex(ctx context.Context, clip *asset.Asset, fileHash string) error
}
