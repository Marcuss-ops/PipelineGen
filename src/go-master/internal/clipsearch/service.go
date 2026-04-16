// Package clipsearch dynamically searches, downloads, and uploads video clips for keywords.
package clipsearch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// Service dynamically searches, downloads, and uploads video clips.
type Service struct {
	driveClient *drive.Client
	stockDB     *stockdb.StockDB
	artlistDB   *artlistdb.ArtlistDB
	downloadDir string
	ytDlpPath   string
	mu          sync.Mutex
}

// SearchResult represents a found and processed clip.
type SearchResult struct {
	Keyword  string `json:"keyword"`
	ClipID   string `json:"clip_id"`
	Filename string `json:"filename"`
	DriveURL string `json:"drive_url"`
	DriveID  string `json:"drive_id"`
	Folder   string `json:"folder"`
}

// New creates a new dynamic clip search service.
func New(driveClient *drive.Client, stockDB *stockdb.StockDB, artlistDB *artlistdb.ArtlistDB, downloadDir, ytDlpPath string) *Service {
	return &Service{
		driveClient: driveClient,
		stockDB:     stockDB,
		artlistDB:   artlistDB,
		downloadDir: downloadDir,
		ytDlpPath:   ytDlpPath,
	}
}

// SearchClips searches for clips matching keywords, downloads, uploads, and saves to DB.
// Returns found clips (from DB cache or newly downloaded).
func (s *Service) SearchClips(ctx context.Context, keywords []string) ([]SearchResult, error) {
	var results []SearchResult

	for _, kw := range keywords {
		kw = strings.TrimSpace(kw)
		if kw == "" {
			continue
		}

		// 1. Check if we already have a clip for this keyword in DB
		existing, err := s.findClipInDB(kw)
		if err == nil && existing != nil {
			results = append(results, *existing)
			logger.Info("Found clip in DB cache",
				zap.String("keyword", kw),
				zap.String("clip_id", existing.ClipID),
			)
			continue
		}

		// 2. Search and download via yt-dlp
		downloadedPath, err := s.downloadClip(ctx, kw)
		if err != nil {
			logger.Warn("Failed to download clip for keyword",
				zap.String("keyword", kw),
				zap.Error(err),
			)
			continue
		}

		// 3. Upload to Drive
		driveResult, err := s.uploadToDrive(ctx, downloadedPath, kw)
		if err != nil {
			logger.Warn("Failed to upload clip to Drive",
				zap.String("keyword", kw),
				zap.Error(err),
			)
			os.Remove(downloadedPath)
			continue
		}

		// 4. Save to StockDB
		if s.stockDB != nil {
			err = s.saveToStockDB(kw, driveResult)
			if err != nil {
				logger.Warn("Failed to save clip to StockDB",
					zap.String("keyword", kw),
					zap.Error(err),
				)
			}
		}

		// 5. Save to ArtlistDB (Registration back to Artlist index)
		if s.artlistDB != nil {
			err = s.saveToArtlistDB(kw, driveResult, downloadedPath)
			if err != nil {
				logger.Warn("Failed to save clip to ArtlistDB",
					zap.String("keyword", kw),
					zap.Error(err),
				)
			}
		}

		// 6. Cleanup downloaded file
		os.Remove(downloadedPath)

		result := SearchResult{
			Keyword:  kw,
			ClipID:   driveResult.DriveID,
			Filename: driveResult.Filename,
			DriveURL: driveResult.DriveURL,
			DriveID:  driveResult.DriveID,
			Folder:   "Stock/Artlist/" + kw,
		}
		results = append(results, result)

		logger.Info("Dynamic clip processed and registered",
			zap.String("keyword", kw),
			zap.String("drive_url", driveResult.DriveURL),
		)
	}

	return results, nil
}

// findClipInDB searches StockDB for an existing clip matching the keyword.
func (s *Service) findClipInDB(keyword string) (*SearchResult, error) {
	// ... (no changes here but keeping for context)
	if s.stockDB == nil {
		return nil, fmt.Errorf("StockDB not available")
	}

	allClips, err := s.stockDB.GetAllClips()
	if err != nil {
		return nil, err
	}

	keywordLower := strings.ToLower(keyword)
	for _, clip := range allClips {
		tags := strings.ToLower(strings.Join(clip.Tags, ","))
		folderID := strings.ToLower(clip.FolderID)

		// Check if keyword appears in tags or folder
		if strings.Contains(tags, keywordLower) ||
			strings.Contains(folderID, keywordLower) {
			return &SearchResult{
				Keyword:  keyword,
				ClipID:   clip.ClipID,
				Filename: clip.Filename,
				DriveURL: fmt.Sprintf("https://drive.google.com/file/d/%s/view", clip.ClipID),
				DriveID:  clip.ClipID,
				Folder:   clip.FolderID,
			}, nil
		}
	}

	return nil, fmt.Errorf("clip not found for keyword: %s", keyword)
}

