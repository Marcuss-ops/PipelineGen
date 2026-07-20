package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/app"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

type MusicTrack struct {
	Timestamp string
	Title     string
	Slug      string
}

func runDownloadKidsMusic(args []string) error {
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

	ctx := context.Background()

	url := "https://www.youtube.com/watch?v=FNXjuu1OTZ8"
	if len(args) > 0 {
		url = args[0]
	}

	tracks := []MusicTrack{
		{"0:00", "Sneaky Snitch", "sneaky_snitch"},
		{"2:14", "Fluffing A Duck", "fluffing_a_duck"},
		{"3:17", "Old MacDonald", "old_macdonald"},
		{"5:36", "Twirly Tops", "twirly_tops"},
		{"7:44", "Sneaky Business", "sneaky_business"},
		{"9:18", "Itsy Bitsy Spider", "itsy_bitsy_spider"},
		{"11:24", "After School Jamboree", "after_school_jamboree"},
		{"13:46", "Claudio The Worm", "claudio_the_worm"},
		{"15:48", "Bunny Hop", "bunny_hop"},
		{"18:42", "Monkeys Spinning Monkeys", "monkeys_spinning_monkeys"},
		{"20:47", "Bike Rides", "bike_rides"},
		{"22:40", "Lovable Clown Sit Com", "lovable_clown_sit_com"},
		{"24:39", "Mr. Turtle", "mr_turtle"},
		{"26:43", "Rainy Day Games", "rainy_day_games"},
		{"28:46", "Splashing Around", "splashing_around"},
	}

	tmpDir := filepath.Join(cfg.Storage.DataDir, "tmp")
	_ = os.MkdirAll(tmpDir, 0755)

	fullAudioPath := filepath.Join(tmpDir, "full_kids_compilation.mp4")

	// 1. Download full audio via yt-dlp
	fmt.Printf("Downloading audio from %s using yt-dlp...\n", url)
	dlCmd := exec.CommandContext(ctx, "/home/pierone/.local/bin/yt-dlp",
		"-f", "18", "--extractor-args", "youtube:player_client=android",
		"-o", fullAudioPath, url,
	)
	dlCmd.Stdout = os.Stdout
	dlCmd.Stderr = os.Stderr
	if err := dlCmd.Run(); err != nil {
		// Try fallback if yt-dlp path is different
		dlCmd2 := exec.CommandContext(ctx, "yt-dlp",
			"-f", "18", "--extractor-args", "youtube:player_client=android",
			"-o", fullAudioPath, url,
		)
		if err := dlCmd2.Run(); err != nil {
			return fmt.Errorf("yt-dlp download failed: %w", err)
		}
	}

	sfxDir := filepath.Join(cfg.Storage.DataDir, "media", "sound_effects")
	_ = os.MkdirAll(sfxDir, 0755)

	destFolderID := "17GkTNuqlt1RKSso8lTaLKElIi75xXaWO" // Background Music Drive folder

	fmt.Println("--- Processing and Uploading Tracks (5 seconds each) ---")
	for i, t := range tracks {
		startSecs, err := parseTimestampToSeconds(t.Timestamp)
		if err != nil {
			fmt.Printf("Error parsing timestamp %s for %s: %v\n", t.Timestamp, t.Title, err)
			continue
		}

		filename := fmt.Sprintf("sfx_music_kids_%s_01.mp3", t.Slug)
		localPath := filepath.Join(sfxDir, filename)

		// Slice 5 seconds starting from timestamp
		fmt.Printf("[%d/%d] Extracting %q starting at %s (%ds)...\n", i+1, len(tracks), t.Title, t.Timestamp, startSecs)
		sliceCmd := exec.CommandContext(ctx, "ffmpeg", "-y",
			"-ss", strconv.Itoa(startSecs),
			"-t", "5",
			"-i", fullAudioPath,
			"-c:a", "libmp3lame", "-b:a", "192k",
			localPath,
		)
		if out, err := sliceCmd.CombinedOutput(); err != nil {
			fmt.Printf("ffmpeg slice error for %s: %v\nOutput:\n%s\n", t.Title, err, string(out))
			continue
		}

		// Calculate file hash
		hashBytes, err := sha256FileBytes(localPath)
		if err != nil {
			fmt.Printf("Error hashing %s: %v\n", localPath, err)
			continue
		}
		hash := hex.EncodeToString(hashBytes)

		// Upload to Google Drive
		fmt.Printf("Uploading %s to Drive...\n", filename)
		uploadResult, err := root.Drive.Admin.UploadFile(ctx, localPath, destFolderID, filename)
		if err != nil {
			fmt.Printf("Drive upload failed for %s: %v\n", filename, err)
			continue
		}

		// Index in SQLite and Qdrant
		now := time.Now().UTC()
		clip := &asset.Asset{
			ID:             uploadResult.FileID,
			Name:           fmt.Sprintf("sfx_music_kids_%s_01", t.Slug),
			Filename:       filename,
			Source:         asset.Source("sfx_drive"),
			MediaType:      asset.MediaType("audio"),
			Category:       "music",
			Group:          "Background Music",
			Duration:       5 * time.Second,
			Tags:           []string{"music", "kids", "funny", "happy", "background", "loop", t.Slug, strings.ToLower(t.Title)},
			LifecycleState: asset.StateActive,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		clip.SearchTerms = clip.Tags
		clip.SearchText = fmt.Sprintf("%s background music kids happy funny instrumental loop %s", clip.Name, t.Title)
		clip.SetDriveFileID(uploadResult.FileID)
		clip.SetDriveLink("https://drive.google.com/file/d/" + uploadResult.FileID + "/view")
		clip.SetDownloadLink("https://drive.google.com/uc?export=download&id=" + uploadResult.FileID)
		clip.SetLocalPath(localPath)
		clip.SetFileHash(hash)
		clip.SetMetadataString("mime_type", "audio/mpeg")
		clip.SetMetadataString("sfx_family", "music")
		clip.SetMetadataString("sfx_category", "kids")
		clip.SetMetadataString("sfx_tags", strings.Join(clip.Tags, ","))
		clip.SetMetadataString("parent_folder_id", destFolderID)

		if err := root.Outbox.Dispatcher.EnqueueAndIndex(ctx, clip, hash); err != nil {
			fmt.Printf("Database/Qdrant indexing failed for %s: %v\n", filename, err)
		} else {
			fmt.Printf("Indexed successfully: %s -> DriveID: %s\n", filename, uploadResult.FileID)
		}
	}

	// Clean up full downloaded audio file
	_ = os.Remove(fullAudioPath)
	fmt.Println("\nAll child tracks processed successfully!")
	return nil
}

func parseTimestampToSeconds(ts string) (int, error) {
	parts := strings.Split(ts, ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid format")
	}
	mins, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, err
	}
	secs, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, err
	}
	return mins*60 + secs, nil
}

func sha256FileBytes(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}
