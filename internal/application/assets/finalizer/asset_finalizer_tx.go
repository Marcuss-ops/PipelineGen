package finalizer

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	metrics "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/observability"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"go.uber.org/zap"

	texttracks "github.com/Marcuss-ops/PipelineGen/internal/application/assets/texttracks"
)

// AssetTxFinalizer is the concrete implementation of
// finalization.AssetFinalizerTx.
//
// It writes the canonical media_assets, asset_versions, and asset_locations
// records inside the caller's transaction. It participates in the
// JobFinalizer's transaction via the finalization.Transaction interface —
// it never opens its own transaction.
//
// For each PublishedArtifact, it produces:
//   - ArtifactRef (lightweight reference for downstream consumers)
//   - []OutboxEvent (indexing requests for Qdrant)
//
// Schema tables written (inside the caller's tx):
//
//	media_assets    — INSERT ... ON CONFLICT(id) DO UPDATE (canonical asset)
//	asset_versions  — INSERT with MAX(version_number)+1 (sequential version)
//	asset_locations — INSERT ... ON CONFLICT(asset_id, location_kind) DO UPDATE
//
// Canonical reference: Piano d'Azione Completo § 5.1–5.2.
type AssetTxFinalizer struct {
	log    *zap.Logger
	fanout *texttracks.MaterializeFanOut // optional nil-safe post-publish fan-out helper
}

// NewAssetTxFinalizer creates an AssetTxFinalizer.
//
// godlike/07 backward-compat: the constructor signature is
// unchanged from pre-Fase-4 (log only). The fanout helper is
// optionally attached via WithFanOut. Composition roots that
// need post-publish fan-out MUST call WithFanOut AFTER
// NewAssetTxFinalizer + AFTER the MaterializeFanOut is built
// (which requires the JobsBundle to be assembled first).
// This sequencing is the canonical SSOT order.
func NewAssetTxFinalizer(log *zap.Logger) *AssetTxFinalizer {
	if log == nil {
		log = zap.NewNop()
	}
	return &AssetTxFinalizer{log: log}
}

// WithFanOut attaches the post-publish fan-out helper. Returns
// the receiver for fluent chaining at composition root. nil-safe
// (passing nil clears the fan-out hook but does not delete the
// finalizer itself; FirePostCommitHooks short-circuits to no-op
// when fanout is nil).
//
// godlike/06 SSOT: this is the SOLE canonical extension seam
// for adding fan-out to AssetTxFinalizer. Composition roots
// MUST NOT inline the fan-out call inside FinalizeAsset (the
// caller owns the tx; the fan-out must fire AFTER commit).
func (s *AssetTxFinalizer) WithFanOut(fanout *texttracks.MaterializeFanOut) *AssetTxFinalizer {
	if s == nil {
		return s
	}
	s.fanout = fanout
	return s
}

