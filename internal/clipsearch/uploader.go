package clipsearch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"velox/go-master/internal/clip"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

type DriveUploader struct {
	driveClient    *drive.Client
	uploadFolderID string
	folderCache    map[string]string
	mu             sync.Mutex
}

type DriveTextUploadResult struct {
	DriveID  string
	Filename string
	DriveURL string
}

func NewDriveUploader(driveClient *drive.Client, uploadFolderID string) *DriveUploader {
	return &DriveUploader{
		driveClient:    driveClient,
		uploadFolderID: uploadFolderID,
		folderCache:    make(map[string]string),
	}
}

func (u *DriveUploader) UploadTextSidecar(ctx context.Context, folderID, baseVideoFilename, keyword, content string) (*DriveTextUploadResult, error) {
	if u.driveClient == nil {
		return nil, fmt.Errorf("Drive client not available")
	}
	if strings.TrimSpace(folderID) == "" {
		return nil, fmt.Errorf("target folder ID is empty")
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("empty sidecar content")
	}

	base := strings.TrimSuffix(baseVideoFilename, filepath.Ext(baseVideoFilename))
	if strings.TrimSpace(base) == "" {
		base = "clip_" + sanitizeFilename(keyword)
	}
	textFilename := base + ".txt"

	// Keep only one txt with this logical name in the target folder:
	// remove existing duplicates, then upload the refreshed one.
	if err := u.deleteFilesByName(ctx, folderID, textFilename); err != nil {
		return nil, err
	}

	tmp, err := os.CreateTemp("", "velox_clip_sidecar_*.txt")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.WriteString(content + "\n"); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}

	fileID, err := u.driveClient.UploadFile(ctx, tmpPath, folderID, textFilename)
	if err != nil {
		return nil, fmt.Errorf("failed to upload sidecar txt: %w", err)
	}
	return &DriveTextUploadResult{
		DriveID:  fileID,
		Filename: textFilename,
		DriveURL: fmt.Sprintf("https://drive.google.com/file/d/%s/view", fileID),
	}, nil
}

func (u *DriveUploader) deleteFilesByName(ctx context.Context, folderID, filename string) error {
	content, err := u.driveClient.GetFolderContent(ctx, folderID)
	if err != nil {
		return fmt.Errorf("failed to read target folder content: %w", err)
	}
	for _, f := range content.Files {
		if strings.TrimSpace(f.Name) != strings.TrimSpace(filename) {
			continue
		}
		if err := u.driveClient.DeleteFile(ctx, f.ID); err != nil {
			return fmt.Errorf("failed deleting existing txt %s (%s): %w", f.Name, f.ID, err)
		}
	}
	return nil
}

func (u *DriveUploader) UploadToDrive(ctx context.Context, filePath, keyword string) (*DriveUploadResult, error) {
	if u.driveClient == nil {
		return nil, fmt.Errorf("Drive client not available")
	}
	if strings.TrimSpace(u.uploadFolderID) == "" {
		return nil, fmt.Errorf("Artlist upload root folder not configured")
	}

	targetFolderID, folderName, err := u.getOrCreateKeywordFolder(ctx, keyword)
	if err != nil {
		return nil, err
	}

	filename := sanitizeFilename(keyword) + "_" + filepath.Base(filePath)
	fileID, err := u.driveClient.UploadFile(ctx, filePath, targetFolderID, filename)
	if err != nil {
		return nil, fmt.Errorf("failed to upload file: %w", err)
	}
	logger.Info("Dynamic clip uploaded in keyword folder",
		zap.String("keyword", keyword),
		zap.String("folder", folderName),
		zap.String("folder_id", targetFolderID),
		zap.String("file_id", fileID),
	)

	return &DriveUploadResult{
		DriveID:    fileID,
		Filename:   filename,
		DriveURL:   fmt.Sprintf("https://drive.google.com/file/d/%s/view", fileID),
		FolderID:   targetFolderID,
		FolderName: folderName,
		FolderPath: "Stock/Artlist/" + folderName,
	}, nil
}

