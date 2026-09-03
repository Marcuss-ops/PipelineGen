// Package testsupport — TEST-ONLY SQLite AssetCommitter.
//
// POSTGRES-MEDIA-CUTOVER demolition note: the production SQLite media
// writer family was REMOVED (the canonical media writer is
// PostgresMediaCommitter over PostgreSQL + pgvector). Legacy engine-level
// test suites (finalizer, catalogsync, artlist integration, jobs, youtube
// adapters) still exercise the AssetCommitter CONTRACT against a hermetic
// SQLite engine. This package provides that test double, clearly marked
// test-only: it is NEVER imported by production code.
package testsupport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	capcontrol "github.com/Marcuss-ops/PipelineGen/internal/capabilities/controlplane"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	sqlitecontrol "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/controlplane"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
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
//
// Canonical-UoW routing: when the database carries the canonical_mutations
// protocol AND the request emits an index event, the commit runs through the
// UoW so the asset write and the outbox event share one idempotency claim.
// Requests without an index event (folder upserts, legacy store saves) take
// the raw path: the UoW protocol exists to make the asset+event pair atomic
// and replay-safe, and the media_assets UPSERT is idempotent on its own.
func (c *SQLiteAssetCommitter) CommitAndIndex(ctx context.Context, req persistence.CommitRequest) (persistence.CommitResult, error) {
	if c.uow != nil && req.EmitIndexEvent {
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
// request inside the caller-owned transaction. On migrated databases it
// applies the canonical UoW protocol without taking ownership of the tx,
// but only when the request emits an index event (see CommitAndIndex:
// event-less commits have no event idempotency to uphold, so the raw
// writer runs directly).
func (c *SQLiteAssetCommitter) CommitTx(ctx context.Context, tx persistence.Transaction, req persistence.CommitRequest) (persistence.CommitResult, error) {
	if c.uow != nil && req.EmitIndexEvent {
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
		// The UoW command owns the durable index-request emission for this
		// claim (buildAssetMutationCommand). Emit again inside the mutation
		// and the same commit inserts TWO asset.index.requested rows.
		committed, mutationErr := c.commitTxRawNoEvent(ctx, uowSQLTx, req)
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
		// Same as commitTxWithUnitOfWork: the UoW claim owns the event.
		committed, mutationErr := c.commitTxRawNoEvent(ctx, sqlTx, req)
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

// commitTxRawNoEvent runs CommitTxRaw with the durable index-request
// emission suppressed. The canonical UoW owns the outbox write for the claim
// (see buildAssetMutationCommand); emitting again inside the mutation would
// insert a second asset.index.requested row for the same commit.
func (c *SQLiteAssetCommitter) commitTxRawNoEvent(ctx context.Context, sqlTx *sql.Tx, req persistence.CommitRequest) (persistence.CommitResult, error) {
	noEvent := req
	noEvent.EmitIndexEvent = false
	return c.CommitTxRaw(ctx, sqlTx, noEvent)
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

// mediaAssetSQLExecutor is the canonical SQL mutation boundary for
// media_assets. Both *sql.DB and *sql.Tx satisfy it; keeping the executor
// narrow lets callers preserve their transaction while the SQL itself stays
// owned by this file.
type mediaAssetSQLExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// PersistEmbeddingJSON persists one embedding channel through the canonical
// asset mutation boundary. Channel names are deliberately typed as a closed
// set at this infrastructure boundary; producers never select SQL columns.
func (c *SQLiteAssetCommitter) PersistEmbeddingJSON(ctx context.Context, assetID, channel string, embedding []float64, status string) error {
	raw, err := json.Marshal(embedding)
	if err != nil {
		return fmt.Errorf("asset committer: marshal %s embedding: %w", channel, err)
	}
	var update func(context.Context, mediaAssetSQLExecutor, string, string) error
	switch channel {
	case "semantic":
		update = UpdateMediaAssetEmbeddingJSON
	case "transcript":
		update = UpdateMediaAssetTranscriptEmbedding
	case "visual":
		update = UpdateMediaAssetVisualEmbedding
	case "audio":
		update = UpdateMediaAssetAudioEmbedding
	default:
		return fmt.Errorf("asset committer: unsupported embedding channel %q", channel)
	}
	if err := update(ctx, c.db, assetID, string(raw)); err != nil {
		return err
	}
	if status == "" {
		return nil
	}
	return PatchMediaAssetMetadataJSON(ctx, c.db, assetID, mustMarshalJSON(map[string]any{"embedding_status": status}), time.Now().UTC().Format(time.RFC3339))
}

// SetIndexState delegates the canonical index-state mutation to the same
// committer that owns asset creation. Indexing workers remain responsible for
// metrics and retry policy; this method owns only durable SQLite state.
func (c *SQLiteAssetCommitter) SetIndexState(ctx context.Context, assetID string, state asset.IndexState, lastError string) error {
	return UpdateMediaAssetIndexState(ctx, c.db, assetID, string(state), time.Now().UTC().Format(time.RFC3339), lastError)
}

// SetIndexed performs the compare-and-set terminal index transition through
// the canonical committer boundary.
func (c *SQLiteAssetCommitter) SetIndexed(ctx context.Context, assetID, contentHash, sourceVersion, embeddingModel, embeddingVersion, contractHash string) (bool, error) {
	ok, err := SetMediaAssetIndexed(ctx, c.db, assetID, contentHash, sourceVersion,
		time.Now().UTC().Format(time.RFC3339), embeddingModel, embeddingVersion, contractHash)
	return ok, err
}

// PatchMetadataJSON applies a JSON patch through the canonical committer.
func (c *SQLiteAssetCommitter) PatchMetadataJSON(ctx context.Context, assetID, patchJSON, updatedAt string) error {
	return PatchMediaAssetMetadataJSON(ctx, c.db, assetID, patchJSON, updatedAt)
}

// PatchMetadataJSONTx applies a metadata patch in a caller-owned transaction.
// It is the only tx-bound metadata mutation exposed to producer adapters.
func (c *SQLiteAssetCommitter) PatchMetadataJSONTx(ctx context.Context, tx *sql.Tx, assetID, patchJSON, updatedAt string) error {
	return PatchMediaAssetMetadataJSON(ctx, tx, assetID, patchJSON, updatedAt)
}

// ReplaceMetadataJSON replaces the metadata snapshot through the canonical
// committer. It is used by legacy enrichment adapters that still provide a
// complete JSON envelope rather than a typed patch.
func (c *SQLiteAssetCommitter) ReplaceMetadataJSON(ctx context.Context, assetID, metadataJSON, updatedAt string) error {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return updateMediaAssetMetadata(ctx, c.db, assetID, metadataJSON,
		"metadata_json = ?, updated_at = ?", metadataJSON, updatedAt)
}

func (c *SQLiteAssetCommitter) UpdateFolderPath(ctx context.Context, assetID, folderID, folderPath, updatedAt string) error {
	return UpdateMediaAssetFolderPath(ctx, c.db, assetID, folderID, folderPath, updatedAt)
}

// UpdateFolderPathTx applies a folder-path mutation in the caller-owned
// transaction so the caller can emit the canonical index request atomically.
func (c *SQLiteAssetCommitter) UpdateFolderPathTx(ctx context.Context, tx *sql.Tx, assetID, folderID, folderPath, updatedAt string) error {
	return UpdateMediaAssetFolderPath(ctx, tx, assetID, folderID, folderPath, updatedAt)
}

func (c *SQLiteAssetCommitter) UpdateLifecycle(ctx context.Context, assetID string, state, deletedAt, updatedAt string) error {
	return UpdateMediaAssetLifecycle(ctx, c.db, assetID, state, deletedAt, updatedAt)
}

func (c *SQLiteAssetCommitter) UpdateTaxonomy(ctx context.Context, taxonomy mediaregistry.AssetTaxonomy) error {
	return UpdateMediaAssetTaxonomy(ctx, c.db, taxonomy)
}

func (c *SQLiteAssetCommitter) LinkContent(ctx context.Context, assetID, contentSHA256 string) error {
	return LinkMediaAssetContent(ctx, c.db, assetID, contentSHA256)
}

func (c *SQLiteAssetCommitter) UpdateSearchText(ctx context.Context, assetID, searchText, updatedAt string) error {
	return UpdateMediaAssetSearchText(ctx, c.db, assetID, searchText, updatedAt)
}

func (c *SQLiteAssetCommitter) RefreshUpdatedAt(ctx context.Context, assetID, updatedAt string) error {
	return UpdateMediaAssetUpdatedAt(ctx, c.db, assetID, updatedAt)
}

func (c *SQLiteAssetCommitter) UpdateOrphanMetadata(ctx context.Context, assetID string, detectedAt time.Time, kind string) error {
	return UpdateMediaAssetOrphanMetadata(ctx, c.db, assetID, detectedAt, kind)
}

// UpdateDriveDeliveryByLegacyHash applies the post-commit Drive projection
// update through the canonical asset boundary and keeps asset_locations in
// sync in the same transaction.
func (c *SQLiteAssetCommitter) UpdateDriveDeliveryByLegacyHash(ctx context.Context, hash string, mutation persistence.DriveDeliveryMutation) error {
	if strings.TrimSpace(hash) == "" {
		return fmt.Errorf("asset committer: legacy file hash is required")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("asset committer: begin Drive delivery tx: %w", err)
	}
	defer tx.Rollback()
	preserveIdentity := strings.HasPrefix(mutation.Status, "delivery_failed:") && mutation.DriveFileID == "" && mutation.DriveLink == "" && mutation.DownloadLink == ""
	var result sql.Result
	if preserveIdentity {
		result, err = tx.ExecContext(ctx, `UPDATE media_assets SET metadata_json = json_set(COALESCE(metadata_json, '{}'), '$.delivery_status', ?), updated_at = CURRENT_TIMESTAMP WHERE source = 'image' AND legacy_file_md5 = ?`, mutation.Status, hash)
	} else {
		result, err = tx.ExecContext(ctx, `UPDATE media_assets SET drive_file_id = ?, drive_link = ?, download_link = ?, metadata_json = json_set(COALESCE(metadata_json, '{}'), '$.delivery_status', ?), updated_at = CURRENT_TIMESTAMP WHERE source = 'image' AND legacy_file_md5 = ?`, mutation.DriveFileID, mutation.DriveLink, mutation.DownloadLink, mutation.Status, hash)
	}
	if err != nil {
		return fmt.Errorf("asset committer: update Drive delivery: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 0 {
		if err != nil {
			return fmt.Errorf("asset committer: inspect Drive delivery update: %w", err)
		}
		return fmt.Errorf("asset committer: image with legacy hash %q not found", hash)
	}
	if !preserveIdentity && mutation.DriveFileID != "" {
		var assetID string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM media_assets WHERE source = 'image' AND legacy_file_md5 = ?`, hash).Scan(&assetID); err != nil {
			return fmt.Errorf("asset committer: resolve Drive delivery asset: %w", err)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		if _, err := tx.ExecContext(ctx, `INSERT INTO asset_locations (asset_id, location_kind, uri, external_id, web_view_link, download_url, mime_type, file_size_bytes, legacy_file_md5, is_primary, created_at, updated_at) VALUES (?, 'drive', ?, ?, ?, ?, '', 0, ?, 0, ?, ?) ON CONFLICT(asset_id, location_kind) DO UPDATE SET uri=excluded.uri, external_id=excluded.external_id, web_view_link=excluded.web_view_link, download_url=excluded.download_url, legacy_file_md5=excluded.legacy_file_md5, updated_at=excluded.updated_at`, assetID, "drive://"+mutation.DriveFileID, mutation.DriveFileID, mutation.DriveLink, mutation.DownloadLink, hash, now, now); err != nil {
			return fmt.Errorf("asset committer: upsert Drive location: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("asset committer: commit Drive delivery: %w", err)
	}
	return nil
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

func updateMediaAssetMetadata(ctx context.Context, exec mediaAssetSQLExecutor, assetID, metadataJSON, setClause string, args ...any) error {
	if strings.TrimSpace(metadataJSON) == "" {
		metadataJSON = "{}"
	}
	if !json.Valid([]byte(metadataJSON)) {
		return fmt.Errorf("asset committer: metadata JSON is invalid")
	}
	values := append([]any{metadataJSON}, args...)
	return execAssetUpdate(ctx, exec, assetID, "metadata update", "UPDATE media_assets SET "+setClause+" WHERE id = ?", append(values, assetID)...)
}

func PatchMediaAssetMetadataJSON(ctx context.Context, exec mediaAssetSQLExecutor, assetID, patchJSON, updatedAt string) error {
	if strings.TrimSpace(patchJSON) == "" {
		patchJSON = "{}"
	}
	if !json.Valid([]byte(patchJSON)) {
		return fmt.Errorf("asset committer: metadata patch JSON is invalid")
	}
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return execAssetUpdate(ctx, exec, assetID, "metadata patch", `
		UPDATE media_assets
		SET metadata_json = json_patch(COALESCE(metadata_json, '{}'), ?), updated_at = ?
		WHERE id = ?`, patchJSON, updatedAt, assetID)
}

func UpdateMediaAssetEmbeddingJSON(ctx context.Context, exec mediaAssetSQLExecutor, assetID, value string) error {
	return execAssetUpdate(ctx, exec, assetID, "semantic embedding update", `UPDATE media_assets SET embedding_json = ?, updated_at = ? WHERE id = ?`, value, time.Now().UTC().Format(time.RFC3339), assetID)
}

func UpdateMediaAssetTranscriptEmbedding(ctx context.Context, exec mediaAssetSQLExecutor, assetID, value string) error {
	return execAssetUpdate(ctx, exec, assetID, "transcript embedding update", `UPDATE media_assets SET transcript_embedding = ?, updated_at = ? WHERE id = ?`, value, time.Now().UTC().Format(time.RFC3339), assetID)
}

func UpdateMediaAssetVisualEmbedding(ctx context.Context, exec mediaAssetSQLExecutor, assetID, value string) error {
	return execAssetUpdate(ctx, exec, assetID, "visual embedding update", `UPDATE media_assets SET visual_embedding = ?, updated_at = ? WHERE id = ?`, value, time.Now().UTC().Format(time.RFC3339), assetID)
}

func UpdateMediaAssetAudioEmbedding(ctx context.Context, exec mediaAssetSQLExecutor, assetID, value string) error {
	return execAssetUpdate(ctx, exec, assetID, "audio embedding update", `UPDATE media_assets SET audio_embedding = ?, updated_at = ? WHERE id = ?`, value, time.Now().UTC().Format(time.RFC3339), assetID)
}

func UpdateMediaAssetIndexState(ctx context.Context, exec mediaAssetSQLExecutor, assetID, state, updatedAt, lastError string) error {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	var query string
	var args []any
	if strings.TrimSpace(lastError) == "" {
		query = `UPDATE media_assets SET index_state = ?, index_state_updated_at = ?, metadata_json = json_remove(COALESCE(metadata_json, '{}'), '$.last_index_error'), updated_at = ? WHERE id = ?`
		args = []any{state, updatedAt, updatedAt, assetID}
	} else {
		query = `UPDATE media_assets SET index_state = ?, index_state_updated_at = ?, metadata_json = json_set(COALESCE(metadata_json, '{}'), '$.last_index_error', ?), updated_at = ? WHERE id = ?`
		args = []any{state, updatedAt, lastError, updatedAt, assetID}
	}
	return execAssetUpdate(ctx, exec, assetID, "index state update", query, args...)
}

func SetMediaAssetIndexed(ctx context.Context, exec mediaAssetSQLExecutor, assetID, contentHash, sourceVersion, updatedAt, embeddingModel, embeddingVersion, contractHash string) (bool, error) {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	result, err := exec.ExecContext(ctx, `
		UPDATE media_assets
		SET index_state = 'INDEXED', index_state_updated_at = ?, updated_at = ?,
			metadata_json = json_set(
				json_set(
					json_set(
						json_set(
							json_set(COALESCE(metadata_json, '{}'), '$.indexed_at', ?),
							'$.indexed_content_hash', ?),
						'$.embedding_model', ?),
					'$.embedding_model_version', ?),
				'$.embedding_contract_hash', ?)
		WHERE id = ? AND source_version = ? AND index_state = 'INDEXING'`,
		updatedAt, updatedAt, updatedAt, contentHash, embeddingModel, embeddingVersion, contractHash, assetID, sourceVersion)
	if err != nil {
		return false, fmt.Errorf("asset committer: indexed state update: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("asset committer: indexed state rows affected: %w", err)
	}
	return affected == 1, nil
}

func UpdateMediaAssetFolderPath(ctx context.Context, exec mediaAssetSQLExecutor, assetID, folderID, folderPath, updatedAt string) error {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return execAssetUpdate(ctx, exec, assetID, "folder path update", `UPDATE media_assets SET folder_id = ?, folder_path = ?, updated_at = ? WHERE id = ?`, folderID, folderPath, updatedAt, assetID)
}

func UpdateMediaAssetLifecycle(ctx context.Context, exec mediaAssetSQLExecutor, assetID, state, deletedAt, updatedAt string) error {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return execAssetUpdate(ctx, exec, assetID, "lifecycle update", `UPDATE media_assets SET lifecycle_state = ?, deleted_at = ?, updated_at = ? WHERE id = ?`, state, deletedAt, updatedAt, assetID)
}

func UpdateMediaAssetTaxonomy(ctx context.Context, exec mediaAssetSQLExecutor, taxonomy mediaregistry.AssetTaxonomy) error {
	if err := taxonomy.Validate(); err != nil {
		return fmt.Errorf("asset committer: taxonomy update: %w", err)
	}
	return execAssetUpdate(ctx, exec, taxonomy.AssetID, "taxonomy update", `UPDATE media_assets SET namespace = ?, asset_kind = ?, source_type = ?, semantic_role = ?, updated_at = ? WHERE id = ?`, taxonomy.Namespace, taxonomy.AssetKind, taxonomy.SourceType, taxonomy.SemanticRole, time.Now().UTC().Format(time.RFC3339), taxonomy.AssetID)
}

func LinkMediaAssetContent(ctx context.Context, exec mediaAssetSQLExecutor, assetID, contentSHA256 string) error {
	return execAssetUpdate(ctx, exec, assetID, "content link", `UPDATE media_assets SET content_sha256 = ?, updated_at = ? WHERE id = ?`, contentSHA256, time.Now().UTC().Format(time.RFC3339), assetID)
}

func UpdateMediaAssetSearchText(ctx context.Context, exec mediaAssetSQLExecutor, assetID, searchText, updatedAt string) error {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return execAssetUpdate(ctx, exec, assetID, "search text update", `UPDATE media_assets SET search_text = ?, updated_at = ? WHERE id = ?`, searchText, updatedAt, assetID)
}

func UpdateMediaAssetUpdatedAt(ctx context.Context, exec mediaAssetSQLExecutor, assetID, updatedAt string) error {
	if strings.TrimSpace(updatedAt) == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	return execAssetUpdate(ctx, exec, assetID, "updated-at refresh", `UPDATE media_assets SET updated_at = ? WHERE id = ?`, updatedAt, assetID)
}

func UpdateMediaAssetOrphanMetadata(ctx context.Context, exec mediaAssetSQLExecutor, assetID string, detectedAt time.Time, kind string) error {
	at := detectedAt.UTC().Format(time.RFC3339)
	key := "orphan_" + strings.TrimSpace(kind)
	if key != "orphan_local" && key != "orphan_drive" {
		key = "orphan_unknown"
	}
	return execAssetUpdate(ctx, exec, assetID, "orphan metadata update", `UPDATE media_assets SET metadata_json = json_set(json_set(json_set(COALESCE(metadata_json, '{}'), '$.`+key+`', 1), '$.orphan_reason', ?), '$.orphan_detected_at', ?), updated_at = ? WHERE id = ?`, kind, at, at, assetID)
}
