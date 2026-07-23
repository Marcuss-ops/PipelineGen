// Package finalizer — asset_finalizer_asset.go (split from
// asset_finalizer_tx.go, July 2026): helper SQL for the canonical
// media_assets table.
//
// Owns:
//
//  1. func (s *AssetTxFinalizer) upsertMediaAsset — writes the
//     canonical media_assets row inside the caller's tx. The INSERT
//     path sets lifecycle_state='PUBLISHED' + index_state='DISCOVERED'
//     (godlike/06 SSOT canonical projection-time hint, mirrored
//     across PR-008 wire shape + PR-009 DB column). The ON CONFLICT
//     DO UPDATE path INTENTIONALLY OMITS the index_state column —
//     the clipindexer owns index_state after the initial INSERT and
//     a re-finalization must NOT clobber a state-transition that
//     already advanced (INDEXING / INDEXED / INDEX_FAILED).
//
// Caller-owned-tx discipline (godlike/06 SSOT, non-negotiable
// architectural rule): this helper does NOT own BeginTx / Commit /
// Rollback. FinalizeAsset (in asset_finalizer_tx.go) supplies the
// finalization.Transaction interface (production concrete: *sql.Tx
// via finalizer.WrapTx). The tx boundary belongs to the CALLER
// (JobFinalizer at internal/application/jobs/finalizer/job_finalizer.go).
//
// PR-FINALIZER-METRICS (July 2026) order invariant: the err check
// MUST run BEFORE res.RowsAffected(). Per database/sql semantics
// the returned sql.Result can be nil when ExecContext fails at the
// connection level (driver-side bad connection, mid-stmt context
// cancel). Calling RowsAffected() on a nil Result panics with
// nil-pointer dereference. The err check short-circuits BEFORE
// any res.* call. The FinalizerMediaAssetsInsertTotal counter
// captures the four outcome classes (failed / rows_affected_err /
// insert / update_on_conflict / no_op_silent) so dashboards can
// alert on driver-implementation divergence.
//
// content_hash Tier 1 source_version contract (godlike/07
// no-fake-availability, supersede-gate fix): metadata_json MUST
// include content_hash = artifact.SHA256 so SourceVersionFor()
// reads the canonical fingerprint from the same write boundary
// as the outbox event. Without this key a republish that changes
// file_hash would leave metadata_json.$.file_hash stale (from the
// previous ingest), causing SourceVersionFor to return the OLD
// hash and the IndexingHandler to mark the NEW event as
// superseded — Qdrant never updates.
//
// ArtifactMetadata merge (source-specific enrichment data from
// ChunkState: title, round, tags, category, source_provider,
// drive_path, etc.) so the Qdrant PayloadMapper can read rich
// data from media_assets.metadata_json. NULL + zero-value scalars
// are skipped (omitempty semantics — without this, a round=0
// would dominate the payload and obscure the real semantic fields).
//
// Mechanical split from asset_finalizer_tx.go. Zero behavior
// change. The receiver (s *AssetTxFinalizer) is unchanged so the
// orchestrator can call this helper as `s.upsertMediaAsset(...)`
// without any wiring change.
package finalizer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
)

