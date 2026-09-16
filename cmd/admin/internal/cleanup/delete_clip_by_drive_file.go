package cleanup

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
)

// runDeleteClipByDriveFile removes exactly one catalog asset, addressed by
// Drive file ID or by media asset id. It defaults to reversible Drive trash;
// --permanently is an explicit opt-in for physical Drive deletion.
//
// The two keys are not redundant. --drive-file-id is the safe, cross-checkable
// key (--expected-asset-id proves the Drive file and the named asset agree).
// --asset-id reaches the assets the other key cannot: an asset whose Drive
// identity is absent is invisible to every Drive-keyed lookup, and it is
// exactly the asset an operator most needs to retire.
func RunDeleteClipByDriveFile(args []string) error {
	fs := flag.NewFlagSet("delete-clip-by-drive-file", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	driveFileID := fs.String("drive-file-id", "", "Exact Google Drive file ID (either this or --asset-id is required)")
	assetID := fs.String("asset-id", "", "Exact media asset id (the only key that can reach an asset with NO Drive identity)")
	source := fs.String("source", "youtube", "Canonical asset source")
	expectedAssetID := fs.String("expected-asset-id", "", "Expected asset ID safety check (required with --drive-file-id)")
	permanently := fs.Bool("permanently", false, "Permanently delete from Drive instead of moving to trash")
	timeout := fs.Duration("timeout", 10*time.Minute, "Maximum time to wait for the outbox deletion chain")
	if err := fs.Parse(args); err != nil {
		return err
	}
	driveKey := strings.TrimSpace(*driveFileID)
	assetKey := strings.TrimSpace(*assetID)
	if driveKey == "" && assetKey == "" {
		return errors.New("either --drive-file-id or --asset-id is required")
	}
	if driveKey != "" && assetKey != "" {
		return errors.New("--drive-file-id and --asset-id are mutually exclusive: pass exactly one key")
	}
	if strings.TrimSpace(*source) == "" {
		return errors.New("--source cannot be empty")
	}
	if driveKey != "" && strings.TrimSpace(*expectedAssetID) == "" {
		return errors.New("--expected-asset-id is required with --drive-file-id: the safety check proves the Drive file and the named asset agree")
	}
	if *timeout <= 0 {
		return errors.New("--timeout must be positive")
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, rootCleanup, err := wiring.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()
	if root == nil || root.DB == nil || root.DB.DB == nil || root.Maint == nil || root.Maint.DeletionSvc == nil {
		return errors.New("database and canonical deletion service are required")
	}
	// The authoritative media catalog is PostgreSQL; root.DB is the SQLite
	// CONTROL plane. Planning the deletion against root.DB made this command
	// report "no asset found" for an asset that provably exists in the media
	// SSOT (observed live 2026-09-16 on yt_gT0amKtXWdU_5_15_v1, whose Drive
	// file id 1G69rV930sR5AgoeJjwOu1VC2FtCqrzNd is recorded in
	// media_assets.drive_file_id). A cleanup command that cannot see the
	// assets it exists to clean is a false negative, not a safety property.
	// NOTE: the sibling `remove-drive-folder-recursive` was RETIRED for this
	// exact defect; this command is the canonical replacement, so it must
	// read the media SSOT and fail closed when it is unavailable.
	if root.MediaPostgres == nil {
		return errors.New("media PostgreSQL SSOT is required: refusing to plan a deletion against the quarantined SQLite media catalog")
	}
	mediaDB := root.MediaPostgres
	if root.Outbox == nil || root.Outbox.EventsPool == nil {
		return errors.New("outbox events pool is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	// Start the canonical worker pool so Drive and Qdrant deletion handlers
	// consume the event emitted by DeletionService. No direct store mutation
	// is performed by this command.
	go root.Outbox.EventsPool.Start(ctx, 1)
	defer func() { _ = root.Outbox.EventsPool.Stop(15 * time.Second) }()

	var assetBefore *driveAssetRow
	if assetKey != "" {
		// Asset-id planning. An asset with NO Drive identity (the exact shape
		// a mis-resolved destination produces) is unreachable by
		// --drive-file-id, so the canonical cleanup could not retire it at
		// all. DeleteAsset is the asset-centric dispatcher entry point and
		// does not require a Drive locator.
		assetBefore, err = findAssetByID(ctx, mediaDB, assetKey, *source)
		if err != nil {
			return err
		}
		if assetBefore == nil {
			return fmt.Errorf("no asset found with id %s (source %s)", assetKey, *source)
		}
	} else {
		assetBefore, err = findAssetByDriveFileID(ctx, mediaDB, driveKey, *source)
		if err != nil {
			return err
		}
		if assetBefore == nil {
			return fmt.Errorf("no asset found for Drive file %s", driveKey)
		}
		if *expectedAssetID != "" && assetBefore.id != *expectedAssetID {
			return fmt.Errorf("safety check failed: Drive file %s belongs to asset %s, expected %s", driveKey, assetBefore.id, *expectedAssetID)
		}
	}

	if assetKey != "" {
		if err := root.Maint.DeletionSvc.DeleteAsset(ctx, assetBefore.id, *permanently); err != nil {
			return fmt.Errorf("enqueue deletion for asset %s: %w", assetBefore.id, err)
		}
	} else if err := root.Maint.DeletionSvc.DeleteByDriveFile(ctx, driveKey, *source, *permanently); err != nil {
		return fmt.Errorf("enqueue deletion for %s: %w", driveKey, err)
	}

	if err := cli.WaitForAssetDeletion(ctx, mediaDB, assetBefore.id); err != nil {
		return err
	}
	mode := "trashed"
	if *permanently {
		mode = "permanently deleted"
	}
	fmt.Printf("Clip deletion completed: asset=%s key=%s mode=%s\n", assetBefore.id, firstNonEmptyKey(driveKey, assetKey), mode)
	return nil
}

// firstNonEmptyKey reports which key the run was planned with, for the
// operator-facing completion line.
func firstNonEmptyKey(keys ...string) string {
	for _, key := range keys {
		if key != "" {
			return key
		}
	}
	return ""
}

func findAssetByID(ctx context.Context, db *sql.DB, assetID, source string) (*driveAssetRow, error) {
	var row driveAssetRow
	err := db.QueryRowContext(ctx, `
		SELECT id, COALESCE(lifecycle_state, ''), COALESCE(index_state, '')
		FROM media_assets
		WHERE id = $1 AND source = $2
		ORDER BY id
		LIMIT 1`, assetID, source).Scan(&row.id, &row.lifecycleState, &row.indexState)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("look up asset %s: %w", assetID, err)
	}
	return &row, nil
}

type driveAssetRow struct {
	id             string
	lifecycleState string
	indexState     string
}

func findAssetByDriveFileID(ctx context.Context, db *sql.DB, driveFileID, source string) (*driveAssetRow, error) {
	var row driveAssetRow
	err := db.QueryRowContext(ctx, `
		SELECT id, COALESCE(lifecycle_state, ''), COALESCE(index_state, '')
		FROM media_assets
		WHERE drive_file_id = $1 AND source = $2
		ORDER BY id
		LIMIT 1`, driveFileID, source).Scan(&row.id, &row.lifecycleState, &row.indexState)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("look up Drive file %s: %w", driveFileID, err)
	}
	return &row, nil
}

func WaitForAssetDeletion(ctx context.Context, db *sql.DB, assetID string) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		var lifecycleState, indexState string
		err := db.QueryRowContext(ctx, `
			SELECT COALESCE(lifecycle_state, ''), COALESCE(index_state, '')
			FROM media_assets WHERE id = $1`, assetID).Scan(&lifecycleState, &indexState)
		if errors.Is(err, sql.ErrNoRows) {
			if err := verifyDeletionEvents(ctx, db, assetID); err != nil {
				return err
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("poll deletion state for %s: %w", assetID, err)
		}
		if lifecycleState == "DRIVE_DELETED" || lifecycleState == "DELETED" {
			if err := verifyDeletionEvents(ctx, db, assetID); err == nil {
				return nil
			} else if !strings.Contains(err.Error(), "row disappeared before both deletion events completed") {
				return err
			}
		}
		if err := verifyDeletionEventFailures(ctx, db, assetID); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for deletion of %s: lifecycle_state=%s index_state=%s: %w", assetID, lifecycleState, indexState, ctx.Err())
		case <-ticker.C:
		}
	}
}

