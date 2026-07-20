package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

const fishOutputFolderID = "1Kssuh0eQ7Wmg8uMg29aI7fShXSLCaw3x"

// fishClipDriveIDEnvName is the canonical env var consulted by
// runUploadFishClip's resolution chain when --drive-id is empty.
// godlike/06 SSOT: this is the SOLE owner of the env var name; any
// renames must update this constant.
const fishClipDriveIDEnvName = "VELOX_FISH_CLIP_DRIVE_ID"

// resolveFishClipDriveID picks a non-empty Drive file ID for the
// fish clip upload via the canonical resolution chain (godlike/06
// SSOT — single canonical ordering per fact):
//
//  1. CLI --drive-id flag (operator's explicit override).
//  2. $VELOX_FISH_CLIP_DRIVE_ID env var (operator's CI env).
//
// If neither is non-empty (after TrimSpace), the chain fails closed
// with a typed error so the operator gets a clear restart-required
// message instead of an upload-routed-to-wrong-Drive-file silent
// failure. godlike/07 NO-FAKE-AVAILABILITY: an empty/whitespace-only
// value MUST NOT pass through to the upstream GetClip call.
// envLookup is a function (default: os.Getenv) so the chain is
// hermetically testable without process-level env mutation.
func resolveFishClipDriveID(flagValue string, envLookup func(string) string) (driveID string, source string, err error) {
	if v := strings.TrimSpace(flagValue); v != "" {
		return v, "flag", nil
	}
	if v := strings.TrimSpace(envLookup(fishClipDriveIDEnvName)); v != "" {
		return v, "env", nil
	}
	return "", "", fmt.Errorf("no fish clip drive ID resolved (pass --drive-id or set $%s)", fishClipDriveIDEnvName)
}

func runUploadFishClip(args []string) error {
	fs := flag.NewFlagSet("upload-fish-clip", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	folderID := fs.String("folder-id", fishOutputFolderID, "Google Drive destination folder ID")
	filename := fs.String("filename", "stargazer-fish-sand-ambush.mp4", "Drive filename")
	inputPath := fs.String("input", "", "local file to upload (defaults to the indexed source clip)")
	driveIDFlag := fs.String("drive-id", "", "Drive file ID of the source clip (overrides $VELOX_FISH_CLIP_DRIVE_ID)")
	replace := fs.Bool("replace", false, "replace an existing file with the same name")
	if err := fs.Parse(args); err != nil {
		return err
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
	if root == nil || root.Drive == nil || root.Drive.Admin == nil || root.Drive.Reader == nil || root.Repos == nil || root.Repos.ClipsRepo == nil {
		return fmt.Errorf("Drive admin, reader and clips repository are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	// Resolve the source Drive file ID via the canonical chain
	// (CLI --drive-id > $VELOX_FISH_CLIP_DRIVE_ID > fail-closed).
	// godlike/07: the helper fail-closes when no source resolves.
	driveID, source, err := resolveFishClipDriveID(*driveIDFlag, os.Getenv)
	if err != nil {
		return fmt.Errorf("resolve fish clip drive ID: %w", err)
	}
	log.Info("upload-fish-clip: resolved drive ID", zap.String("source", source), zap.String("drive_id", driveID))

	clip, err := root.Repos.ClipsRepo.GetClip(ctx, driveID)
	if err != nil {
		return fmt.Errorf("load indexed fish clip: %w", err)
	}
	if clip == nil {
		return fmt.Errorf("indexed fish clip not found")
	}
	localPath := strings.TrimSpace(*inputPath)
	if localPath == "" {
		localPath = strings.TrimSpace(clip.LocalPath())
	}
	if localPath == "" {
		return fmt.Errorf("indexed fish clip has no local path")
	}
	if _, err := os.Stat(localPath); err != nil {
		return fmt.Errorf("local fish clip unavailable: %w", err)
	}
	if strings.TrimSpace(*folderID) == "" || strings.TrimSpace(*filename) == "" {
		return fmt.Errorf("folder-id and filename are required")
	}

	// Avoid creating a duplicate if this exact output name is already in the
	// destination folder.
	matches, err := root.Drive.Reader.FindFileByName(ctx, *folderID, filepath.Base(*filename))
	if err != nil {
		return fmt.Errorf("check destination folder: %w", err)
	}
	if len(matches.Matches) > 1 {
		return fmt.Errorf("destination already contains %d files named %q; refusing ambiguous upload", len(matches.Matches), *filename)
	}
	if len(matches.Matches) == 1 {
		if !*replace {
			fmt.Printf("Fish clip already present: id=%s name=%s\n", matches.Matches[0].FileID, matches.Matches[0].Name)
			return nil
		}
		fmt.Printf("Replacing existing fish clip: id=%s name=%s\n", matches.Matches[0].FileID, matches.Matches[0].Name)
	}

	result, err := root.Drive.Admin.UploadFile(ctx, localPath, *folderID, filepath.Base(*filename))
	if err != nil {
		return fmt.Errorf("upload fish clip: %w", err)
	}
	if result == nil || strings.TrimSpace(result.FileID) == "" {
		return fmt.Errorf("upload completed without a Drive file ID")
	}
	fmt.Printf("Fish clip uploaded: id=%s folder=%s link=%s\n", result.FileID, *folderID, result.WebViewLink)
	return nil
}
