package artlistpipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// SmartDownloader handles intelligent clip download with caching, worker pool, and smart trimming.
type SmartDownloader struct {
	driveClient *drive.Client
	downloadDir string
	ytDlpPath   string
	ffmpegPath  string
	cacheDir    string
	workerPool  int
	maxDuration int // max seconds per clip, default 7
	width       int // target width, default 1920
	height      int // target height, default 1080
}

// SmartDownloadResult holds the result of a smart download operation.
type SmartDownloadResult struct {
	ClipID      string `json:"clip_id"`
	LocalPath   string `json:"local_path"`
	DriveFileID string `json:"drive_file_id"`
	DriveURL    string `json:"drive_url"`
	DrivePath   string `json:"drive_path"`
	Filename    string `json:"filename"`
	Cached      bool   `json:"cached"`
	Duration    int    `json:"duration"`
}

// NewSmartDownloader creates a new intelligent downloader.
func NewSmartDownloader(
	driveClient *drive.Client,
	downloadDir, ytDlpPath, ffmpegPath string,
	workerPool int,
) *SmartDownloader {
	cacheDir := filepath.Join(downloadDir, "clips_cache")
	os.MkdirAll(cacheDir, 0755)

	if workerPool <= 0 {
		workerPool = 4
	}

	return &SmartDownloader{
		driveClient: driveClient,
		downloadDir: downloadDir,
		ytDlpPath:   ytDlpPath,
		ffmpegPath:  ffmpegPath,
		cacheDir:    cacheDir,
		workerPool:  workerPool,
		maxDuration: 7,
		width:       1920,
		height:      1080,
	}
}

// DownloadClipsWithPool downloads multiple clips using a worker pool with caching.
func (sd *SmartDownloader) DownloadClipsWithPool(ctx context.Context, clips []artlistdb.ArtlistClip, term, videoID string) ([]SmartDownloadResult, error) {
	var (
		results []SmartDownloadResult
		mu      sync.Mutex
		wg      sync.WaitGroup
		sem     = make(chan struct{}, sd.workerPool)
	)

	for _, clip := range clips {
		wg.Add(1)
		sem <- struct{}{}

		go func(clip artlistdb.ArtlistClip) {
			defer wg.Done()
			defer func() { <-sem }()

			result, err := sd.downloadClipWithcache(ctx, clip, term, videoID)
			if err != nil {
				logger.Warn("Failed to download clip",
					zap.String("clip_id", clip.ID),
					zap.String("term", term),
					zap.Error(err))
				return
			}

			mu.Lock()
			results = append(results, *result)
			mu.Unlock()
		}(clip)
	}

	wg.Wait()
	return results, nil
}

// downloadClipWithcache downloads a single clip, checking local cache first.
func (sd *SmartDownloader) downloadClipWithcache(ctx context.Context, clip artlistdb.ArtlistClip, term, videoID string) (*SmartDownloadResult, error) {
	// Check cache first
	cacheKey := computeCacheKey(clip)
	cachePath := filepath.Join(sd.cacheDir, fmt.Sprintf("%s_%d_%ds.mp4", cacheKey, sd.height, sd.maxDuration))

	if _, err := os.Stat(cachePath); err == nil {
		logger.Info("Cache hit for clip", zap.String("clip_id", clip.ID))
		return &SmartDownloadResult{
			ClipID:   clip.ID,
			LocalPath: cachePath,
			Cached:   true,
			Duration: sd.maxDuration,
		}, nil
	}

	// Download with yt-dlp/curl
	rawPath, err := sd.downloadWithRetry(ctx, clip, 3)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	defer os.Remove(rawPath)

	// Smart trim: try to find action peak, fallback to center 7s
	trimmedPath, err := sd.smartTrim(rawPath, clip)
	if err != nil {
		return nil, fmt.Errorf("smart trim failed: %w", err)
	}

	// Upload to Drive with organized folder structure
	driveResult, err := sd.uploadToOrganizedFolder(trimmedPath, clip, term, videoID)
	if err != nil {
		os.Remove(trimmedPath)
		return nil, fmt.Errorf("upload failed: %w", err)
	}

	// Copy to cache for future reuse
	if err := os.Rename(trimmedPath, cachePath); err != nil {
		// If rename fails, copy instead
		cmd := exec.Command("cp", trimmedPath, cachePath)
		if cpErr := cmd.Run(); cpErr != nil {
			logger.Warn("Failed to cache clip (rename and cp both failed)",
				zap.String("clip_id", clip.ID),
				zap.Error(cpErr))
			// Don't fail the whole download just because caching failed
			return &SmartDownloadResult{
				ClipID:      clip.ID,
				LocalPath:   trimmedPath,
				DriveFileID: driveResult.DriveFileID,
				DriveURL:    driveResult.DriveURL,
				DrivePath:   driveResult.DrivePath,
				Filename:    driveResult.Filename,
				Cached:      false,
				Duration:    sd.maxDuration,
			}, nil
		}
		os.Remove(trimmedPath)
	}

	return &SmartDownloadResult{
		ClipID:      clip.ID,
		LocalPath:   cachePath,
		DriveFileID: driveResult.DriveFileID,
		DriveURL:    driveResult.DriveURL,
		DrivePath:   driveResult.DrivePath,
		Filename:    driveResult.Filename,
		Cached:      false,
		Duration:    sd.maxDuration,
	}, nil
}

