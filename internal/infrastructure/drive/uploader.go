package drive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
	"golang.org/x/sync/singleflight"

	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// Uploader handles Google Drive file operations.
//
// F1.6 (June 2026, P0 #4 + #5 + #6): folderOps is the in-process race-safety
// keyed lock applied to GetOrCreateFolder. The zero value is ready to use;
// concurrent calls for the same (parentID, canonicalName) pair are
// deduplicated by singleflight.Group.Do(key=parentID+":"+canonicalName, ...).
// The shared call observes only ONE List/Create pair; concurrent callers
// receive the same result without racing through Create.
type Uploader struct {
	Service   *driveapi.Service
	Log       *zap.Logger
	folderOps singleflight.Group // F1.6 P0 #5 keyed lock: parentID+":"+canonicalName
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

// ExistingFileLookup is the multi-match result of FindFileByName.
// Pre-Wave B2 (June 2026) FindFileByName returned only the first match,
// silently truncating the second/third/... matches — which made
// overwrite/skip non-deterministic when multiple files shared the same
// name+parent (e.g. a user manually uploaded a sibling copy and the
// pipeline then uploaded another). Wave B2 makes the surface
// exhaustive: callers MUST branch on len(Matches) to distinguish
//
//	0 matches → no existing file, take the Create branch
//	1 match   → apply the chosen ConflictPolicy against Matches[0]
//	>1 match  → fail-closed: surface ErrAmbiguousDriveFile
//	            (NEVER silently pick the first match on ambiguous state)
//
// The zero value (len(Matches) == 0) is the canonical "no match" surface,
// matching the pre-Wave B2 (nil, nil) return contract semantically.
type ExistingFileLookup struct {
	Matches []RemoteFile
}

// ErrAmbiguousDriveFile is the canonical sentinel returned when
// FindFileByName reports more than one non-trashed match for the
// (folderID, filename) tuple. Callers errors.Is against this sentinel
// to distinguish "multiple omonimi on Drive" from other lookup
// failures (rate limit, network timeout, malformed query). Surfacing
// the sentinel at the port boundary is the Wave B2 contract change —
// pre-Wave B2 the truncation to first-match hid this case entirely.
//
// Mirrors the per-package sentinels already defined in publisher.go
// (ErrMissingDestinationRegistry, ErrMissingFolderManager,
// ErrMissingFileUploader) — exported, errors.Is-friendly, surface-stable.
var ErrAmbiguousDriveFile = errors.New("drive: ambiguous file match: multiple non-trashed files with the same name+parent exist on Drive")

// UploadFile uploads a file to the specified Drive folder.
// This properly uses .Media(f) to upload the file content (unlike the broken artlist/drive_uploader.go).
// Retries up to 3 times with exponential backoff on transient errors (429, 503, timeouts).
func (u *Uploader) UploadFile(ctx context.Context, localPath, folderID, filename string) (*UploadResult, error) {
	return u.UploadFileWithDescription(ctx, localPath, folderID, filename, "")
}

// UploadFileWithDescription uploads a file to the Google Drive folder with a description.
// The description is visible in the Google Drive UI under file details.
//
// Retries up to 3 times with exponential backoff (2s, 4s) on transient errors
// (rate limit 429, server 503, timeouts) via pkg/retry. Keeps the retry policy
// uniform with the other call sites in the codebase that already use pkg/retry
// (segment.go, veloxclient, ollama client, script batch QA/coherence, engine.go).
// Non-retryable errors short-circuit immediately via the IsRetryable predicate.
func (u *Uploader) UploadFileWithDescription(ctx context.Context, localPath, folderID, filename, description string) (*UploadResult, error) {
	result, err := retry.DoWithValue(ctx, func() (*UploadResult, error) {
		return u.doUploadFile(ctx, localPath, folderID, filename, description)
	}, retry.Options{
		MaxAttempts:    3,
		InitialBackoff: 2 * time.Second,
		IsRetryable:    isRetryableDriveErr,
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

	f, err := openFile(localPath)
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

// FindFileByName returns ALL non-trashed files in a folder with the
// given name. Pre-Wave B2 (June 2026) this returned the first match
// only — silently truncating the second/third/... matches, which made
// overwrite/skip non-deterministic when multiple files shared the
// same name+parent (e.g. a user manually uploaded a sibling copy and
// the pipeline uploaded another).
//
// Wave B2 makes the surface exhaustive: callers MUST branch on
// len(ExistingFileLookup.Matches) to distinguish 0/1/>1 matches per
// the routing table documented on the ExistingFileLookup type. The
// zero-value ExistingFileLookup (len(Matches) == 0) is the canonical
// "no match" surface, matching the pre-Wave B2 (nil, nil) return
// contract semantically.
//
// The >1 case is NOT signalled here — FindFileByName returns all
// matches; it is the CALLER's job to detect len > 1 and surface
// ErrAmbiguousDriveFile (fail-closed). This split is intentional:
// the port method is a pure read, while the ambiguous-state error is
// a policy decision owned by the caller.
func (u *Uploader) FindFileByName(ctx context.Context, folderID, filename string) (ExistingFileLookup, error) {
	if u.Service == nil {
		return ExistingFileLookup{}, fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(folderID) == "" || strings.TrimSpace(filename) == "" {
		return ExistingFileLookup{}, nil
	}

	query := fmt.Sprintf("name = '%s' and '%s' in parents and trashed = false", strings.ReplaceAll(filename, "'", "\\'"), folderID)
	list, err := u.Service.Files.List().
		Q(query).
		Fields("files(id, name, webViewLink, md5Checksum)").
		Context(ctx).
		Do()
	if err != nil {
		return ExistingFileLookup{}, err
	}

	lookup := ExistingFileLookup{Matches: make([]RemoteFile, 0, len(list.Files))}
	for _, file := range list.Files {
		lookup.Matches = append(lookup.Matches, RemoteFile{
			FileID:      file.Id,
			Name:        file.Name,
			WebViewLink: file.WebViewLink,
			MD5Checksum: file.Md5Checksum,
		})
	}
	return lookup, nil
}

// UploadFileIfChanged uploads a file only when the Drive file does not already exist with the same hash.
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

// openFile is a helper to open a file (easily mockable for tests).
var openFile = func(path string) (*os.File, error) {
	return os.Open(path)
}

// isRetryableDriveErr returns true for transient Google Drive API errors that
// may succeed on retry: rate limits (429), server errors (503), and timeouts.
func isRetryableDriveErr(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	// Google API errors embed status codes in the message
	if strings.Contains(errStr, "429") || strings.Contains(errStr, "rateLimitExceeded") || strings.Contains(errStr, "userRateLimitExceeded") {
		return true
	}
	if strings.Contains(errStr, "503") || strings.Contains(errStr, "backendError") || strings.Contains(errStr, "serviceUnavailable") {
		return true
	}
	if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadlineExceeded") || strings.Contains(errStr, "connection reset") {
		return true
	}
	return false
}
