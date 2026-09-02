// Package media — committer.go: the canonical PostgreSQL AssetCommitter
// adapter (PR-ASSET-COMMITTER parity, September 2026).
//
// INDEXED_WRITER_SCOPE: clipindexer (post-cutover projection owner)
// The terminal INDEXED CAS is exposed solely as the persistence adapter
// invoked by the canonical outbox consumer; no workflow writes this state.
//
// This file is the canonical PostgreSQL implementation of
// persistence.AssetCommitter. It owns the SQL that writes media_assets,
// asset_locations, and the durable index-request event inside one
// PostgreSQL transaction, mirroring SQLiteAssetCommitter
// (internal/platform/sqlite/assets/imagesregistry/asset_committer.go)
// statement-for-statement so the FASE 7 cutover is a composition-root swap.
//
// Note on the canonical UnitOfWork: the SQLite adapter layers the
// control-plane canonical_mutations protocol above CommitTxRaw when the
// table is present. The PostgreSQL media database does not yet carry the
// canonical_mutations surface; until it does, this adapter always runs the
// raw path — the same code path the SQLite parity suite exercises against
// a schema without canonical_mutations.
package media

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// PostgresAssetCommitter is the canonical adapter for
// persistence.AssetCommitter over PostgreSQL.
type PostgresAssetCommitter struct {
	db  *sql.DB
	box outboxRepository
	log *zap.Logger
}