// downloadWithRetry downloads a clip with exponential backoff retry.
func (sd *SmartDownloader) downloadWithRetry(ctx context.Context, clip artlistdb.ArtlistClip, maxRetries int) (string, error) {
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		rawPath, err := sd.download(ctx, clip)
		if err == nil {
			return rawPath, nil
		}

		lastErr = err
		if attempt < maxRetries {
			backoff := time.Duration(attempt*2) * time.Second
			logger.Debug("Download failed, retrying",
				zap.String("clip_id", clip.ID),
				zap.Int("attempt", attempt),
				zap.Duration("backoff", backoff),
				zap.Error(err))
			time.Sleep(backoff)
		}
	}

	return "", fmt.Errorf("all %d retries exhausted: %w", maxRetries, lastErr)
}

// download downloads a clip using yt-dlp (fallback: curl).
func (sd *SmartDownloader) download(ctx context.Context, clip artlistdb.ArtlistClip) (string, error) {
	tempDir := filepath.Join(sd.downloadDir, "temp")
	os.MkdirAll(tempDir, 0755)

	rawPath := filepath.Join(tempDir, fmt.Sprintf("%s_raw.mp4", clip.ID))

	// Try yt-dlp first
	cmd := exec.CommandContext(ctx, sd.ytDlpPath,
		"-o", rawPath,
		"--no-playlist",
		"--socket-timeout", "30",
		"--retries", "2",
		"--no-check-certificates",
		clip.URL)
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Run(); err == nil {
		if stat, err := os.Stat(rawPath); err == nil && stat.Size() > 0 {
			return rawPath, nil
		}
	}

	// Fallback to curl
	curlCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd = exec.CommandContext(curlCtx, "curl",
		"-L", "-s", "--max-time", "60",
		"--connect-timeout", "15",
		"-o", rawPath, clip.URL)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("curl download failed: %w", err)
	}

	if stat, err := os.Stat(rawPath); err != nil || stat.Size() == 0 {
		return "", fmt.Errorf("downloaded file is empty or missing")
	}

	return rawPath, nil
}

// smartTrim trims the clip intelligently:
// - If duration > maxDuration, takes the center 7s (assumed action peak)
// - Otherwise uses the full clip
func (sd *SmartDownloader) smartTrim(rawPath string, clip artlistdb.ArtlistClip) (string, error) {
	trimmedPath := filepath.Join(sd.cacheDir, fmt.Sprintf("%s_trimmed.mp4", clip.ID))

	// Get actual duration
	actualDuration, err := sd.getVideoDuration(rawPath)
	if err != nil {
		// Fallback: just convert without trimming
		return sd.convertVideo(rawPath, trimmedPath, 0, float64(sd.maxDuration))
	}

	// Calculate start time for center trim
	var startTime float64
	if actualDuration > float64(sd.maxDuration) {
		startTime = (actualDuration - float64(sd.maxDuration)) / 2.0
	}

	return sd.convertVideo(rawPath, trimmedPath, startTime, float64(sd.maxDuration))
}

