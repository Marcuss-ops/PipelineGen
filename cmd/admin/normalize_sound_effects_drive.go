package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/application/adminmedia"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/rustexec"
)

// runNormalizeSoundEffectsDrive parses the optional root folder and delegates
// traversal, media policy, and publication to the application use case.
func runNormalizeSoundEffectsDrive(args []string) error {
	rootFolder := soundEffectsDriveFolderID
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		rootFolder = strings.TrimSpace(args[0])
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
	if root == nil || root.Drive == nil || root.Drive.Reader == nil || root.Drive.Publisher == nil {
		return fmt.Errorf("Drive reader and publisher are required")
	}
	uploader, err := delivery.NewAdminUploadService(root.Drive.Publisher)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Hour)
	defer cancel()
	report, err := adminmedia.NormalizeDriveSoundEffects(ctx, rootFolder, 2, drive.AdminMediaReader{Reader: root.Drive.Reader}, rustexec.NewAdminMediaProcessor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, cfg.Video.WithDefaults().EncoderPolicy(), log), uploader)
	if err != nil {
		return err
	}
	for _, update := range report.Updates {
		fmt.Printf("fixed %.3fs -> %.3fs: %s\n", update.Before.Seconds(), update.After.Seconds(), update.Filename)
	}
	fmt.Printf("Remote SFX checked=%d fixed=%d max_seconds=2.00\n", report.Checked, report.Changed)
	return nil
}
