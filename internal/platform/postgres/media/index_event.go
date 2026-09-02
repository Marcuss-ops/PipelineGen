// Package media — index_event.go: the tx-bound index-request emission for
// callers that already own the asset transaction.
//
// Mirrors the SQLite AssetIndexEventCommitter implementation: the outbox
// write has the same canonical owner as the asset row, it is merely exposed
// as a separate narrow interface so producer fakes do not gain a second
// method.
package media

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

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
			`SELECT COALESCE(NULLIF(source_version,''), NULLIF(legacy_file_md5,'')) FROM media_assets WHERE id = $1`,
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
