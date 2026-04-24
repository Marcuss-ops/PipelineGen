// Test script: search Artlist keywords, download clips, upload to Drive, update DB
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	_ "github.com/mattn/go-sqlite3"

	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/clip"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/config"
)

// Test keywords — 10 diverse English terms from the seed pool
var testKeywords = []string{"spider", "technology", "people", "nature", "city"}

const (
	clipsPerKeyword = 10
	parallel        = 3
)

func main() {
	log.Println("=== Artlist Pipeline Test ===")
	log.Printf("Keywords: %v", testKeywords)
	log.Printf("Clips per keyword: %d", clipsPerKeyword)

	// 1. Open Artlist SQLite source
	dbPath := "src/node-scraper/artlist_videos.db"
	if _, err := os.Stat(dbPath); err != nil {
		// Try alternative path
		dbPath = "../../src/node-scraper/artlist_videos.db"
	}
	artlistSrc := clip.NewArtlistSource(dbPath)
	if err := artlistSrc.Connect(); err != nil {
		log.Fatalf("Failed to connect Artlist source (%s): %v", dbPath, err)
	}
	defer artlistSrc.Close()
	log.Printf("✓ Artlist SQLite connected (%s)", dbPath)

	// 2. Open local ArtlistDB
	localDBPath := config.ResolveDataPath("artlist_local.db.json")
	artlistDB, err := artlistdb.Open(localDBPath)
	if err != nil {
		log.Fatalf("Failed to open ArtlistDB: %v", err)
	}
	log.Printf("✓ ArtlistDB opened (%s)", localDBPath)

	// 3. Init Drive client
	driveClient, err := initDriveClient()
	if err != nil {
		log.Printf("⚠ Drive client failed (%v), uploads will be skipped", err)
	} else {
		log.Println("✓ Drive client initialized")
	}

	// 4. Ensure Stock/Artlist root folder exists
	// Stock root folder: https://drive.google.com/drive/u/1/folders/1wt4hqmHD5qEsNhpUUBszlRkSHhyFgtGh
	stockRootID := "1wt4hqmHD5qEsNhpUUBszlRkSHhyFgtGh"
	artlistRootID := ""
	if driveClient != nil {
		ctx := context.Background()
		// Try to find "Artlist" folder inside Stock
		folders, err := driveClient.ListFolders(ctx, drive.ListFoldersOptions{
			ParentID: stockRootID,
			MaxDepth: 0,
			MaxItems: 100,
		})
		if err != nil {
			log.Printf("⚠ Failed to list Stock subfolders: %v", err)
		} else {
			for _, f := range folders {
				if f.Name == "Artlist" {
					artlistRootID = f.ID
					break
				}
			}
		}

		if artlistRootID == "" {
			// Create Artlist folder inside Stock
			artlistRootID, err = driveClient.CreateFolder(ctx, "Artlist", stockRootID)
			if err != nil {
				log.Printf("⚠ Failed to create Artlist folder in Stock: %v", err)
			} else {
				log.Printf("✓ Created Artlist folder inside Stock: %s", artlistRootID)
			}
		} else {
			log.Printf("✓ Found Artlist folder inside Stock: %s", artlistRootID)
		}
	}

	// 5. Process keywords
	var (
		mu              sync.Mutex
		wg              sync.WaitGroup
		sem             = make(chan struct{}, parallel)
		totalSearched   int
		totalIndexed    int
		totalDownloaded int
		totalUploaded   int
		results         []map[string]interface{}
	)

	for _, keyword := range testKeywords {
		wg.Add(1)
		sem <- struct{}{}

		go func(kw string) {
			defer wg.Done()
			defer func() { <-sem }()

			result := processKeyword(artlistSrc, artlistDB, driveClient, artlistRootID, kw, clipsPerKeyword)
			mu.Lock()
			results = append(results, result)
			totalSearched += result["searched"].(int)
			totalIndexed += result["indexed"].(int)
			totalDownloaded += result["downloaded"].(int)
			totalUploaded += result["uploaded"].(int)
			mu.Unlock()
		}(keyword)
	}

	wg.Wait()

	// 6. Save DB
	if err := artlistDB.Save(); err != nil {
		log.Printf("⚠ Failed to save DB: %v", err)
	} else {
		log.Println("✓ ArtlistDB saved")
	}

	// 7. Print summary
	fmt.Println("\n========================================")
	fmt.Println("           RESULTS SUMMARY")
	fmt.Println("========================================")
	fmt.Printf("Keywords tested:      %d\n", len(testKeywords))
	fmt.Printf("Terms searched:       %d\n", totalSearched)
	fmt.Printf("Clips indexed:        %d\n", totalIndexed)
	fmt.Printf("Clips downloaded:     %d\n", totalDownloaded)
	fmt.Printf("Clips uploaded Drive: %d\n", totalUploaded)
	fmt.Printf("Total clips in DB:    %d\n", artlistDB.GetStats().TotalClips)
	fmt.Println("========================================")

	// Per-key detail
	fmt.Println("\nPer-key results:")
	for _, r := range results {
		status := "✓"
		if r["error"] != "" {
			status = fmt.Sprintf("✗ %s", r["error"])
		}
		fmt.Printf("  %-20s  searched=%v  indexed=%d  downloaded=%d  uploaded=%d  %s\n",
			r["keyword"], r["searched"], r["indexed"], r["downloaded"], r["uploaded"], status)
	}
}

