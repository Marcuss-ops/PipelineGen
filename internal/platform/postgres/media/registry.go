// Package media — registry.go: transaction-scoped registry operations for
// the PostgreSQL media domain.
//
// Mirrors internal/platform/sqlite/mediaregistry/tx.go (godlike/06 SSOT:
// the SQL for each registry operation lives in exactly one helper per
// engine; both the *sql.DB and *sql.Tx methods delegate to it). The
// PostgresMediaCommitter needs these writes inside the SAME transaction as
// the media_assets upsert and the outbox write so the canonical commit is
// atomic.
package media

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/event"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/idempotency"
	"github.com/Marcuss-ops/PipelineGen/pkg/sqlport"
	"github.com/google/uuid"
)

// Tx is the transaction-scoped registry write surface over PostgreSQL
// (SQLite mirror: sqlitemediaregistry.Ledger).
type Tx struct {
	db *sql.DB
}

// NewRegistry constructs the adapter. Fail-fast: a nil database is a
// programmer error, not a silent no-op (godlike/07).
func NewRegistry(db *sql.DB) (*Tx, error) {
	if db == nil {
		return nil, errors.New("media registry postgres adapter: nil database")
	}
	return &Tx{db: db}, nil
}

// compile-time assertion: *Tx satisfies the surface the committer consumes.
var _ registryTxWriter = (*Tx)(nil)

// RegisterSourceTx upserts a provenance record inside tx (SQLite mirror:
// mediaregistry.registerSource — idempotent on source_id; re-putting the
// same source refreshes content link, version, discovery timestamp and
// primary flag).
func (r *Tx) RegisterSourceTx(ctx context.Context, tx *sql.Tx, src capregistry.AssetSource) error {
	if tx == nil {
		return errors.New("media registry postgres adapter: tx is required")
	}
	if err := validateAssetSource(src); err != nil {
		return err
	}
	primary := 0
	if src.IsPrimary {
		primary = 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO media_asset_sources
		(source_id, asset_id, content_sha256, source_type, source_uri,
		 source_version, discovered_at, is_primary)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (source_id) DO UPDATE SET
			content_sha256 = excluded.content_sha256,
			source_version = excluded.source_version,
			discovered_at  = excluded.discovered_at,
			is_primary     = excluded.is_primary`,
		src.SourceID, src.AssetID, src.ContentSHA256, src.SourceType, src.SourceURI,
		src.SourceVersion, src.DiscoveredAt, primary); err != nil {
		return fmt.Errorf("register asset source %q: %w", src.SourceID, err)
	}
	return nil
}

// LinkContentTx sets media_assets.content_sha256 inside tx. Fails closed
// when the asset row does not exist (SQLite mirror: execAssetUpdate
// rows-affected gate).
func (r *Tx) LinkContentTx(ctx context.Context, tx *sql.Tx, assetID, contentSHA256 string) error {
	if tx == nil {
		return errors.New("media registry postgres adapter: tx is required")
	}
	if assetID == "" || contentSHA256 == "" {
		return fmt.Errorf("%w: asset_id and content_sha256 are required", capregistry.ErrAssetSourceInvalid)
	}
	result, err := tx.ExecContext(ctx,
		`UPDATE media_assets SET content_sha256 = $1, updated_at = $2, updated_at_ts = NULLIF($2, '')::timestamptz WHERE id = $3`,
		contentSHA256, time.Now().UTC().Format(time.RFC3339), assetID)
	if err != nil {
		return fmt.Errorf("asset committer: content link: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("asset committer: content link rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("asset committer: content link: asset %q not found", assetID)
	}
	return nil
}

// UpsertTaxonomyTx writes the canonical taxonomy dimensions inside tx
// (SQLite mirror: UpdateMediaAssetTaxonomy — validate then conditional
// update; fails closed on unknown asset).
func (r *Tx) UpsertTaxonomyTx(ctx context.Context, tx *sql.Tx, t capregistry.AssetTaxonomy) error {
	if tx == nil {
		return errors.New("media registry postgres adapter: tx is required")
	}
	if err := t.Validate(); err != nil {
		return fmt.Errorf("asset committer: taxonomy update: %w", err)
	}
	result, err := tx.ExecContext(ctx,
		`UPDATE media_assets SET namespace = $1, asset_kind = $2, source_type = $3, semantic_role = $4, updated_at = $5, updated_at_ts = NULLIF($5, '')::timestamptz WHERE id = $6`,
		t.Namespace, t.AssetKind, t.SourceType, t.SemanticRole, time.Now().UTC().Format(time.RFC3339), t.AssetID)
	if err != nil {
		return fmt.Errorf("asset committer: taxonomy update: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("asset committer: taxonomy update rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("asset committer: taxonomy update: asset %q not found", t.AssetID)
	}
	return nil
}

// AppendEventTx appends a registry event inside tx and returns its sequence.
// Deterministic event_id keeps replays idempotent: ON CONFLICT DO NOTHING
// preserves the original seq, and the read-back returns it (SQLite mirror:
// mediaregistry.appendEvent).
func (r *Tx) AppendEventTx(ctx context.Context, tx *sql.Tx, event capregistry.Event) (int64, error) {
	if tx == nil {
		return 0, errors.New("media registry postgres adapter: tx is required")
	}
	if event.EventID == "" || event.EventType == "" || event.CreatedAt == "" {
		return 0, errors.New("media registry postgres adapter: event_id, event_type and created_at are required")
	}
	var seq int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO registry_events
		(event_id, asset_id, event_type, run_id, actor, before_hash, after_hash,
		 payload_json, git_sha, app_version, created_at)
		VALUES ($1, NULLIF($2, ''), $3, NULLIF($4, ''), $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (event_id) DO NOTHING
		RETURNING seq`,
		event.EventID, event.AssetID, event.EventType, event.RunID, event.Actor,
		event.BeforeHash, event.AfterHash, defaultEventJSON(event.PayloadJSON), event.GitSHA,
		event.AppVersion, event.CreatedAt).Scan(&seq)
	if err == nil {
		return seq, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("append registry event %q: %w", event.EventID, err)
	}
	// Conflict fired: the event already exists — return its original seq.
	if queryErr := tx.QueryRowContext(ctx,
		`SELECT seq FROM registry_events WHERE event_id = $1`, event.EventID).Scan(&seq); queryErr != nil {
		return 0, fmt.Errorf("read existing registry event sequence %q: %w", event.EventID, queryErr)
	}
	return seq, nil
}

