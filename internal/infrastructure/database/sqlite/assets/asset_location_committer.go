package assets

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
	"go.uber.org/zap"
)

// SQLiteAssetLocationCommitter persists verified Drive locations and emits
// the corresponding Qdrant index request in one SQLite transaction.
//
// The adapter intentionally owns only the narrow location-reconciliation
// update. Full asset creation remains owned by SQLiteAssetCommitter.
type SQLiteAssetLocationCommitter struct {
	db  *sql.DB
	box *outboxevents.Repository
	log *zap.Logger
}

// NewSQLiteAssetLocationCommitter constructs the durable reconciliation
// adapter. Both dependencies are required so a composition gap fails at
// startup rather than silently dropping the Qdrant projection update.
func NewSQLiteAssetLocationCommitter(db *sql.DB, box *outboxevents.Repository, log *zap.Logger) *SQLiteAssetLocationCommitter {
	if db == nil {
		panic("assets.NewSQLiteAssetLocationCommitter: db is required")
	}
	if box == nil {
		panic("assets.NewSQLiteAssetLocationCommitter: outboxevents.Repository is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &SQLiteAssetLocationCommitter{db: db, box: box, log: log}
}

var _ scriptpkg.AssetLocationCommitter = (*SQLiteAssetLocationCommitter)(nil)

// CommitAssetLocations updates every existing media_assets row and emits
// one idempotent index-request event per asset. Missing rows are skipped:
// an ORPHAN_DRIVE_FILE has no authoritative SQLite row to mutate.
func (c *SQLiteAssetLocationCommitter) CommitAssetLocations(ctx context.Context, changes []scriptpkg.AssetLocationChange) error {
	if c == nil || c.db == nil || c.box == nil {
		return fmt.Errorf("asset location committer: adapter is not wired")
	}
	if len(changes) == 0 {
		return nil
	}

	normalized, err := normalizeAssetLocationChanges(changes)
	if err != nil {
		return err
	}

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("asset location committer: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, change := range normalized {
		var source, mediaType, sourceVersion, fileHash, existingFileID, existingLink string
		err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(source, ''), COALESCE(media_type, ''),
			       COALESCE(source_version, ''), COALESCE(file_hash, ''),
			       COALESCE(drive_file_id, ''), COALESCE(drive_link, '')
			FROM media_assets WHERE id = ?`, change.AssetID).
			Scan(&source, &mediaType, &sourceVersion, &fileHash, &existingFileID, &existingLink)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return fmt.Errorf("asset location committer: read asset %q: %w", change.AssetID, err)
		}
		if sourceVersion == "" {
			sourceVersion = fileHash
		}
		if sourceVersion == "" {
			return fmt.Errorf("asset location committer: asset %q has no source_version or file_hash", change.AssetID)
		}

		var existingLocationFileID, existingLocationLink string
		locationErr := tx.QueryRowContext(ctx, `
			SELECT COALESCE(external_id, ''), COALESCE(web_view_link, '')
			FROM asset_locations
			WHERE asset_id = ? AND location_kind = 'drive'`, change.AssetID).
			Scan(&existingLocationFileID, &existingLocationLink)
		if locationErr != nil && locationErr != sql.ErrNoRows {
			return fmt.Errorf("asset location committer: read drive location %q: %w", change.AssetID, locationErr)
		}

		// An empty DriveFileID means "preserve the known identity" rather
		// than delete it. Prefer the asset row, then the durable location
		// projection, so clearing a link never discards diagnostics.
		persistedChange := change
		if persistedChange.DriveFileID == "" {
			persistedChange.DriveFileID = existingFileID
			if persistedChange.DriveFileID == "" && locationErr == nil {
				persistedChange.DriveFileID = existingLocationFileID
			}
		}
		if strings.TrimSpace(persistedChange.DriveLink) != "" && strings.TrimSpace(persistedChange.DriveFileID) == "" {
			return fmt.Errorf("asset location committer: asset %q has a Drive link without a Drive file ID", change.AssetID)
		}

		locationMatches := locationErr == nil &&
			existingLocationFileID == persistedChange.DriveFileID && existingLocationLink == persistedChange.DriveLink
		if existingFileID == persistedChange.DriveFileID && existingLink == persistedChange.DriveLink && locationMatches {
			continue
		}

		result, err := tx.ExecContext(ctx, `
			UPDATE media_assets
			SET drive_file_id = ?, drive_link = ?, updated_at = ?
			WHERE id = ?`,
			persistedChange.DriveFileID, persistedChange.DriveLink, now, persistedChange.AssetID)
		if err != nil {
			return fmt.Errorf("asset location committer: update asset %q: %w", change.AssetID, err)
		}
		if affected, err := result.RowsAffected(); err != nil {
			return fmt.Errorf("asset location committer: inspect update %q: %w", change.AssetID, err)
		} else if affected == 0 {
			return fmt.Errorf("asset location committer: asset %q disappeared during update", change.AssetID)
		}

		if err := c.upsertDriveLocation(ctx, tx, persistedChange, now); err != nil {
			return fmt.Errorf("asset location committer: update drive location %q: %w", change.AssetID, err)
		}

		if _, err := CommitIndexRequestTx(ctx, tx, c.box, IndexRequest{
			AssetID:        persistedChange.AssetID,
			Source:         source,
			MediaType:      mediaType,
			SourceVersion:  sourceVersion,
			RequestedAt:    time.Now().UTC(),
			EventKeySuffix: locationEventKeySuffix(persistedChange),
		}); err != nil {
			return fmt.Errorf("asset location committer: enqueue Qdrant index request for %q: %w", change.AssetID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("asset location committer: commit tx: %w", err)
	}
	committed = true
	if c.log != nil {
		c.log.Debug("asset locations committed with Qdrant index requests",
			zap.Int("requested_changes", len(normalized)),
		)
	}
	return nil
}

func (c *SQLiteAssetLocationCommitter) upsertDriveLocation(ctx context.Context, tx *sql.Tx, change scriptpkg.AssetLocationChange, now string) error {
	// An empty DriveLink is a durable, non-publishable location state.
	// Keep the row and its external_id for diagnosis/republication.
	primary, err := c.driveLocationPrimary(ctx, tx, change.AssetID)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO asset_locations
			(asset_id, location_kind, uri, external_id, web_view_link, download_url,
			 mime_type, file_size_bytes, file_hash, is_primary, created_at, updated_at)
		VALUES (?, 'drive', ?, ?, ?, '', '', 0, '', ?, ?, ?)
		ON CONFLICT(asset_id, location_kind) DO UPDATE SET
			uri = excluded.uri,
			external_id = excluded.external_id,
			web_view_link = excluded.web_view_link,
			updated_at = excluded.updated_at`,
		change.AssetID, change.DriveLink, change.DriveFileID, change.DriveLink, locationBoolToInt(primary), now, now)
	return err
}

// driveLocationPrimary preserves an existing Drive primary flag. When a
// Drive row is created, it becomes primary only if the asset has no other
// primary location; reconciliation must not demote a local primary asset.
func (c *SQLiteAssetLocationCommitter) driveLocationPrimary(ctx context.Context, tx *sql.Tx, assetID string) (bool, error) {
	var existing int
	err := tx.QueryRowContext(ctx, `
		SELECT is_primary FROM asset_locations
		WHERE asset_id = ? AND location_kind = 'drive'`, assetID).Scan(&existing)
	if err == nil {
		return existing != 0, nil
	}
	if err != sql.ErrNoRows {
		return false, err
	}
	var primaryCount int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM asset_locations WHERE asset_id = ? AND is_primary = 1`, assetID).
		Scan(&primaryCount); err != nil {
		return false, err
	}
	return primaryCount == 0, nil
}

func locationBoolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func locationEventKeySuffix(change scriptpkg.AssetLocationChange) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(change.DriveFileID) + "|" + strings.TrimSpace(change.DriveLink)))
	return ":location:" + hex.EncodeToString(sum[:])[:16]
}

func normalizeAssetLocationChanges(changes []scriptpkg.AssetLocationChange) ([]scriptpkg.AssetLocationChange, error) {
	out := make([]scriptpkg.AssetLocationChange, 0, len(changes))
	seen := make(map[string]scriptpkg.AssetLocationChange, len(changes))
	for _, change := range changes {
		change.AssetID = strings.TrimSpace(change.AssetID)
		change.DriveFileID = strings.TrimSpace(change.DriveFileID)
		change.DriveLink = strings.TrimSpace(change.DriveLink)
		if change.AssetID == "" {
			return nil, fmt.Errorf("asset location committer: asset_id is required")
		}
		if previous, ok := seen[change.AssetID]; ok {
			if previous != change {
				return nil, fmt.Errorf("asset location committer: conflicting changes for asset %q", change.AssetID)
			}
			continue
		}
		seen[change.AssetID] = change
		out = append(out, change)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AssetID < out[j].AssetID })
	return out, nil
}
