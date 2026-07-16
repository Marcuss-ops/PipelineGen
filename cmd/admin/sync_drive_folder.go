package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	appjob "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
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
	if strings.TrimSpace(*folder) == "" {
		return fmt.Errorf("folder is required")
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

	job, err := root.Jobs.Service.Enqueue(context.Background(), &appjob.EnqueueRequest{
		Type: "drive.folder.sync", MaxRetries: 2,
		Payload: map[string]any{"drive_folder_id": strings.TrimSpace(*folder), "source": strings.TrimSpace(*source), "name": strings.TrimSpace(*name), "media_type": strings.TrimSpace(*mediaType)},
	})
	if err != nil {
		return fmt.Errorf("enqueue drive folder sync: %w", err)
	}
	if job == nil || job.ID == "" {
		return fmt.Errorf("enqueue drive folder sync: empty job id")
	}
	log.Info("normal clips Drive sync enqueued", zap.String("job_id", job.ID), zap.String("folder_id", *folder), zap.String("source", *source))
	fmt.Printf("Drive folder sync enqueued: job_id=%s folder_id=%s source=%s media_type=%s\n", job.ID, *folder, *source, *mediaType)
	return nil
}