// processKeyword searches, indexes, downloads, and uploads clips for one keyword.
func processKeyword(
	src *clip.ArtlistSource,
	db *artlistdb.ArtlistDB,
	driveClient *drive.Client,
	artlistRootID string,
	keyword string,
	maxClips int,
) map[string]interface{} {
	result := map[string]interface{}{
		"keyword":    keyword,
		"searched":   0,
		"indexed":    0,
		"downloaded": 0,
		"uploaded":   0,
		"error":      "",
	}

	log.Printf("\n--- Keyword: %s ---", keyword)

	// STEP 1: Search Artlist (if not already indexed)
	if db.HasSearchedTerm(keyword) {
		log.Printf("  Term already indexed, skipping search")
	} else {
		clips, err := src.SearchClips(keyword, maxClips*3)
		if err != nil {
			result["error"] = fmt.Sprintf("search failed: %v", err)
			return result
		}
		if len(clips) == 0 {
			result["error"] = "no clips found on Artlist"
			return result
		}
		result["searched"] = 1

		// Convert to ArtlistClip
		var artlistClips []artlistdb.ArtlistClip
		for _, c := range clips {
			// Skip already in DB
			if _, found := db.IsClipAlreadyDownloaded(c.ID, c.DownloadLink); found {
				continue
			}
			artlistClips = append(artlistClips, artlistdb.ArtlistClip{
				ID:          c.ID,
				VideoID:     c.Filename,
				Title:       c.Name,
				OriginalURL: c.DownloadLink,
				URL:         c.DownloadLink,
				Duration:    int(c.Duration),
				Width:       c.Width,
				Height:      c.Height,
				Category:    c.FolderPath,
				Tags:        c.Tags,
			})
		}

		if len(artlistClips) > 0 {
			db.AddSearchResults(keyword, artlistClips)
			result["indexed"] = len(artlistClips)
			log.Printf("  Indexed %d clips from Artlist", len(artlistClips))
		}
	}

	// STEP 2: Get clips for term
	allClips, found := db.GetClipsForTerm(keyword)
	if !found {
		result["error"] = "no clips in DB for term"
		return result
	}

	// STEP 3: Download undownloaded clips (up to maxClips)
	var toDownload []artlistdb.ArtlistClip
	for _, c := range allClips {
		if !c.Downloaded && len(toDownload) < maxClips {
			toDownload = append(toDownload, c)
		}
	}

	if len(toDownload) == 0 {
		log.Printf("  All clips already downloaded")
		return result
	}

	log.Printf("  Downloading %d clips...", len(toDownload))

	// Create Drive folder for this keyword
	termFolderID := ""
	if driveClient != nil && artlistRootID != "" {
		folderName := capitalize(keyword)
		termFolderID, _ = getOrCreateDriveFolder(driveClient, folderName, artlistRootID)
		log.Printf("  Drive folder: %s", folderName)
	}

	// Download each clip
	for _, c := range toDownload {
		localPath, err := downloadClip(c.URL, keyword, c.VideoID)
		if err != nil {
			log.Printf("  ✗ Download failed for %s: %v", c.VideoID, err)
			continue
		}

		// Convert to 1920x1080, 7s
		convertedPath, err := convertClip(localPath, c.VideoID, keyword)
		if err != nil {
			os.Remove(localPath)
			log.Printf("  ✗ Convert failed for %s: %v", c.VideoID, err)
			continue
		}
		os.Remove(localPath) // remove raw

		// Upload to Drive
		driveFileID := ""
		driveURL := ""
		if driveClient != nil && termFolderID != "" {
			filename := fmt.Sprintf("%s_%s.mp4", keyword, c.VideoID)
			ctx := context.Background()
			driveFileID, err = driveClient.UploadFile(ctx, convertedPath, termFolderID, filename)
			if err != nil {
				log.Printf("  ✗ Upload failed for %s: %v", c.VideoID, err)
			} else {
				driveURL = fmt.Sprintf("https://drive.google.com/file/d/%s/view", driveFileID)
				result["uploaded"] = result["uploaded"].(int) + 1
				log.Printf("  ✓ Uploaded: %s → %s", filename, driveURL)
			}
		} else {
			// No drive client — just keep local file
			log.Printf("  ✓ Converted (no Drive): %s", convertedPath)
		}

		// Mark as downloaded in DB
		db.MarkClipDownloaded(c.ID, keyword, driveFileID, driveURL, convertedPath)
		result["downloaded"] = result["downloaded"].(int) + 1
	}

	return result
}

