package testsupport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	capcontrol "github.com/Marcuss-ops/PipelineGen/internal/capabilities/controlplane"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"go.uber.org/zap"
	"time"
)

func buildAssetMutationOutboxEvent(req persistence.CommitRequest) (capcontrol.OutboxEvent, error) {
	sourceVersion := req.Metadata.SourceVersion
	if sourceVersion == "" {
		sourceVersion = req.ContentHash
	}
	if sourceVersion == "" {
		return capcontrol.OutboxEvent{}, fmt.Errorf("asset committer: source version is required for canonical outbox event")
	}
	requestedAt := req.RequestedAt
	if requestedAt.IsZero() {
		requestedAt = time.Now()
	}
	// Derive the event from the SAME builder CommitIndexRequestTx uses, so
	// the UoW claim and the raw tx path produce byte-identical envelopes and
	// identical provider-scoped idempotency keys (SSOT: no second shape).
	_, eventKey, payload, err := BuildIndexRequestEvent(IndexRequest{
		AssetID:       req.AssetID,
		Source:        req.Source,
		MediaType:     req.MediaType,
		SourceVersion: sourceVersion,
		RequestedAt:   requestedAt,
	})
	if err != nil {
		return capcontrol.OutboxEvent{}, err
	}
	return capcontrol.OutboxEvent{EventType: outboxevents.EventAssetIndexRequested, AggregateType: "media_asset", AggregateID: req.AssetID, PayloadJSON: string(payload), EventKey: eventKey}, nil
}

