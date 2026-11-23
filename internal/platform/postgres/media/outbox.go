// Package media — outbox.go: the PostgreSQL canonical outbox write surface.
//
// Mirrors internal/platform/sqlite/outboxevents (godlike/06 SSOT: one
// outbox fact, two engine adapters of the same contract). Every code path
// that mutates authoritative media data AND triggers an external
// side-effect routes through Enqueue inside the producer's transaction:
//
//	BEGIN
//	UPDATE media_assets ...
//	INSERT INTO outbox_events (...) ON CONFLICT (event_key) WHERE event_key <> '' DO NOTHING
//	COMMIT
//
// A worker polls ClaimNext and dispatches to the appropriate handler.
// Events have five states:
//   - pending     — eligible for ClaimNext
//   - processing  — claimed by a worker (lease_id non-empty)
//   - completed   — terminal success
//   - dead_letter — terminal failure (terminal error or max_attempts)
//   - superseded  — terminal "skipped" (event obsoleted by a newer
//     aggregate version)
//
// The status column accepts any TEXT (no CHECK constraint on the canonical
// lifecycle set); writes to completed / dead_letter / superseded MUST go
// through the canonical lifecycle mutators to keep lease fencing intact.
package media

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
	"github.com/google/uuid"

	appexport "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/metadataexport"
	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
)

// Outbox event-type and lifecycle constants — canonical values shared with
// the SQLite outbox adapter (single fact family, two engine adapters).
// Do not introduce alternate spellings: the literal identity is OWNED by
// internal/kernel/event (godlike/06 one owner per fact) and this block is a
// compile-time re-export, so the two engine adapters cannot drift.
const (
	// EventAssetIndexRequested is the canonical asset.index.requested event.
	EventAssetIndexRequested = event.AssetIndexRequested
	// EventMetadataEnrichRequested is the canonical durable async
	// metadata-enrichment request (Sept 2026). The YouTube clip commit
	// emits it in the SAME transaction as media_assets so the LLM
	// (Ollama) analysis runs OUTSIDE the extraction critical path; the
	// media outbox worker consumes it, runs the analyzer, and writes the
	// semantic snapshot + asset.index.requested atomically.
	EventMetadataEnrichRequested = event.MetadataEnrichRequested
	// ReindexEnvelopeV1Schema is the schema_version stamped in the payload.
	ReindexEnvelopeV1Schema = event.AssetIndexRequestedV1Schema
	// SupersedeStatus is the terminal "skipped" status. Re-export of the
	// canonical value OWNED by internal/kernel/event/outbox_status.go; the
	// SQLite outbox carries the same value and the two must never drift.
	SupersedeStatus = event.OutboxStatusSuperseded

	// PriorityNormal is the default scheduling priority.
	PriorityNormal = event.OutboxPriorityNormal
	// PriorityHigh is the script-required index request priority.
	PriorityHigh = event.OutboxPriorityHigh
)

// EnqueueResult is the typed feedback from Enqueue (SQLite parity:
// outboxevents.EnqueueResult). Inserted=true means the INSERT landed;
// Inserted=false means the conflict arbiter fired and ExistingStatus
// carries the existing row's status.
type EnqueueResult struct {
	EventID               int64
	Inserted              bool
	ExistingStatus        string
	ExistingEventType     string
	ExistingAggregateType string
	ExistingAggregateID   string
}

// Repository wraps SQL access to the outbox_events table over PostgreSQL.
type Repository struct {
	db *sql.DB
}

// NewOutboxRepository creates a Repository backed by db.
func NewOutboxRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

// compile-time assertion: *Repository satisfies the narrow surface the
// committer consumes.
var _ outboxRepository = (*Repository)(nil)

// Enqueue inserts one outbox row using the caller-owned transaction.
// The partial unique index ux_outbox_events_event_key (event_key <> ”)
// is the conflict arbiter: non-empty event keys are idempotent-arbitered,
// empty event keys (one-shot inserts) never conflict.
func (r *Repository) Enqueue(ctx context.Context, tx *sql.Tx, eventType, aggregateID, aggregateType, payloadJSON, eventKey string) (*EnqueueResult, error) {
	return r.enqueue(ctx, tx, false, 0, eventType, aggregateID, aggregateType, payloadJSON, eventKey)
}

