package drive

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	"google.golang.org/api/drive/v3"
)

// Uploader handles Google Drive file operations.
type Uploader struct {
	Service *drive.Service
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

	file := &drive.File{
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
		DownloadLink:  "https://drive.google.com/uc?id=" + created.Id,
		MD5Checksum:   created.Md5Checksum,
	}, nil
}

// GetOrCreateFolder gets an existing folder or creates it.
func (u *Uploader) GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	if u.Service == nil {
		return "", fmt.Errorf("drive service not configured")
	}

	// Search for existing folder
	query := fmt.Sprintf("name = '%s' and '%s' in parents and mimeType = 'application/vnd.google-apps.folder' and trashed = false",
		escapeQuery(name), parentID)
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
	folder := &drive.File{
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

// FileIDFromLink extracts a Google Drive file ID from various URL formats.
// Supports: /file/d/ID, ?id=ID, /open?id=ID
func FileIDFromLink(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	// Try parsing as URL
	if u, err := url.Parse(raw); err == nil {
		// Check for id parameter (?id=FILE_ID)
		if id := strings.TrimSpace(u.Query().Get("id")); id != "" {
			return id
		}

		// Check path: /file/d/FILE_ID or /open?id=FIELD_ID
		path := strings.Trim(u.Path, "/")
		parts := strings.Split(path, "/")
		for i := 0; i < len(parts)-1; i++ {
			if parts[i] == "d" || parts[i] == "file" {
				return parts[i+1]
			}
		}
	}

	// Fallback: look for id= in string
	if idx := strings.Index(raw, "id="); idx >= 0 {
		id := raw[idx+3:]
		if cut := strings.IndexAny(id, "&?#"); cut >= 0 {
			id = id[:cut]
		}
		return id
	}

	return ""
}

// MD5FromMetadata extracts MD5 checksum from a JSON metadata string.
func MD5FromMetadata(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	// Simple string search for common keys
	for _, key := range []string{"drive_md5_checksum", "md5_checksum", "file_hash"} {
		searchStr := fmt.Sprintf(`"%s":"`, key)
		if idx := strings.Index(raw, searchStr); idx >= 0 {
			start := idx + len(searchStr)
			end := strings.Index(raw[start:], `"`)
			if end >= 0 {
				return raw[start : start+end]
			}
		}
	}
	return ""
}

// escapeQuery escapes single quotes for use in Drive API queries.
func escapeQuery(s string) string {
	return strings.ReplaceAll(s, "'", "\\'")
}

// openFile is a helper to open a file (easily mockable for tests).
var openFile = func(path string) (*os.File, error) {
	return os.Open(path)
}
