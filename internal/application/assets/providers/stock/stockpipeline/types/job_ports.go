// Package stockpipeline — job_ports.go (PR-SPLIT-STOCK-PORTS, July 2026).
//
// Owns the job-side narrow infra ports used by the stock pipeline
// (3 narrow Pattern 0 interfaces scoped to the methods the pipeline
// actually invokes). Extracted from ports.go per godlike/06 SSOT
// one-canonical-owner-per-fact: this file is the SOLE canonical owner
// of stockAssetIndexUpserter + stockClipsSearchTermUpdater +
// stockChunkDispatcher.
//
// Each interface exposes only the methods the stock pipeline actually
// calls, so test fakes satisfy them via Go's structural subtyping without
// dragging the full concrete surface into test fixtures.
//
// Moved from service.go so infrastructure imports are confined to
// job_ports.go — service.go stays clean of internal/infrastructure/...
// imports (godlike/06 import-boundary discipline).
package types

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
)

// stockAssetIndexUpserter is the narrow surface the stock pipeline
// uses from *assetindex.Service. Only Upsert is invoked
//
//nolint:audit-pin:gdl-07-14 stock-cutover-commit4-expanded
type stockAssetIndexUpserter interface {
	Upsert(ctx context.Context, rec *assetindex.AssetRecord) error
}

// stockClipsSearchTermUpdater is the narrow surface the stock pipeline
// uses from *assets.ClipsRepository. Only UpdateSearchTerms is invoked
//
//nolint:audit-pin:gdl-07-14 stock-cutover-commit4-expanded
type stockClipsSearchTermUpdater interface {
	UpdateSearchTerms(ctx context.Context, clipID, source, name string, tags []string, searchText string) error
}

// stockChunkDispatcher is the narrow surface the stock pipeline
// uses from *outbox.Dispatcher. Only EnqueueAndIndex is invoked.
//
//nolint:audit-pin:gdl-07-14 stock-cutover-commit4-expanded
type stockChunkDispatcher interface {
	EnqueueAndIndex(ctx context.Context, clip *asset.Asset, fileHash string) error
}
