// Package assets — SQLite AssetCommitter adapter (PR-ASSET-COMMITTER).
//
// This file is the sole canonical implementation of
// persistence.AssetCommitter. It owns the SQL that writes media_assets,
// asset_locations, and the durable index-request event inside one SQLite
// transaction.
package assets

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// SQLiteAssetCommitter is the canonical adapter for
// persistence.AssetCommitter.
type SQLiteAssetCommitter struct {
	db  *sql.DB
	box *outboxevents.Repository
	log *zap.Logger
}

// NewSQLiteAssetCommitter constructs the adapter. Both db and box are
// required; a nil value panics at construction time so wiring gaps
// surface at boot rather than at first commit.
func NewSQLiteAssetCommitter(db *sql.DB, box *outboxevents.Repository, log *zap.Logger) *SQLiteAssetCommitter {
	if db == nil {
		panic("assets.NewSQLiteAssetCommitter: db is required")
	}
	if box == nil {
		panic("assets.NewSQLiteAssetCommitter: outboxevents.Repository is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &SQLiteAssetCommitter{db: db, box: box, log: log}
}

// Compile-time assertion.
var _ persistence.AssetCommitter = (*SQLiteAssetCommitter)(nil)

// CommitAsset is the canonical user-facing entry point. It opens a fresh
// SQLite transaction, writes the canonical asset, locations, metadata and
// durable indexing request, then commits atomically.
func (c *SQLiteAssetCommitter) CommitAsset(ctx context.Context, req persistence.AssetCommitRequest) (persistence.CommittedAsset, error) {
	return c.CommitAndIndex(ctx, persistence.CommitRequest(req))
}

// CommitAndIndex opens a new transaction, writes the asset, and commits.
// This is the standalone-producer entry point.
func (c *SQLiteAssetCommitter) CommitAndIndex(ctx context.Context, req persistence.CommitRequest) (persistence.CommitResult, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return persistence.CommitResult{}, fmt.Errorf("asset committer: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	res, err := c.CommitTx(ctx, tx, req)
	if err != nil {
		return persistence.CommitResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return persistence.CommitResult{}, fmt.Errorf("asset committer: commit: %w", err)
	}
	committed = true

	// Post-commit terminal-conflict check (mirrors the BLOCKER #4
	// contract in clip_atomic_writer_outbox.go).
	if !res.OutboxInserted && res.OutboxEventKey != "" {
		status, err := c.queryOutboxStatus(ctx, res.OutboxEventKey)
		if err == nil && isTerminalOutboxStatus(status) {
			return res, fmt.Errorf("%w: event_key=%q status=%q", persistence.ErrAssetCommitOutboxTerminal, res.OutboxEventKey, status)
		}
	}

	return res, nil
}

// CommitTx writes the asset, locations, metadata and optional indexing
// request inside the caller-owned transaction.
func (c *SQLiteAssetCommitter) CommitTx(ctx context.Context, tx persistence.Transaction, req persistence.CommitRequest) (persistence.CommitResult, error) {
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

	now := time.Now()
	nowStr := now.UTC().Format(time.RFC3339)
	requestedAt := req.RequestedAt
	if requestedAt.IsZero() {
		requestedAt = now
	}

	// Merge request-level column hints with typed metadata.
	title := firstNonEmpty(req.Title, req.Metadata.Title)
	sourceProvider := firstNonEmpty(req.SourceProvider, req.Metadata.SourceProvider)
	sourceVideoID := firstNonEmpty(req.SourceVideoID, req.Metadata.SourceVideoID)
	startMs := req.StartMs
	if startMs == 0 && req.Metadata.StartSec != 0 {
		startMs = int64(req.Metadata.StartSec * 1000)
	}
	endMs := req.EndMs
	if endMs == 0 && req.Metadata.EndSec != 0 {
		endMs = int64(req.Metadata.EndSec * 1000)
	}

	// 1. Build metadata_json from typed metadata.
	metadataMap := req.Metadata.ToMap()
	// Canonical keys that the committer always stamps, regardless of
	// what the caller put in Extra.
	metadataMap["content_hash"] = req.ContentHash
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
	indexState := req.IndexState
	if indexState == "" {
		indexState = "DISCOVERED"
	}
	name := req.Name
	if name == "" {
		name = req.Filename
	}
	sourceVersion := req.Metadata.SourceVersion
	if sourceVersion == "" {
		sourceVersion = req.ContentHash
	}

	res, err := sqlTx.ExecContext(ctx, `
		INSERT INTO media_assets (
			id, source, name, filename, media_type,
			category, duration_ms,
			file_hash, drive_file_id, drive_link, download_link,
			local_path, folder_id, folder_path,
			lifecycle_state, index_state, metadata_json,
			search_text, source_version,
			created_at, updated_at, thumbnail_url, url,
			asset_version, asset_location, rendition,
			source_provider, source_video_id, source_url,
			start_ms, end_ms, title
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source = excluded.source,
			name = excluded.name,
			filename = excluded.filename,
				media_type = excluded.media_type,
				category = excluded.category,
				duration_ms = excluded.duration_ms,
			file_hash = excluded.file_hash,
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
			title = excluded.title
	`,
		req.AssetID, req.Source, name, req.Filename, req.MediaType,
		req.Category, req.DurationMs,
		req.ContentHash, primaryDriveFileID(req.Locations), primaryWebViewLink(req.Locations), primaryDownloadURL(req.Locations),
		req.LocalPath, req.FolderID, req.FolderPath,
		req.LifecycleState, indexState, string(metadataJSON),
		req.SearchText, sourceVersion,
		nowStr, nowStr, req.ThumbnailURL, req.SourceURL,
		req.AssetVersion, req.AssetLocation, req.Rendition,
		sourceProvider, sourceVideoID, req.SourceURL,
		startMs, endMs, title,
	)
	if err != nil {
		return persistence.CommitResult{}, fmt.Errorf("asset committer: upsert media_assets: %w", err)
	}
	rowsAffected, _ := res.RowsAffected()

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
			AssetID:               req.AssetID,
			Source:                req.Source,
			MediaType:             req.MediaType,
			SourceVersion:         sourceVersion,
			RequestedAt:           requestedAt,
			UseProviderEventKey:   req.Source == "artlist",
			IncludeSourceMetadata: req.Source == "artlist",
			Priority:              req.IndexPriority,
		})
		if err != nil {
			return persistence.CommitResult{}, err
		}
		result.OutboxEventKey = indexResult.EventKey
		result.OutboxInserted = indexResult.Inserted
		result.OutboxExistingStatus = indexResult.ExistingStatus
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

// upsertLocations writes the asset_locations rows inside the tx.
func (c *SQLiteAssetCommitter) upsertLocations(ctx context.Context, tx *sql.Tx, assetID string, locations []persistence.LocationCommit, nowStr string) error {
	if len(locations) == 0 {
		return nil
	}
	for i, loc := range locations {
		if loc.Kind == "" {
			return fmt.Errorf("asset committer: location[%d] has empty Kind", i)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO asset_locations
				(asset_id, location_kind, uri, external_id, web_view_link, download_url,
				 mime_type, file_size_bytes, file_hash, is_primary, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
		`, assetID, loc.Kind, loc.URI, loc.ExternalID, loc.WebViewLink, loc.DownloadURL,
			loc.MimeType, loc.FileSizeBytes, loc.FileHash, boolToInt(loc.IsPrimary), nowStr, nowStr); err != nil {
			return fmt.Errorf("asset committer: upsert location %s: %w", loc.Kind, err)
		}
	}
	return nil
}

// queryOutboxStatus returns the status of an existing outbox event.
func (c *SQLiteAssetCommitter) queryOutboxStatus(ctx context.Context, eventKey string) (string, error) {
	var status string
	err := c.db.QueryRowContext(ctx, `SELECT status FROM outbox_events WHERE event_key = ?`, eventKey).Scan(&status)
	if err != nil {
		return "", err
	}
	return status, nil
}

// primaryDriveFileID returns the ExternalID of the first primary
// location, falling back to the first location.
func primaryDriveFileID(locations []persistence.LocationCommit) string {
	for _, loc := range locations {
		if loc.IsPrimary && loc.ExternalID != "" {
			return loc.ExternalID
		}
	}
	for _, loc := range locations {
		if loc.ExternalID != "" {
			return loc.ExternalID
		}
	}
	return ""
}

// primaryWebViewLink returns the WebViewLink of the first primary
// location, falling back to the first location.
func primaryWebViewLink(locations []persistence.LocationCommit) string {
	for _, loc := range locations {
		if loc.IsPrimary && loc.WebViewLink != "" {
			return loc.WebViewLink
		}
	}
	for _, loc := range locations {
		if loc.WebViewLink != "" {
			return loc.WebViewLink
		}
	}
	return ""
}

// primaryDownloadURL returns the DownloadURL of the first primary
// location, falling back to the first location.
func primaryDownloadURL(locations []persistence.LocationCommit) string {
	for _, loc := range locations {
		if loc.IsPrimary && loc.DownloadURL != "" {
			return loc.DownloadURL
		}
	}
	for _, loc := range locations {
		if loc.DownloadURL != "" {
			return loc.DownloadURL
		}
	}
	return ""
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
