package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/storage"
	"velox/go-master/pkg/config"
)

func main() {
	tag := flag.String("tag", "", "Tag to process")
	limit := flag.Int("limit", 5, "Number of clips to download/upload")
	flag.Parse()

	if *tag == "" {
		log.Fatal("tag is required")
	}

	cfg := config.Get()
	ctx := context.Background()

	// 1. Initialize Drive Client
	driveSvc, err := initDriveService(ctx, cfg)
	if err != nil {
		log.Fatalf("Failed to init Drive service: %v", err)
	}

	// 2. Open DB and get clips
	logger, _ := zap.NewProduction()
	db, err := storage.NewSQLiteDB(cfg.Storage.DataDir, "velox.db.sqlite", logger)
	if err != nil {
		log.Fatalf("Failed to open DB: %v", err)
	}
	repo := clips.NewRepository(db.DB)

	// Search in main DB (already imported from scraper DB)
	allClips, err := repo.SearchClips(ctx, *tag)
	if err != nil {
		log.Fatalf("Failed to search clips: %v", err)
	}

	// Get Artlist root folder ID
	rootFolderID := cfg.Harvester.DriveFolderID
	if rootFolderID == "" {
		rootFolderID = "1OAAf5dawAppdopsgCq1yHFGPUXCI9Vbk"
	}

	// 3. Create or get Tag Folder on Drive
	tagFolderID, err := getOrCreateFolder(driveSvc, *tag, rootFolderID)
	if err != nil {
		log.Fatalf("Failed to get/create tag folder: %v", err)
	}

	fmt.Printf("📂 Processing tag: %s (Drive Folder: %s)\n", *tag, tagFolderID)

	count := 0
	// Build a set of already uploaded external_urls and file hashes to avoid duplicates
	uploadedURLs := make(map[string]bool)
	uploadedHashes := make(map[string]bool)
	for _, clip := range allClips {
		if clip.Source == "artlist" && strings.Contains(clip.DriveLink, "drive.google.com") {
			uploadedURLs[clip.ExternalURL] = true
			if clip.FileHash != "" {
				uploadedHashes[clip.FileHash] = true
			}
		}
	}

	for _, clip := range allClips {
		if count >= *limit {
			break
		}
		if clip.Source != "artlist" {
			continue
		}

		// Skip if already uploaded (by external_url)
		if uploadedURLs[clip.ExternalURL] {
			fmt.Printf("⏭️  Skipping (already uploaded): %s\n", clip.Name)
			continue
		}

		// If drive link is already a Google Drive link, skip (unless we want to force re-upload)
		if strings.Contains(clip.DriveLink, "drive.google.com") && !strings.Contains(clip.DriveLink, "artlist.io") {
			fmt.Printf("⏭️  Skipping (already on Drive): %s\n", clip.Name)
			continue
		}

		url := clip.ExternalURL
		if url == "" {
			url = clip.DownloadLink
		}
		if !strings.Contains(url, "artlist.io") {
			continue
		}

		fmt.Printf("⏳ Downloading: %s\n", clip.Name)
		rawPath := filepath.Join(os.TempDir(), fmt.Sprintf("raw_%s.mp4", clip.ID))
		processedPath := filepath.Join(os.TempDir(), fmt.Sprintf("proc_%s.mp4", clip.ID))
		
		// Download with yt-dlp
		cmdDl := exec.Command("yt-dlp", "-o", rawPath, url)
		if err := cmdDl.Run(); err != nil {
			log.Printf("❌ Failed to download %s: %v", url, err)
			continue
		}

		// 4. Process Video: 1080p, 7s max, no audio
		fmt.Printf("✂️ Processing (1080p, 7s, mute)...\n")
		if err := processVideo(rawPath, processedPath); err != nil {
			log.Printf("❌ Failed to process video %s: %v", clip.Name, err)
			os.Remove(rawPath)
			continue
		}

		// Calculate file hash to check for duplicates
		fileHash, err := calculateFileHash(processedPath)
		if err != nil {
			log.Printf("❌ Failed to calculate hash for %s: %v", clip.Name, err)
			os.Remove(rawPath)
			os.Remove(processedPath)
			continue
		}

		// Check if this file hash already exists in uploaded clips
		if uploadedHashes[fileHash] {
			fmt.Printf("⏭️  Skipping (duplicate file hash): %s (hash: %s)\n", clip.Name, fileHash)
			os.Remove(rawPath)
			os.Remove(processedPath)
			continue
		}

		fmt.Printf("⬆️ Uploading to Drive...\n")
		f, err := os.Open(processedPath)
		if err != nil {
			log.Printf("❌ Failed to open processed file: %v", err)
			os.Remove(rawPath)
			os.Remove(processedPath)
			continue
		}

		driveFile, err := driveSvc.Files.Create(&drive.File{
			Name:    fmt.Sprintf("%s_7s.mp4", clip.Name),
			Parents: []string{tagFolderID},
		}).Fields("id,webViewLink").Media(f).Do()
		f.Close()
		
		// Cleanup temp files
		os.Remove(rawPath)
		os.Remove(processedPath)

		if err != nil {
			log.Printf("❌ Failed to upload to Drive: %v", err)
			continue
		}

		// Update DB with Drive info and file hash
		clip.DriveLink = driveFile.WebViewLink
		clip.DownloadLink = "https://drive.google.com/uc?id=" + driveFile.Id
		clip.FileHash = fileHash
		if err := repo.UpsertClip(ctx, clip); err != nil {
			log.Printf("❌ Failed to update DB: %v", err)
		}
		uploadedHashes[fileHash] = true

		fmt.Printf("✅ Success: %s\n", clip.Name)
		count++
	}

	fmt.Printf("🏁 Completed: %d clips processed for tag %s\n", count, *tag)
}

