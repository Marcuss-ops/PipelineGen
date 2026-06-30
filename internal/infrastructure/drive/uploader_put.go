// Package drive — uploader_put.go (P0 #1 fix, June 2026)
//
// *Uploader.PutFile is the conflict-aware single entry point for Drive
// uploads. It supersedes the pre-refactor UploadFileWithDescription
// method on the Publisher's FileUploaderPort (see publisher.go).
//
// Routing table (ConflictPolicy → action):
//
//	ConflictOverwrite (zero-value, legacy default):
//	  - existing match   → Files.Update with Media  → PutActionUpdated
//	  - no match         → Files.Create with Media  → PutActionCreated
//	ConflictSkip:
//	  - existing match   → return existing metadata → PutActionSkipped
//	                        (NO upload performed)
//	  - no match         → Files.Create with Media  → PutActionCreated
//	ConflictRename:
//	  - existing match   → Files.Create with new name (timestamp suffix)
//	                                              → PutActionRenamed
//	  - no match         → Files.Create with Media  → PutActionCreated
//
// Retries on transient Drive errors (429, 503, timeouts, network blips)
// via pkg/retry — same exponential backoff policy (3 attempts, 2s → 4s)
// as the legacy UploadFileWithDescription path. The lookup step
// (FindFileByName) follows the soft-error-on-miss pattern: a transient
// lookup failure is logged and treated as "no match" so the retry of
// the Create/Update step can succeed. The P0 #4 fix on
// findOrCreateFolder is orthogonal and lands in a separate commit.
package drive

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// PutFile is the conflict-aware upload entry point. Implements
// FileUploaderPort.PutFile (used by delivery.Publisher) and
// drive.Admin.PutFile (used by application adapters). Returns a
// structured PutFileResult with an explicit Action so callers do not
// have to infer success semantics from a boolean-style return value.
//
// Lookups use the same FindFileByName surface as the legacy
// path. Reusing the existing helper keeps behaviour consistent and
// avoids re-implementing the query-construction logic.
//
// The retry wrapper covers Create / Update operations only — FindFileByName
// runs once per attempt and is soft-failed on transient errors so a
// race between lookup and write doesn't surface as a hard error.
func (u *Uploader) PutFile(ctx context.Context, req PutFileRequest) (*PutFileResult, error) {
	if u.Service == nil {
		return nil, fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(req.LocalPath) == "" {
		return nil, fmt.Errorf("putFile: local path is required")
	}
	if strings.TrimSpace(req.FolderID) == "" {
		return nil, fmt.Errorf("putFile: folder id is required")
	}
	if strings.TrimSpace(req.Filename) == "" {
		return nil, fmt.Errorf("putFile: filename is required")
	}

	result, err := retry.DoWithValue(ctx, func() (*PutFileResult, error) {
		// Step 1: lookup existing (soft-fail on transient errors).
		existing, lookupErr := u.FindFileByName(ctx, req.FolderID, req.Filename)
		if lookupErr != nil {
			u.Log.Warn("putFile: lookup failed, falling through to create-or-update",
				zap.String("filename", req.Filename),
				zap.String("folder_id", req.FolderID),
				zap.Error(lookupErr))
		}
		return u.doPutFile(ctx, req, existing)
	}, retry.Options{
		MaxAttempts:    3,
		InitialBackoff: 2 * time.Second,
		IsRetryable:    isRetryableDriveErr,
		OnRetry: func(attempt int, err error) {
			u.Log.Warn("transient drive put error, retrying",
				zap.String("filename", req.Filename),
				zap.Int("attempt", attempt+1),
				zap.Error(err),
			)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("drive put failed after 3 attempts: %w", err)
	}
	return result, nil
}

// doPutFile performs the actual Files.Create / Files.Update / skip
// branch based on ConflictPolicy + lookup match. Split out from PutFile
// so the retry wrapper can re-invoke it (FindFileByName is re-run on
// each attempt because race conditions between attempt N and attempt
// N+1 are real — a sibling worker may have created the file in between).
func (u *Uploader) doPutFile(ctx context.Context, req PutFileRequest, existing *RemoteFile) (*PutFileResult, error) {
	// ConflictSkip on existing match: short-circuit, no upload, return
	// existing metadata. This is the only branch where the local file
	// is NOT opened.
	if req.ConflictPolicy == delivery.ConflictSkip && existing != nil && existing.FileID != "" {
		return &PutFileResult{
			FileID:       existing.FileID,
			WebViewLink:  existing.WebViewLink,
			DownloadLink: "https://drive.google.com/uc?id=" + existing.FileID,
			MD5Checksum:  existing.MD5Checksum,
			Action:       PutActionSkipped,
		}, nil
	}

	// ConflictSkip without existing match: log explicit so callers
	// reading the audit trail see "skip requested but created" rather
	// than a silent fall-through (P0 #1 #Q2 action-vs-policy mismatch).
	if req.ConflictPolicy == delivery.ConflictSkip {
		u.Log.Info("putFile: skip requested but no existing match; creating (no-op skip)",
			zap.String("filename", req.Filename),
			zap.String("folder_id", req.FolderID))
	}

	f, err := openFile(req.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("open local file: %w", err)
	}
	defer f.Close()

	// ConflictOverwrite on existing match: drive Files.Update (which
	// preserves the file's ID and version history when keepRevisionForever
	// is unset on the file, matching legacy semantics).
	if req.ConflictPolicy == delivery.ConflictOverwrite && existing != nil && existing.FileID != "" {
		updateFile := &driveapi.File{}
		if req.Description != "" {
			updateFile.Description = req.Description
		}
		updated, err := u.Service.Files.Update(existing.FileID, updateFile).
			Fields("id,webViewLink,md5Checksum").
			Media(f).
			Context(ctx).
			Do()
		if err != nil {
			return nil, fmt.Errorf("drive put (update %q): %w", req.Filename, err)
		}
		return &PutFileResult{
			FileID:       updated.Id,
			WebViewLink:  updated.WebViewLink,
			DownloadLink: "https://drive.google.com/uc?id=" + updated.Id,
			MD5Checksum:  updated.Md5Checksum,
			Action:       PutActionUpdated,
		}, nil
	}

	// ConflictRename on existing match: Create with timestamp suffix.
	// Drive's API does not provide auto-rename on filename collision
	// (multiple files with the same name + parent get distinct IDs),
	// and visually-duplicate entries are a UI antipattern, so manual
	// suffixing is the canonical workaround. UnixNano is the canonical
	// burst-protection timestamp: UnixSeconds would collide between
	// two renames within the same second (rare but observable) and
	// produce a Drive 409 (not retryable per pkg/retry's isRetryable
	// predicate — only 429/503/timeouts qualify).
	if req.ConflictPolicy == delivery.ConflictRename && existing != nil && existing.FileID != "" {
		newName := renameWithTimestamp(req.Filename, time.Now().UnixNano())
		file := &driveapi.File{Name: newName}
		if req.Description != "" {
			file.Description = req.Description
		}
		file.Parents = []string{req.FolderID}
		created, err := u.Service.Files.Create(file).
			Fields("id,webViewLink,md5Checksum").
			Media(f).
			Context(ctx).
			Do()
		if err != nil {
			return nil, fmt.Errorf("drive put (rename-create %q): %w", newName, err)
		}
		u.Log.Info("putFile: renamed to avoid collision",
			zap.String("original", req.Filename),
			zap.String("renamed", newName),
			zap.String("file_id", created.Id),
		)
		return &PutFileResult{
			FileID:       created.Id,
			WebViewLink:  created.WebViewLink,
			DownloadLink: "https://drive.google.com/uc?id=" + created.Id,
			MD5Checksum:  created.Md5Checksum,
			Action:       PutActionRenamed,
		}, nil
	}

	// Plain Create: covers (a) all policies with no existing match,
	// and (b) ConflictRename/ConflictSkip with no existing match.
	file := &driveapi.File{Name: req.Filename}
	if req.Description != "" {
		file.Description = req.Description
	}
	file.Parents = []string{req.FolderID}
	created, err := u.Service.Files.Create(file).
		Fields("id,webViewLink,md5Checksum").
		Media(f).
		Context(ctx).
		Do()
	if err != nil {
		return nil, fmt.Errorf("drive put (create %q): %w", req.Filename, err)
	}
	return &PutFileResult{
		FileID:       created.Id,
		WebViewLink:  created.WebViewLink,
		DownloadLink: "https://drive.google.com/uc?id=" + created.Id,
		MD5Checksum:  created.Md5Checksum,
		Action:       PutActionCreated,
	}, nil
}

// renameWithTimestamp inserts a UnixNano suffix into the filename
// preserving the extension. Example:
//
//	clip.mp4               → clip_1719612345123456789.mp4
//	complex.name.v2.tar.gz → complex.name.v2_1719612345123456789.tar.gz
func renameWithTimestamp(name string, ts int64) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	if ext == "" {
		return fmt.Sprintf("%s_%d", base, ts)
	}
	return fmt.Sprintf("%s_%d%s", base, ts, ext)
}

// keep zap + retry imported — both used by the PutFile + doPutFile
// bodies above. The Package import references are intentional and
// survive compiler-side import pruning.
var _ = zap.NewNop
var _ retry.Options