func (u *DriveUploader) getOrCreateKeywordFolder(ctx context.Context, keyword string) (string, string, error) {
	folderName := sanitizeDriveFolderName(keyword)
	candidates := keywordFolderCandidates(keyword)

	// If upload root already matches keyword candidates, use it directly.
	if strings.TrimSpace(u.uploadFolderID) != "" && u.driveClient != nil {
		if root, err := u.driveClient.GetFile(ctx, u.uploadFolderID); err == nil && root != nil {
			rootNorm := normalizeFolderComparable(root.Name)
			for _, c := range candidates {
				if normalizeFolderComparable(c) == rootNorm {
					return u.uploadFolderID, root.Name, nil
				}
			}
		}
	}

	u.mu.Lock()
	if id, ok := u.folderCache[folderName]; ok && strings.TrimSpace(id) != "" {
		u.mu.Unlock()
		return id, folderName, nil
	}
	u.mu.Unlock()

	if existingID, existingName, err := u.findExistingKeywordFolder(ctx, candidates); err == nil && strings.TrimSpace(existingID) != "" {
		u.mu.Lock()
		u.folderCache[folderName] = existingID
		u.folderCache[existingName] = existingID
		u.mu.Unlock()
		logger.Info("Reusing existing Drive keyword folder",
			zap.String("keyword", keyword),
			zap.String("requested_folder", folderName),
			zap.String("resolved_folder", existingName),
			zap.String("folder_id", existingID),
		)
		return existingID, existingName, nil
	}

	folderID, err := u.driveClient.GetOrCreateFolder(ctx, folderName, u.uploadFolderID)
	if err != nil {
		return "", folderName, fmt.Errorf("failed to get/create Drive folder %q: %w", folderName, err)
	}

	u.mu.Lock()
	u.folderCache[folderName] = folderID
	u.mu.Unlock()

	return folderID, folderName, nil
}

func (u *DriveUploader) findExistingKeywordFolder(ctx context.Context, candidates []string) (string, string, error) {
	if len(candidates) == 0 {
		return "", "", nil
	}

	normalizedCandidates := make([]string, 0, len(candidates))
	for _, c := range candidates {
		n := normalizeFolderComparable(c)
		if n != "" {
			normalizedCandidates = append(normalizedCandidates, n)
		}
	}
	if len(normalizedCandidates) == 0 {
		return "", "", nil
	}

	folders, err := u.driveClient.ListFolders(ctx, drive.ListFoldersOptions{
		ParentID: u.uploadFolderID,
		MaxDepth: 4,
		MaxItems: 500,
	})
	if err != nil {
		return "", "", err
	}
	allFolders := flattenFolders(folders)

	for _, candidate := range normalizedCandidates {
		for _, folder := range allFolders {
			if normalizeFolderComparable(folder.Name) == candidate {
				return folder.ID, folder.Name, nil
			}
		}
	}

	return "", "", nil
}

func flattenFolders(folders []drive.Folder) []drive.Folder {
	out := make([]drive.Folder, 0, len(folders))
	var walk func(items []drive.Folder)
	walk = func(items []drive.Folder) {
		for _, f := range items {
			out = append(out, f)
			if len(f.Subfolders) > 0 {
				walk(f.Subfolders)
			}
		}
	}
	walk(folders)
	return out
}

func (u *DriveUploader) SetUploadFolderID(folderID string) {
	u.mu.Lock()
	u.uploadFolderID = strings.TrimSpace(folderID)
	u.mu.Unlock()
}

func (u *DriveUploader) ClearCache() {
	u.mu.Lock()
	u.folderCache = make(map[string]string)
	u.mu.Unlock()
}

func searchResultFromDrive(kw string, driveResult *DriveUploadResult) SearchResult {
	folder := driveResult.FolderPath
	if folder == "" {
		folder = "Stock/Artlist/" + kw
	}
	return SearchResult{
		Keyword:      kw,
		ClipID:       driveResult.DriveID,
		Filename:     driveResult.Filename,
		DriveURL:     driveResult.DriveURL,
		DriveID:      driveResult.DriveID,
		Folder:       folder,
		FolderID:     driveResult.FolderID,
		TextDriveID:  driveResult.TextFileID,
		TextDriveURL: driveResult.TextURL,
	}
}

func (s *Service) uploadClipSidecarText(ctx context.Context, keyword string, driveResult *DriveUploadResult, content string) {
	// Default behavior: avoid per-clip txt explosion in Drive.
	// Enable only if explicitly requested.
	if strings.ToLower(strings.TrimSpace(os.Getenv("VELOX_ENABLE_PER_CLIP_TXT"))) != "true" {
		return
	}
	if s.uploader == nil || driveResult == nil || strings.TrimSpace(content) == "" {
		return
	}
	res, err := s.uploader.UploadTextSidecar(ctx, driveResult.FolderID, driveResult.Filename, keyword, content)
	if err != nil {
		logger.Warn("Failed to upload clip sidecar text",
			zap.String("keyword", keyword),
			zap.String("drive_id", driveResult.DriveID),
			zap.Error(err),
		)
		return
	}
	driveResult.TextFileID = res.DriveID
	driveResult.TextURL = res.DriveURL
	driveResult.TextName = res.Filename
}