// FirePostCommitHooks is the canonical post-commit fan-out hook
// (PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4, July 2026).
//
// Callers (the JobFinalizer + every AssetFinalizerTx caller —
// Stock, Artlist, Soundeffect, SlideWorker, future Video
// re-render) MUST invoke this AFTER tx.Commit returns nil.
// Inside the tx-bound context, the materialize job would be
// observable to workers BEFORE the asset row is durable — a
// TOCTOU race; firing post-commit guarantees the source row is
// visible to the materialize handler when the worker picks up
// the enqueued job.
//
// Activation gate (godlike/07 fail-closed):
//   - artifact.SourceTextHash == "" → no fan-out (pre-Fase-4
//     assets without source text skip silently).
//   - artifact.SourceLanguage == "" → no fan-out (no BCP-47).
//   - s.fanout == nil → no fan-out (disabled-mode wiring).
//   - Any non-nil error from EnqueueMaterializeOne is logged at
//     Warn level and swallowed — the canonical asset row +
//     asset.index.requested outbox event are already durable;
//     the materialize enqueue failure MUST NOT roll them back.
//     The broker has its own retry policy for the resulting
//     job; a future reconciliation pass can backstop missed
//     fan-outs.
func (s *AssetTxFinalizer) FirePostCommitHooks(
	ctx context.Context,
	artifact finalization.PublishedArtifact,
) {
	if s == nil {
		// Nil-receiver guard (mirrors WithFanOut). Required
		// because field access on a nil pointer receiver
		// panics in Go. Composition roots that build a nil
		// AssetTxFinalizer by mistake (e.g., a test seam
		// without Log wiring) MUST NOT crash the post-commit
		// caller path.
		return
	}
	if s.fanout == nil {
		// Disabled-mode wiring — no fan-out (godlike/07
		// NO-FAKE-AVAILABILITY: this is observable to
		// composition root configs that opt out of the
		// texttracks pipeline; operators see no asset.text.materialize
		// jobs enqueued for these assets).
		return
	}
	if artifact.SourceTextHash == "" || artifact.SourceLanguage == "" {
		// No source text available — this is the canonical
		// fan-out precondition. Pure-audio / pure-image
		// assets without a text source skip silently.
		return
	}
	// Fire the canonical post-publish enqueue. We use the
	// canonical kinds slice so fan-out covers transcript +
	// description + summary (the 3 textual kinds that benefit
	// most from translation; Title + Keywords are already
	// short, deterministic, and don't need translation).
	kinds := []asset.TextTrackKind{
		asset.TextTrackTranscript,
		asset.TextTrackDescription,
		asset.TextTrackSummary,
	}
	if err := s.fanout.EnqueueMaterializeOne(
		ctx,
		artifact.ArtifactID,
		artifact.SourceLanguage,
		artifact.SourceTextHash,
		kinds,
	); err != nil {
		// godlike/07 NO-FAKE-AVAILABILITY: log + swallow. The
		// canonical asset row + outbox event are already
		// committed; rolling back the tx would be wrong (the
		// tx is closed). The recovery path is operator-runs
		// `pipelinegen-admin text-tracks-backfill` which
		// discovers the just-published asset and fans out
		// translation fan-out for any target languages. We
		// deliberately do NOT escalate to FAILED — the caller
		// (StockFinalizeStep / Artlist stagePersistResults /
		// etc.) needs the tx-Commit-success verdict clean for
		// its own verdict-stamping path.
		if s.log != nil {
			s.log.Warn("AssetTxFinalizer.FirePostCommitHooks: fan-out enqueue failed (canonical asset row preserved; operator backfill will recover)",
				zap.String("artifact_id", artifact.ArtifactID),
				zap.String("source_language", artifact.SourceLanguage),
				zap.String("source_text_hash", artifact.SourceTextHash),
				zap.Error(err))
		}
		return
	}
	if s.log != nil {
		s.log.Info("AssetTxFinalizer.FirePostCommitHooks: asset.text.materialize enqueued",
			zap.String("artifact_id", artifact.ArtifactID),
			zap.String("source_language", artifact.SourceLanguage),
			zap.String("source_text_hash", artifact.SourceTextHash),
			zap.Int("kinds_count", len(kinds)),
		)
	}
}

// Compile-time assertion.
var _ finalization.AssetFinalizerTx = (*AssetTxFinalizer)(nil)

