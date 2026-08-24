package drive

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
)

const normalClipsDriveFolderID = "1ll2RlTaAbhnaLkAjEDBg41lAXUyo-zJ2"

func RunSyncDriveFolder(args []string) error {
	fs := flag.NewFlagSet("sync-drive-folder", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	folder := fs.String("folder", "", "Drive folder ID to scan recursively (defaults to config drive.normal_clips_source_folder)")
	source := fs.String("source", "youtube", "canonical source label")
	mediaType := fs.String("media-type", "video", "canonical media type")
	name := fs.String("name", "normal YouTube clips", "human-readable sync name")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, log, cleanup, err := cli.AppLogger()
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
	root, _, rootCleanup, err := wiring.InitComposition(cfg, log)
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
	if root.Outbox == nil || root.Outbox.EventsPool == nil || root.Outbox.EventsRepo == nil {
		return fmt.Errorf("outbox events pool is not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	deadLettersBefore, err := root.Outbox.EventsRepo.CountByEventTypeAndStatus(ctx, "asset.index.requested", "dead_letter")
	if err != nil {
		return fmt.Errorf("read outbox dead-letter baseline: %w", err)
	}
	// Pool.Start owns the worker lifecycle and blocks until ctx is
	// cancelled. Run it asynchronously so the folder sync can enqueue
	// indexing events while the pool processes them.
	go root.Outbox.EventsPool.Start(ctx, 1)
	defer func() { _ = root.Outbox.EventsPool.Stop(15 * time.Second) }()

	summary, err := root.Sync.CatalogSync.SyncFolderID(
		ctx, strings.TrimSpace(*folder), strings.TrimSpace(*source),
		strings.TrimSpace(*name), strings.TrimSpace(*mediaType), root.Repos.ClipsRepo, root.Repos.ClipsRepo,
	)
	if err != nil {
		return fmt.Errorf("sync Drive folder recursively: %w", err)
	}
	if summary == nil {
		return fmt.Errorf("sync Drive folder recursively: empty summary")
	}
	if err := cli.WaitForAssetIndexOutbox(ctx, root, deadLettersBefore); err != nil {
		return err
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

func WaitForAssetIndexOutbox(ctx context.Context, root *wiring.ComposeRoot, deadLettersBefore int64) error {
	for {
		pending, err := root.Outbox.EventsRepo.CountByEventTypeAndStatus(ctx, "asset.index.requested", "pending")
		if err != nil {
			return fmt.Errorf("read pending asset index events: %w", err)
		}
		processing, err := root.Outbox.EventsRepo.CountByEventTypeAndStatus(ctx, "asset.index.requested", "processing")
		if err != nil {
			return fmt.Errorf("read processing asset index events: %w", err)
		}
		deadLetters, err := root.Outbox.EventsRepo.CountByEventTypeAndStatus(ctx, "asset.index.requested", "dead_letter")
		if err != nil {
			return fmt.Errorf("read asset index dead letters: %w", err)
		}
		if deadLetters > deadLettersBefore {
			return fmt.Errorf("Qdrant indexing failed: %d asset.index.requested events moved to dead-letter", deadLetters-deadLettersBefore)
		}
		if pending == 0 && processing == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for Qdrant indexing: pending=%d processing=%d", pending, processing)
		case <-time.After(250 * time.Millisecond):
		}
	}
}