// upsertMediaAsset inserts or updates the canonical media_assets row.
// See package doc-comment for the index_state exclusion contract,
// PR-FINALIZER-METRICS order invariant, and content_hash Tier 1
// source_version contract.
func (s *AssetTxFinalizer) upsertMediaAsset(
	ctx context.Context,
	tx finalization.Transaction,
	a *finalization.PublishedArtifact,
	nowStr string,
) error {
	mediaType := kindToMediaType(a.Kind)
	metadata := map[string]any{
		"publish_action": string(a.Location.Action),
		"source_version": a.SourceVersion,
		"size_bytes":     a.SizeBytes,
		// Tier 1 of SourceVersionFor (see
		// internal/infrastructure/database/sqlite/assets/source_version.go):
		// content_hash is the dispatcher-aware write boundary — the
		// finalizer writes it atomically inside the same tx as the
		// outbox event, so the IndexingHandler's supersede gate reads
		// a consistent fingerprint. Without this key, a republish
		// that changes file_hash but preserves an older metadata_json
		// would cause SourceVersionFor to read the stale Tier 2
		// (metadata_json.$.file_hash) and mark the event as superseded.
		"content_hash": a.SHA256,
	}
	if a.Description != "" {
		metadata["description"] = a.Description
	}
	// Merge ArtifactMetadata (source-specific enrichment data
	// from ChunkState: title, round, tags, category, source_provider,
	// drive_path, etc.) so the Qdrant PayloadMapper can read rich
	// data from media_assets.metadata_json. Without this merge,
	// the semantic fields are lost at the PublishedArtifact boundary
	// and the indexer falls back to filename-only payloads.
	for k, v := range a.ArtifactMetadata {
		if v == nil {
			continue
		}
		// Skip zero-value scalars (omitempty semantics).
		switch val := v.(type) {
		case string:
			if val == "" {
				continue
			}
		case int:
			if val == 0 {
				continue
			}
		case float64:
			if val == 0 {
				continue
			}
		case []string:
			if len(val) == 0 {
				continue
			}
		}
		metadata[k] = v
	}
	metadataJSON, _ := json.Marshal(metadata)

	// Source column: use explicit Source if set ("stock", "youtube",
	// "artlist", etc.), otherwise fall back to Location.Action ("created").
	// PR-STOCK-SOURCE-FIX: stock assets must NOT use Location.Action as
	// source — that's the publish action, not the content source.
	sourceStr := a.Source
	if sourceStr == "" {
		sourceStr = string(a.Location.Action)
	}

	_, initIndex := asset.NewIndexableAssetState()
	res, err := tx.ExecContext(ctx, `
		INSERT INTO media_assets (
			id, source, name, filename, media_type,
			file_hash, drive_file_id, drive_link, download_link,
			folder_id, folder_path, lifecycle_state, index_state,
			metadata_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		filename = excluded.filename,
		media_type = excluded.media_type,
		file_hash = excluded.file_hash,
		drive_file_id = excluded.drive_file_id,
		drive_link = excluded.drive_link,
		download_link = excluded.download_link,
		folder_id = excluded.folder_id,
		folder_path = excluded.folder_path,
		lifecycle_state = excluded.lifecycle_state,
		-- index_state is INTENTIONALLY omitted from the ON CONFLICT
		-- DO UPDATE clause (godlike/06 SSOT): a re-finalization must
		-- NOT clobber a state the clipindexer has already transitioned
		-- (INDEXING / INDEXED / INDEX_FAILED). Only the clipindexer
		-- owns the index_state column after the initial INSERT. The
		-- fresh INSERT path still sets 'DISCOVERED' so the
		-- clipindexer downstream has a non-DISCOVERED state to advance
		-- from; the DO UPDATE path leaves any prior transition intact.
		metadata_json = excluded.metadata_json,
		updated_at = excluded.updated_at
	`,
	a.ArtifactID,
	sourceStr,
	a.Filename,
	a.Filename,
	mediaType,
	a.SHA256,
	a.Location.FileID,
	a.Location.WebViewLink,
	a.Location.DownloadLink,
	a.Location.FolderID,
	a.Location.FolderPath,		string(asset.StatePublished),
		string(initIndex),
	string(metadataJSON),
	nowStr,
	nowStr,
	)
	// ── PR-FINALIZER-METRICS (July 2026) ──────────────────────────
	// Increment FinalizerMediaAssetsInsertTotal per SQLite
	// rows-affected outcome. ORDER INVARIANT: err-check MUST run
	// BEFORE res.RowsAffected(). Per database/sql semantics the
	// returned sql.Result can be nil when ExecContext fails at the
	// connection level (e.g. driver-side bad connection, mid-stmt
	// context cancel). Calling RowsAffected() on a nil Result panics
	// with nil-pointer dereference. The err check short-circuits
	// BEFORE any res.* call.
	if err != nil {
		metrics.FinalizerMediaAssetsInsertTotal.WithLabelValues("failed").Inc()
		return fmt.Errorf("asset finalizer: upsert media_asset %s: %w", a.ArtifactID, err)
	}
	resRows, rowsErr := res.RowsAffected()
	if rowsErr != nil {
		// driver-implementation-divergence case (rare; e.g. driver
		// doesn't implement RowsAffected). One increment captures
		// the entire divergence class so an alert on
		// finalizer_media_assets_insert_total{outcome="rows_affected_err"}
		// becomes the canonical SRE surface.
		metrics.FinalizerMediaAssetsInsertTotal.WithLabelValues("rows_affected_err").Inc()
		return nil
	}
	var outcome string
	switch resRows {
	case 1:
		outcome = "insert"
	case 2:
		outcome = "update_on_conflict"
	case 0:
		outcome = "no_op_silent"
	}
	metrics.FinalizerMediaAssetsInsertTotal.WithLabelValues(outcome).Inc()
	return nil
}
