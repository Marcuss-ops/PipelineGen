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

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

// runDeleteClipByDriveFile removes exactly one catalog asset identified by
// Drive file ID. It defaults to reversible Drive trash; --permanently is an
// explicit opt-in for physical Drive deletion.
func RunDeleteClipByDriveFile(args []string) error {
	fs := flag.NewFlagSet("delete-clip-by-drive-file", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	driveFileID := fs.String("drive-file-id", "", "Exact Google Drive file ID (required)")
	source := fs.String("source", "youtube", "Canonical asset source")
	expectedAssetID := fs.String("expected-asset-id", "", "Expected asset ID safety check (required)")
	permanently := fs.Bool("permanently", false, "Permanently delete from Drive instead of moving to trash")
	timeout := fs.Duration("timeout", 10*time.Minute, "Maximum time to wait for the outbox deletion chain")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*driveFileID) == "" {
		return errors.New("--drive-file-id is required")
	}
	if strings.TrimSpace(*source) == "" {
		return errors.New("--source cannot be empty")
	}
	if strings.TrimSpace(*expectedAssetID) == "" {
		return errors.New("--expected-asset-id is required for this destructive operation")
	}
	if *timeout <= 0 {
		return errors.New("--timeout must be positive")
	}

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()
	if root == nil || root.DB == nil || root.DB.DB == nil || root.Maint == nil || root.Maint.DeletionSvc == nil {
		return errors.New("database and canonical deletion service are required")
	}
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

	assetBefore, err := findAssetByDriveFileID(ctx, root.DB.DB, *driveFileID, *source)
	if err != nil {
		return err
	}
	if assetBefore == nil {
		return fmt.Errorf("no asset found for Drive file %s", *driveFileID)
	}
	if *expectedAssetID != "" && assetBefore.id != *expectedAssetID {
		return fmt.Errorf("safety check failed: Drive file %s belongs to asset %s, expected %s", *driveFileID, assetBefore.id, *expectedAssetID)
	}

	if err := root.Maint.DeletionSvc.DeleteByDriveFile(ctx, *driveFileID, *source, *permanently); err != nil {
		return fmt.Errorf("enqueue deletion for %s: %w", *driveFileID, err)
	}

	if err := waitForAssetDeletion(ctx, root.DB.DB, assetBefore.id); err != nil {
		return err
	}
	mode := "trashed"
	if *permanently {
		mode = "permanently deleted"
	}
	fmt.Printf("Clip deletion completed: asset=%s drive_file_id=%s mode=%s\n", assetBefore.id, *driveFileID, mode)
	return nil
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
		WHERE drive_file_id = ? AND source = ?
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

func waitForAssetDeletion(ctx context.Context, db *sql.DB, assetID string) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		var lifecycleState, indexState string
		err := db.QueryRowContext(ctx, `
			SELECT COALESCE(lifecycle_state, ''), COALESCE(index_state, '')
			FROM media_assets WHERE id = ?`, assetID).Scan(&lifecycleState, &indexState)
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
		WHERE aggregate_id = ?
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
		WHERE aggregate_id = ?
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
