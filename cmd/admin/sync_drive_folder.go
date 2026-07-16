package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

const normalClipsDriveFolderID = "1ll2RlTaAbhnaLkAjEDBg41lAXUyo-zJ2"

func runSyncDriveFolder(args []string) error {
	fs := flag.NewFlagSet("sync-drive-folder", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	folder := fs.String("folder", "", "Drive folder ID to scan recursively (defaults to config drive.normal_clips_source_folder)")
	source := fs.String("source", "youtube", "canonical source label")
	mediaType := fs.String("media-type", "video", "canonical media type")
	name := fs.String("name", "normal YouTube clips", "human-readable sync name")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()
	if strings.TrimSpace(*folder) == "" {
		*folder = strings.TrimSpace(cfg.Drive.NormalClipsSourceFolder)
	}
	if strings.TrimSpace(*folder) == "" {
		*folder = normalClipsDriveFolderID
	}
	root, _, rootCleanup, err := app.InitComposition(cfg, log)
	if err != nil {
		return fmt.Errorf("initialize composition: %w", err)
	}
	defer rootCleanup()
	if root == nil || root.Jobs == nil || root.Jobs.Service == nil {
		return fmt.Errorf("jobs service is not configured")
	}
	if root.Sync == nil || root.Sync.CatalogSync == nil {
		return fmt.Errorf("catalog sync service is not configured")
	}
	if root.Repos == nil || root.Repos.ClipsRepo == nil {
		return fmt.Errorf("clips repository is not configured")
	}

	summary, err := root.Sync.CatalogSync.SyncFolderID(
		context.Background(), strings.TrimSpace(*folder), strings.TrimSpace(*source),
		strings.TrimSpace(*name), strings.TrimSpace(*mediaType), root.Repos.ClipsRepo,
	)
	if err != nil {
		return fmt.Errorf("sync Drive folder recursively: %w", err)
	}
	if summary == nil {
		return fmt.Errorf("sync Drive folder recursively: empty summary")
	}
	log.Info("normal clips Drive sync completed",
		zap.String("folder_id", *folder), zap.String("source", *source),
		zap.Int("requested", summary.Requested), zap.Int("synced", summary.Synced),
		zap.Int("failed", summary.Failed))
	fmt.Printf("Drive folder sync completed: folder_id=%s source=%s media_type=%s requested=%d synced=%d failed=%d\n",
		*folder, *source, *mediaType, summary.Requested, summary.Synced, summary.Failed)
	if summary.Failed > 0 {
		return fmt.Errorf("Drive folder sync completed with %d failed items", summary.Failed)
	}
	return nil
}