// FinalizeAsset writes the canonical asset, version, and location records
// for a published artifact inside the caller's transaction.
//
// The caller (JobFinalizer) owns the transaction lifecycle — AssetTxFinalizer
// only executes SQL via the Transaction surface.
func (s *AssetTxFinalizer) FinalizeAsset(
	ctx context.Context,
	tx finalization.Transaction,
	artifact finalization.PublishedArtifact,
) (finalization.ArtifactRef, []finalization.OutboxEvent, error) {
	if artifact.ArtifactID == "" {
		return finalization.ArtifactRef{}, nil,
			fmt.Errorf("asset finalizer: ArtifactID is empty")
	}

	nowStr := timeutil.FormatRFC3339(time.Now())

	// 1. UPSERT media_assets — canonical asset row.
	if err := s.upsertMediaAsset(ctx, tx, &artifact, nowStr); err != nil {
		return finalization.ArtifactRef{}, nil, err
	}

	// 2. INSERT asset_versions — new version row.
	versionNum, err := s.insertAssetVersion(ctx, tx, &artifact, nowStr)
	if err != nil {
		return finalization.ArtifactRef{}, nil, err
	}

	// 3. UPSERT asset_locations — canonical location row.
	if err := s.upsertAssetLocation(ctx, tx, &artifact, nowStr); err != nil {
		return finalization.ArtifactRef{}, nil, err
	}

	// 3b. UPSERT rendition locations + asset_renditions for each
	// additional technical variant supplied by the caller.
	for i := range artifact.Renditions {
		if err := s.upsertRenditionLocation(ctx, tx, &artifact, &artifact.Renditions[i], nowStr); err != nil {
			return finalization.ArtifactRef{}, nil, err
		}
	}

	// 4. Build ArtifactRef and outbox events.
	ref := finalization.ArtifactRef{
		ArtifactID:    artifact.ArtifactID,
		AssetID:       artifact.ArtifactID, // AssetID = ArtifactID (logical identity)
		Kind:          artifact.Kind,
		SourceVersion: int64(versionNum),
		ContentHash:   artifact.SHA256,
		Location:      artifact.Location,
	}

	// Outbox event: index this asset in Qdrant.
	// Canonical v1 envelope matching the IndexingHandler contract
	// (schema_version, event_id, asset_id, source_version,
	// idempotency_key are REQUIRED by the handler).
	eventID := uuid.NewString()
	eventKey := fmt.Sprintf("index:%s:%s", artifact.ArtifactID, artifact.SHA256)
	// Compute source + media_type for the outbox payload, mirroring
	// the fallback logic used in upsertMediaAsset for the media_assets
	// row. The gate04 outbox test asserts these fields are populated
	// in the JSON envelope consumed by the dispatcher worker + Qdrant
	// indexer; without this fix the JSON silently omitted them and the
	// test failed with payload["source"]=nil, payload["media_type"]=nil.
	sourceStr := artifact.Source
	if sourceStr == "" {
		sourceStr = string(artifact.Location.Action)
	}
	mediaTypeStr := kindToMediaType(artifact.Kind)
	indexPayload, err := json.Marshal(map[string]any{
		"schema_version":  outboxevents.ReindexEnvelopeV1Schema,
		"event_id":        eventID,
		"asset_id":        artifact.ArtifactID,
		"operation":       "UPSERT",
		"source":          sourceStr,
		"media_type":      mediaTypeStr,
		"source_version":  artifact.SHA256,
		"idempotency_key": eventKey,
	})
	if err != nil {
		return finalization.ArtifactRef{}, nil,
			fmt.Errorf("asset finalizer: marshal index payload: %w", err)
	}
	events := []finalization.OutboxEvent{
		{
			EventType:   outboxevents.EventAssetIndexRequested,
			AggregateID: artifact.ArtifactID,
			EventKey:    eventKey,
			Payload:     json.RawMessage(indexPayload),
		},
	}

	// Persist the outbox event inside the same transaction so the
	// IndexingHandler can pick it up atomically after commit.
	if err := s.insertOutboxEvent(ctx, tx, events[0], nowStr); err != nil {
		return finalization.ArtifactRef{}, nil, err
	}

	s.log.Debug("asset finalised in tx",
		zap.String("artifact_id", artifact.ArtifactID),
		zap.Int("version", versionNum),
		zap.String("media_type", kindToMediaType(artifact.Kind)),
	)

	return ref, events, nil
}

// ── SQL helpers ─────────────────────────────────────────────────────

