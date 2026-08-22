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
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
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

// AssetLocationCommitResult describes the durable work observed by a
// reconciliation commit. EventsInserted counts newly inserted index requests;
// an idempotent replay that finds the same location returns zero.
type AssetLocationCommitResult struct {
	EventsInserted int
	RowsUpdated    int
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
// one idempotent index-request event per asset. Missing rows fail closed:
// an ORPHAN_DRIVE_FILE must be handled before reaching this durable port,
// because it has no authoritative SQLite row to mutate.
func (c *SQLiteAssetLocationCommitter) CommitAssetLocations(ctx context.Context, changes []scriptpkg.AssetLocationChange) error {
	_, err := c.CommitAssetLocationsWithResult(ctx, changes)
	return err
}

// CommitAssetLocationsWithResult preserves the narrow committer interface
// while exposing the actual number of newly inserted outbox events to admin
// audit callers. The underlying location update remains one transaction.
func (c *SQLiteAssetLocationCommitter) CommitAssetLocationsWithResult(ctx context.Context, changes []scriptpkg.AssetLocationChange) (AssetLocationCommitResult, error) {
	if c == nil || c.db == nil || c.box == nil {
		return AssetLocationCommitResult{}, fmt.Errorf("asset location committer: adapter is not wired")
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return AssetLocationCommitResult{}, fmt.Errorf("asset location committer: begin tx: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	result, err := c.CommitAssetLocationsTx(ctx, tx, changes)
	if err != nil {
		return AssetLocationCommitResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return AssetLocationCommitResult{}, fmt.Errorf("asset location committer: commit tx: %w", err)
	}
	committed = true
	return result, nil
}

// CommitAssetLocationsTx applies the location changes and outbox writes to a
// caller-owned transaction. It is used by the repair command to keep the job
// result and the canonical asset projection atomic with the outbox.
func (c *SQLiteAssetLocationCommitter) CommitAssetLocationsTx(ctx context.Context, tx *sql.Tx, changes []scriptpkg.AssetLocationChange) (AssetLocationCommitResult, error) {
	if c == nil || c.db == nil || c.box == nil || tx == nil {
		return AssetLocationCommitResult{}, fmt.Errorf("asset location committer: adapter or transaction is not wired")
	}
	normalized, err := normalizeAssetLocationChanges(changes)
	if err != nil {
		return AssetLocationCommitResult{}, err
	}
	return c.commitAssetLocationsTx(ctx, tx, normalized)
}

func (c *SQLiteAssetLocationCommitter) commitAssetLocationsTx(ctx context.Context, tx *sql.Tx, normalized []scriptpkg.AssetLocationChange) (AssetLocationCommitResult, error) {
	if len(normalized) == 0 {
		return AssetLocationCommitResult{}, nil
	}
	if c == nil || c.db == nil || c.box == nil || tx == nil {
		return AssetLocationCommitResult{}, fmt.Errorf("asset location committer: adapter or transaction is not wired")
	}
	commitResult := AssetLocationCommitResult{}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, change := range normalized {
		var source, mediaType, sourceVersion, fileHash, existingFileID, existingLink, lifecycleState string
		err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(source, ''), COALESCE(media_type, ''),
			       COALESCE(source_version, ''), COALESCE(legacy_file_md5, ''),
			       COALESCE(drive_file_id, ''), COALESCE(drive_link, ''),
			       COALESCE(lifecycle_state, 'ACTIVE')
			FROM media_assets WHERE id = ?`, change.AssetID).
			Scan(&source, &mediaType, &sourceVersion, &fileHash, &existingFileID, &existingLink, &lifecycleState)
		if err == sql.ErrNoRows {
			return AssetLocationCommitResult{}, fmt.Errorf("asset location committer: asset %q does not exist in media_assets", change.AssetID)
		}
		if err != nil {
			return AssetLocationCommitResult{}, fmt.Errorf("asset location committer: read asset %q: %w", change.AssetID, err)
		}
		if sourceVersion == "" {
			sourceVersion = fileHash
		}
		if sourceVersion == "" {
			return AssetLocationCommitResult{}, fmt.Errorf("asset location committer: asset %q has no source_version or legacy_file_md5", change.AssetID)
		}

		var existingLocationFileID, existingLocationLink string
		locationErr := tx.QueryRowContext(ctx, `
			SELECT COALESCE(external_id, ''), COALESCE(web_view_link, '')
			FROM asset_locations
			WHERE asset_id = ? AND location_kind = 'drive'`, change.AssetID).
			Scan(&existingLocationFileID, &existingLocationLink)
		if locationErr != nil && locationErr != sql.ErrNoRows {
			return AssetLocationCommitResult{}, fmt.Errorf("asset location committer: read drive location %q: %w", change.AssetID, locationErr)
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
			return AssetLocationCommitResult{}, fmt.Errorf("asset location committer: asset %q has a Drive link without a Drive file ID", change.AssetID)
		}

		// A cleared Drive location must not leave an otherwise searchable
		// asset ACTIVE when no alternate location remains. Preserve the
		// SQLite row and Drive identity for diagnosis, but invalidate the
		// searchable projection atomically with the location and outbox
		// write. Other usable locations (for example a local primary)
		// keep the asset searchable.
		nextLifecycleState := lifecycleState
		if strings.TrimSpace(persistedChange.DriveLink) == "" {
			hasAlternate, err := hasOtherUsableLocation(ctx, tx, persistedChange.AssetID)
			if err != nil {
				return AssetLocationCommitResult{}, fmt.Errorf("asset location committer: inspect alternate locations %q: %w", change.AssetID, err)
			}
			if !hasAlternate &&
				(lifecycleState == "" || lifecycleState == string(asset.StateActive) || lifecycleState == string(asset.StatePublished)) {
				nextLifecycleState = string(asset.StateError)
			}
		}
		locationMatches := locationErr == nil &&
			existingLocationFileID == persistedChange.DriveFileID && existingLocationLink == persistedChange.DriveLink
		if existingFileID == persistedChange.DriveFileID && existingLink == persistedChange.DriveLink &&
			locationMatches && lifecycleState == nextLifecycleState {
			continue
		}

		if err := c.upsertDriveLocation(ctx, tx, persistedChange, now); err != nil {
			return AssetLocationCommitResult{}, fmt.Errorf("asset location committer: update drive location %q: %w", change.AssetID, err)
		}

		updateResult, err := tx.ExecContext(ctx, `
			UPDATE media_assets
			SET drive_file_id = ?, drive_link = ?, lifecycle_state = ?, updated_at = ?
			WHERE id = ?`,
			persistedChange.DriveFileID, persistedChange.DriveLink, nextLifecycleState, now, persistedChange.AssetID)
		if err != nil {
			return AssetLocationCommitResult{}, fmt.Errorf("asset location committer: update asset %q: %w", change.AssetID, err)
		}
		if affected, err := updateResult.RowsAffected(); err != nil {
			return AssetLocationCommitResult{}, fmt.Errorf("asset location committer: inspect update %q: %w", change.AssetID, err)
		} else if affected == 0 {
			return AssetLocationCommitResult{}, fmt.Errorf("asset location committer: asset %q disappeared during update", change.AssetID)
		} else {
			commitResult.RowsUpdated += int(affected)
		}

		enqueueResult, err := CommitIndexRequestTx(ctx, tx, c.box, IndexRequest{
			AssetID:        persistedChange.AssetID,
			Source:         source,
			MediaType:      mediaType,
			SourceVersion:  sourceVersion,
			RequestedAt:    time.Now().UTC(),
			EventKeySuffix: locationEventKeySuffix(persistedChange),
		})
		if err != nil {
			return AssetLocationCommitResult{}, fmt.Errorf("asset location committer: enqueue Qdrant index request for %q: %w", change.AssetID, err)
		}
		if enqueueResult.Inserted {
			commitResult.EventsInserted++
		}
	}

	if c.log != nil {
		c.log.Debug("asset locations committed with Qdrant index requests",
			zap.Int("requested_changes", len(normalized)),
			zap.Int("events_inserted", commitResult.EventsInserted),
		)
	}
	return commitResult, nil
}

func hasOtherUsableLocation(ctx context.Context, tx *sql.Tx, assetID string) (bool, error) {
	var count int
	// A non-empty URI is only an identity hint, not proof that the
	// alternate location can be read. The canonical location schema has
	// no separate verification-state column, so require the durable
	// integrity evidence written by verified publishers. This keeps the
	// reconciliation fail-closed: arbitrary local paths and unverified
	// object-storage URIs cannot keep an asset searchable after Drive is
	// invalidated.
	err := tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM asset_locations
		WHERE asset_id = ? AND location_kind IN ('local', 'object_storage')
		  AND TRIM(COALESCE(uri, '')) <> ''
		  AND TRIM(COALESCE(legacy_file_md5, '')) <> ''
		  AND COALESCE(file_size_bytes, 0) > 0`, assetID).Scan(&count)
	return count > 0, err
}

func (c *SQLiteAssetLocationCommitter) upsertDriveLocation(ctx context.Context, tx *sql.Tx, change scriptpkg.AssetLocationChange, now string) error {
	// An empty DriveLink is a durable, non-publishable location state.
	// Keep the row and its external_id for diagnosis/republication.
	primary, err := c.driveLocationPrimary(ctx, tx, change.AssetID)
	if err != nil {
		return err
	}
	uri := ""
	if strings.TrimSpace(change.DriveFileID) != "" {
		uri = "drive://" + strings.TrimSpace(change.DriveFileID)
	} else {
		uri = strings.TrimSpace(change.DriveLink)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO asset_locations
			(asset_id, location_kind, uri, external_id, web_view_link, download_url,
			 mime_type, file_size_bytes, legacy_file_md5, is_primary, created_at, updated_at)
		VALUES (?, 'drive', ?, ?, ?, '', '', 0, '', ?, ?, ?)
		ON CONFLICT(asset_id, location_kind) DO UPDATE SET
			uri = excluded.uri,
			external_id = excluded.external_id,
			web_view_link = excluded.web_view_link,
			updated_at = excluded.updated_at`,
		change.AssetID, uri, change.DriveFileID, change.DriveLink, locationBoolToInt(primary), now, now)
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
