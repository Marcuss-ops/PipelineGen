// Package drive — uploader_deprecated.go: legacy single-file upload methods (admin-only).
//
// 2026-07-06 (Pattern 5 split): extracted from uploader.go. Owns the deprecated
// UploadFile, UploadFileWithDescription, doUploadFile, and UploadFileIfChanged
// methods. All marked deprecated — use delivery.Publisher.Publish instead.
package drive

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// UploadFile uploads a file to the specified Drive folder.
// This properly uses .Media(f) to upload the file content (unlike the broken artlist/drive_uploader.go).
// Retries up to 3 times with exponential backoff on transient errors (429, 503, timeouts).
//
// Deprecated: use delivery.Publisher.Publish instead.
// P1-3 (July 2026): raw admin-only — DO NOT use in application code.
// The canonical write surface is delivery.Publisher.Publish.
func (u *Uploader) UploadFile(ctx context.Context, localPath, folderID, filename string) (*UploadResult, error) {
	return u.UploadFileWithDescription(ctx, localPath, folderID, filename, "")
}

// UploadFileWithDescription uploads a file to the Google Drive folder with a description.
// The description is visible in the Google Drive UI under file details.
//
// Retries up to 3 times with exponential backoff (2s, 4s) on transient errors
// (rate limit 429, server 503, timeouts) via pkg/retry.
// Non-retryable errors short-circuit immediately via the IsRetryable predicate.
//
// Deprecated: use delivery.Publisher.Publish instead.
// P1-3 (July 2026): raw admin-only — same contract as UploadFile.
func (u *Uploader) UploadFileWithDescription(ctx context.Context, localPath, folderID, filename, description string) (*UploadResult, error) {
	result, err := retry.DoWithValue(ctx, func() (*UploadResult, error) {
		return u.doUploadFile(ctx, localPath, folderID, filename, description)
	}, retry.Options{
		MaxAttempts:    3,
		InitialBackoff: 2 * time.Second,
		IsRetryable:    retry.IsTransient,
		OnRetry: func(attempt int, err error) {
			u.Log.Warn("transient drive upload error, retrying",
				zap.String("filename", filename),
				zap.Int("attempt", attempt+1),
				zap.Error(err),
			)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("drive upload failed after 3 attempts: %w", err)
	}
	return result, nil
}

// doUploadFile performs a single upload attempt without retry.
func (u *Uploader) doUploadFile(ctx context.Context, localPath, folderID, filename, description string) (*UploadResult, error) {
	if u.Service == nil {
		return nil, fmt.Errorf("drive service not configured")
	}

	f, err := u.openReader(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	// Check if a file with the same name already exists in this folder to avoid duplicates.
	//
	// Wave B2 (June 2026): FindFileByName now returns ExistingFileLookup
	// with ALL non-trashed matches. We branch on len(Matches) for 0/1/>1:
	//   0 matches → existing stays nil → next branch Creates
	//   1 match   → existing = &lookup.Matches[0] → next branch Updates
	//   >1 match  → fail-closed: return ErrAmbiguousDriveFile wrapped
	//               with the filename. Never fall through to Create on
	//               ambiguous state (silently truncating was the
	//               pre-Wave B2 bug).
	lookup, err := u.FindFileByName(ctx, folderID, filename)
	if err != nil {
		u.Log.Warn("failed to check for existing file on Drive", zap.String("name", filename), zap.Error(err))
	} else if len(lookup.Matches) > 1 {
		return nil, fmt.Errorf("doUploadFile lookup %q: %w", filename, ErrAmbiguousDriveFile)
	}
	var existing *RemoteFile
	if len(lookup.Matches) == 1 {
		existing = &lookup.Matches[0]
	}

	var created *driveapi.File
	start := time.Now()

	if existing != nil && existing.FileID != "" {
		u.Log.Info("updating existing file on drive",
			zap.String("file_path", localPath),
			zap.String("file_id", existing.FileID),
			zap.String("filename", filename),
		)
		updateFile := &driveapi.File{}
		if description != "" {
			updateFile.Description = description
		}
		created, err = u.Service.Files.Update(existing.FileID, updateFile).
			Fields("id,webViewLink,md5Checksum").
			Media(f).
			Context(ctx).
			Do()
	} else {
		file := &driveapi.File{
			Name: filename,
		}
		if description != "" {
			file.Description = description
		}
		if folderID != "" {
			file.Parents = []string{folderID}
		}
		u.Log.Info("uploading new file to drive",
			zap.String("file_path", localPath),
			zap.String("folder_id", folderID),
			zap.String("filename", filename),
		)
		created, err = u.Service.Files.Create(file).
			Fields("id,webViewLink,md5Checksum").
			Media(f).
			Context(ctx).
			Do()
	}

	if err != nil {
		u.Log.Error("failed to upload/update file",
			zap.String("file_path", localPath),
			zap.Error(err),
		)
		return nil, fmt.Errorf("drive upload failed: %w", err)
	}

	u.Log.Info("file uploaded/updated successfully",
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

// UploadFileIfChanged uploads a file only when the Drive file does not already exist with the same hash.
//
// Deprecated: use delivery.Publisher.Publish instead.
// P1-3 (July 2026): raw admin-only — same contract as UploadFile.
func (u *Uploader) UploadFileIfChanged(ctx context.Context, localPath, folderID, filename string) (*UploadResult, bool, error) {
	localHash, err := hashutil.MD5File(localPath)
	if err != nil {
		return nil, false, fmt.Errorf("failed to hash local file: %w", err)
	}

	lookup, err := u.FindFileByName(ctx, folderID, filename)
	if err != nil {
		return nil, false, err
	}
	// Wave B2 (June 2026): 0/1/>1 branch logic on ExistingFileLookup.
	// >1 match is fail-closed — surfaces ErrAmbiguousDriveFile wrapped
	// with the filename. Pre-Wave B2 this case was silently hidden by
	// the first-match truncation in FindFileByName.
	if len(lookup.Matches) > 1 {
		return nil, false, fmt.Errorf("UploadFileIfChanged lookup %q: %w", filename, ErrAmbiguousDriveFile)
	}
	var existing *RemoteFile
	if len(lookup.Matches) == 1 {
		existing = &lookup.Matches[0]
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
