// Package drive — ports.go (FASE 9, June 2026)
//
// Canonical high-level interfaces for Google Drive operations.
// These replace the raw *gdrive.Service and *drive.Uploader references
// that previously leaked across composition-root boundaries.
//
// Pattern 0 (AGENTS.md): structural port interfaces so the application
// layer never imports google.golang.org/api/drive/v3 or sees the
// concrete Uploader type.
//
// The concrete *drive.Uploader satisfies both Admin and Reader
// structurally — no wrapper needed. The compile-time assertions
// at the bottom of this file pin the conformance.
package drive

import (
	"context"
	"io"
)

// Admin is the canonical port for administrative Drive operations:
// folder management, file lifecycle (trash/delete/move/rename),
// and liveness probing.
//
// Consumers: driveAdminAdapter, storageDriveAdapter, clipsDriveAdapter,
// sourcingDriveAdapter, driveFolderMgrAdapter (YouTube).
type Admin interface {
	// Folder operations
	GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error)
	GetFolderName(ctx context.Context, folderID string) (string, error)
	TrashFolder(ctx context.Context, folderID string) error
	DeleteFolder(ctx context.Context, folderID string) error

	// File lifecycle
	//
	// Deprecated: use FileLifecycle.Trash instead.
	TrashFile(ctx context.Context, fileID string) error
	// Deprecated: use FileLifecycle.Delete instead.
	DeleteFile(ctx context.Context, fileID string) error
	// MoveFile moves a file from one folder to another (read parents +
	// add new parent + remove old parent = true "move"). Distinct from
	// FileLifecycle.AddParent which only ADDS a new parent without
	// removing the old one (multi-parent semantics). For callers that
	// only need multi-parent, prefer FileLifecycle.AddParent; for true
	// move, keep using Admin.MoveFile (the two are intentionally
	// separate surfaces per godlike/06 one-owner-per-fact).
	MoveFile(ctx context.Context, fileID, fromFolderID, toFolderID string) error
	// Deprecated: use FileLifecycle.Rename instead.
	RenameFile(ctx context.Context, fileID, newName string) error

	// Upload (raw path-based, bypassing DestinationRegistry).
	//
	// Deprecated: use delivery.Publisher.Publish instead.
	// P1-3 (July 2026): raw admin-only — DO NOT use in application
	// code. The canonical write surface is delivery.Publisher.Publish
	// (DestinationRegistry + ConflictPolicy + RetryWithJitter).
	// These methods remain on Admin solely for cmd/admin one-shot
	// commands that need raw Drive access without the full publish
	// infrastructure.
	UploadFile(ctx context.Context, localPath, folderID, filename string) (*UploadResult, error)
	// Deprecated: use delivery.Publisher.Publish instead.
	// P1-3 (July 2026): raw admin-only — same contract as UploadFile.
	UploadFileWithDescription(ctx context.Context, localPath, folderID, filename, description string) (*UploadResult, error)
	// Deprecated: use delivery.Publisher.Publish instead.
	// P1-3 (July 2026): raw admin-only — same contract as UploadFile.
	UploadFileIfChanged(ctx context.Context, localPath, folderID, filename string) (*UploadResult, bool, error)

	// Liveness probe (replaces direct About.Get on *gdrive.Service)
	Ping(ctx context.Context) error
}

// Reader is the canonical port for read-only Drive operations:
// file download, metadata retrieval, listing, and existence checks.
//
// Consumers: clipsDriveAdapter, storageDriveAdapter, ingest lifecycle.
type Reader interface {
	DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error)
	GetFileMD5(ctx context.Context, fileID string) (string, error)
	GetFileMeta(ctx context.Context, fileID string) (*FileMeta, error)
	ListFiles(ctx context.Context, parentID string) ([]DriveFileInfo, error)
	// FindFileByName returns ALL non-trashed files matching
	// (folderID, filename). Callers MUST branch on
	// len(ExistingFileLookup.Matches):
	//   0 matches → no existing file, take the Create path
	//   1 match   → apply the chosen ConflictPolicy against Matches[0]
	//   >1 match  → caller MUST treat as ambiguous and surface
	//               ErrAmbiguousDriveFile (fail-closed; never silently
	//               pick the first match). Pre-Wave B2 (June 2026) the
	//               method returned only (*RemoteFile, error) and
	//               silently truncated to the first match — Wave B2
	//               closes that ambiguity hole by surfacing the full
	//               match set so callers can detect >1.
	FindFileByName(ctx context.Context, folderID, filename string) (ExistingFileLookup, error)
	FileIsNotTrashed(ctx context.Context, fileID string) (bool, error)
	FileExists(ctx context.Context, fileID string) (bool, error)
	// SearchFiles lists files matching an arbitrary Drive query string.
	// Unlike ListFiles (which filters by parent folder), SearchFiles
	// passes the raw query directly to Files.List().Q().
	SearchFiles(ctx context.Context, query string) ([]DriveFileInfo, error)
}

// Compile-time assertions: *Uploader satisfies both Admin and Reader.
// If a method is added/removed from either interface, the build breaks
// here rather than at the first consumer site.
var (
	_ Admin  = (*Uploader)(nil)
	_ Reader = (*Uploader)(nil)
)