// downloadClip uses yt-dlp to download a short clip for the keyword.
func (s *Service) downloadClip(ctx context.Context, keyword string) (string, error) {
	// ... (no changes here but keeping for context)
	if s.ytDlpPath == "" {
		return "", fmt.Errorf("yt-dlp not configured")
	}

	outputDir := filepath.Join(s.downloadDir, "dynamic_clips")
	os.MkdirAll(outputDir, 0755)

	outputPattern := filepath.Join(outputDir, fmt.Sprintf("dynamic_%s_%%(id)s.%%(ext)s", sanitizeFilename(keyword)))

	// Search YouTube for the keyword and download a short clip (max 60s)
	args := []string{
		"--format", "bestvideo[height<=1080][ext=mp4]+bestaudio[ext=m4a]/best[ext=mp4]/best",
		"--max-downloads", "1",
		"--match-filter", "duration < 60",
		"--output", outputPattern,
		"--no-playlist",
		fmt.Sprintf("ytsearch1:%s boxing highlights", keyword),
	}

	cmd := exec.CommandContext(ctx, s.ytDlpPath, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("yt-dlp failed: %w", err)
	}

	// Find the downloaded file
	files, err := filepath.Glob(filepath.Join(outputDir, fmt.Sprintf("dynamic_%s_*", sanitizeFilename(keyword))))
	if err != nil || len(files) == 0 {
		return "", fmt.Errorf("no files found after download")
	}

	// Return the video file (not thumbnail)
	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f))
		if ext == ".mp4" || ext == ".webm" || ext == ".mkv" {
			return f, nil
		}
	}

	return files[0], nil
}

// uploadToDrive uploads a file to Google Drive and returns the Drive URL.
func (s *Service) uploadToDrive(ctx context.Context, filePath, keyword string) (*DriveUploadResult, error) {
	// ... (no changes here but keeping for context)
	if s.driveClient == nil {
		return nil, fmt.Errorf("Drive client not available")
	}

	// Upload file to stock root folder (folder creation not yet implemented)
	filename := sanitizeFilename(keyword) + "_" + filepath.Base(filePath)
	fileID, err := s.driveClient.UploadFile(ctx, filePath, "", filename)
	if err != nil {
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}

	return &DriveUploadResult{
		DriveID:  fileID,
		Filename: filename,
		DriveURL: fmt.Sprintf("https://drive.google.com/file/d/%s/view", fileID),
	}, nil
}

// saveToStockDB saves the new clip metadata to StockDB.
func (s *Service) saveToStockDB(keyword string, driveResult *DriveUploadResult) error {
	if s.stockDB == nil {
		return fmt.Errorf("StockDB not available")
	}

	clip := stockdb.StockClipEntry{
		ClipID:   driveResult.DriveID,
		FolderID: "Stock/Artlist/" + keyword,
		Filename: driveResult.Filename,
		Source:   "dynamic",
		Tags:     []string{keyword},
		Duration: 0,
	}

	return s.stockDB.UpsertClip(clip)
}

// saveToArtlistDB saves the new clip metadata to ArtlistDB.
func (s *Service) saveToArtlistDB(keyword string, driveResult *DriveUploadResult, downloadPath string) error {
	if s.artlistDB == nil {
		return fmt.Errorf("ArtlistDB not available")
	}

	clip := artlistdb.ArtlistClip{
		ID:             "dynamic_" + driveResult.DriveID,
		VideoID:        driveResult.DriveID,
		Title:          driveResult.Filename,
		Name:           driveResult.Filename,
		Term:           keyword,
		Folder:         "Stock/Artlist/" + keyword,
		DriveFileID:    driveResult.DriveID,
		DriveURL:       driveResult.DriveURL,
		DownloadPath:   downloadPath,
		Downloaded:     true,
		DownloadedAt:   time.Now().Format(time.RFC3339),
		AddedAt:        time.Now().Format(time.RFC3339),
		Category:       "Dynamic Search",
		Tags:           []string{keyword, "dynamic", "auto-registered"},
		LocalPathDrive: "Stock/Artlist/" + keyword + "/" + driveResult.Filename,
	}

	// Add search result entry if doesn't exist
	err := s.artlistDB.AddSearchResults(keyword, []artlistdb.ArtlistClip{clip})
	if err != nil {
		return err
	}

	// Mark it as downloaded explicitly to ensure metadata is consistent
	return s.artlistDB.MarkClipDownloaded(clip.ID, keyword, driveResult.DriveID, driveResult.DriveURL, downloadPath)
}

// DriveUploadResult holds the result of a Drive upload.
type DriveUploadResult struct {
	DriveID  string
	Filename string
	DriveURL string
}

// sanitizeFilename removes special characters from a filename.
func sanitizeFilename(name string) string {
	result := strings.ReplaceAll(name, " ", "_")
	result = strings.ReplaceAll(result, "/", "_")
	result = strings.ReplaceAll(result, "\\", "_")
	result = strings.ReplaceAll(result, ":", "_")
	result = strings.ReplaceAll(result, "*", "_")
	result = strings.ReplaceAll(result, "?", "_")
	result = strings.ReplaceAll(result, "\"", "_")
	result = strings.ReplaceAll(result, "<", "_")
	result = strings.ReplaceAll(result, ">", "_")
	result = strings.ReplaceAll(result, "|", "_")
	return result
}
