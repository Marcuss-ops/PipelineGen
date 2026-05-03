package drive

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	drivequery "velox/go-master/pkg/drive"
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
		MD5Checksum:   created.Md5Checksum,
	}, nil
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

// openFile is a helper to open a file (easily mockable for tests).
var openFile = func(path string) (*os.File, error) {
	return os.Open(path)
}