// upsertMediaAsset inserts or updates the canonical media_assets row.
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

	res, err := tx.ExecContext(ctx, `
		INSERT INTO media_assets (
			id, source, name, filename, media_type,
			file_hash, drive_file_id, drive_link, download_link,
			folder_id, folder_path, lifecycle_state, index_state,
			metadata_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'PUBLISHED', 'INDEXING_PENDING', ?, ?, ?)
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
		-- fresh INSERT path still sets 'INDEXING_PENDING' so the
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
		a.Location.FolderPath,
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

// insertAssetVersion inserts a new version row with MAX(version_number)+1.
func (s *AssetTxFinalizer) insertAssetVersion(
	ctx context.Context,
	tx finalization.Transaction,
	a *finalization.PublishedArtifact,
	nowStr string,
) (int, error) {
	// Compute next version_number inside the transaction.
	var nextVer int
	row := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(version_number), 0) + 1 FROM asset_versions WHERE asset_id = ?`,
		a.ArtifactID,
	)
	if err := row.Scan(&nextVer); err != nil {
		return 0, fmt.Errorf("asset finalizer: compute next version for %s: %w", a.ArtifactID, err)
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO asset_versions
			(asset_id, version_number, source_uri, file_hash, file_size_bytes, mime_type, metadata_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		a.ArtifactID,
		nextVer,
		a.Location.FileID, // source_uri — where this version came from
		a.SHA256,
		a.SizeBytes,
		a.MIMEType,
		"{}",
		nowStr,
	)
	if err != nil {
		return 0, fmt.Errorf("asset finalizer: insert version %d for %s: %w", nextVer, a.ArtifactID, err)
	}

	return nextVer, nil
}

// upsertAssetLocation inserts or updates the canonical asset_locations row.
func (s *AssetTxFinalizer) upsertAssetLocation(
	ctx context.Context,
	tx finalization.Transaction,
	a *finalization.PublishedArtifact,
	nowStr string,
) error {
	locationKind := a.Location.Provider
	if locationKind == "" {
		locationKind = "drive"
	}

	_, err := tx.ExecContext(ctx, `
		INSERT INTO asset_locations
			(asset_id, location_kind, uri, external_id, web_view_link, download_url,
			 mime_type, file_size_bytes, file_hash, is_primary, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT(asset_id, location_kind) DO UPDATE SET
			uri = excluded.uri,
			external_id = excluded.external_id,
			web_view_link = excluded.web_view_link,
			download_url = excluded.download_url,
			mime_type = excluded.mime_type,
			file_size_bytes = excluded.file_size_bytes,
			file_hash = excluded.file_hash,
			is_primary = excluded.is_primary,
			updated_at = excluded.updated_at
	`,
		a.ArtifactID,
		locationKind,
		a.Location.FileID,
		a.Location.FileID,
		a.Location.WebViewLink,
		a.Location.DownloadLink,
		a.MIMEType,
		a.SizeBytes,
		a.SHA256,
		nowStr,
		nowStr,
	)
	if err != nil {
		return fmt.Errorf("asset finalizer: upsert location for %s: %w", a.ArtifactID, err)
	}
	return nil
}

