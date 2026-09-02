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
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
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
		`UPDATE media_assets SET content_sha256 = $1, updated_at = $2 WHERE id = $3`,
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
		`UPDATE media_assets SET namespace = $1, asset_kind = $2, source_type = $3, semantic_role = $4, updated_at = $5 WHERE id = $6`,
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
