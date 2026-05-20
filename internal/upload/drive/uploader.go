package drive

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"velox/go-master/internal/pkg/hashutil"
	drivequery "velox/go-master/internal/storage/drive"
)

// Uploader handles Google Drive file operations.
type Uploader struct {
	Service *driveapi.Service
	Log     *zap.Logger
}

// UploadResult holds the result of a file upload.
type UploadResult struct {
	FileID       string `json:"file_id"`
	WebViewLink  string `json:"web_view_link"`
	DownloadLink string `json:"download_link"`
	MD5Checksum  string `json:"md5_checksum"`
}

// RemoteFile describes a file already present on Google Drive.
type RemoteFile struct {
	FileID      string
	Name        string
	WebViewLink string
	MD5Checksum string
}

// UploadFile uploads a file to the specified Drive folder.
// This properly uses .Media(f) to upload the file content (unlike the broken artlist/drive_uploader.go).
func (u *Uploader) UploadFile(ctx context.Context, localPath, folderID, filename string) (*UploadResult, error) {
	if u.Service == nil {
		return nil, fmt.Errorf("drive service not configured")
	}

	f, err := openFile(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	file := &driveapi.File{
		Name: filename,
	}
	if folderID != "" {
		file.Parents = []string{folderID}
	}

	start := time.Now()
	u.Log.Info("uploading file to drive",
		zap.String("file_path", localPath),
		zap.String("folder_id", folderID),
		zap.String("filename", filename),
	)

	created, err := u.Service.Files.Create(file).
		Fields("id,webViewLink,md5Checksum").
		Media(f).
		Context(ctx).
		Do()
	if err != nil {
		u.Log.Error("failed to upload file",
			zap.String("file_path", localPath),
			zap.Error(err),
		)
		return nil, fmt.Errorf("drive upload failed: %w", err)
	}

	u.Log.Info("file uploaded successfully",
		zap.String("file_id", created.Id),
		zap.Duration("duration", time.Since(start)),
	)

	return &UploadResult{
		FileID:       created.Id,
		WebViewLink:  created.WebViewLink,
		DownloadLink: "https://drive.google.com/uc?id=" + created.Id,
		MD5Checksum:  created.Md5Checksum,
	}, nil
}

// FindFileByName returns the first non-trashed file in a folder with the given name.
func (u *Uploader) FindFileByName(ctx context.Context, folderID, filename string) (*RemoteFile, error) {
	if u.Service == nil {
		return nil, fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(folderID) == "" || strings.TrimSpace(filename) == "" {
		return nil, nil
	}

	query := fmt.Sprintf("name = '%s' and '%s' in parents and trashed = false", strings.ReplaceAll(filename, "'", "\\'"), folderID)
	list, err := u.Service.Files.List().
		Q(query).
		Fields("files(id, name, webViewLink, md5Checksum)").
		Context(ctx).
		Do()
	if err != nil {
		return nil, err
	}
	if len(list.Files) == 0 {
		return nil, nil
	}

	file := list.Files[0]
	return &RemoteFile{
		FileID:      file.Id,
		Name:        file.Name,
		WebViewLink: file.WebViewLink,
		MD5Checksum: file.Md5Checksum,
	}, nil
}

// UploadFileIfChanged uploads a file only when the Drive file does not already exist with the same hash.
func (u *Uploader) UploadFileIfChanged(ctx context.Context, localPath, folderID, filename string) (*UploadResult, bool, error) {
	localHash, err := hashutil.MD5File(localPath)
	if err != nil {
		return nil, false, fmt.Errorf("failed to hash local file: %w", err)
	}

	existing, err := u.FindFileByName(ctx, folderID, filename)
	if err != nil {
		return nil, false, err
	}
	if existing != nil && existing.MD5Checksum != "" && strings.EqualFold(existing.MD5Checksum, localHash) {
		return &UploadResult{
			FileID:       existing.FileID,
			WebViewLink:  existing.WebViewLink,
			DownloadLink: "https://drive.google.com/uc?id=" + existing.FileID,
			MD5Checksum:  existing.MD5Checksum,
		}, true, nil
	}

	result, err := u.UploadFile(ctx, localPath, folderID, filename)
	if err != nil {
		return nil, false, err
	}
	return result, false, nil
}

// GetOrCreateFolder gets an existing folder or creates it.
func (u *Uploader) GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	if u.Service == nil {
		return "", fmt.Errorf("drive service not configured")
	}

	// Search for existing folder
	query := drivequery.BuildNameQuery(parentID, name, "application/vnd.google-apps.folder")
	list, err := u.Service.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err != nil {
		u.Log.Error("failed to search for folder",
			zap.String("name", name),
			zap.Error(err),
		)
		return "", fmt.Errorf("failed to search folder: %w", err)
	}

	// Return existing folder if found
	if len(list.Files) > 0 {
		u.Log.Info("found existing folder",
			zap.String("folder_id", list.Files[0].Id),
			zap.String("name", name),
		)
		return list.Files[0].Id, nil
	}

	// Create new folder
	folder := &driveapi.File{
		Name:     name,
		MimeType: "application/vnd.google-apps.folder",
	}
	if parentID != "" {
		folder.Parents = []string{parentID}
	}

	created, err := u.Service.Files.Create(folder).Fields("id").Context(ctx).Do()
	if err != nil {
		u.Log.Error("failed to create folder",
			zap.String("name", name),
			zap.Error(err),
		)
		return "", fmt.Errorf("failed to create folder: %w", err)
	}

	u.Log.Info("folder created",
		zap.String("folder_id", created.Id),
		zap.String("name", name),
	)

	return created.Id, nil
}

// TrashFile moves a file to the trash in Google Drive.
// This is safer than permanent deletion as files can be recovered.
func (u *Uploader) TrashFile(ctx context.Context, fileID string) error {
	if u.Service == nil {
		return fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(fileID) == "" {
		return fmt.Errorf("file id is required")
	}

	_, err := u.Service.Files.Update(fileID, &driveapi.File{
		Trashed: true,
	}).Fields("id", "trashed").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to trash drive file: %w", err)
	}

	if u.Log != nil {
		u.Log.Info("drive file moved to trash", zap.String("file_id", fileID))
	}
	return nil
}

// DeleteFile permanently deletes a file from Google Drive.
// Use TrashFile instead for safer operations.
func (u *Uploader) DeleteFile(ctx context.Context, fileID string) error {
	if u.Service == nil {
		return fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(fileID) == "" {
		return fmt.Errorf("file id is required")
	}
	if err := u.Service.Files.Delete(fileID).Context(ctx).Do(); err != nil {
		return fmt.Errorf("failed to delete drive file: %w", err)
	}

	if u.Log != nil {
		u.Log.Info("drive file deleted", zap.String("file_id", fileID))
	}
	return nil
}

// TrashFolder moves a folder to trash in Google Drive.
func (u *Uploader) TrashFolder(ctx context.Context, folderID string) error {
	if u.Service == nil {
		return fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(folderID) == "" {
		return fmt.Errorf("folder id is required")
	}

	_, err := u.Service.Files.Update(folderID, &driveapi.File{
		Trashed: true,
	}).Fields("id", "trashed").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to trash drive folder: %w", err)
	}

	if u.Log != nil {
		u.Log.Info("drive folder moved to trash", zap.String("folder_id", folderID))
	}
	return nil
}

// DeleteFolder permanently deletes a folder from Google Drive.
func (u *Uploader) DeleteFolder(ctx context.Context, folderID string) error {
	if u.Service == nil {
		return fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(folderID) == "" {
		return fmt.Errorf("folder id is required")
	}

	if err := u.Service.Files.Delete(folderID).Context(ctx).Do(); err != nil {
		return fmt.Errorf("failed to delete drive folder: %w", err)
	}

	if u.Log != nil {
		u.Log.Info("drive folder deleted", zap.String("folder_id", folderID))
	}
	return nil
}

// GetFileMD5 retrieves the MD5 checksum of a file from Google Drive.
func (u *Uploader) GetFileMD5(ctx context.Context, fileID string) (string, error) {
	if u.Service == nil {
		return "", fmt.Errorf("drive service not configured")
	}
	file, err := u.Service.Files.Get(fileID).Fields("id,md5Checksum").Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return file.Md5Checksum, nil
}

// openFile is a helper to open a file (easily mockable for tests).
var openFile = func(path string) (*os.File, error) {
	return os.Open(path)
}

// FileExists checks if a file exists on Google Drive.
func (u *Uploader) FileExists(ctx context.Context, fileID string) (bool, error) {
	if u.Service == nil {
		return false, fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(fileID) == "" {
		return false, nil
	}

	_, err := u.Service.Files.Get(fileID).Fields("id", "trashed").Context(ctx).Do()
	if err != nil {
		// Check if it's a 404
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "notFound") {
			return false, nil
		}
		return false, err
	}

	return true, nil
}
