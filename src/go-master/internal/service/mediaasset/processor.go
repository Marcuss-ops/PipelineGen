package mediaasset

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/hashutil"
	"velox/go-master/pkg/media/downloader"
	"velox/go-master/pkg/media/ffmpeg"
)

type Processor struct {
	dl       YTDLP
	httpDL   HTTPDownloader
	ffmpeg   VideoProcessor
	driveSvc *driveapi.Service
	log      *zap.Logger
	dataDir  string
	tempDir  string
	videoCfg ffmpeg.NormalizeOptions
}

type ProcessorConfig struct {
	DataDir  string
	TempDir  string
	VideoCfg ffmpeg.NormalizeOptions
}

func NewProcessor(
	dl YTDLP,
	httpDL HTTPDownloader,
	ff VideoProcessor,
	driveSvc *driveapi.Service,
	log *zap.Logger,
	cfg ProcessorConfig,
) *Processor {
	return &Processor{
		dl:       dl,
		httpDL:   httpDL,
		ffmpeg:   ff,
		driveSvc: driveSvc,
		log:      log,
		dataDir:  cfg.DataDir,
		tempDir:  cfg.TempDir,
		videoCfg: cfg.VideoCfg,
	}
}

// DownloadProcessUpload orchestrates the full pipeline: download, process, hash, upload.
// This is a facade method that delegates to smaller internal methods.
func (p *Processor) DownloadProcessUpload(ctx context.Context, input AssetInput) (*AssetResult, error) {
	result := &AssetResult{
		ID:     input.ID,
		Status: "failed",
	}

	// Setup paths
	tmpDir, saveDir := p.setupDirectories(input)
	finalFilename := SafeName(input.Name) + "_" + input.ID + ".mp4"
	processedPath := OutputPath(saveDir, finalFilename)

	// Step 1: Download (use path without extension so yt-dlp can add %(ext)s correctly)
	rawPath := TmpPath(tmpDir, fmt.Sprintf("raw_%s", input.ID))
	actualRawPath, err := p.downloadStep(ctx, input, rawPath)
	if err != nil {
		result.Error = fmt.Sprintf("download failed: %v", err)
		return result, err
	}

	// Step 2: Process/Normalize
	processedPath, err = p.processStep(ctx, input, actualRawPath, processedPath)
	if err != nil {
		_ = os.Remove(actualRawPath)
		result.Error = fmt.Sprintf("process failed: %v", err)
		return result, err
	}

	// Step 3: Hash
	fileHash, err := p.hashStep(ctx, processedPath)
	if err != nil {
		_ = os.Remove(actualRawPath)
		_ = os.Remove(processedPath)
		result.Error = fmt.Sprintf("hash failed: %v", err)
		return result, err
	}
	result.FileHash = fileHash
	result.LocalPath = processedPath
	result.Filename = filepath.Base(processedPath)

	// Cleanup raw file after processing
	_ = os.Remove(actualRawPath)

	// Step 4: Upload to Drive
	if err := p.uploadStep(ctx, input, processedPath, result); err != nil {
		result.Error = fmt.Sprintf("upload failed: %v", err)
		return result, err
	}

	result.Status = "processed"
	return result, nil
}

// setupDirectories creates temp and save directories, returning their paths.
func (p *Processor) setupDirectories(input AssetInput) (tmpDir, saveDir string) {
	tmpDir = filepath.Join(p.dataDir, p.tempDir)
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		p.log.Error("failed to create temp directory", zap.String("dir", tmpDir), zap.Error(err))
		tmpDir = os.TempDir()
	}

	saveDir = input.OutputDir
	if saveDir == "" {
		saveDir = filepath.Join(p.dataDir, "mediaassets", SafeName(input.Term))
	}
	if err := os.MkdirAll(saveDir, 0755); err != nil {
		p.log.Error("failed to create save directory", zap.String("dir", saveDir), zap.Error(err))
		saveDir = tmpDir
	}

	return tmpDir, saveDir
}

