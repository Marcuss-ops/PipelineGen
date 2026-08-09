package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/application/adminmedia"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/rustexec"
)

func runRenderShort(args []string) error {
	fs := flag.NewFlagSet("render-short", flag.ContinueOnError)
	manifestPath := fs.String("manifest", "", "JSON render manifest (required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*manifestPath) == "" {
		return fmt.Errorf("--manifest is required")
	}
	data, err := os.ReadFile(*manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	var manifest adminmedia.RenderManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("decode manifest: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	cfg, log, cleanup, err := appLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	var uploader adminmedia.AdminUploader
	policy := cfg.Video.WithDefaults().EncoderPolicy()
	if manifest.Upload != nil {
		root, _, rootCleanup, err := app.InitComposition(cfg, log)
		if err != nil {
			return fmt.Errorf("initialize composition: %w", err)
		}
		defer rootCleanup()
		if root == nil || root.Drive == nil || root.Drive.Publisher == nil {
			return fmt.Errorf("Drive publisher is required for upload")
		}
		uploader, err = delivery.NewAdminUploadService(root.Drive.Publisher)
		if err != nil {
			return err
		}
	}
	result, err := adminmedia.RenderShort(ctx, manifest, rustexec.NewAdminMediaProcessor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, policy, cfg.Video.CanonicalVideoProfile(), log), uploader)
	if err != nil {
		return err
	}
	if result != nil {
		fmt.Printf("Uploaded rendered short: id=%s folder=%s\n", result.FileID, manifest.Upload.FolderID)
	} else if manifest.Upload != nil {
		fmt.Printf("Uploaded rendered short: folder=%s\n", manifest.Upload.FolderID)
	} else {
		fmt.Printf("Rendered short: %s\n", manifest.Output)
	}
	return nil
}