func validateAssetSource(src capregistry.AssetSource) error {
	if src.SourceID == "" || src.AssetID == "" || src.SourceType == "" || src.SourceURI == "" || src.DiscoveredAt == "" {
		return fmt.Errorf("%w: source_id, asset_id, source_type, source_uri and discovered_at are required", capregistry.ErrAssetSourceInvalid)
	}
	return nil
}

func defaultEventJSON(value string) string {
	if strings.TrimSpace(value) == "" {
		return "{}"
	}
	return value
}

// ── absorbed from index_request.go ──

// collectionVersion is the canonical index-schema version stamped in the
// reindex envelope. It mirrors clipindexer.collectionVersion ("v3", VECTOR-003);
// the parity suite asserts equality with clipindexer.CollectionVersion() so
// the two engine adapters can never drift apart.
const collectionVersion = "v3"

// IndexRequest describes one canonical indexing request emitted after the
// corresponding media_assets write has succeeded in the same transaction.
type IndexRequest struct {
	AssetID       string
	Source        string
	MediaType     string
	SourceVersion string
	RequestedAt   time.Time
	// EventKeySuffix differentiates deterministic reindex requests that
	// share the same asset source_version but represent a changed
	// projection input, such as a repaired Drive location. It is appended
	// only to the idempotency key; source_version remains the canonical
	// asset fingerprint used by the supersede gate.
	EventKeySuffix string
	// Priority is the outbox scheduling priority. 0 means "use the default
	// normal priority" (PriorityNormal). Requires the backing repository to
	// implement EnqueueWithPriority; otherwise the request is enqueued at
	// the default priority (never dropped).
	Priority int
}