// downloadStep downloads the asset from the source URL.
func (p *Processor) downloadStep(ctx context.Context, input AssetInput, rawPath string) (actualPath string, err error) {
	// Try HTTP download first for direct URLs (e.g., Artlist with direct links)
	if p.httpDL != nil && p.isDirectURL(input.SourceURL) {
		p.log.Info("using HTTP downloader for direct URL", zap.String("id", input.ID), zap.String("url", input.SourceURL))
		httpReq := &downloader.HTTPDownloadRequest{
			URL:        input.SourceURL,
			OutputPath: rawPath,
		}
		if err := p.httpDL.Download(ctx, httpReq); err != nil {
			p.log.Warn("HTTP download failed, falling back to yt-dlp", zap.Error(err))
			// Fall through to yt-dlp
		} else {
			p.log.Info("HTTP download succeeded", zap.String("path", rawPath))
			return rawPath, nil
		}
	}

	// Use FFmpeg for HLS URLs (e.g., Artlist .m3u8 streams)
	if p.ffmpeg != nil && p.isHLSURL(input.SourceURL) {
		p.log.Info("using FFmpeg for HLS URL",
			zap.String("id", input.ID),
			zap.String("url", input.SourceURL),
		)

		if err := p.ffmpeg.RemuxHLS(ctx, input.SourceURL, rawPath); err != nil {
			p.log.Warn("FFmpeg HLS remux failed, falling back to yt-dlp", zap.Error(err))
		} else {
			p.log.Info("FFmpeg HLS remux succeeded", zap.String("path", rawPath))
			return rawPath, nil
		}
	}

	// Use yt-dlp for complex URLs (YouTube, etc.)
	dlReq := &downloader.DownloadRequest{
		URL:              input.SourceURL,
		OutputPath:       rawPath,
		ForceKeyframes:   input.ForceKeyframes,
		DownloadSections: input.DownloadSections,
	}
	if len(input.DownloadSections) > 0 {
		dlReq.Format = "bv*[height<=1080][ext=mp4]+ba[ext=m4a]/b[height<=1080][ext=mp4]/best[height<=1080]"
		dlReq.MergeFormat = "mp4"
		dlReq.NoPlaylist = true
		dlReq.Timeout = 10 * time.Minute
	}

	p.log.Info("downloading asset with yt-dlp", zap.String("id", input.ID), zap.String("url", input.SourceURL), zap.Strings("sections", input.DownloadSections))
	if err := p.dl.Download(ctx, dlReq); err != nil {
		return "", err
	}

	actualPath = ResolveDownloadedFile(rawPath)
	if actualPath != rawPath {
		p.log.Info("resolved actual download path", zap.String("expected", rawPath), zap.String("actual", actualPath))
	}

	return actualPath, nil
}

// isDirectURL checks if URL is likely a direct download (not needing yt-dlp).
func (p *Processor) isDirectURL(url string) bool {
	// Check for known direct download patterns
	directPatterns := []string{
		"artlist.io/download",
		"artlist.io/api",
		".mp4",
		".mov",
		".avi",
	}
	for _, pattern := range directPatterns {
		if strings.Contains(url, pattern) {
			return true
		}
	}
	return false
}

// isHLSURL checks if URL points to an HLS playlist.
func (p *Processor) isHLSURL(url string) bool {
	u := strings.ToLower(strings.TrimSpace(url))
	return strings.Contains(u, ".m3u8")
}

// processStep normalizes/processes the video if needed.
func (p *Processor) processStep(ctx context.Context, input AssetInput, rawPath, processedPath string) (string, error) {
	shouldNormalize := input.Normalize == nil || *input.Normalize
	if !shouldNormalize {
		p.log.Info("skipping normalization as requested, moving raw to processed path", zap.String("id", input.ID))
		// Move raw file to processed path
		if err := os.Rename(rawPath, processedPath); err != nil {
			// If rename fails (cross-device), try copy
			p.log.Warn("rename failed, attempting copy", zap.Error(err))
			if err := copyFile(rawPath, processedPath); err != nil {
				return "", fmt.Errorf("failed to move raw file to processed path: %w", err)
			}
		}
		return processedPath, nil
	}

	opts := p.videoCfg
	opts.KeepAudio = input.KeepAudio
	opts.DisableDuration = input.DisableDuration

	p.log.Info("processing video", zap.String("id", input.ID), zap.String("output", processedPath), zap.Bool("disable_duration", opts.DisableDuration))
	if err := p.ffmpeg.Normalize(ctx, rawPath, processedPath, opts); err != nil {
		return "", err
	}

	return processedPath, nil
}

// hashStep calculates the MD5 hash of the processed file.
func (p *Processor) hashStep(ctx context.Context, path string) (string, error) {
	p.log.Info("calculating file hash", zap.String("path", path))
	return hashutil.MD5File(path)
}

// uploadStep uploads the processed file to Google Drive.
func (p *Processor) uploadStep(ctx context.Context, input AssetInput, path string, result *AssetResult) error {
	if p.driveSvc == nil || input.FolderID == "" {
		return nil
	}

	filename := filepath.Base(path)
	p.log.Info("uploading to Drive", zap.String("id", input.ID), zap.String("filename", filename))

	uploader := &drive.Uploader{Service: p.driveSvc, Log: p.log}
	uploadResult, err := uploader.UploadFile(ctx, path, input.FolderID, filename)
	if err != nil {
		return err
	}

	result.DriveLink = uploadResult.WebViewLink
	result.DownloadLink = "https://drive.google.com/uc?id=" + uploadResult.FileID
	p.log.Info("drive upload success", zap.String("id", input.ID), zap.String("file_id", uploadResult.FileID))

	return nil
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