// convertVideo converts a video with ffmpeg to target specs.
func (sd *SmartDownloader) convertVideo(rawPath, outputPath string, startTime, duration float64) (string, error) {
	ffmpegArgs := []string{
		"-y",
	}

	if startTime > 0 {
		ffmpegArgs = append(ffmpegArgs, "-ss", fmt.Sprintf("%.1f", startTime))
	}

	ffmpegArgs = append(ffmpegArgs,
		"-i", rawPath,
		"-t", fmt.Sprintf("%.1f", duration),
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2:black",
			sd.width, sd.height, sd.width, sd.height),
		"-c:v", "libx264",
		"-preset", "fast",
		"-crf", "23",
		"-c:a", "aac",
		"-b:a", "128k",
		"-movflags", "+faststart",
		outputPath,
	)

	cmd := exec.Command(sd.ffmpegPath, ffmpegArgs...)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ffmpeg convert failed: %w", err)
	}

	return outputPath, nil
}

// uploadToOrganizedFolder uploads to Drive with organized structure:
// Stock/Artlist/[Term]/[VideoID]/[SegmentIdx]/
func (sd *SmartDownloader) uploadToOrganizedFolder(localPath string, clip artlistdb.ArtlistClip, term, videoID string) (*UploadResult, error) {
	ctx := context.Background()

	// Create folder hierarchy: Artlist > Term > VideoID
	artlistRootID, err := sd.getOrCreateDriveFolder("Artlist", "")
	if err != nil {
		artlistRootID = ""
	}

	termFolderID, err := sd.getOrCreateDriveFolder(capitalize(term), artlistRootID)
	if err != nil {
		termFolderID = artlistRootID
	}

	videoFolderID, err := sd.getOrCreateDriveFolder(videoID, termFolderID)
	if err != nil {
		videoFolderID = termFolderID
	}

	// Upload file
	filename := fmt.Sprintf("%s_%s.mp4", clip.ID[:minInt(12, len(clip.ID))], term)
	fileID, err := sd.driveClient.UploadFile(ctx, localPath, videoFolderID, filename)
	if err != nil {
		return nil, err
	}

	result := &UploadResult{
		DriveFileID: fileID,
		DriveURL:    fmt.Sprintf("https://drive.google.com/file/d/%s/view", fileID),
		DriveFolder: fmt.Sprintf("Stock/Artlist/%s/%s", capitalize(term), videoID),
		Filename:    filename,
	}
	result.DrivePath = result.DriveFolder + "/" + filename

	return result, nil
}

// getOrCreateDriveFolder gets or creates a Drive folder.
func (sd *SmartDownloader) getOrCreateDriveFolder(name, parentID string) (string, error) {
	if sd.driveClient == nil {
		return "", fmt.Errorf("Drive client not available")
	}

	ctx := context.Background()

	folders, err := sd.driveClient.ListFolders(ctx, drive.ListFoldersOptions{
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

	return sd.driveClient.CreateFolder(ctx, name, parentID)
}

// getVideoDuration gets the duration of a video file in seconds.
func (sd *SmartDownloader) getVideoDuration(filePath string) (float64, error) {
	cmd := exec.Command(sd.ffmpegPath,
		"-i", filePath,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// ffprobe/ffmpeg returns non-zero exit for probe, but output still has duration
		// Log the error but try to parse anyway
		logger.Debug("ffprobe returned non-zero (expected for -i without output)",
			zap.String("file", filePath),
			zap.Error(err))
	}

	// Parse duration from ffmpeg output
	durationStr := extractDuration(string(output))
	if durationStr == "" {
		return 0, fmt.Errorf("could not parse duration from output")
	}

	// Parse HH:MM:SS.ms or MM:SS.ms
	var h, m, s float64
	fmt.Sscanf(durationStr, "%f:%f:%f", &h, &m, &s)
	return h*3600 + m*60 + s, nil
}

// extractDuration extracts duration string from ffmpeg output.
func extractDuration(output string) string {
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Duration:") {
			parts := strings.Split(line, "Duration:")
			if len(parts) > 1 {
				duration := strings.TrimSpace(parts[1])
				// Remove trailing comma if present
				duration = strings.Split(duration, ",")[0]
				return strings.TrimSpace(duration)
			}
		}
	}
	return ""
}

// computeCacheKey creates a unique cache key for a clip.
func computeCacheKey(clip artlistdb.ArtlistClip) string {
	h := sha256.Sum256([]byte(clip.ID + clip.URL + clip.OriginalURL))
	return hex.EncodeToString(h[:8])
}
