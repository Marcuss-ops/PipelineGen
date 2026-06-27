// Package clips — ReuploadUseCase (P0.5, June 2026).
//
// Extracts the business logic previously inlined in
// internal/api/assets/clips/clip_action.go::Handler.ReuploadClip.
// The API handler is now a thin transport shim:
//
//	result, err := h.reupload.Execute(ctx, request)
//
// This removes *config.Config, *drive.Uploader, *assets.ClipsRepository,
// and outbox.Dispatcher imports from the API layer, restoring AGENTS.md
// Pattern 8 compliance (API = thin transport only).
package clips

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// ── Request / Result DTOs ──────────────────────────────────────────────

// ReuploadRequest is the typed input for ReuploadUseCase.Execute.
type ReuploadRequest struct {
	Source string
	ClipID string
}

// ReuploadResult is the typed reply of ReuploadUseCase.Execute.
// Mirrors the legacy api-output keys verbatim.
type ReuploadResult struct {
	OK         bool
	Source     string
	ClipID     string
	DriveLink  string
	FileHash   string
	UploadedAt string
}

// ── Typed errors ───────────────────────────────────────────────────────

var (
	// ErrReuploadAssetRepoUnavailable is returned when the asset repo
	// port was not wired. Mirror of the handler's 500 path.
	ErrReuploadAssetRepoUnavailable = errors.New("reupload: asset repository not available")

	// ErrReuploadNotFound is returned when the clip is not found.
	ErrReuploadNotFound = errors.New("reupload: clip not found")

	// ErrReuploadNoLocalPath is returned when the clip has no local_path.
	ErrReuploadNoLocalPath = errors.New("reupload: clip has no local path")

	// ErrReuploadLocalFileMissing is returned when the local file does
	// not exist on disk.
	ErrReuploadLocalFileMissing = errors.New("reupload: local file not found")

	// ErrReuploadDriveUploaderUnavailable is returned when the drive
	// uploader port was not wired.
	ErrReuploadDriveUploaderUnavailable = errors.New("reupload: drive uploader not configured")

	// ErrReuploadFolderResolutionFailed is returned when the dynamic
	// folder resolution could not produce a folder ID.
	ErrReuploadFolderResolutionFailed = errors.New("reupload: clip has no folder ID and dynamic resolution failed")

	// ErrReuploadDispatcherUnavailable is returned when the dispatcher
	// port was not wired. Mirror of the handler's 503 path.
	ErrReuploadDispatcherUnavailable = errors.New("reupload: dispatcher not wired")
)

// ── Folder root configuration ──────────────────────────────────────────

// ReuploadFolderRoot maps a canonical source name to its Drive folder
// root ID and the local-path marker used for dynamic folder resolution.
// Populated from config at composition time.
type ReuploadFolderRoot struct {
	RootID     string // Drive folder ID for the source root
	PathMarker string // substring of clip.LocalPath() that marks the source root
}

// ── Use case ───────────────────────────────────────────────────────────

// ReuploadUseCase orchestrates the clip-to-Drive reupload flow.
// Dependencies are narrow ports — zero infrastructure imports.
type ReuploadUseCase struct {
	assetRepo     asset.Repository
	driveUploader ClipDriveUploaderPort
	dispatcher    ClipIndexDispatcherPort
	folderRoots   map[string]ReuploadFolderRoot
	log           *zap.Logger
}

// NewReuploadUseCase constructs the canonical use case.
// folderRoots is keyed by canonical source name (e.g. "clips", "artlist",
// "stock"). A nil/empty map means dynamic folder resolution is disabled
// for all sources.
func NewReuploadUseCase(
	assetRepo asset.Repository,
	driveUploader ClipDriveUploaderPort,
	dispatcher ClipIndexDispatcherPort,
	folderRoots map[string]ReuploadFolderRoot,
	log *zap.Logger,
) *ReuploadUseCase {
	if log == nil {
		log = zap.NewNop()
	}
	if folderRoots == nil {
		folderRoots = map[string]ReuploadFolderRoot{}
	}
	return &ReuploadUseCase{
		assetRepo:     assetRepo,
		driveUploader: driveUploader,
		dispatcher:    dispatcher,
		folderRoots:   folderRoots,
		log:           log,
	}
}

