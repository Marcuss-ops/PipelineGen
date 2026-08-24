package drive

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
)

// TrashFile moves a file to the trash in Google Drive.
// This is safer than permanent deletion as files can be recovered.
//
// Deprecated: use FileLifecycle.Trash instead.
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
//
// Deprecated: use FileLifecycle.Delete instead.
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
//
// Deprecated: use FileLifecycle.Rename instead.
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
	Trashed     bool
}

// GetFileMeta retrieves metadata for a Drive file.
func (u *Uploader) GetFileMeta(ctx context.Context, fileID string) (*FileMeta, error) {
	if u.Service == nil {
		return nil, fmt.Errorf("drive service not configured")
	}
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	f, err := u.Service.Files.Get(fileID).Fields("id, name, mimeType, size, webViewLink, parents, trashed").Context(requestCtx).Do()
	if err != nil {
		return nil, fmt.Errorf("get Drive metadata for %s: %w", fileID, err)
	}
	return &FileMeta{
		ID:          f.Id,
		Name:        f.Name,
		MimeType:    f.MimeType,
		Size:        f.Size,
		WebViewLink: f.WebViewLink,
		Parents:     f.Parents,
		Trashed:     f.Trashed,
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

// MoveFile moves a file from one folder to another (true "move" — read
// current parents + add new + remove old). Distinct from
// FileLifecycle.AddParent which only ADDS a new parent without removing
// the old one (multi-parent semantics). Per godlike/06 one-owner-per-fact:
// Admin.MoveFile owns the true-move semantic; FileLifecycle.AddParent owns
// multi-parent-add. The two are intentionally separate surfaces.
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
		if DriveIsNotFound(err) {
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
		if DriveIsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}
