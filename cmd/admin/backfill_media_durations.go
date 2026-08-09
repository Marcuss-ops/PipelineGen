package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/rustexec"
)

// runBackfillMediaDurations measures existing Drive-backed video assets with
// ffprobe and republishes each changed asset through the canonical outbox.
// It intentionally does not leave a local copy behind: the catalog remains
// Drive-backed while duration and measured dimensions become durable metadata.
func runBackfillMediaDurations(args []string) error {
	fs := flag.NewFlagSet("backfill-media-durations", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	folderID := fs.String("folder-id", "", "Limit to assets in this Drive folder or its immediate catalog path")
	assetIDs := fs.String("asset-ids", "", "Comma-separated asset IDs to process (optional)")
	limit := fs.Int("limit", 0, "Maximum number of assets to process; zero means all")
	force := fs.Bool("force", false, "Re-probe selected assets even when duration_ms is already positive")
	retainDir := fs.String("retain-dir", "", "Persist downloaded probe files under this directory for physical render verification")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*folderID) == "" && strings.TrimSpace(*assetIDs) == "" {
		return fmt.Errorf("one of --folder-id or --asset-ids is required")
	}
	if *limit < 0 {
		return fmt.Errorf("--limit must be non-negative")
	}
	if strings.TrimSpace(*retainDir) != "" {
		if err := os.MkdirAll(*retainDir, 0o755); err != nil {
			return fmt.Errorf("create retain directory: %w", err)
		}
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()
	if root == nil || root.DB == nil || root.Drive == nil || root.Drive.Reader == nil ||
		root.Repos == nil || root.Repos.ClipsRepo == nil || root.Outbox == nil ||
		root.Outbox.Dispatcher == nil || root.Outbox.EventsPool == nil || root.Outbox.EventsRepo == nil {
		return fmt.Errorf("database, Drive reader, clips repository and outbox are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	rows, err := selectDurationBackfillRows(ctx, root.DB, *folderID, *assetIDs, *limit, *force)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("No ACTIVE/INDEXED Drive-backed video assets require duration backfill")
		return nil
	}

	deadLettersBefore, err := root.Outbox.EventsRepo.CountByEventTypeAndStatus(ctx, "asset.index.requested", "dead_letter")
	if err != nil {
		return fmt.Errorf("read outbox baseline: %w", err)
	}
	go root.Outbox.EventsPool.Start(ctx, 1)
	defer func() { _ = root.Outbox.EventsPool.Stop(15 * time.Second) }()

	probe := rustexec.NewVideoProcessor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, log)
	var failed int
	for _, row := range rows {
		if err := backfillOneMediaDuration(ctx, root, probe, &row, *retainDir); err != nil {
			failed++
			fmt.Printf("FAILED id=%s: %v\n", row.ID, err)
			continue
		}
		fmt.Printf("MEASURED id=%s duration_ms=%d width=%d height=%d\n", row.ID, row.DurationMS, row.Width, row.Height)
	}
	if err := waitForAssetIndexOutbox(ctx, root, deadLettersBefore); err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("duration backfill failed for %d of %d assets", failed, len(rows))
	}
	fmt.Printf("PASS: duration backfill completed assets=%d\n", len(rows))
	return nil
}

type durationBackfillRow struct {
	ID          string
	DriveFileID string
	DurationMS  int64
	Width       int
	Height      int
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func selectDurationBackfillRows(ctx context.Context, db queryer, folderID, rawIDs string, limit int, force bool) ([]durationBackfillRow, error) {
	where := []string{
		"media_type IN ('video', 'clip')",
		"UPPER(COALESCE(lifecycle_state, '')) = 'ACTIVE'",
		"UPPER(COALESCE(index_state, '')) = 'INDEXED'",
		"TRIM(COALESCE(drive_file_id, '')) <> ''",
	}
	if !force {
		where = append(where, "COALESCE(duration_ms, 0) <= 0")
	}
	args := make([]any, 0)
	if ids := splitBackfillCSV(rawIDs); len(ids) > 0 {
		marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		where = append(where, "id IN ("+marks+")")
		for _, id := range ids {
			args = append(args, id)
		}
	}
	if folderID = strings.TrimSpace(folderID); folderID != "" {
		where = append(where, "(parent_folder_id = ? OR drive_folder_id = ? OR folder_id = ?)")
		args = append(args, folderID, folderID, folderID)
	}
	query := "SELECT id, drive_file_id, COALESCE(duration_ms, 0), COALESCE(width, 0), COALESCE(height, 0) FROM media_assets WHERE " + strings.Join(where, " AND ") + " ORDER BY id"
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("select duration backfill assets: %w", err)
	}
	defer rows.Close()
	var result []durationBackfillRow
	for rows.Next() {
		var row durationBackfillRow
		if err := rows.Scan(&row.ID, &row.DriveFileID, &row.DurationMS, &row.Width, &row.Height); err != nil {
			return nil, fmt.Errorf("scan duration backfill asset: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate duration backfill assets: %w", err)
	}
	return result, nil
}

func backfillOneMediaDuration(ctx context.Context, root *wiring.ComposeRoot, probe *rustexec.VideoProcessor, row *durationBackfillRow, retainDir string) error {
	reader, _, err := root.Drive.Reader.DownloadFile(ctx, row.DriveFileID)
	if err != nil {
		return fmt.Errorf("download Drive file: %w", err)
	}
	defer reader.Close()
	tmpPath := ""
	if strings.TrimSpace(retainDir) != "" {
		tmpPath = filepath.Join(retainDir, row.ID+".mp4")
	} else {
		tmp, createErr := os.CreateTemp("", "pipelinegen-duration-*.media")
		if createErr != nil {
			return fmt.Errorf("create probe temp file: %w", createErr)
		}
		tmpPath = tmp.Name()
		_ = tmp.Close()
		defer os.Remove(tmpPath)
	}
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("open probe file: %w", err)
	}
	if _, err := io.Copy(tmp, reader); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("download Drive file bytes: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close probe temp file: %w", err)
	}
	info, err := probe.Probe(ctx, tmpPath)
	if err != nil {
		return fmt.Errorf("ffprobe: %w", err)
	}
	if info == nil || !info.HasVideo || info.Duration <= 0 || info.Width <= 0 || info.Height <= 0 {
		return fmt.Errorf("invalid video probe result duration=%s width=%d height=%d has_video=%t", info.Duration, info.Width, info.Height, info.HasVideo)
	}

	clip, err := root.Repos.ClipsRepo.GetClip(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("load asset: %w", err)
	}
	if clip == nil {
		return fmt.Errorf("asset disappeared from repository")
	}
	clip.Duration = info.Duration
	if strings.TrimSpace(retainDir) != "" {
		clip.SetLocalPath(tmpPath)
	}
	clip.SetMetadataString("duration_source", "ffprobe_backfill")
	clip.SetMetadataInt("width", info.Width)
	clip.SetMetadataInt("height", info.Height)
	if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, clip.FileHash()); err != nil {
		return fmt.Errorf("persist and index measured asset: %w", err)
	}
	row.DurationMS = info.Duration.Milliseconds()
	row.Width = info.Width
	row.Height = info.Height
	return nil
}

func splitBackfillCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}
	return result
}
