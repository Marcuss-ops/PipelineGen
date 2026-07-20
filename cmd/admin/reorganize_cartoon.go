package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
)

func runReorganizeCartoon(args []string) error {
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// 1. Re-index the footstep sound file: sfx_music_rnb_hiphop_loop_01 -> sfx_foley_fast_footsteps_01

	newName := "sfx_foley_fast_footsteps_01"
	driveFileID := "1sALcq2Ov11HomF4dMd4CW08tFe6gKRXu"
	foleyFolderID := "1bCGQCeS8mxsTbqRcGktezbsJ3eTrAyWP"

	fmt.Println("--- Step 1: Re-indexing Footsteps Sound Effect ---")
	clip, err := root.Repos.ClipsRepo.GetClip(ctx, driveFileID)
	if err != nil {
		return fmt.Errorf("look up footsteps clip: %w", err)
	}

	if clip != nil {
		oldLocalPath := clip.LocalPath()
		newLocalPath := filepath.Join(filepath.Dir(oldLocalPath), newName+".mp3")

		// Check if old file exists locally and rename it
		if _, err := os.Stat(oldLocalPath); err == nil {
			fmt.Printf("Renaming local file from %s to %s\n", oldLocalPath, newLocalPath)
			if err := os.Rename(oldLocalPath, newLocalPath); err != nil {
				return fmt.Errorf("rename local footsteps file: %w", err)
			}
		} else if _, err := os.Stat(newLocalPath); err == nil {
			fmt.Printf("Local file already renamed to %s\n", newLocalPath)
		} else {
			fmt.Printf("Warning: Local footsteps file not found at %s or %s\n", oldLocalPath, newLocalPath)
		}

		hash, err := sha256File(newLocalPath)
		if err != nil {
			// fallback if file doesn't exist
			hash = clip.FileHash()
		}

		clip.Name = newName
		clip.Filename = newName + ".mp3"
		clip.Group = "Foley"
		clip.Tags = []string{"foley", "footsteps", "fast", "walking", "quick", "feet", "run", "walk"}
		clip.SearchTerms = clip.Tags
		clip.SearchText = "sfx_foley_fast_footsteps_01 foley footsteps fast walking quick feet run walk"
		clip.SetLocalPath(newLocalPath)
		clip.SetFileHash(hash)

		// Set group inside metadata JSON if present
		clip.SetMetadataString("sfx_family", "foley")
		clip.SetMetadataString("sfx_category", "foley")
		clip.SetMetadataString("sfx_tags", strings.Join(clip.Tags, ","))

		fmt.Printf("Saving and re-indexing footsteps to DB & Qdrant...\n")
		if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, hash); err != nil {
			return fmt.Errorf("save and index footsteps: %w", err)
		}

		// Update google drive filename and parent folder
		fmt.Printf("Renaming Drive file %s to %s.mp3\n", driveFileID, newName)
		if err := root.Drive.Admin.RenameFile(ctx, driveFileID, newName+".mp3"); err != nil {
			fmt.Printf("Warning: Drive RenameFile failed: %v\n", err)
		}

		fmt.Printf("Moving Drive file %s to Foley folder %s\n", driveFileID, foleyFolderID)
		if err := root.Drive.Admin.MoveFile(ctx, driveFileID, "1752jI24N7QWPtWJB7IQoMBz8R57P-GCt", foleyFolderID); err != nil {
			// Try move from Cartoon as well just in case
			_ = root.Drive.Admin.MoveFile(ctx, driveFileID, "12AY75eIFSvtxbte1ECocZ2A7WXAwBC3Q", foleyFolderID)
		}
	} else {
		fmt.Printf("Clip not found in DB with Drive ID %s, skipping local/DB update\n", driveFileID)
	}

	// 2. Create subfolders in Cartoon (12AY75eIFSvtxbte1ECocZ2A7WXAwBC3Q)
	fmt.Println("\n--- Step 2: Creating Subfolders inside Cartoon ---")
	cartoonRootID := "12AY75eIFSvtxbte1ECocZ2A7WXAwBC3Q"
	subfolders := []string{"Anime", "Meme", "Comico", "Cartoni"}
	subfolderIDs := make(map[string]string)

	for _, sf := range subfolders {
		id, err := root.Drive.Admin.GetOrCreateFolder(ctx, sf, cartoonRootID)
		if err != nil {
			return fmt.Errorf("create subfolder %s: %w", sf, err)
		}
		subfolderIDs[sf] = id
		fmt.Printf("Subfolder: %s -> ID: %s\n", sf, id)
	}

	// 3. List direct files in Cartoon (12AY75eIFSvtxbte1ECocZ2A7WXAwBC3Q)
	fmt.Println("\n--- Step 3: Moving floating files to subfolders ---")
	files, err := root.Drive.Reader.ListFiles(ctx, cartoonRootID)
	if err != nil {
		return fmt.Errorf("list files: %w", err)
	}

	for _, f := range files {
		if f.MimeType == "application/vnd.google-apps.folder" {
			// Skip the subfolders we just created or other folders
			continue
		}

		nameLower := strings.ToLower(f.Name)
		var targetSubfolder string

		// Classification logic
		if strings.Contains(nameLower, "one_piece") ||
			strings.Contains(nameLower, "anime") ||
			strings.Contains(nameLower, "dragon_ball") ||
			strings.Contains(nameLower, "sukuna") ||
			strings.Contains(nameLower, "yowai_mo") ||
			strings.Contains(nameLower, "kuru_kuru") ||
			strings.Contains(nameLower, "ara_ara") {
			targetSubfolder = "Anime"
		} else if strings.Contains(nameLower, "mail_bike") ||
			strings.Contains(nameLower, "upin_ipin") ||
			strings.Contains(nameLower, "spongebob") ||
			strings.Contains(nameLower, "yoshi") ||
			strings.Contains(nameLower, "boing") ||
			strings.Contains(nameLower, "spring") ||
			strings.Contains(nameLower, "bloop") ||
			strings.Contains(nameLower, "bubble") {
			targetSubfolder = "Cartoni"
		} else if strings.Contains(nameLower, "snore") ||
			strings.Contains(nameLower, "laughter") ||
			strings.Contains(nameLower, "fart") ||
			strings.Contains(nameLower, "gulp") ||
			strings.Contains(nameLower, "kiss") ||
			strings.Contains(nameLower, "chuaks") ||
			strings.Contains(nameLower, "bo_womp") ||
			strings.Contains(nameLower, "hiyakkk") ||
			strings.Contains(nameLower, "splat") {
			targetSubfolder = "Comico"
		} else {
			targetSubfolder = "Meme"
		}

		targetFolderID := subfolderIDs[targetSubfolder]
		fmt.Printf("Moving %q (%s) -> %s (%s)\n", f.Name, f.ID, targetSubfolder, targetFolderID)

		if err := root.Drive.Admin.MoveFile(ctx, f.ID, cartoonRootID, targetFolderID); err != nil {
			fmt.Printf("  Error moving file %s: %v\n", f.Name, err)
		} else {
			// Update local DB parent folder if mapped
			_ = updateDBParentFolder(ctx, root, f.ID, targetFolderID)
		}
	}

	// 4. Force wait/flush Qdrant outbox indexing
	time.Sleep(3 * time.Second)
	fmt.Println("\n--- Reorganization Complete ---")
	return nil
}

func updateDBParentFolder(ctx context.Context, root *app.ComposeRoot, driveID, newParentID string) error {
	clip, err := root.Repos.ClipsRepo.GetClip(ctx, driveID)
	if err != nil || clip == nil {
		return err
	}
	clip.SetMetadataString("parent_folder_id", newParentID)
	hash := clip.FileHash()
	return root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, hash)
}
