// Package persistence — aux_ports.go: the narrow, engine-named write ports
// for the media aggregate's auxiliary surfaces (asset_locations and
// asset_processing).
//
// WHY THESE EXIST SEPARATELY FROM AssetCommitter. media_assets, asset_locations
// and the index outbox are committed atomically through AssetCommitter. Two
// further media-authoritative surfaces have a DIFFERENT write shape:
//
//   - asset_locations is also mutated point-wise (a single location upserted
//     after the media row already exists, e.g. the ingest lifecycle attaching
//     a local/Drive location);
//   - asset_processing is per-step pipeline progress whose rows transition
//     repeatedly over the life of one asset (Start → Complete/Fail), so it can
//     never ride inside the single create transaction.
//
// Both were previously written through the generic
// detail.LocationRepository / detail.ProcessingRepository seam. That seam
// carries no information about which database owns the write, so the
// composition root satisfied it with the operational SQLite facade while
// PostgreSQL held media_assets: the media aggregate was split across two
// engines and every SQL-level gate stayed green. Naming the surfaces here
// removes that ambiguity — the PostgreSQL committer answers both, the
// resolution rule lives in exactly one place (CanonicalX below), and a caller
// that loses the port fails closed instead of writing an unnamed database.
//
// godlike/06 SSOT: these ports declare no SQL and no engine. The single
// resolution rule is CanonicalAssetLocationWriter / CanonicalAssetProcessingWriter;
// capabilities compose those instead of type-asserting the committer themselves.
package persistence

import (
	"context"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// AssetLocationWriter is the narrow point-wise asset_locations write port
// owned by the canonical asset persistence boundary.
//
// Narrowness is deliberate (Pattern 0): a caller that only attaches a storage
// location cannot reach the other media mutations by accident. The
// PostgreSQL concrete (PostgresMediaCommitter) answers it against the same
// asset_locations table the atomic CommitRequest path writes, so a location
// attached after the commit lands on the media SSOT rather than the
// operational mirror.
type AssetLocationWriter interface {
	// UpsertAssetLocation inserts or updates one (asset_id, location_kind)
	// row. Implementations MUST be idempotent: the canonical conflict target
	// is (asset_id, location_kind).
	UpsertAssetLocation(ctx context.Context, loc *asset.Location) error
}

// AssetProcessingWriter is the narrow asset_processing write port owned by
// the canonical asset persistence boundary.
//
// asset_processing is classified MEDIA-AUTHORITATIVE (see
// migrations/postgres/008_media_asset_processing.sql): per-step pipeline
// progress is observable state of the media aggregate, so it lands on the
// canonical PostgreSQL media database next to media_assets and
// asset_locations. The three methods are the canonical transition set
// (mirrors detail.ProcessingRepository.Start/Complete/Fail, narrowed to the
// facts the production capabilities actually emit).
type AssetProcessingWriter interface {
	// StartAssetProcessing records that a step began running. Implementations
	// MUST be idempotent for a repeated Start of the same (asset, step).
	StartAssetProcessing(ctx context.Context, assetID, step string) error
	// CompleteAssetProcessing records a successful step completion.
	CompleteAssetProcessing(ctx context.Context, assetID, step string) error
	// FailAssetProcessing records a failed step and its error message.
	FailAssetProcessing(ctx context.Context, assetID, step, errMsg string) error
}

// CanonicalAssetLocationWriter resolves the asset_locations write surface
// behind a committer. The PostgreSQL media engine is the ONLY implementation
// that answers; nil means the media plane is closed and the caller MUST fail
// closed rather than select an engine of its own.
func CanonicalAssetLocationWriter(committer AssetCommitter) AssetLocationWriter {
	if committer == nil {
		return nil
	}
	writer, ok := committer.(AssetLocationWriter)
	if !ok {
		return nil
	}
	return writer
}

// CanonicalAssetProcessingWriter resolves the asset_processing write surface
// behind a committer. The PostgreSQL media engine is the ONLY implementation
// that answers; nil means the media plane is closed and the caller MUST either
// fail closed or (for a documented best-effort observability write) skip the
// write — never fall back to a second engine.
func CanonicalAssetProcessingWriter(committer AssetCommitter) AssetProcessingWriter {
	if committer == nil {
		return nil
	}
	writer, ok := committer.(AssetProcessingWriter)
	if !ok {
		return nil
	}
	return writer
}