// EnqueueWithPriority inserts one outbox row with an explicit scheduling
// priority (SQLite parity: outboxevents.EnqueueWithPriority).
func (r *Repository) EnqueueWithPriority(ctx context.Context, tx *sql.Tx, eventType, aggregateID, aggregateType, payloadJSON, eventKey string, priority int) (*EnqueueResult, error) {
	return r.enqueue(ctx, tx, true, priority, eventType, aggregateID, aggregateType, payloadJSON, eventKey)
}

func (r *Repository) enqueue(ctx context.Context, tx *sql.Tx, withPriority bool, priority int, eventType, aggregateID, aggregateType, payloadJSON, eventKey string) (*EnqueueResult, error) {
	now := timeutil.FormatRFC3339(time.Now())
	queryRow := r.db.QueryRowContext
	if tx != nil {
		queryRow = tx.QueryRowContext
	}

	// RETURNING id distinguishes insert from arbiter suppression directly:
	// DO NOTHING returns zero rows when the conflict fired.
	// Dual-write expand (004): legacy TEXT + _ts TIMESTAMPTZ mirrored via NULLIF cast.
	var insertedID int64
	var err error
	if withPriority {
		err = queryRow(ctx, `
			INSERT INTO outbox_events (event_type, aggregate_id, aggregate_type, payload_json, event_key, priority, created_at, updated_at, created_at_ts, updated_at_ts)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NULLIF($7, '')::timestamptz, NULLIF($8, '')::timestamptz)
			ON CONFLICT (event_key) WHERE event_key <> '' DO NOTHING
			RETURNING id`,
			eventType, aggregateID, aggregateType, payloadJSON, eventKey, priority, now, now).Scan(&insertedID)
	} else {
		err = queryRow(ctx, `
			INSERT INTO outbox_events (event_type, aggregate_id, aggregate_type, payload_json, event_key, created_at, updated_at, created_at_ts, updated_at_ts)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($6, '')::timestamptz, NULLIF($7, '')::timestamptz)
			ON CONFLICT (event_key) WHERE event_key <> '' DO NOTHING
			RETURNING id`,
			eventType, aggregateID, aggregateType, payloadJSON, eventKey, now, now).Scan(&insertedID)
	}
	if err == nil {
		return &EnqueueResult{EventID: insertedID, Inserted: true}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("media.outbox.Enqueue(%s, %s): %w", eventType, aggregateID, err)
	}
	// Arbiter suppressed the insert — query the existing row's status so
	// the producer knows whether the event was squelched by a completed,
	// dead_letter, or superseded row. IMPORTANT: use the same handle as
	// the INSERT (tx when provided, db otherwise) so the read observes
	// the conflicting row inside the same snapshot.
	var (
		existingID                                                                    int64
		existingStatus, existingEventType, existingAggregateType, existingAggregateID string
	)
	err = queryRow(ctx, `
		SELECT id, status, event_type, aggregate_type, aggregate_id
		FROM outbox_events WHERE event_key = $1`, eventKey).
		Scan(&existingID, &existingStatus, &existingEventType, &existingAggregateType, &existingAggregateID)
	if err != nil {
		return nil, fmt.Errorf("media.outbox.Enqueue(%s, %s): resolve existing row: %w", eventType, aggregateID, err)
	}
	return &EnqueueResult{
		EventID:               existingID,
		Inserted:              false,
		ExistingStatus:        existingStatus,
		ExistingEventType:     existingEventType,
		ExistingAggregateType: existingAggregateType,
		ExistingAggregateID:   existingAggregateID,
	}, nil
}

// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-20): the private helper
// Repository.exec was DELETED here. It resolved an exec-handle between the
// in-scope *sql.Tx and r.db, but every media outbox write goes through the
// explicit Enqueue/EnqueueWithPriority tx path, so it had no caller and
// `deadcode -test ./...` reported it as unreachable.

