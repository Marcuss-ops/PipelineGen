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
// sfxDriveUploaderAdapter, voiceoverDriveAdapter, sourcingDriveAdapter,
// driveFolderMgrAdapter (YouTube), driveFolderAdapterImpl (Script).
type Admin interface {
	// Folder operations
	GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error)
	GetFolderName(ctx context.Context, folderID string) (string, error)
	TrashFolder(ctx context.Context, folderID string) error
	DeleteFolder(ctx context.Context, folderID string) error

	// File lifecycle
	TrashFile(ctx context.Context, fileID string) error
	DeleteFile(ctx context.Context, fileID string) error
	MoveFile(ctx context.Context, fileID, fromFolderID, toFolderID string) error
	RenameFile(ctx context.Context, fileID, newName string) error

	// Upload (raw path-based, bypassing DestinationRegistry)
	UploadFile(ctx context.Context, localPath, folderID, filename string) (*UploadResult, error)
	UploadFileWithDescription(ctx context.Context, localPath, folderID, filename, description string) (*UploadResult, error)
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