func verifyDeletionEventFailures(ctx context.Context, db *sql.DB, assetID string) error {
	rows, err := db.QueryContext(ctx, `
		SELECT event_type, COALESCE(last_error, '')
		FROM outbox_events
		WHERE aggregate_id = $1
		  AND event_type IN ('asset.drive.delete_requested', 'asset.index.delete_requested')
		  AND status = 'dead_letter'`, assetID)
	if err != nil {
		return fmt.Errorf("read deletion failures for %s: %w", assetID, err)
	}
	defer rows.Close()
	if rows.Next() {
		var eventType, lastError string
		if err := rows.Scan(&eventType, &lastError); err != nil {
			return fmt.Errorf("scan deletion failure for %s: %w", assetID, err)
		}
		return fmt.Errorf("deletion event %s for %s dead-lettered: %s", eventType, assetID, lastError)
	}
	return rows.Err()
}

func verifyDeletionEvents(ctx context.Context, db *sql.DB, assetID string) error {
	rows, err := db.QueryContext(ctx, `
		SELECT event_type, status, COALESCE(last_error, '')
		FROM outbox_events
		WHERE aggregate_id = $1
		  AND event_type IN ('asset.drive.delete_requested', 'asset.index.delete_requested')
		ORDER BY id`, assetID)
	if err != nil {
		return fmt.Errorf("read deletion events for %s: %w", assetID, err)
	}
	defer rows.Close()

	completed := map[string]bool{}
	for rows.Next() {
		var eventType, status, lastError string
		if err := rows.Scan(&eventType, &status, &lastError); err != nil {
			return fmt.Errorf("scan deletion event for %s: %w", assetID, err)
		}
		if status == "dead_letter" {
			return fmt.Errorf("deletion event %s for %s dead-lettered: %s", eventType, assetID, lastError)
		}
		if status == "completed" {
			completed[eventType] = true
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read deletion events for %s: %w", assetID, err)
	}
	if !completed["asset.drive.delete_requested"] || !completed["asset.index.delete_requested"] {
		return fmt.Errorf("asset %s row disappeared before both deletion events completed", assetID)
	}
	return nil
}
