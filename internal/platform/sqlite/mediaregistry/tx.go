// Package mediaregistry — tx.go: transaction-scoped registry operations.
//
// The MediaCommitter (internal/platform/sqlite/assets) needs
// to run the registry writes (RegisterSource, LinkContent, UpsertTaxonomy,
// AppendEvent) inside the SAME SQLite transaction as the media_assets upsert
// and the outbox write, so the canonical commit is atomic. The plain Ledger
// methods operate on *sql.DB (auto-commit); the *Tx variants operate on the
// caller-owned *sql.Tx.
//
// godlike/06 SSOT: the SQL for each operation lives in exactly one helper
// (below); both the *sql.DB method and the *sql.Tx method delegate to it, so
// the registry writes have a single owner regardless of transaction scope.
package mediaregistry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
)

// execer is the narrow write surface shared by *sql.DB and *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// UpsertTaxonomyTx writes the canonical taxonomy dimensions inside tx.
func (l *Ledger) UpsertTaxonomyTx(ctx context.Context, tx *sql.Tx, t capregistry.AssetTaxonomy) error {
	if tx == nil {
		return ErrNotWired
	}
	return upsertTaxonomy(ctx, tx, t)
}

func upsertTaxonomy(ctx context.Context, e execer, t capregistry.AssetTaxonomy) error {
	if err := t.Validate(); err != nil {
		return err
	}
	result, err := e.ExecContext(ctx, `UPDATE media_assets SET namespace=?, asset_kind=?, source_type=?, semantic_role=?, updated_at=datetime('now') WHERE id=?`,
		t.Namespace, t.AssetKind, t.SourceType, t.SemanticRole, t.AssetID)
	if err != nil {
		return fmt.Errorf("upsert media taxonomy %q: %w", t.AssetID, err)
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return fmt.Errorf("upsert media taxonomy %q: asset not found", t.AssetID)
	}
	return nil
}

// AppendEventTx appends a registry event inside tx and returns its sequence.
func (l *Ledger) AppendEventTx(ctx context.Context, tx *sql.Tx, event capregistry.Event) (int64, error) {
	if tx == nil {
		return 0, ErrNotWired
	}
	return appendEvent(ctx, tx, event)
}

func appendEvent(ctx context.Context, e execer, event capregistry.Event) (int64, error) {
	if event.EventID == "" || event.EventType == "" || event.CreatedAt == "" {
		return 0, errors.New("media registry sqlite ledger: event_id, event_type and created_at are required")
	}
	res, err := e.ExecContext(ctx, `
		INSERT INTO registry_events
		(event_id, asset_id, event_type, run_id, actor, before_hash, after_hash,
		 payload_json, git_sha, app_version, created_at)
		VALUES (?, NULLIF(?, ''), ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(event_id) DO NOTHING`,
		event.EventID, event.AssetID, event.EventType, event.RunID, event.Actor,
		event.BeforeHash, event.AfterHash, defaultJSON(event.PayloadJSON), event.GitSHA,
		event.AppVersion, event.CreatedAt)
	if err != nil {
		return 0, fmt.Errorf("append registry event %q: %w", event.EventID, err)
	}
	seq, err := res.LastInsertId()
	if err != nil || seq == 0 {
		if queryErr := e.QueryRowContext(ctx, `SELECT seq FROM registry_events WHERE event_id = ?`, event.EventID).Scan(&seq); queryErr != nil {
			if err != nil {
				return 0, fmt.Errorf("read registry event sequence %q: %w", event.EventID, err)
			}
			return 0, fmt.Errorf("read existing registry event sequence %q: %w", event.EventID, queryErr)
		}
	}
	return seq, nil
}

// LinkContentTx sets media_assets.content_sha256 inside tx.
func (l *Ledger) LinkContentTx(ctx context.Context, tx *sql.Tx, assetID, contentSHA256 string) error {
	if tx == nil {
		return ErrNotWired
	}
	return linkContent(ctx, tx, assetID, contentSHA256)
}

func linkContent(ctx context.Context, e execer, assetID, contentSHA256 string) error {
	if assetID == "" || contentSHA256 == "" {
		return fmt.Errorf("%w: asset_id and content_sha256 are required", capregistry.ErrAssetSourceInvalid)
	}
	res, err := e.ExecContext(ctx,
		`UPDATE media_assets SET content_sha256 = ? WHERE id = ?`, contentSHA256, assetID)
	if err != nil {
		return fmt.Errorf("link content %q -> %q: %w", assetID, contentSHA256, err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("link content %q -> %q: asset not found", assetID, contentSHA256)
	}
	return nil
}

// RegisterSourceTx upserts a provenance record inside tx.
func (l *Ledger) RegisterSourceTx(ctx context.Context, tx *sql.Tx, src capregistry.AssetSource) error {
	if tx == nil {
		return ErrNotWired
	}
	return registerSource(ctx, tx, src)
}

func registerSource(ctx context.Context, e execer, src capregistry.AssetSource) error {
	if err := validateAssetSource(src); err != nil {
		return err
	}
	primary := 0
	if src.IsPrimary {
		primary = 1
	}
	_, err := e.ExecContext(ctx, `
		INSERT INTO media_asset_sources
		(source_id, asset_id, content_sha256, source_type, source_uri,
		 source_version, discovered_at, is_primary)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_id) DO UPDATE SET
			content_sha256 = excluded.content_sha256,
			source_version = excluded.source_version,
			discovered_at  = excluded.discovered_at,
			is_primary     = excluded.is_primary`,
		src.SourceID, src.AssetID, src.ContentSHA256, src.SourceType, src.SourceURI,
		src.SourceVersion, src.DiscoveredAt, primary)
	if err != nil {
		return fmt.Errorf("register asset source %q: %w", src.SourceID, err)
	}
	return nil
}
