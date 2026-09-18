// Package testsupport — TEST-ONLY SQLite AssetCommitter.
//
// POSTGRES-MEDIA-CUTOVER demolition note: the production SQLite media
// writer family was REMOVED (the canonical media writer is
// PostgresMediaCommitter over PostgreSQL + pgvector). Legacy engine-level
// test suites (finalizer, catalogsync, artlist integration, jobs, youtube
// adapters) still exercise the AssetCommitter CONTRACT against a hermetic
// SQLite engine. This package provides that test double, clearly marked
// test-only: it is NEVER imported by production code.
//
// KNOWN DEBT — a second owner of commit-normalisation semantics (reviewed
// 2026-09-15, deliberately NOT refactored). Nine helpers here are byte-identical
// mirrors of their production counterparts in `internal/platform/postgres/media`:
//
//	normalizeAssetCommitFields   (asset_commit_fields.go ↔ committer.go)
//	normalizeIndexTaxonomy       (sqlite_asset_committer_testdouble.go ↔ committer.go)
//	execAssetUpdate              (sqlite_asset_committer_testdouble.go ↔ committer.go)
//	primaryDriveFileID           (sqlite_asset_committer_testdouble.go ↔ committer.go)
//	primaryWebViewLink           (sqlite_asset_committer_testdouble.go ↔ committer.go)
//	primaryDownloadURL           (sqlite_asset_committer_testdouble.go ↔ committer.go)
//	clipTagsNorm                 (clip_writer_helpers.go ↔ committer.go)
//	derivePolicyVersion          (clip_writer_helpers.go ↔ clip_writer_helpers.go)
//	localizedClipTextsToTextTracks (clip_writer_helpers.go ↔ clip_writer_helpers.go)
//
// Why it is not fixed here: the production helpers are unexported, so sharing
// them means either exporting canonical-writer internals (a production API
// widened for a test's convenience) or extracting the pure normalisation into a
// package both sides import — an architecture decision, not a cleanup.
//
// The rule that keeps this honest until that decision is made: when a
// normalisation rule changes in `internal/platform/postgres/media`, the mirror
// in this package changes in the SAME commit. A test that only passes after
// editing one side is the signal that the other side is now stale, and a test
// suite passing here proves the CONTRACT shape, never the production rule.
package testsupport

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	capcontrol "github.com/Marcuss-ops/PipelineGen/internal/capabilities/controlplane"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	sqlitecontrol "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/controlplane"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"go.uber.org/zap"
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