func buildArtlistClipSidecarText(keyword string, c clip.IndexedClip) string {
	var b strings.Builder
	b.WriteString("keyword: " + strings.TrimSpace(keyword) + "\n")
	b.WriteString("source: artlist\n")
	if strings.TrimSpace(c.ID) != "" {
		b.WriteString("clip_id: " + strings.TrimSpace(c.ID) + "\n")
	}
	if strings.TrimSpace(c.Name) != "" {
		b.WriteString("title: " + strings.TrimSpace(c.Name) + "\n")
	}
	if strings.TrimSpace(c.DownloadLink) != "" {
		b.WriteString("source_url: " + strings.TrimSpace(c.DownloadLink) + "\n")
	} else if strings.TrimSpace(c.DriveLink) != "" {
		b.WriteString("source_url: " + strings.TrimSpace(c.DriveLink) + "\n")
	}
	if len(c.Tags) > 0 {
		b.WriteString("tags: " + strings.Join(c.Tags, ", ") + "\n")
	}
	b.WriteString("\ntranscript:\n")
	b.WriteString("Not available for Artlist source in current pipeline.\n")
	return b.String()
}

func buildYouTubeClipSidecarText(keyword string, m *YouTubeClipMetadata) string {
	var b strings.Builder
	b.WriteString("keyword: " + strings.TrimSpace(keyword) + "\n")
	b.WriteString("source: youtube\n")
	if m == nil {
		b.WriteString("note: metadata unavailable (fallback download path)\n")
		return b.String()
	}
	if strings.TrimSpace(m.VideoID) != "" {
		b.WriteString("video_id: " + strings.TrimSpace(m.VideoID) + "\n")
	}
	if strings.TrimSpace(m.VideoURL) != "" {
		b.WriteString("video_url: " + strings.TrimSpace(m.VideoURL) + "\n")
	}
	if strings.TrimSpace(m.Title) != "" {
		b.WriteString("title: " + strings.TrimSpace(m.Title) + "\n")
	}
	if strings.TrimSpace(m.Channel) != "" {
		b.WriteString("channel: " + strings.TrimSpace(m.Channel) + "\n")
	}
	if strings.TrimSpace(m.Uploader) != "" {
		b.WriteString("uploader: " + strings.TrimSpace(m.Uploader) + "\n")
	}
	if m.ViewCount > 0 {
		b.WriteString(fmt.Sprintf("views: %d\n", m.ViewCount))
	}
	if m.DurationSec > 0 {
		b.WriteString(fmt.Sprintf("duration_sec: %.1f\n", m.DurationSec))
	}
	if strings.TrimSpace(m.UploadDate) != "" {
		b.WriteString("upload_date: " + strings.TrimSpace(m.UploadDate) + "\n")
	}
	if strings.TrimSpace(m.SearchQuery) != "" {
		b.WriteString("search_query: " + strings.TrimSpace(m.SearchQuery) + "\n")
	}
	if m.Relevance != 0 {
		b.WriteString(fmt.Sprintf("relevance_score: %d\n", m.Relevance))
	}
	if m.SelectedMoment != nil {
		b.WriteString(fmt.Sprintf("selected_moment_start_sec: %.1f\n", m.SelectedMoment.StartSec))
		b.WriteString(fmt.Sprintf("selected_moment_end_sec: %.1f\n", m.SelectedMoment.EndSec))
		if strings.TrimSpace(m.SelectedMoment.Reason) != "" {
			b.WriteString("selected_moment_reason: " + strings.TrimSpace(m.SelectedMoment.Reason) + "\n")
		}
		if strings.TrimSpace(m.SelectedMoment.Source) != "" {
			b.WriteString("selected_moment_source: " + strings.TrimSpace(m.SelectedMoment.Source) + "\n")
		}
	}
	if hash := buildYouTubeInterviewHash(m); hash != "" {
		b.WriteString("interview_hash: " + hash + "\n")
	}
	if strings.TrimSpace(m.Description) != "" {
		b.WriteString("\ndescription:\n")
		b.WriteString(strings.TrimSpace(m.Description) + "\n")
	}
	b.WriteString("\ntranscript:\n")
	if strings.TrimSpace(m.Transcript) != "" {
		b.WriteString(strings.TrimSpace(m.Transcript) + "\n")
	} else {
		b.WriteString("Subtitles/transcript not available from source.\n")
	}
	return b.String()
}
