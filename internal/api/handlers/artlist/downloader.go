package artlistpipeline

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// Downloader handles clip download, conversion, and Drive upload.
type Downloader struct {
	driveClient *drive.Client
	downloadDir string
	ytDlpPath   string
	ffmpegPath  string
}

// NewDownloader creates a new clip downloader.
func NewDownloader(driveClient *drive.Client, downloadDir, ytDlpPath, ffmpegPath string) *Downloader {
	return &Downloader{
		driveClient: driveClient,
		downloadDir: downloadDir,
		ytDlpPath:   ytDlpPath,
		ffmpegPath:  ffmpegPath,
	}
}

// ProcessResult holds the result of processing a single clip.
type ProcessResult struct {
	Keyword      string
	ClipID       string
	LocalPath    string
	DriveFileID  string
	DriveURL     string
	Filename     string
	DriveFolder  string
}

// ProcessClip downloads a clip, converts to 1080p, trims to 7s, uploads to Drive.
func (d *Downloader) ProcessClip(clip artlistdb.ArtlistClip, keyword, topic string) (*ProcessResult, error) {
	// Check if already downloaded locally
	if clip.DownloadPath != "" {
		if _, err := os.Stat(clip.DownloadPath); err == nil {
			return &ProcessResult{
				Keyword:     keyword,
				ClipID:      clip.ID,
				LocalPath:   clip.DownloadPath,
				DriveFileID: clip.DriveFileID,
				DriveURL:    clip.DriveURL,
				Filename:    clip.VideoID,
			}, nil
		}
	}

	// Download
	rawPath, err := d.download(clip, keyword)
	if err != nil {
		return nil, err
	}

	// Convert to 1080p, 7s
	outputPath, err := d.convert(rawPath, clip.VideoID, keyword)
	if err != nil {
		os.Remove(rawPath)
		return nil, err
	}

	// Upload to Drive with subfolder
	filename := fmt.Sprintf("%s_%s.mp4", keyword, clip.VideoID)
	result, err := d.upload(outputPath, keyword, filename)
	if err != nil {
		os.Remove(rawPath)
		os.Remove(outputPath)
		return nil, err
	}

	// Cleanup raw file
	os.Remove(rawPath)

	return &ProcessResult{
		Keyword:     keyword,
		ClipID:      clip.ID,
		LocalPath:   outputPath,
		DriveFileID: result.DriveFileID,
		DriveURL:    result.DriveURL,
		Filename:    filename,
		DriveFolder: result.DriveFolder,
	}, nil
}

// download downloads a clip using yt-dlp (fallback: curl).
func (d *Downloader) download(clip artlistdb.ArtlistClip, keyword string) (string, error) {
	outputDir := filepath.Join(d.downloadDir, "artlist", keyword)
	os.MkdirAll(outputDir, 0755)

	rawPath := filepath.Join(outputDir, fmt.Sprintf("%s_raw.mp4", clip.VideoID))

	// Try yt-dlp first
	cmd := exec.Command(d.ytDlpPath,
		"-o", rawPath, "--no-playlist", "--socket-timeout", "30", "--retries", "3", clip.URL)
	if err := cmd.Run(); err != nil {
		// Fallback to curl
		cmd = exec.Command("curl", "-L", "-o", rawPath, clip.URL)
		if err := cmd.Run(); err != nil {
			return "", fmt.Errorf("download failed (yt-dlp and curl): %w", err)
		}
	}

	// Verify file
	stat, err := os.Stat(rawPath)
	if err != nil || stat.Size() == 0 {
		return "", fmt.Errorf("downloaded file is empty or missing: %s", rawPath)
	}

	return rawPath, nil
}

// convert converts a clip to 1920x1080, max 7 seconds.
func (d *Downloader) convert(rawPath, videoID, keyword string) (string, error) {
	outputPath := filepath.Join(filepath.Dir(rawPath), fmt.Sprintf("%s_1080p_7s.mp4", videoID))

	ffmpegArgs := []string{
		"-y", "-i", rawPath,
		"-t", "7",
		"-vf", "scale=1920:1080:force_original_aspect_ratio=decrease,pad=1920:1080:(ow-iw)/2:(oh-ih)/2:black",
		"-c:v", "libx264", "-preset", "fast", "-crf", "23",
		"-c:a", "aac", "-b:a", "128k",
		"-movflags", "+faststart",
		outputPath,
	}

	if err := exec.Command(d.ffmpegPath, ffmpegArgs...).Run(); err != nil {
		return "", fmt.Errorf("ffmpeg convert failed: %w", err)
	}

	return outputPath, nil
}

// UploadResult holds upload metadata.
type UploadResult struct {
	DriveFileID string
	DriveURL    string
	DriveFolder string
	DrivePath   string
	Filename    string
}

// upload uploads to Drive in Stock/Artlist/[Term]/ subfolder.
func (d *Downloader) upload(localPath, keyword, filename string) (*UploadResult, error) {
	folderName := capitalize(keyword)

	artlistRootID, err := d.getOrCreateFolder("Artlist", "")
	if err != nil {
		logger.Warn("Failed to get/create Artlist root, using Drive root", zap.Error(err))
		return d.uploadToRoot(localPath, filename)
	}

	termFolderID, err := d.getOrCreateFolder(folderName, artlistRootID)
	if err != nil {
		logger.Warn("Failed to create term folder, using Artlist root",
			zap.String("folder", folderName), zap.Error(err))
		return d.uploadToFolder(localPath, filename, artlistRootID, "Stock/Artlist")
	}

	return d.uploadToFolder(localPath, filename, termFolderID,
		fmt.Sprintf("Stock/Artlist/%s", folderName))
}

// uploadToRoot uploads to Drive root folder.
func (d *Downloader) uploadToRoot(localPath, filename string) (*UploadResult, error) {
	fileID, err := d.driveClient.UploadFile(context.Background(), localPath, "", filename)
	if err != nil {
		return nil, err
	}
	return &UploadResult{
		DriveFileID: fileID,
		DriveURL:    fmt.Sprintf("https://drive.google.com/file/d/%s/view", fileID),
		DriveFolder: "Stock/Artlist",
	}, nil
}

// uploadToFolder uploads to a specific Drive folder.
func (d *Downloader) uploadToFolder(localPath, filename, folderID, folderPath string) (*UploadResult, error) {
	fileID, err := d.driveClient.UploadFile(context.Background(), localPath, folderID, filename)
	if err != nil {
		return nil, err
	}
	return &UploadResult{
		DriveFileID: fileID,
		DriveURL:    fmt.Sprintf("https://drive.google.com/file/d/%s/view", fileID),
		DriveFolder: folderPath,
	}, nil
}

// getOrCreateFolder gets or creates a Drive folder.
func (d *Downloader) getOrCreateFolder(name, parentID string) (string, error) {
	ctx := context.Background()

	folders, err := d.driveClient.ListFolders(ctx, drive.ListFoldersOptions{
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

	return d.driveClient.CreateFolder(ctx, name, parentID)
}