// IndexRequestCommitResult reports the durable outbox write performed by
// CommitIndexRequestTx.
type IndexRequestCommitResult struct {
	EventID        string
	EventKey       string
	Inserted       bool
	ExistingStatus string
}

// assetIndexRequestedPayload is the canonical payload map for the
// asset.index.requested envelope on the PostgreSQL media plane.
//
// Extracted from CommitIndexRequestTx so the payload CONTRACT — most notably
// `requested_vectors` — is pinned by a test that needs no database: a fact that
// is only reachable through a transaction is a fact no gate protects.
//
// `requested_vectors` comes from the single canonical source
// (event.AssetIndexRequestedVectorChannels) rather than a literal list, so the
// envelope can never declare a channel the plane does not write. The retired
// "transcript" channel was exactly that kind of phantom declaration: the
// envelope asked for two vectors while postgres/media wrote only embedding_type
// 'text', and a consumer honouring the declaration would wait forever for work
// that no producer performed.
func assetIndexRequestedPayload(req IndexRequest, eventID, eventKey string) map[string]any {
	return map[string]any{
		"schema_version":       ReindexEnvelopeV1Schema,
		"event_id":             eventID,
		"asset_id":             req.AssetID,
		"operation":            indexRequestOperationUpsert,
		"source_version":       req.SourceVersion,
		"index_revision":       req.SourceVersion,
		"target_index_version": collectionVersion,
		"requested_vectors":    event.AssetIndexRequestedVectorChannels(),
		"requested_at":         req.RequestedAt.UTC().Format(time.RFC3339Nano),
		"idempotency_key":      eventKey,
		"source":               req.Source,
		"media_type":           req.MediaType,
		"embedding_model":      coreembedding.ModelIDMultilingualE5,
		"embedding_version":    coreembedding.ModelRevisionMultilingualE5,
	}
}

// CommitIndexRequestTx is the sole production emitter for the canonical
// index-request event on PostgreSQL. The payload map, event-key vector and
// envelope fields MUST stay byte-identical to the SQLite emitter so the
// outbox consumer cannot tell which engine produced the event.
func CommitIndexRequestTx(
	ctx context.Context,
	tx sqlport.WriteTx,
	box outboxRepository,
	req IndexRequest,
) (IndexRequestCommitResult, error) {
	if tx == nil {
		return IndexRequestCommitResult{}, fmt.Errorf("asset committer: index request tx is required")
	}
	if box == nil {
		return IndexRequestCommitResult{}, fmt.Errorf("asset committer: index request outbox is required")
	}
	if req.AssetID == "" {
		return IndexRequestCommitResult{}, fmt.Errorf("asset committer: index request asset_id is required")
	}
	if req.SourceVersion == "" {
		return IndexRequestCommitResult{}, fmt.Errorf("asset committer: index request source_version is required")
	}
	if req.RequestedAt.IsZero() {
		req.RequestedAt = time.Now()
	}

	var (
		eventID  string
		eventKey string
		payload  []byte
		err      error
	)

	eventID = uuid.NewString()
	eventKey, err = idempotency.OutboxKey(
		EventAssetIndexRequested,
		req.Source,
		req.AssetID,
		req.SourceVersion,
	)
	if err != nil {
		return IndexRequestCommitResult{}, fmt.Errorf("asset committer: build outbox event_key: %w", err)
	}
	payloadMap := assetIndexRequestedPayload(req, eventID, eventKey)
	payload, err = json.Marshal(payloadMap)
	if err != nil {
		return IndexRequestCommitResult{}, fmt.Errorf("asset committer: build outbox payload: %w", err)
	}

	if suffix := strings.TrimSpace(req.EventKeySuffix); suffix != "" {
		eventKey += suffix
		var payloadMap map[string]any
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.UseNumber()
		if err := decoder.Decode(&payloadMap); err != nil {
			return IndexRequestCommitResult{}, fmt.Errorf("asset committer: decode outbox payload for suffix: %w", err)
		}
		payloadMap["idempotency_key"] = eventKey
		payload, err = json.Marshal(payloadMap)
		if err != nil {
			return IndexRequestCommitResult{}, fmt.Errorf("asset committer: encode outbox payload for suffix: %w", err)
		}
	}

	// Prefer the priority-aware enqueue when the producer requested a
	// non-default priority; fallback to plain Enqueue keeps structural
	// fakes (and repositories without priority support) fully compatible.
	enqueue := func() (*EnqueueResult, error) {
		return box.Enqueue(
			ctx,
			tx,
			EventAssetIndexRequested,
			req.AssetID,
			"media_asset",
			string(payload),
			eventKey,
		)
	}
	if req.Priority > 0 {
		enqueue = func() (*EnqueueResult, error) {
			return box.EnqueueWithPriority(
				ctx,
				tx,
				EventAssetIndexRequested,
				req.AssetID,
				"media_asset",
				string(payload),
				eventKey,
				req.Priority,
			)
		}
	}
	enqueueResult, err := enqueue()
	if err != nil {
		return IndexRequestCommitResult{}, fmt.Errorf("asset committer: enqueue outbox event: %w", err)
	}
	if enqueueResult == nil {
		return IndexRequestCommitResult{}, fmt.Errorf("asset committer: enqueue outbox event returned nil result")
	}

	return IndexRequestCommitResult{
		EventID:        eventID,
		EventKey:       eventKey,
		Inserted:       enqueueResult.Inserted,
		ExistingStatus: enqueueResult.ExistingStatus,
	}, nil
}