// NewPostgresAssetCommitter constructs the adapter. Both db and box are
// required; a nil value panics at construction time so wiring gaps surface
// at boot rather than at first commit.
func NewPostgresAssetCommitter(db *sql.DB, box outboxRepository, log *zap.Logger) *PostgresAssetCommitter {
	if db == nil {
		panic("media.NewPostgresAssetCommitter: db is required")
	}
	if box == nil {
		panic("media.NewPostgresAssetCommitter: outbox repository is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &PostgresAssetCommitter{db: db, box: box, log: log}
}

// Compile-time assertion.
var _ persistence.AssetCommitter = (*PostgresAssetCommitter)(nil)

// CommitAsset is the canonical user-facing entry point. It opens a fresh
// PostgreSQL transaction, writes the canonical asset, locations, metadata
// and durable indexing request, then commits atomically.
func (c *PostgresAssetCommitter) CommitAsset(ctx context.Context, req persistence.AssetCommitRequest) (persistence.CommittedAsset, error) {
	return c.CommitAndIndex(ctx, persistence.CommitRequest(req))
}

// CommitAndIndex opens a new transaction, writes the asset, and commits.
// This is the standalone-producer entry point.
func (c *PostgresAssetCommitter) CommitAndIndex(ctx context.Context, req persistence.CommitRequest) (persistence.CommitResult, error) {
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

	// Post-commit terminal-conflict checks cover both the canonical index
	// event and every additional durable intent. The asset row is already
	// owned by PostgreSQL; a terminal intent is surfaced so callers can
	// trigger explicit recovery instead of reporting an unqualified success.
	if !res.OutboxInserted && res.OutboxEventKey != "" {
		status, err := c.queryOutboxStatus(ctx, res.OutboxEventKey)
		if err == nil && isTerminalOutboxStatus(status) {
			return res, fmt.Errorf("%w: event_key=%q status=%q", persistence.ErrAssetCommitOutboxTerminal, res.OutboxEventKey, status)
		}
	}
	for _, additional := range res.AdditionalOutbox {
		if !additional.Inserted && isTerminalOutboxStatus(additional.ExistingStatus) {
			return res, fmt.Errorf("%w: event_key=%q status=%q", persistence.ErrAssetCommitOutboxTerminal, additional.EventKey, additional.ExistingStatus)
		}
	}

	return res, nil
}

// CommitTx writes the asset, locations, metadata and optional indexing
// request inside the caller-owned transaction.
func (c *PostgresAssetCommitter) CommitTx(ctx context.Context, tx persistence.Transaction, req persistence.CommitRequest) (persistence.CommitResult, error) {
	return c.CommitTxRaw(ctx, tx, req)
}

// CommitTxRaw is the raw canonical write path: validate, upsert
// media_assets, upsert asset_locations, emit the optional index request and
// any additional outbox intents — all inside the caller-owned transaction.
func (c *PostgresAssetCommitter) CommitTxRaw(ctx context.Context, tx persistence.Transaction, req persistence.CommitRequest) (persistence.CommitResult, error) {
	if err := normalizeIndexTaxonomy(&req); err != nil {
		return persistence.CommitResult{}, err
	}
	if err := req.Validate(); err != nil {
		return persistence.CommitResult{}, err
	}

	// The outbox emitter needs a concrete *sql.Tx. The application port
	// intentionally hides *sql.Tx; the adapter is the boundary where the
	// concrete transaction is unwrapped.
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

	// 2. UPSERT media_assets. Column-for-column mirror of the SQLite
	// canonical INSERT projection; the taxonomy dimensions ride in the
	// same upsert with insert-wins / COALESCE-keep update semantics.
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
			$1, $2, $3, $4, $5,
			$6, $7, $8, $9,
			$10, $11, $12, $13,
			$14, $15, $16,
			$17, $18, $19,
			$20, $21,
			$22, $23, $24, $25,
			$26, $27, $28,
			$29, $30, $31,
			$32, $33, $34,
			$35, $36,
			$37, $38, $39, $40
		)
		ON CONFLICT (id) DO UPDATE SET
			source = excluded.source,
			name = excluded.name,
			filename = excluded.filename,
			media_type = excluded.media_type,
			category = excluded.category,
			duration_ms = excluded.duration_ms,
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
			origin = COALESCE(NULLIF(excluded.origin, ''), media_assets.origin),
			provider = COALESCE(NULLIF(excluded.provider, ''), media_assets.provider),
			namespace = COALESCE(NULLIF(excluded.namespace, ''), media_assets.namespace),
			asset_kind = COALESCE(NULLIF(excluded.asset_kind, ''), media_assets.asset_kind),
			source_type = COALESCE(NULLIF(excluded.source_type, ''), media_assets.source_type),
			semantic_role = COALESCE(NULLIF(excluded.semantic_role, ''), media_assets.semantic_role)
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

	// group_name and thumb_url exist on the canonical PostgreSQL schema, so
	// the compatibility probes the SQLite writer performs against legacy
	// databases collapse into unconditional single-row updates here.
	if req.GroupName != "" {
		if _, err := sqlTx.ExecContext(ctx, `UPDATE media_assets SET group_name = $1 WHERE id = $2`, req.GroupName, req.AssetID); err != nil {
			return persistence.CommitResult{}, fmt.Errorf("asset committer: update group name: %w", err)
		}
	}
	if _, err := sqlTx.ExecContext(ctx, `UPDATE media_assets SET thumb_url = $1 WHERE id = $2`, req.ThumbnailURL, req.AssetID); err != nil {
		return persistence.CommitResult{}, fmt.Errorf("asset committer: update thumb_url: %w", err)
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
			// envelope. The producer must never choose a different
			// idempotency scheme based on its source; that belongs to this
			// infrastructure boundary.
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
// this boundary with an empty taxonomy. (Mirror of
// imagesregistry.normalizeIndexTaxonomy.)
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

// upsertLocations writes the asset_locations rows inside the tx.
func (c *PostgresAssetCommitter) upsertLocations(ctx context.Context, tx *sql.Tx, assetID string, locations []persistence.LocationCommit, nowStr string) error {
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
				 mime_type, file_size_bytes, legacy_file_md5, is_primary, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (asset_id, location_kind) DO UPDATE SET
				uri = excluded.uri,
				external_id = excluded.external_id,
				web_view_link = excluded.web_view_link,
				download_url = excluded.download_url,
				mime_type = excluded.mime_type,
				file_size_bytes = excluded.file_size_bytes,
				legacy_file_md5 = excluded.legacy_file_md5,
				is_primary = excluded.is_primary,
				updated_at = excluded.updated_at
		`, assetID, loc.Kind, loc.URI, loc.ExternalID, loc.WebViewLink, loc.DownloadURL,
			loc.MimeType, loc.FileSizeBytes, loc.LegacyFileMD5, pgBoolInt(loc.IsPrimary), nowStr, nowStr); err != nil {
			return fmt.Errorf("asset committer: upsert location %s: %w", loc.Kind, err)
		}
	}
	return nil
}

// queryOutboxStatus returns the status of an existing outbox event.
func (c *PostgresAssetCommitter) queryOutboxStatus(ctx context.Context, eventKey string) (string, error) {
	var status string
	err := c.db.QueryRowContext(ctx, `SELECT status FROM outbox_events WHERE event_key = $1`, eventKey).Scan(&status)
	if err != nil {
		return "", err
	}
	return status, nil
}

// ── Location projection helpers (SQLite mirror) ─────────────────────────

// primaryDriveFileID returns the ExternalID of the first primary location,
// falling back to the first location.
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

// primaryWebViewLink returns the WebViewLink of the first primary location,
// falling back to the first location.
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

// primaryDownloadURL returns the DownloadURL of the first primary location,
// falling back to the first location.
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

// pgBoolInt maps a boolean onto the canonical 0/1 SMALLINT projection.
func pgBoolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ── Field normalization (SQLite mirror: asset_commit_fields.go) ─────────

type assetCommitFields struct {
	now, requestedAt time.Time
	nowString        string
	title            string
	sourceProvider   string
	sourceVideoID    string
	startMS          int64
	endMS            int64
	sourceVersion    string
	indexState       string
	name             string
}

func normalizeAssetCommitFields(req persistence.CommitRequest, now time.Time) assetCommitFields {
	requestedAt := req.RequestedAt
	if requestedAt.IsZero() {
		requestedAt = now
	}
	sourceVersion := req.Metadata.SourceVersion
	if sourceVersion == "" {
		sourceVersion = req.ContentHash
	}
	startMS := req.StartMs
	if startMS == 0 && req.Metadata.StartSec != 0 {
		startMS = int64(req.Metadata.StartSec * 1000)
	}
	endMS := req.EndMs
	if endMS == 0 && req.Metadata.EndSec != 0 {
		endMS = int64(req.Metadata.EndSec * 1000)
	}
	indexState := req.IndexState
	if indexState == "" {
		indexState = "DISCOVERED"
	}
	name := req.Name
	if name == "" {
		name = req.Filename
	}
	return assetCommitFields{
		now: now, requestedAt: requestedAt, nowString: now.UTC().Format(time.RFC3339),
		title:          firstNonEmpty(req.Title, req.Metadata.Title),
		sourceProvider: firstNonEmpty(req.SourceProvider, req.Metadata.SourceProvider),
		sourceVideoID:  firstNonEmpty(req.SourceVideoID, req.Metadata.SourceVideoID),
		startMS:        startMS, endMS: endMS, sourceVersion: sourceVersion,
		indexState: indexState, name: name,
	}
}

// ── Tag projection helpers (SQLite mirror: clip_writer_helpers.go) ──────

// clipTagsJSON marshals the tag list as a JSON array string for the
// media_assets.tags column (empty slice → empty string, SQLite parity).
func clipTagsJSON(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	raw, _ := json.Marshal(tags)
	return string(raw)
}

// clipTagsNorm derives the media_assets.tags_norm search string: the
// space-joined lowercase tag list. Empty for an empty tag list.
func clipTagsNorm(tags []string) string {
	var b strings.Builder
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strings.ToLower(t))
	}
	return b.String()
}

// execAssetUpdate is the canonical rows-affected gate for single-asset
// mutations (SQLite mirror: imagesregistry.execAssetUpdate).
func execAssetUpdate(ctx context.Context, exec mediaAssetSQLExecutor, assetID, operation, query string, args ...any) error {
	if exec == nil {
		return fmt.Errorf("asset committer: %s: executor is unavailable", operation)
	}
	result, err := exec.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("asset committer: %s: %w", operation, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("asset committer: %s rows affected: %w", operation, err)
	}
	if affected == 0 {
		return fmt.Errorf("asset committer: %s: asset %q not found", operation, assetID)
	}
	return nil
}

// mediaAssetSQLExecutor is the canonical SQL mutation boundary for
// media_assets. Both *sql.DB and *sql.Tx satisfy it; keeping the executor
// narrow lets callers preserve their transaction while the SQL itself stays
// owned by this file family.
type mediaAssetSQLExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// mustMarshalJSON is used only for small internal metadata patches. The
// inputs are constructed by the canonical writer, so an encoding failure is
// a programmer error rather than a recoverable producer failure.
func mustMarshalJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("asset committer: marshal internal metadata patch: %v", err))
	}
	return string(raw)
}