// Execute reuploads a clip to Google Drive and persists the new
// Drive link + MD5 hash via the canonical dispatcher (outbox + tx).
// The caller is responsible for translating the typed errors into
// appropriate HTTP status codes.
func (uc *ReuploadUseCase) Execute(ctx context.Context, req ReuploadRequest) (*ReuploadResult, error) {
	if uc.assetRepo == nil {
		return nil, ErrReuploadAssetRepoUnavailable
	}

	clip, err := uc.assetRepo.Get(ctx, req.ClipID)
	if err != nil || clip == nil {
		return nil, ErrReuploadNotFound
	}

	if clip.LocalPath() == "" {
		return nil, ErrReuploadNoLocalPath
	}

	if _, statErr := os.Stat(clip.LocalPath()); statErr != nil {
		return nil, ErrReuploadLocalFileMissing
	}

	if uc.driveUploader == nil {
		return nil, ErrReuploadDriveUploaderUnavailable
	}

	// Determine folder ID. First use the clip's stored folder ID;
	// otherwise attempt dynamic resolution from the source root config.
	folderID := clip.FolderID()
	if folderID == "" {
		folderID = uc.resolveFolder(ctx, req.Source, clip.LocalPath(), clip)
		if folderID == "" {
			return nil, ErrReuploadFolderResolutionFailed
		}
	}

	// Upload to Drive.
	filename := clip.Filename
	if filename == "" {
		filename = filepath.Base(clip.LocalPath())
	}
	result, uploadErr := uc.driveUploader.UploadFile(ctx, clip.LocalPath(), folderID, filename)
	if uploadErr != nil {
		return nil, fmt.Errorf("reupload: upload failed: %w", uploadErr)
	}

	// Update clip metadata.
	driveLink := result.DownloadLink
	if driveLink == "" && result.FileID != "" {
		driveLink = driveFileURLFromID(result.FileID)
	}
	clip.SetDriveLink(driveLink)
	if result.MD5Checksum != "" {
		clip.SetFileHash(result.MD5Checksum)
	}

	// Persist via dispatcher.
	if uc.dispatcher == nil {
		uc.log.Error("ReuploadUseCase: dispatcher not wired",
			zap.String("clip_id", req.ClipID))
		return nil, ErrReuploadDispatcherUnavailable
	}
	contentHash := clip.FileHash()
	if contentHash == "" {
		contentHash = req.ClipID
	}
	if err := uc.dispatcher.EnqueueAndIndex(ctx, clip, contentHash); err != nil {
		uc.log.Error("ReuploadUseCase: dispatcher.EnqueueAndIndex failed",
			zap.String("clip_id", req.ClipID), zap.Error(err))
		return nil, fmt.Errorf("reupload: dispatcher.EnqueueAndIndex: %w", err)
	}

	return &ReuploadResult{
		OK:         true,
		Source:     req.Source,
		ClipID:     req.ClipID,
		DriveLink:  clip.DriveLink(),
		FileHash:   clip.FileHash(),
		UploadedAt: timeutil.FormatRFC3339(time.Now()),
	}, nil
}

// resolveFolder attempts dynamic Drive folder resolution for clips
// whose FolderID is empty. Uses the folder root config keyed by
// canonical source name.
func (uc *ReuploadUseCase) resolveFolder(ctx context.Context, source, localPath string, clip *asset.Asset) string {
	root, ok := uc.folderRoots[source]
	if !ok {
		return ""
	}
	if root.RootID == "" || root.PathMarker == "" {
		return ""
	}
	if !strings.Contains(localPath, root.PathMarker) {
		return ""
	}

	idx := strings.Index(localPath, root.PathMarker)
	relPath := localPath[idx+len(root.PathMarker):]
	dir := filepath.Dir(relPath)
	if dir == "." || dir == "" {
		return root.RootID
	}

	segments := strings.Split(dir, string(filepath.Separator))
	currentID := root.RootID
	for _, seg := range segments {
		if seg == "" {
			continue
		}
		id, err := uc.driveUploader.GetOrCreateFolder(ctx, seg, currentID)
		if err != nil {
			uc.log.Error("ReuploadUseCase: failed to create drive folder",
				zap.String("segment", seg), zap.Error(err))
			return ""
		}
		currentID = id
	}
	clip.SetFolderID(currentID)
	return currentID
}

// driveFileURLFromID constructs a Google Drive viewer URL from a file ID.
// Kept package-private; the public API surface for Drive URL construction
// is driveutil.FileURLFromID, but that package is in infrastructure/.
func driveFileURLFromID(fileID string) string {
	return "https://drive.google.com/file/d/" + fileID + "/view"
}