// ── absorbed from index_event.go ──

// CommitIndexEventTx emits the canonical asset.index.requested event when
// the caller already owns the asset transaction.
func (c *PostgresAssetCommitter) CommitIndexEventTx(ctx context.Context, tx *sql.Tx, assetID, source, contentHash, mediaType string) error {
	if c == nil || c.box == nil {
		return fmt.Errorf("asset committer: canonical outbox is unavailable")
	}
	if tx == nil {
		return fmt.Errorf("asset committer: index event transaction is required")
	}
	sourceVersion := contentHash
	if sourceVersion == "" {
		// Mirror the canonical field normalization: the source version
		// falls back to the stored fingerprint when the caller cannot
		// supply one.
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(NULLIF(source_version,''), NULLIF(content_sha256,'')) FROM media_assets WHERE id = $1`,
			assetID).Scan(&sourceVersion); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("asset committer: index event asset %q not found", assetID)
			}
			return fmt.Errorf("asset committer: resolve index event source version: %w", err)
		}
	}
	if sourceVersion == "" {
		return fmt.Errorf("asset committer: index request source_version is required")
	}
	_, err := CommitIndexRequestTx(ctx, tx, c.box, IndexRequest{
		AssetID:       assetID,
		Source:        source,
		MediaType:     mediaType,
		SourceVersion: sourceVersion,
		RequestedAt:   time.Now(),
	})
	return err
}

// CommitIndexEventTx on the aggregate committer delegates to the embedded
// canonical asset committer.
func (c *PostgresMediaCommitter) CommitIndexEventTx(ctx context.Context, tx *sql.Tx, assetID, source, contentHash, mediaType string) error {
	if c == nil || c.assets == nil {
		return fmt.Errorf("media committer: canonical asset committer is unavailable")
	}
	return c.assets.CommitIndexEventTx(ctx, tx, assetID, source, contentHash, mediaType)
}

// ── absorbed from outbox_index_fence.go ──

// indexFenceKind is the closed outcome of applyIndexedFenceTx.
type indexFenceKind int

const (
	// indexFenceApplied — the row was flipped to INDEXED; the caller may
	// write the vector and commit.
	indexFenceApplied indexFenceKind = iota
	// indexFenceAssetMissing — no media_assets row owns the id. Fail
	// closed (never write an orphan vector); the caller owns the retry
	// decision, exactly as before this fence existed.
	indexFenceAssetMissing
	// indexFenceAssetRetired — the row exists but its index_state forbids
	// indexing forever. Terminal: retrying can never make it applicable.
	indexFenceAssetRetired
)

// indexFenceOutcome couples the decision with its operator-facing reason.
type indexFenceOutcome struct {
	Kind   indexFenceKind
	Reason string
}

// retiredIndexStates returns the canonical closed set of index_state values
// that FORBID the terminal INDEXED transition: a row being retired
// (DELETE_PENDING) and a retired row (DELETED).
//
// Restore is deliberately NOT blocked by this set: the restore producer
// (delete_saga.go::EnqueueAndRestore) stamps index_state=DISCOVERED in its own
// transaction BEFORE emitting the canonical index request, so a restored asset
// reaches the worker already outside the retired set.
func retiredIndexStates() []string {
	return []string{string(asset.StateIndexDeletePending), string(asset.StateDELETED)}
}

// retiredIndexStatePredicate renders `COALESCE(<column>, ”) NOT IN ($n, …)`
// and appends the bound values to args starting at ordinal `first`.
//
// The predicate is built from retiredIndexStates so a future state added to the
// canonical enum list cannot be silently ignored by the SQL.
func retiredIndexStatePredicate(column string, args *[]any, first int) string {
	states := retiredIndexStates()
	placeholders := make([]string, 0, len(states))
	for i, state := range states {
		*args = append(*args, state)
		placeholders = append(placeholders, fmt.Sprintf("$%d", first+i))
	}
	return fmt.Sprintf("COALESCE(%s, '') NOT IN (%s)", column, strings.Join(placeholders, ", "))
}

// indexStamp is the operator-visible proof that the vector was built from the
// CURRENT asset snapshot. The old SQLite→vector chain wrote it through
// SetMediaAssetIndexed; the PostgreSQL fence replaced that CAS but initially
// dropped the metadata stamp, leaving every freshly indexed row at
// index_health=STALE (content_hash != indexed_content_hash) forever.
//
// The content hash is resolved from the row itself (binary_sha256 wins, exactly
// like operatorItemProjection) unless the caller carries one; binding values
// are never invented.
type indexStamp struct {
	ContentHash      string
	EmbeddingModel   string
	EmbeddingVersion string
}

// applyIndexedFenceTx performs the FENCED terminal INDEXED transition inside
// the caller-owned transaction and reports what happened.
//
// Exactly one row is expected: media_assets.id is the primary key, so a
// rows-affected of 1 means the fence passed and anything else means the fence
// rejected the row (absent or retired). A 0-row result is NOT an error — it is
// the fence doing its job — so the outcome is classified, not thrown.
func applyIndexedFenceTx(ctx context.Context, tx *sql.Tx, assetID, updatedAt string, stamp indexStamp) (indexFenceOutcome, error) {
	if tx == nil {
		return indexFenceOutcome{}, errors.New("media index fence: transaction is required")
	}
	if strings.TrimSpace(assetID) == "" {
		return indexFenceOutcome{}, errors.New("media index fence: asset id is required")
	}
	if strings.TrimSpace(updatedAt) == "" {
		return indexFenceOutcome{}, errors.New("media index fence: updated-at is required")
	}

	args := []any{string(asset.StateIndexed), updatedAt, assetID, stamp.ContentHash, stamp.EmbeddingModel, stamp.EmbeddingVersion}
	predicate := retiredIndexStatePredicate("index_state", &args, len(args)+1)
	// Dual-write expand (BASELINE_PLAN.md §5.2, parity with the canonical
	// UpdateMediaAssetIndexState): the same RFC3339 string is written to the
	// legacy TEXT column AND mirrored into the TIMESTAMPTZ column. Writing
	// only the TEXT side left the two representations of one fact diverging.
	//
	// The metadata stamp is part of the SAME fenced UPDATE: a row can never be
	// INDEXED without the indexed hash that proves what was embedded. Every
	// COALESCE keeps an existing value when the caller has no better fact.
	res, err := tx.ExecContext(ctx, `
		UPDATE media_assets
		SET index_state = $1,
		    index_state_updated_at = $2,
		    index_state_updated_at_ts = NULLIF($2, '')::timestamptz,
		    metadata_json = jsonb_set(
		        jsonb_set(
		            jsonb_set(
		                jsonb_set(
		                    COALESCE(NULLIF(metadata_json, ''), '{}')::jsonb,
		                    '{indexed_at}', to_jsonb($2::text)
		                ),
		                '{indexed_content_hash}',
		                to_jsonb(COALESCE(
		                    NULLIF($4::text, ''),
		                    NULLIF(binary_sha256, ''),
		                    NULLIF(content_sha256, ''),
		                    COALESCE(NULLIF(metadata_json, ''), '{}')::jsonb->>'content_hash',
		                    ''
		                ))
		            ),
		            '{embedding_model}',
		            to_jsonb(COALESCE(
		                NULLIF($5::text, ''),
		                COALESCE(NULLIF(metadata_json, ''), '{}')::jsonb->>'embedding_model',
		                ''
		            ))
		        ),
		        '{embedding_model_version}',
		        to_jsonb(COALESCE(
		            NULLIF($6::text, ''),
		            COALESCE(NULLIF(metadata_json, ''), '{}')::jsonb->>'embedding_model_version',
		            ''
		        ))
		    )::text
		WHERE id = $3 AND `+predicate,
		args...)
	if err != nil {
		return indexFenceOutcome{}, fmt.Errorf("media index fence: flip asset %q: %w", assetID, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return indexFenceOutcome{}, fmt.Errorf("media index fence: rows affected asset %q: %w", assetID, err)
	}
	if affected == 1 {
		return indexFenceOutcome{Kind: indexFenceApplied}, nil
	}

	// The fence rejected the row. Classify it on the SAME transaction so the
	// observed state is the one the fence compared against.
	var state string
	readErr := tx.QueryRowContext(ctx,
		`SELECT COALESCE(index_state, '') FROM media_assets WHERE id = $1`, assetID,
	).Scan(&state)
	if errors.Is(readErr, sql.ErrNoRows) {
		return indexFenceOutcome{Kind: indexFenceAssetMissing, Reason: "asset_not_found"}, nil
	}
	if readErr != nil {
		return indexFenceOutcome{}, fmt.Errorf("media index fence: read index_state asset %q: %w", assetID, readErr)
	}
	state = strings.TrimSpace(state)
	return indexFenceOutcome{
		Kind:   indexFenceAssetRetired,
		Reason: "asset_retired:" + state,
	}, nil
}

// ── absorbed from reindex_requester.go ──

// ReindexRequester is the canonical compatibility seam for callers that still
// expose an imperative IndexClip/IndexAsset method. In PostgreSQL media mode
// those calls must NOT execute the retired SQLite -> pgvector indexing pipeline;
// they enqueue the canonical asset.index.requested event in the PostgreSQL
// outbox and let PostgresIndexWorker perform embedding + pgvector indexing.
type ReindexRequester struct {
	db  *sql.DB
	box *Repository
}

// NewReindexRequester binds imperative reindex requests to the same
// PostgreSQL media SSOT/outbox used by canonical asset commits.
func NewReindexRequester(db *sql.DB) *ReindexRequester {
	if db == nil {
		return nil
	}
	return &ReindexRequester{db: db, box: NewOutboxRepository(db)}
}

// RequestIndex implements the narrow compatibility port consumed by the
// legacy-named clipindexer service. It is deliberately an event request, not
// an inline index operation: one canonical worker remains the only writer of
// media_embeddings/index_state in PostgreSQL mode.
func (r *ReindexRequester) RequestIndex(ctx context.Context, assetID string) error {
	if r == nil || r.db == nil || r.box == nil {
		return fmt.Errorf("postgres media reindex requester: not wired")
	}
	assetID = strings.TrimSpace(assetID)
	if assetID == "" {
		return fmt.Errorf("postgres media reindex requester: asset id is required")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres media reindex requester: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var (
		source, mediaType, assetKind, sourceVersion string
		contentSHA, binarySHA                       string
		assetVersion                                string
	)
	if err := tx.QueryRowContext(ctx, `
		SELECT source, media_type, COALESCE(asset_kind, ''), source_version,
		       content_sha256, binary_sha256, asset_version
		FROM media_assets
		WHERE id = $1 AND deleted_at = ''
		FOR UPDATE
	`, assetID).Scan(&source, &mediaType, &assetKind, &sourceVersion, &contentSHA, &binarySHA, &assetVersion); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("postgres media reindex requester: asset %q not found", assetID)
		}
		return fmt.Errorf("postgres media reindex requester: load asset %q: %w", assetID, err)
	}

	// Preserve the canonical registered-vs-searchable policy before emitting
	// work. The decision comes from the shared mediaregistry.AssetTaxonomy rule,
	// read here inside the already-open PostgreSQL transaction.
	eligibility := capregistry.AssetTaxonomy{
		AssetID:   assetID,
		AssetKind: capregistry.AssetKind(assetKind),
		MediaType: capregistry.MediaType(mediaType),
	}.IndexEligibility()
	if eligibility == capregistry.IndexEligibilityRegistered {
		return tx.Commit()
	}

	// Old rows may predate source_version. Preserve fail-closed identity while
	// accepting the strongest canonical fingerprint already stored on the row.
	for _, candidate := range []string{sourceVersion, contentSHA, binarySHA, assetVersion} {
		if strings.TrimSpace(candidate) != "" {
			sourceVersion = strings.TrimSpace(candidate)
			break
		}
	}
	if sourceVersion == "" {
		return fmt.Errorf("postgres media reindex requester: asset %q has no canonical source/content version", assetID)
	}

	req := IndexRequest{
		AssetID:       assetID,
		Source:        source,
		MediaType:     mediaType,
		SourceVersion: sourceVersion,
		RequestedAt:   time.Now(),
		Priority:      PriorityHigh,
	}
	result, err := CommitIndexRequestTx(ctx, tx, r.box, req)
	if err != nil {
		return fmt.Errorf("postgres media reindex requester: enqueue asset %q: %w", assetID, err)
	}

	// A deterministic initial commit event may already be terminal. An
	// imperative reindex request must then create a fresh idempotency key rather
	// than silently succeeding without work. Pending/processing duplicates are
	// intentionally coalesced into the in-flight canonical request.
	if !result.Inserted && isTerminalOutboxStatus(result.ExistingStatus) {
		req.EventKeySuffix = ":reindex:" + uuid.NewString()
		if _, err := CommitIndexRequestTx(ctx, tx, r.box, req); err != nil {
			return fmt.Errorf("postgres media reindex requester: force reindex asset %q: %w", assetID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres media reindex requester: commit asset %q: %w", assetID, err)
	}
	return nil
}

// isTerminalOutboxStatus delegates to the canonical predicate OWNED by
// internal/kernel/event (godlike/06 one owner per fact). The local copy used to
// omit the legacy `dead` spelling, so it disagreed with the control-plane UoW
// on the same row.
func isTerminalOutboxStatus(status string) bool {
	return event.IsTerminalOutboxStatus(status)
}

// ── absorbed from metadata_export_resolver.go ──

// MetadataExportResolver reads media identity and event scope from the
// PostgreSQL media SSOT while retaining delivery history on the operational
// SQLite database. This split prevents exports from silently mixing stale
// media rows with current operational delivery records.
type MetadataExportResolver struct {
	mediaDB    *sql.DB
	operations *sql.DB
}

var _ appexport.AssetResolver = (*MetadataExportResolver)(nil)

// NewMetadataExportResolver constructs the split metadata-export reader.
// mediaDB is authoritative for media assets, locations, provenance, and the
// PostgreSQL media outbox; operations is authoritative only for delivery_log.
func NewMetadataExportResolver(mediaDB, operations *sql.DB) appexport.AssetResolver {
	if mediaDB == nil || operations == nil {
		return nil
	}
	return &MetadataExportResolver{mediaDB: mediaDB, operations: operations}
}

// ResolveAssetIDs resolves explicit IDs or the media outbox scope for a job.
// Event scope is read from PostgreSQL because media events are emitted into
// the PostgreSQL outbox by the canonical media committer.
func (r *MetadataExportResolver) ResolveAssetIDs(ctx context.Context, jobID string, explicitIDs []string) ([]string, error) {
	if len(explicitIDs) > 0 {
		ids := append([]string(nil), explicitIDs...)
		return ids, nil
	}
	if r == nil || r.mediaDB == nil || jobID == "" {
		return nil, nil
	}

	rows, err := r.mediaDB.QueryContext(ctx, `
		SELECT DISTINCT aggregate_id
		FROM outbox_events
		WHERE aggregate_id <> ''
		  AND (payload_json LIKE $1 OR aggregate_id = $2)
		ORDER BY aggregate_id
		LIMIT 500
	`, "%"+jobID+"%", jobID)
	if err != nil {
		return nil, fmt.Errorf("postgres metadata export: resolve job scope %q: %w", jobID, err)
	}
	defer func() { _ = rows.Close() }()

	ids := make([]string, 0, 16)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres metadata export: scan job scope: %w", err)
		}
		if id != "" {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres metadata export: iterate job scope: %w", err)
	}
	return ids, nil
}

// LoadTechnicalSection reads technical media metadata and the canonical Drive
// location from PostgreSQL. Missing assets are represented as nil, nil.
func (r *MetadataExportResolver) LoadTechnicalSection(ctx context.Context, assetID string) (map[string]any, error) {
	if r == nil || r.mediaDB == nil {
		return nil, fmt.Errorf("postgres metadata export: media reader unavailable")
	}

	var (
		id, source, name, mediaType, category string
		quality                               sql.NullFloat64
		driveFileID, driveLink                sql.NullString
	)
	err := r.mediaDB.QueryRowContext(ctx, `
		SELECT a.id, COALESCE(a.source, ''), COALESCE(a.name, ''),
		       COALESCE(a.media_type, ''), COALESCE(a.category, ''),
		       a.quality_score,
		       COALESCE(l.external_id, ''), COALESCE(l.web_view_link, '')
		FROM media_assets a
		LEFT JOIN asset_locations l
		  ON l.asset_id = a.id AND l.location_kind = 'drive'
		WHERE a.id = $1
		LIMIT 1
	`, assetID).Scan(&id, &source, &name, &mediaType, &category, &quality, &driveFileID, &driveLink)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres metadata export: load technical %q: %w", assetID, err)
	}

	return map[string]any{
		"asset_id":      id,
		"source":        source,
		"name":          name,
		"media_type":    mediaType,
		"category":      category,
		"drive_file_id": driveFileID.String,
		"drive_link":    driveLink.String,
		"quality_score": quality.Float64,
	}, nil
}

// LoadProvenanceSection reads the current source ledger from PostgreSQL.
func (r *MetadataExportResolver) LoadProvenanceSection(ctx context.Context, assetID string) (map[string]any, error) {
	if r == nil || r.mediaDB == nil {
		return nil, fmt.Errorf("postgres metadata export: media reader unavailable")
	}

	var source string
	err := r.mediaDB.QueryRowContext(ctx, `
		SELECT COALESCE(source_type, '')
		FROM media_asset_sources
		WHERE asset_id = $1
		ORDER BY is_primary DESC, `+tsCastExpr("discovered_at")+` DESC
		LIMIT 1
	`, assetID).Scan(&source)
	if err == sql.ErrNoRows {
		// The asset itself remains the authoritative fallback when an older
		// row has not yet been copied into the provenance ledger.
		err = r.mediaDB.QueryRowContext(ctx,
			`SELECT COALESCE(source, '') FROM media_assets WHERE id = $1`, assetID).Scan(&source)
	}
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("postgres metadata export: load provenance %q: %w", assetID, err)
	}
	return map[string]any{"source": source}, nil
}

// LoadDeliverySection intentionally reads only the operational delivery log
// from SQLite. Delivery history is not media authority and is not moved to
// PostgreSQL merely to simplify this reader.
func (r *MetadataExportResolver) LoadDeliverySection(ctx context.Context, assetID string) ([]any, error) {
	if r == nil || r.operations == nil {
		return nil, fmt.Errorf("postgres metadata export: delivery reader unavailable")
	}

	rows, err := r.operations.QueryContext(ctx, `
		SELECT delivery_id, endpoint_url, status_code, response_hash, delivered_at, note
		FROM delivery_log
		WHERE asset_id = ?
		ORDER BY delivered_at DESC
		LIMIT 50
	`, assetID)
	if err != nil {
		return nil, fmt.Errorf("sqlite metadata export: load delivery %q: %w", assetID, err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]any, 0, 16)
	for rows.Next() {
		var deliveryID, endpointURL, responseHash, deliveredAt, note string
		var statusCode sql.NullInt64
		if err := rows.Scan(&deliveryID, &endpointURL, &statusCode, &responseHash, &deliveredAt, &note); err != nil {
			return nil, fmt.Errorf("sqlite metadata export: scan delivery %q: %w", assetID, err)
		}
		out = append(out, map[string]any{
			"delivery_id":   deliveryID,
			"endpoint_url":  endpointURL,
			"status_code":   statusCode.Int64,
			"response_hash": responseHash,
			"delivered_at":  deliveredAt,
			"note":          note,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite metadata export: iterate delivery %q: %w", assetID, err)
	}
	return out, nil
}
