// Package assets — SQLite AssetCommitter adapter (PR-ASSET-COMMITTER).
//
// This file is the sole canonical implementation of
// persistence.AssetCommitter. It owns the SQL that writes media_assets,
// asset_locations, and the durable index-request event inside one SQLite
// transaction.
package imagesregistry

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	capcontrol "github.com/Marcuss-ops/PipelineGen/internal/capabilities/controlplane"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing/clipindexer"
	sqlitecontrol "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/controlplane"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/pkg/idempotency"
)

// SQLiteAssetCommitter is the canonical adapter for
// persistence.AssetCommitter.
type SQLiteAssetCommitter struct {
	db  *sql.DB
	box *outboxevents.Repository
	log *zap.Logger
	uow capcontrol.UnitOfWork
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
	return &SQLiteAssetCommitter{db: db, box: box, log: log, uow: discoverUnitOfWork(db, box)}
}

func discoverUnitOfWork(db *sql.DB, box *outboxevents.Repository) capcontrol.UnitOfWork {
	var present int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='canonical_mutations'`).Scan(&present); err != nil {
		if strings.Contains(err.Error(), "database is closed") {
			return nil
		}
		panic(fmt.Sprintf("assets.NewSQLiteAssetCommitter: inspect canonical UoW schema: %v", err))
	}
	if present == 1 {
		for _, table := range []string{"registry_events", "outbox_events"} {
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&count); err != nil || count != 1 {
				panic(fmt.Sprintf("assets.NewSQLiteAssetCommitter: required canonical table %q is missing", table))
			}
		}
		for _, column := range []string{"registry_seq", "outbox_event_id"} {
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('canonical_mutations') WHERE name=?`, column).Scan(&count); err != nil || count != 1 {
				panic(fmt.Sprintf("assets.NewSQLiteAssetCommitter: canonical_mutations.%s is missing", column))
			}
		}
		uow, err := sqlitecontrol.NewUnitOfWork(db, box)
		if err != nil {
			panic(fmt.Sprintf("assets.NewSQLiteAssetCommitter: initialize canonical UoW: %v", err))
		}
		return uow
	}
	var ledger int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&ledger); err == nil && ledger == 1 {
		panic("assets.NewSQLiteAssetCommitter: canonical_mutations missing from migrated database")
	}
	return nil
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
	if c.uow != nil {
		return c.commitWithUnitOfWork(ctx, req)
	}
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
	// owned by SQLite; a terminal intent is surfaced so callers can trigger
	// explicit recovery instead of reporting an unqualified success.
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
// request inside the caller-owned transaction. On migrated databases it also
// applies the canonical UoW protocol without taking ownership of the tx.
func (c *SQLiteAssetCommitter) CommitTx(ctx context.Context, tx persistence.Transaction, req persistence.CommitRequest) (persistence.CommitResult, error) {
	if c.uow != nil {
		return c.commitTxWithUnitOfWork(ctx, tx, req)
	}
	return c.CommitTxRaw(ctx, tx, req)
}

func (c *SQLiteAssetCommitter) commitTxWithUnitOfWork(ctx context.Context, tx persistence.Transaction, req persistence.CommitRequest) (persistence.CommitResult, error) {
	if err := normalizeIndexTaxonomy(&req); err != nil {
		return persistence.CommitResult{}, err
	}
	if err := req.Validate(); err != nil {
		return persistence.CommitResult{}, err
	}
	if !req.EmitIndexEvent {
		return persistence.CommitResult{}, fmt.Errorf("asset committer: canonical UoW requires EmitIndexEvent=true")
	}
	command, err := buildAssetMutationCommand(req)
	if err != nil {
		return persistence.CommitResult{}, err
	}
	sqlTx, ok := tx.(*sql.Tx)
	if !ok || sqlTx == nil {
		return persistence.CommitResult{}, fmt.Errorf("asset committer: expected *sql.Tx, got %T", tx)
	}
	result, err := c.uow.RunInTransaction(ctx, sqlitecontrol.WrapTx(sqlTx), command, func(ctx context.Context, uowTx capcontrol.Transaction) (string, error) {
		uowSQLTx, ok := sqlitecontrol.UnwrapSQLTx(uowTx)
		if !ok || uowSQLTx == nil {
			return "", fmt.Errorf("asset committer: uow transaction is not a sqlite transaction")
		}
		committed, mutationErr := c.CommitTxRaw(ctx, uowSQLTx, req)
		if mutationErr != nil {
			return "", mutationErr
		}
		payload, marshalErr := json.Marshal(committed)
		if marshalErr != nil {
			return "", marshalErr
		}
		return string(payload), nil
	})
	if err != nil {
		return persistence.CommitResult{}, err
	}
	var committed persistence.CommitResult
	if err := json.Unmarshal([]byte(result.ResultJSON), &committed); err != nil {
		return persistence.CommitResult{}, fmt.Errorf("asset committer: decode UoW result: %w", err)
	}
	return committed, nil
}

