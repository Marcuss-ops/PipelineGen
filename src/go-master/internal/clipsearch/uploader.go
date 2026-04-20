package clipsearch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

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