func processVideo(input, output string) error {
	// FFmpeg command:
	// -y: overwrite
	// -t 7: duration 7 seconds
	// -i: input
	// -vf: scale to 1920:1080 with padding/crop to fill, 30fps
	// -an: disable audio
	// -c:v libx264: encode to x264
	
	args := []string{
		"-y",
		"-t", "7",
		"-i", input,
		"-vf", "scale=1920:1080:force_original_aspect_ratio=increase,crop=1920:1080,fps=30",
		"-an",
		"-c:v", "libx264",
		"-preset", "fast",
		"-crf", "23",
		output,
	}

	cmd := exec.Command("ffmpeg", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg failed: %v (output: %s)", err, string(out))
	}
	return nil
}

func calculateFileHash(filePath string) (string, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func initDriveService(ctx context.Context, cfg *config.Config) (*drive.Service, error) {
	credsData, err := ioutil.ReadFile(cfg.Paths.CredentialsFile)
	if err != nil {
		return nil, err
	}
	tokenData, err := ioutil.ReadFile(cfg.Paths.TokenFile)
	if err != nil {
		return nil, err
	}

	var token oauth2.Token
	if err := json.Unmarshal(tokenData, &token); err != nil {
		return nil, err
	}

	oauthCfg, err := google.ConfigFromJSON(credsData, drive.DriveScope)
	if err != nil {
		return nil, err
	}

	ts := oauthCfg.TokenSource(ctx, &token)
	return drive.NewService(ctx, option.WithTokenSource(ts))
}

func getOrCreateFolder(svc *drive.Service, name, parentID string) (string, error) {
	query := fmt.Sprintf("name = '%s' and '%s' in parents and mimeType = 'application/vnd.google-apps.folder' and trashed = false", name, parentID)
	list, err := svc.Files.List().Q(query).Do()
	if err != nil {
		return "", err
	}

	if len(list.Files) > 0 {
		return list.Files[0].Id, nil
	}

	f, err := svc.Files.Create(&drive.File{
		Name:     name,
		MimeType: "application/vnd.google-apps.folder",
		Parents:  []string{parentID},
	}).Do()
	if err != nil {
		return "", err
	}
	return f.Id, nil
}
