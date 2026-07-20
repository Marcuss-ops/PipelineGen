package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

const fishOutputFolderID = "1Kssuh0eQ7Wmg8uMg29aI7fShXSLCaw3x"
const fishClipDriveID = ""

func runUploadFishClip(args []string) error {
	fs := flag.NewFlagSet("upload-fish-clip", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	folderID := fs.String("folder-id", fishOutputFolderID, "Google Drive destination folder ID")
	filename := fs.String("filename", "stargazer-fish-sand-ambush.mp4", "Drive filename")
	inputPath := fs.String("input", "", "local file to upload (defaults to the indexed source clip)")
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

	clip, err := root.Repos.ClipsRepo.GetClip(ctx, fishClipDriveID)
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