// downloadClip downloads a clip using yt-dlp or curl.
func downloadClip(url, keyword, videoID string) (string, error) {
	tempDir := filepath.Join(config.ResolveDataPath("downloads"), "artlist", keyword)
	os.MkdirAll(tempDir, 0755)

	rawPath := filepath.Join(tempDir, fmt.Sprintf("%s_raw.mp4", videoID))

	// Try yt-dlp
	cmd := exec.Command("yt-dlp", "-o", rawPath, "--no-playlist", "--socket-timeout", "30", "--retries", "3", url)
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err == nil {
		if stat, err := os.Stat(rawPath); err == nil && stat.Size() > 0 {
			return rawPath, nil
		}
	}

	// Fallback: curl
	cmd = exec.Command("curl", "-L", "-s", "--max-time", "60", "-o", rawPath, url)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}

	if stat, err := os.Stat(rawPath); err != nil || stat.Size() == 0 {
		return "", fmt.Errorf("downloaded file is empty")
	}

	return rawPath, nil
}

// convertClip converts a clip to 1920x1080, max 7 seconds.
func convertClip(rawPath, videoID, keyword string) (string, error) {
	outputPath := filepath.Join(filepath.Dir(rawPath), fmt.Sprintf("%s_1080p_7s.mp4", videoID))

	cmd := exec.Command("ffmpeg", "-y", "-i", rawPath,
		"-t", "7",
		"-vf", "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2:black",
		"-c:v", "libx264", "-preset", "fast", "-crf", "23",
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "+faststart",
		outputPath,
	)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ffmpeg convert: %w", err)
	}

	return outputPath, nil
}

// getOrCreateDriveFolder gets or creates a Drive folder.
func getOrCreateDriveFolder(client *drive.Client, name, parentID string) (string, error) {
	ctx := context.Background()

	folders, err := client.ListFolders(ctx, drive.ListFoldersOptions{
		ParentID: parentID,
		MaxDepth: 0,
		MaxItems: 50,
	})
	if err != nil {
		return "", err
	}

	for _, f := range folders {
		if f.Name == name {
			return f.ID, nil
		}
	}

	return client.CreateFolder(ctx, name, parentID)
}

// initDriveClient initializes the Google Drive client.
func initDriveClient() (*drive.Client, error) {
	credentialsPath := "credentials.json"
	tokenPath := "token.json"

	if _, err := os.Stat(credentialsPath); err != nil {
		return nil, fmt.Errorf("credentials.json not found: %w", err)
	}

	ctx := context.Background()
	config := drive.Config{
		CredentialsFile: credentialsPath,
		TokenFile:       tokenPath,
		Scopes:          []string{"https://www.googleapis.com/auth/drive"},
	}
	return drive.NewClient(ctx, config)
}

// capitalize converts first letter to uppercase.
func capitalize(s string) string {
	if len(s) == 0 {
		return s
	}
	s = strings.ToLower(s)
	return strings.ToUpper(s[:1]) + s[1:]
}

func init() {
	// Suppress verbose yt-dlp output
	os.Setenv("YTDLP_VERBOSE", "0")
}