// upsertRenditionLocation persists a single rendition as an
// asset_locations row and a matching asset_renditions row. The location
// is NOT marked as primary — the primary location is the one carried
// by PublishedArtifact.Location.
func (s *AssetTxFinalizer) upsertRenditionLocation(
	ctx context.Context,
	tx finalization.Transaction,
	a *finalization.PublishedArtifact,
	r *finalization.AssetRenditionLocation,
	nowStr string,
) error {
	if r.URI == "" {
		return nil
	}

	// Use a distinct location_kind per rendition so the
	// (asset_id, location_kind) unique constraint never collides
	// across renditions of the same asset.
	locationKind := r.Provider
	if locationKind == "" {
		locationKind = "local"
	}
	locationKind = fmt.Sprintf("%s_%s", locationKind, r.Kind)

	// 1. Upsert the rendition's location.
	_, err := tx.ExecContext(ctx, `
		INSERT INTO asset_locations
			(asset_id, location_kind, uri, external_id, web_view_link, download_url,
			 mime_type, file_size_bytes, file_hash, is_primary, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?)
		ON CONFLICT(asset_id, location_kind) DO UPDATE SET
			uri = excluded.uri,
			external_id = excluded.external_id,
			web_view_link = excluded.web_view_link,
			download_url = excluded.download_url,
			mime_type = excluded.mime_type,
			file_size_bytes = excluded.file_size_bytes,
			file_hash = excluded.file_hash,
			is_primary = excluded.is_primary,
			updated_at = excluded.updated_at
	`,
		a.ArtifactID,
		locationKind,
		r.URI,
		r.FileID,
		r.WebViewLink,
		r.DownloadLink,
		r.MimeType,
		r.SizeBytes,
		r.FileHash,
		nowStr,
		nowStr,
	)
	if err != nil {
		return fmt.Errorf("asset finalizer: upsert rendition location %s/%s: %w", a.ArtifactID, r.Kind, err)
	}

	// 2. Resolve the location_id after upsert. LastInsertId is unreliable
	// when the row already existed, so we re-read by the unique key.
	var locationID int64
	row := tx.QueryRowContext(ctx,
		`SELECT id FROM asset_locations WHERE asset_id = ? AND location_kind = ?`,
		a.ArtifactID, locationKind,
	)
	if err := row.Scan(&locationID); err != nil {
		return fmt.Errorf("asset finalizer: resolve location_id for %s/%s: %w", a.ArtifactID, r.Kind, err)
	}

	// 3. Upsert the asset_renditions row on (asset_id, kind).
	_, err = tx.ExecContext(ctx, `
		INSERT INTO asset_renditions
			(id, asset_id, location_id, kind, container, codec, width, height,
			 fps, bitrate, sha256, size_bytes, created_at, updated_at)
		VALUES (lower(hex(randomblob(16))), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(asset_id, kind) DO UPDATE SET
			location_id = excluded.location_id,
			container = excluded.container,
			codec = excluded.codec,
			width = excluded.width,
			height = excluded.height,
			fps = excluded.fps,
			bitrate = excluded.bitrate,
			sha256 = excluded.sha256,
			size_bytes = excluded.size_bytes,
			updated_at = excluded.updated_at
	`,
		a.ArtifactID,
		locationID,
		r.Kind,
		r.Container,
		r.Codec,
		r.Width,
		r.Height,
		r.FPS,
		r.Bitrate,
		r.FileHash,
		r.SizeBytes,
		nowStr,
		nowStr,
	)
	if err != nil {
		return fmt.Errorf("asset finalizer: upsert rendition %s/%s: %w", a.ArtifactID, r.Kind, err)
	}
	return nil
}

// ── Helpers ─────────────────────────────────────────────────────────

// insertOutboxEvent persists an outbox event inside the caller's transaction.
func (s *AssetTxFinalizer) insertOutboxEvent(
	ctx context.Context,
	tx finalization.Transaction,
	event finalization.OutboxEvent,
	nowStr string,
) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO outbox_events (
			event_type, aggregate_id, aggregate_type, payload_json, event_key, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(event_key) WHERE event_key != '' DO NOTHING
	`,
		event.EventType,
		event.AggregateID,
		"media_asset",
		string(event.Payload),
		event.EventKey,
		nowStr,
		nowStr,
	)
	if err != nil {
		return fmt.Errorf("asset finalizer: insert outbox event %s: %w", event.EventKey, err)
	}
	return nil
}

// kindToMediaType maps a domain ArtifactKind to a media_type string
// suitable for the media_assets.media_type column.
func kindToMediaType(k finalization.ArtifactKind) string {
	switch k {
	case finalization.KindVideo:
		return "video"
	case finalization.KindImage:
		return "image"
	case finalization.KindAudio, finalization.KindVoiceover, finalization.KindSoundEffect:
		return "audio"
	case finalization.KindDocument:
		return "document"
	case finalization.KindScript:
		return "text"
	case finalization.KindMetadata:
		return "metadata"
	case finalization.KindArchive:
		return "archive"
	default:
		return "other"
	}
}