// ── absorbed from version_store.go ──

// PostgresEntityVersionStore is the PostgreSQL media-SSOT implementation of
// the admin console optimistic-concurrency port
// (`CheckAndIncrementAssetVersion`).
//
// It is the PostgreSQL counterpart of the SQLite
// sqlite/adminconsole.VersionStore: ONE version port, two engine
// implementations, selected by composition. The check-and-bump stays a single
// atomic statement so two concurrent PATCHes cannot observe the same expected
// version.
type PostgresEntityVersionStore struct {
	db *sql.DB
}

// NewPostgresEntityVersionStore wires the version store over the media SSOT
// handle. A nil handle returns nil (degrade signal).
func NewPostgresEntityVersionStore(db *sql.DB) *PostgresEntityVersionStore {
	if db == nil {
		return nil
	}
	return &PostgresEntityVersionStore{db: db}
}

// CheckAndIncrementAssetVersion atomically increments media_assets.admin_version
// only when it currently equals expectedVersion. It returns the observed
// version and ok=true when the increment succeeded; otherwise ok=false with the
// version currently stored (mirroring the SQLite contract exactly).
func (s *PostgresEntityVersionStore) CheckAndIncrementAssetVersion(ctx context.Context, assetID string, expectedVersion int) (int, bool, error) {
	if s == nil || s.db == nil {
		return 0, false, errors.New("postgres media: version store not wired")
	}
	if strings.TrimSpace(assetID) == "" {
		return 0, false, errors.New("asset committer: asset id is required")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE media_assets
		SET admin_version = admin_version + 1
		WHERE id = $1 AND admin_version = $2`, assetID, expectedVersion)
	if err != nil {
		return 0, false, fmt.Errorf("asset committer: increment admin version: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, false, fmt.Errorf("asset committer: admin version rows affected: %w", err)
	}

	var currentVersion int
	if err := s.db.QueryRowContext(ctx, `SELECT admin_version FROM media_assets WHERE id = $1`, assetID).Scan(&currentVersion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, errors.New("asset committer: asset not found")
		}
		return 0, false, fmt.Errorf("asset committer: read admin version: %w", err)
	}
	return currentVersion, affected == 1, nil
}

// AdminVersion reads the current optimistic-concurrency version for an asset
// from the media SSOT (0 for a missing row, matching the admin console read).
func (s *PostgresAssetStore) AdminVersion(ctx context.Context, assetID string) (int, error) {
	if s == nil || s.reader == nil || s.reader.db == nil {
		return 0, errors.New("postgres media: asset store not wired")
	}
	var version int
	err := s.reader.db.QueryRowContext(ctx, `SELECT COALESCE(admin_version, 0) FROM media_assets WHERE id = $1`, assetID).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("postgres media: read admin version: %w", err)
	}
	return version, nil
}