func (c *SQLiteAssetCommitter) commitWithUnitOfWork(ctx context.Context, req persistence.CommitRequest) (persistence.CommitResult, error) {
	if err := normalizeIndexTaxonomy(&req); err != nil {
		return persistence.CommitResult{}, err
	}
	if err := req.Validate(); err != nil {
		return persistence.CommitResult{}, err
	}
	if !req.EmitIndexEvent {
		return persistence.CommitResult{}, fmt.Errorf("asset committer: canonical UoW requires EmitIndexEvent=true")
	}
	command, err := buildAssetMutationCommand(req)
	if err != nil {
		return persistence.CommitResult{}, err
	}
	result, err := c.uow.Run(ctx, command, func(ctx context.Context, tx capcontrol.Transaction) (string, error) {
		sqlTx, ok := sqlitecontrol.UnwrapSQLTx(tx)
		if !ok || sqlTx == nil {
			return "", fmt.Errorf("asset committer: uow transaction is not a sqlite transaction")
		}
		committed, mutationErr := c.CommitTxRaw(ctx, sqlTx, req)
		if mutationErr != nil {
			return "", mutationErr
		}
		payload, marshalErr := json.Marshal(committed)
		if marshalErr != nil {
			return "", marshalErr
		}
		return string(payload), nil
	})
	if err != nil {
		return persistence.CommitResult{}, err
	}
	var committed persistence.CommitResult
	if err := json.Unmarshal([]byte(result.ResultJSON), &committed); err != nil {
		return persistence.CommitResult{}, fmt.Errorf("asset committer: decode UoW result: %w", err)
	}
	return committed, nil
}

func buildAssetMutationCommand(req persistence.CommitRequest) (capcontrol.Command, error) {
	fingerprint, err := commitRequestFingerprint(req)
	if err != nil {
		return capcontrol.Command{}, fmt.Errorf("asset committer: build mutation fingerprint: %w", err)
	}
	outboxEvent, err := buildAssetMutationOutboxEvent(req)
	if err != nil {
		return capcontrol.Command{}, err
	}
	commandID := fmt.Sprintf("asset-commit:%s:%s", req.AssetID, fingerprint)
	return capcontrol.Command{
		CommandID: commandID, IdempotencyKey: commandID, RequestHash: fingerprint,
		AggregateType: "media_asset", AggregateID: req.AssetID, Actor: "asset-committer",
		EventType:   "MEDIA_ASSET_MUTATED",
		PayloadJSON: fmt.Sprintf(`{"asset_id":%q,"request_hash":%q}`, req.AssetID, fingerprint),
		Outbox:      outboxEvent,
	}, nil
}

func commitRequestFingerprint(req persistence.CommitRequest) (string, error) {
	req.RequestedAt = time.Time{}
	payload, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	sum := digest.SHA256Bytes(payload)
	return sum, nil
}

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
	if req.Source == "artlist" {
		eventKey, err := idempotency.OutboxKey(outboxevents.EventAssetIndexRequested, req.Source, req.AssetID, sourceVersion)
		if err != nil {
			return capcontrol.OutboxEvent{}, fmt.Errorf("asset committer: build outbox event key: %w", err)
		}
		return capcontrol.OutboxEvent{EventType: outboxevents.EventAssetIndexRequested, AggregateType: "media_asset", AggregateID: req.AssetID, PayloadJSON: "{}", EventKey: eventKey}, nil
	}
	eventKey, payload, err := outboxevents.BuildReindexEnvelopeV1(req.AssetID, clipindexer.CollectionVersion(), sourceVersion, requestedAt)
	if err != nil {
		return capcontrol.OutboxEvent{}, fmt.Errorf("asset committer: build outbox envelope: %w", err)
	}
	return capcontrol.OutboxEvent{EventType: outboxevents.EventAssetIndexRequested, AggregateType: "media_asset", AggregateID: req.AssetID, PayloadJSON: payload, EventKey: eventKey}, nil
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

	// Resolve the supersede fingerprint once. Metadata.SourceVersion carries
	// the caller-computed indexable-snapshot revision (index_revision); when
	// it is absent the snapshot collapses to byte identity (content_sha256).
	sourceVersion := req.Metadata.SourceVersion
	if sourceVersion == "" {
		sourceVersion = req.ContentHash
	}

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
	indexState := req.IndexState
	if indexState == "" {
		indexState = "DISCOVERED"
	}
	name := req.Name
	if name == "" {
		name = req.Filename
	}

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
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
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
				 mime_type, file_size_bytes, legacy_file_md5, is_primary, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(asset_id, location_kind) DO UPDATE SET
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
			loc.MimeType, loc.FileSizeBytes, loc.LegacyFileMD5, boolToInt(loc.IsPrimary), nowStr, nowStr); err != nil {
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