func (c *SQLiteAssetCommitter) CommitTxRaw(ctx context.Context, tx persistence.Transaction, req persistence.CommitRequest) (persistence.CommitResult, error) {
	if err := normalizeIndexTaxonomy(&req); err != nil {
		return persistence.CommitResult{}, err
	}
	if err := req.Validate(); err != nil {
		return persistence.CommitResult{}, err
	}

	// The outbox repository needs a concrete *sql.Tx. The application
	// port intentionally hides *sql.Tx, but the adapter is the boundary
	// where the concrete transaction is unwrapped.
	sqlTx, ok := tx.(*sql.Tx)
	if !ok {
		return persistence.CommitResult{}, fmt.Errorf("asset committer: expected *sql.Tx, got %T", tx)
	}

	fields := normalizeAssetCommitFields(req, time.Now())
	nowStr := fields.nowString
	requestedAt := fields.requestedAt
	title := fields.title
	sourceProvider := fields.sourceProvider
	sourceVideoID := fields.sourceVideoID
	startMs := fields.startMS
	endMs := fields.endMS
	sourceVersion := fields.sourceVersion

	// 1. Build metadata_json from typed metadata.
	metadataMap := req.Metadata.ToMap()
	// content_hash is BYTE identity (content_sha256) and must NEVER fold
	// text-track/taxonomy/metadata changes. index_revision is the SEPARATE
	// indexable-snapshot fingerprint the supersede gate compares
	// (godlike/06: content_sha256 vs index_revision vs semantic_document_hash
	// are distinct and MUST NOT be conflated).
	metadataMap["content_hash"] = req.ContentHash
	if sourceVersion != "" {
		metadataMap[mediaregistry.IndexRevisionField] = sourceVersion
	}
	if req.Metadata.SourceVersion == "" {
		metadataMap["source_version"] = req.ContentHash
	}
	if title != "" {
		metadataMap["title"] = title
	}
	if sourceProvider != "" {
		metadataMap["source_provider"] = sourceProvider
	}
	if sourceVideoID != "" {
		metadataMap["source_video_id"] = sourceVideoID
	}
	metadataJSON, _ := json.Marshal(metadataMap)

	// 2. UPSERT media_assets.
	indexState := fields.indexState
	name := fields.name

	res, err := sqlTx.ExecContext(ctx, `
		INSERT INTO media_assets (
			id, source, name, filename, media_type,
			category, duration_ms, tags, tags_norm,
			legacy_file_md5, drive_file_id, drive_link, download_link,
			local_path, folder_id, folder_path,
			lifecycle_state, index_state, metadata_json,
			search_text, source_version,
			created_at, updated_at, thumbnail_url, url,
			asset_version, asset_location, rendition,
			source_provider, source_video_id, source_url,
			start_ms, end_ms, title,
			origin, provider,
			namespace, asset_kind, source_type, semantic_role
		) VALUES (
			?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?, ?, ?
		)
		ON CONFLICT(id) DO UPDATE SET
			source = excluded.source,
			name = excluded.name,
			filename = excluded.filename,
				media_type = excluded.media_type,
				category = excluded.category,			duration_ms = excluded.duration_ms,
			tags = excluded.tags,
			tags_norm = excluded.tags_norm,
			legacy_file_md5 = excluded.legacy_file_md5,
			drive_file_id = excluded.drive_file_id,
			drive_link = excluded.drive_link,
			download_link = excluded.download_link,
			local_path = excluded.local_path,
			folder_id = excluded.folder_id,
			folder_path = excluded.folder_path,
			lifecycle_state = excluded.lifecycle_state,
			metadata_json = excluded.metadata_json,
			search_text = excluded.search_text,
			source_version = excluded.source_version,
			updated_at = excluded.updated_at,
			thumbnail_url = excluded.thumbnail_url,
			url = excluded.url,
			asset_version = excluded.asset_version,
			asset_location = excluded.asset_location,
			rendition = excluded.rendition,
			source_provider = excluded.source_provider,
			source_video_id = excluded.source_video_id,
			source_url = excluded.source_url,
			start_ms = excluded.start_ms,
			end_ms = excluded.end_ms,
			title = excluded.title,
			origin = COALESCE(NULLIF(excluded.origin, ''), origin),
			provider = COALESCE(NULLIF(excluded.provider, ''), provider),
			namespace = COALESCE(NULLIF(excluded.namespace, ''), namespace),
			asset_kind = COALESCE(NULLIF(excluded.asset_kind, ''), asset_kind),
			source_type = COALESCE(NULLIF(excluded.source_type, ''), source_type),
			semantic_role = COALESCE(NULLIF(excluded.semantic_role, ''), semantic_role)
	`,
		req.AssetID, req.Source, name, req.Filename, req.MediaType, req.Category, req.DurationMs,
		clipTagsJSON(req.Metadata.Tags), clipTagsNorm(req.Metadata.Tags),
		req.ContentHash, primaryDriveFileID(req.Locations), primaryWebViewLink(req.Locations), primaryDownloadURL(req.Locations),
		req.LocalPath, req.FolderID, req.FolderPath,
		req.LifecycleState, indexState, string(metadataJSON),
		req.SearchText, sourceVersion,
		nowStr, nowStr, req.ThumbnailURL, req.SourceURL,
		req.AssetVersion, req.AssetLocation, req.Rendition,
		sourceProvider, sourceVideoID, req.SourceURL,
		startMs, endMs, title,
		req.Origin, req.Provider,
		req.Taxonomy.Namespace, string(req.Taxonomy.AssetKind), req.Taxonomy.SourceType, req.Taxonomy.SemanticRole,
	)
	if err != nil {
		return persistence.CommitResult{}, fmt.Errorf("asset committer: upsert media_assets: %w", err)
	}
	rowsAffected, _ := res.RowsAffected()
	// group_name was added by the canonical media migrations but is absent
	// from a few pre-migration databases. Keep the compatibility branch inside
	// this canonical writer; producers still have no SQL access.
	if req.GroupName != "" {
		var hasGroupName int
		if err := sqlTx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('media_assets') WHERE name = 'group_name'`).Scan(&hasGroupName); err != nil {
			return persistence.CommitResult{}, fmt.Errorf("asset committer: inspect group_name column: %w", err)
		}
		if hasGroupName == 1 {
			if _, err := sqlTx.ExecContext(ctx, `UPDATE media_assets SET group_name = ? WHERE id = ?`, req.GroupName, req.AssetID); err != nil {
				return persistence.CommitResult{}, fmt.Errorf("asset committer: update group name: %w", err)
			}
		}
	}
	var hasThumbURL int
	if err := sqlTx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('media_assets') WHERE name = 'thumb_url'`).Scan(&hasThumbURL); err != nil {
		return persistence.CommitResult{}, fmt.Errorf("asset committer: inspect thumb_url column: %w", err)
	}
	if hasThumbURL == 1 {
		if _, err := sqlTx.ExecContext(ctx, `UPDATE media_assets SET thumb_url = ? WHERE id = ?`, req.ThumbnailURL, req.AssetID); err != nil {
			return persistence.CommitResult{}, fmt.Errorf("asset committer: update thumb_url: %w", err)
		}
	}

	// 3. UPSERT asset_locations.
	if err := c.upsertLocations(ctx, sqlTx, req.AssetID, req.Locations, nowStr); err != nil {
		return persistence.CommitResult{}, err
	}

	// 4. Optionally emit the indexing request through the single canonical
	// emitter. All callers, including legacy dispatchers, delegate to this
	// same function so the outbox write has one owner.
	result := persistence.CommitResult{AssetRowsAffected: rowsAffected}
	if req.EmitIndexEvent {
		indexResult, err := CommitIndexRequestTx(ctx, sqlTx, c.box, IndexRequest{
			AssetID:       req.AssetID,
			Source:        req.Source,
			MediaType:     req.MediaType,
			SourceVersion: sourceVersion,
			RequestedAt:   requestedAt,
			// All producers use the same provider-scoped canonical key and
			// envelope. The producer must never choose a different idempotency
			// scheme based on its source; that belongs to this infrastructure
			// boundary.
			Priority: req.IndexPriority,
		})
		if err != nil {
			return persistence.CommitResult{}, err
		}
		result.OutboxEventKey = indexResult.EventKey
		result.OutboxInserted = indexResult.Inserted
		result.OutboxExistingStatus = indexResult.ExistingStatus
	}

	// Additional external side effects are represented as outbox intents,
	// never executed synchronously after this transaction commits.
	for i, event := range req.AdditionalOutboxEvents {
		if event.EventType == "" || event.EventKey == "" {
			return persistence.CommitResult{}, fmt.Errorf("asset committer: additional outbox event[%d] requires event type and event key", i)
		}
		enqueueResult, err := c.box.Enqueue(ctx, sqlTx, event.EventType, event.AggregateID, event.AggregateType, event.PayloadJSON, event.EventKey)
		if err != nil {
			return persistence.CommitResult{}, fmt.Errorf("asset committer: enqueue additional event %q: %w", event.EventType, err)
		}
		result.AdditionalOutbox = append(result.AdditionalOutbox, persistence.AdditionalOutboxResult{
			EventKey:       event.EventKey,
			Inserted:       enqueueResult.Inserted,
			ExistingStatus: enqueueResult.ExistingStatus,
		})
	}

	if c.log != nil {
		c.log.Debug("asset committer: asset committed",
			zap.String("asset_id", req.AssetID),
			zap.String("source", req.Source),
			zap.Int64("rows_affected", rowsAffected),
			zap.Bool("outbox_emitted", req.EmitIndexEvent),
			zap.String("outbox_event_key", result.OutboxEventKey),
		)
	}
	return result, nil
}

// normalizeIndexTaxonomy is the compatibility bridge for legacy producers:
// an indexing commit may omit taxonomy at the call site, but the canonical
// writer derives and persists it before validation. No indexed row can leave
// this boundary with an empty taxonomy.
func normalizeIndexTaxonomy(req *persistence.CommitRequest) error {
	if req == nil || !req.EmitIndexEvent || !req.Taxonomy.IsZero() {
		return nil
	}
	taxonomy, err := mediaregistry.ResolveTaxonomy(mediaregistry.TaxonomyInput{
		AssetID:   req.AssetID,
		Provider:  req.Source,
		MediaType: mediaregistry.MediaType(req.MediaType),
	})
	if err != nil {
		return fmt.Errorf("asset committer: derive index taxonomy: %w", err)
	}
	req.Taxonomy = taxonomy
	return nil
}
