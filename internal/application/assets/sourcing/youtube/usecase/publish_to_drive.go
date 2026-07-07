// Package usecase — PublishToDrive is a narrow use case extracted from
// youtube/service.go::Register() (PR-CLIP-DECOM-4, July 2026).
//
// It complements PublishClipToDrive (publish_drive.go) by splitting the
// atomic folder-resolution + file-upload contract into two independent
// narrow ports:
//
//	DriveFolderResolver — resolve/create the target Drive folder
//	DriveFileUploader   — upload the file into the resolved folder
//
// This decomposition lets callers resolve folders without uploading,
// upload into pre-resolved folders, or do both — following the same
// Pattern 0 + best-effort nil-guard conventions as persist_clip.go.
//
// Per godlike/06 SSOT (one canonical owner per fact): this file owns
// the two-step Drive orchestration. The existing publish_drive.go owns
// the combined single-port variant. Callers choose the granularity they
// need.
package usecase

import (
	"context"
	"fmt"
)

// DriveFolderResolver resolves or creates a Drive folder for the given
// group, subject, and optional root-folder override. Returns the Drive
// folder ID on success.
type DriveFolderResolver interface {
	ResolveFolder(ctx context.Context, group string, subject string, rootFolderOverride string) (string, error)
}

// DriveFileUploader uploads a file into the specified Drive folder.
type DriveFileUploader interface {
	UploadFile(ctx context.Context, folderID string, localPath string, filename string, description string, assetID string) (*UploadFileResult, error)
}

// UploadFileResult is the use-case-owned wire shape for a file upload outcome.
type UploadFileResult struct {
	FileID      string
	WebViewLink string
}

// PublishToDriveResult reports the outcome of PublishToDrive.
//
// FolderResolved is true only when ResolveFolder succeeded. FileUploaded
// is true only when UploadFile succeeded. FileID and WebViewLink are
// populated from the uploader on success.
type PublishToDriveResult struct {
	FolderID       string
	FolderResolved bool
	FileID         string
	WebViewLink    string
	FileUploaded   bool
}

// PublishToDriveCommand carries every input needed to resolve a Drive folder
// and upload a file. Same fields as PublishClipCommand.
//
// PR-YT-CLIP-SEMANTIC-LOCATION-FIX: Category, Provider, Tags, Language added.
type PublishToDriveCommand struct {
	AssetID     string   // clipID derived from videoID + file hash
	Group       string   // logical group (e.g. actor / project)
	Subject     string   // folder segment (e.g. videoID-titleSlug)
	RootFolder  string   // backward-compat override for cmd.FolderID
	LocalPath   string   // path to the downloaded .mp4 on disk
	Filename    string   // Drive filename (e.g. "dQw4w9WgXcQ - title.mp4")
	Description string   // human-readable Drive file description
	Category    string   // semantic category (e.g. "Boxe", "Personaggi")
	Provider    string   // upstream source (e.g. "youtube", "pexels")
	Tags        []string // semantic keywords for Qdrant payload
	Language    string   // BCP-47 language tag (optional)
}

// PublishToDrive resolves a Drive folder and uploads a file into it. Each
// port is independently optional (nil → step skipped, flag set to false).
// A non-nil port that returns an error aborts the sequence (fail-closed).
//
// This mirrors the persist_clip.go two-port pattern exactly:
//
//  1. nil folder-resolver → skip resolution
//  2. resolver.ResolveFolder → delegate; on error, abort
//  3. nil file-uploader → skip upload
//  4. uploader.UploadFile → delegate; on error, abort
//
// The caller receives an explicit PublishToDriveResult so partial success
// (FolderResolved=true, FileUploaded=false) is transparent.
func PublishToDrive(ctx context.Context, folderResolver DriveFolderResolver, fileUploader DriveFileUploader, cmd PublishToDriveCommand) (*PublishToDriveResult, error) {
	// PR-YT-CLIP-SEMANTIC-LOCATION-FIX: cmd.Category, cmd.Provider,
	// cmd.Tags, and cmd.Language are available on the command struct
	// but not yet threaded into the folder-resolver or file-uploader
	// calls — that wiring lands in a follow-up step (adapters.go).
	result := &PublishToDriveResult{}

	// ── Step 1: Resolve folder ──────────────────────────────────
	if folderResolver != nil {
		folderID, err := folderResolver.ResolveFolder(ctx, cmd.Group, cmd.Subject, cmd.RootFolder)
		if err != nil {
			return result, fmt.Errorf("usecase.PublishToDrive: resolve folder: %w", err)
		}
		result.FolderID = folderID
		result.FolderResolved = true
	}

	// ── Step 2: Upload file ─────────────────────────────────────
	if fileUploader != nil {
		upload, err := fileUploader.UploadFile(ctx, result.FolderID, cmd.LocalPath, cmd.Filename, cmd.Description, cmd.AssetID)
		if err != nil {
			return result, fmt.Errorf("usecase.PublishToDrive: upload file: %w", err)
		}
		result.FileID = upload.FileID
		result.WebViewLink = upload.WebViewLink
		result.FileUploaded = true
	}

	return result, nil
}
