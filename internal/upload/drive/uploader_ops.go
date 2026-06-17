package drive

import (
	"context"
	"fmt"
	"io"
	"strings"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"velox/go-master/pkg/fileutil"

	drivequery "velox/go-master/internal/storage/drive"
)

// GetOrCreateFolder gets an existing folder or creates it.
func (u *Uploader) GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	if u.Service == nil {
		return "", fmt.Errorf("drive service not configured")
	}

	targetClean := fileutil.CleanFolderName(name)

	// List folders under parent to perform fuzzy/space-insensitive matching
	if parentID != "" {
		listQuery := fmt.Sprintf("'%s' in parents and trashed = false and mimeType = 'application/vnd.google-apps.folder'", parentID)
		list, err := u.Service.Files.List().Q(listQuery).Fields("files(id, name)").Context(ctx).Do()
		if err == nil && len(list.Files) > 0 {
			for _, file := range list.Files {
				if fileutil.CleanFolderName(file.Name) == targetClean {
					u.Log.Info("found existing folder via normalized matching",
						zap.String("folder_id", file.Id),
						zap.String("name", file.Name),
						zap.String("matched_target", name),
					)
					return file.Id, nil
				}
			}
		}
	}

	// Search for existing folder using exact case-sensitive query (as fallback)
	query := drivequery.BuildNameQuery(parentID, name, "application/vnd.google-apps.folder")
	list, err := u.Service.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err == nil && len(list.Files) > 0 {
		u.Log.Info("found existing folder via exact fallback search",
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

// GetFolderName returns the name of a Drive folder by its ID.
func (u *Uploader) GetFolderName(ctx context.Context, folderID string) (string, error) {
	if u.Service == nil {
		return "", fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(folderID) == "" {
		return "", nil
	}
	file, err := u.Service.Files.Get(folderID).Fields("name").Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return file.Name, nil
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

// RenameFile renames a file or folder on Google Drive.
func (u *Uploader) RenameFile(ctx context.Context, fileID, newName string) error {
	if u.Service == nil {
		return fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(fileID) == "" {
		return fmt.Errorf("file id is required")
	}
	if strings.TrimSpace(newName) == "" {
		return fmt.Errorf("new name is required")
	}

	_, err := u.Service.Files.Update(fileID, &driveapi.File{
		Name: newName,
	}).Fields("id", "name").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to rename drive file: %w", err)
	}

	u.Log.Info("drive file renamed",
		zap.String("file_id", fileID),
		zap.String("new_name", newName))
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

// FileMeta holds metadata about a Drive file.
type FileMeta struct {
	ID          string
	Name        string
	MimeType    string
	Size        int64
	WebViewLink string
	Parents     []string
}

// GetFileMeta retrieves metadata for a Drive file.
func (u *Uploader) GetFileMeta(ctx context.Context, fileID string) (*FileMeta, error) {
	if u.Service == nil {
		return nil, fmt.Errorf("drive service not configured")
	}
	f, err := u.Service.Files.Get(fileID).Fields("id, name, mimeType, size, webViewLink, parents").Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	return &FileMeta{
		ID:          f.Id,
		Name:        f.Name,
		MimeType:    f.MimeType,
		Size:        f.Size,
		WebViewLink: f.WebViewLink,
		Parents:     f.Parents,
	}, nil
}

// DownloadFile downloads a file from Drive and returns the response body and content type.
// The caller must close the returned io.ReadCloser.
func (u *Uploader) DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error) {
	if u.Service == nil {
		return nil, "", fmt.Errorf("drive service not configured")
	}
	resp, err := u.Service.Files.Get(fileID).Context(ctx).Download()
	if err != nil {
		return nil, "", err
	}
	return resp.Body, resp.Header.Get("Content-Type"), nil
}

// DriveFileInfo holds summary info for a file in a Drive listing.
type DriveFileInfo struct {
	ID             string
	Name           string
	MimeType       string
	WebViewLink    string
	WebContentLink string
	Parents        []string
}

// ListFiles lists all non-trashed files in a Drive folder.
func (u *Uploader) ListFiles(ctx context.Context, parentID string) ([]DriveFileInfo, error) {
	if u.Service == nil {
		return nil, fmt.Errorf("drive service not configured")
	}
	query := fmt.Sprintf("'%s' in parents and trashed=false", parentID)
	list, err := u.Service.Files.List().
		Q(query).
		Fields("nextPageToken, files(id, name, mimeType, webViewLink, webContentLink, parents)").
		PageSize(1000).
		Context(ctx).
		Do()
	if err != nil {
		return nil, err
	}

	result := make([]DriveFileInfo, 0, len(list.Files))
	for _, f := range list.Files {
		result = append(result, DriveFileInfo{
			ID:             f.Id,
			Name:           f.Name,
			MimeType:       f.MimeType,
			WebViewLink:    f.WebViewLink,
			WebContentLink: f.WebContentLink,
			Parents:        f.Parents,
		})
	}
	return result, nil
}

// MoveFile moves a file from one folder to another by updating its parents.
func (u *Uploader) MoveFile(ctx context.Context, fileID, fromFolderID, toFolderID string) error {
	if u.Service == nil {
		return fmt.Errorf("drive service not configured")
	}
	f, err := u.Service.Files.Get(fileID).Fields("id,parents").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to get file: %w", err)
	}
	var currentParents string
	if len(f.Parents) > 0 {
		currentParents = f.Parents[0]
	}
	_, err = u.Service.Files.Update(fileID, &driveapi.File{}).
		Fields("id,parents").
		AddParents(toFolderID).
		RemoveParents(currentParents).
		Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to move file: %w", err)
	}
	u.Log.Info("file moved", zap.String("file_id", fileID), zap.String("to_folder", toFolderID))
	return nil
}

// FileIsNotTrashed checks that a Drive file/folder exists AND is not in the trash.
// Google Drive's Files.Get returns the resource even for trashed items, so a simple
// existence check (FileExists) is not sufficient to detect trashed folders.
func (u *Uploader) FileIsNotTrashed(ctx context.Context, fileID string) (bool, error) {
	if u.Service == nil {
		return false, fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(fileID) == "" {
		return false, nil
	}

	file, err := u.Service.Files.Get(fileID).Fields("id", "trashed").Context(ctx).Do()
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "notFound") {
			return false, nil
		}
		return false, err
	}

	return !file.Trashed, nil
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
