// Package drive — file_lifecycle.go (CARD-3, June 2026)
//
// FileLifecycleAdapter wraps the concrete *driveapi.Service to satisfy
// the drive.FileLifecycle port (declared below). It owns the raw SDK
// for file-level lifecycle commands: Trash, Move, Rename, Cleanup.
//
// The Trash implementation is migrated out of folder_manager.go
// (DriveFolderManagerAdapter) per godlike/06 "one owner per fact":
// file lifecycle is the canonical owner of `svc.Files.Update{Trashed:true}`
// rather than a folder manager. Move + Rename + Cleanup are introduced
// alongside so a single FileLifecycle is the surface for all
// file-mutation commands — Admin (folder ops on *Uploader) stays narrower.
//
// Compile-time assertion at the bottom of the file pins the
// conformance: drift between the port surface and the concrete is a
// build failure rather than a runtime nil-pointer.
//
// The adapter lives in internal/infrastructure because Drive IS a
// transport/storage mechanism, not part of any application capability
// pipeline. Composition root (internal/app/build_bundles_drive.go)
// builds the SDK once and reuses it across multiple consumers
// (Uploader / FolderManager / Lifecycle).
package drive

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
)

// FileLifecycle is the canonical port for file-lifecycle Drive
// operations: trash a single file, move a file to a new parent,
// rename a file, bulk-cleanup files matching a query.
//
// Consumers: artlist.SemanticEnricher (Trash), future move/rename/cleanup
// orchestrators. The composition root exposes this port as
// root.Drive.Lifecycle alongside root.Drive.Admin (folder ops) and
// root.Drive.Reader (read-only).
//
// Pattern 0 (AGENTS.md): structural port interface so the application
// layer never imports google.golang.org/api/drive/v3 or sees the
// concrete FileLifecycleAdapter.
//
// All methods are user-triggered / idempotent at the Drive API level —
// no retry-with-jitter policy inherited from folder_lookupRetry*
// (P0.4 lesson) because Trash is a one-shot command, Move/Rename are
// rare-user-driven, and Cleanup's pagination loop is bounded by
// explicit page tokens (no silent retry-deferral drift).
type FileLifecycle interface {
	// Trash moves a file to Drive's trash (idempotent — re-trashing a
	// trashed file succeeds). Safer than permanent DeleteFile because
	// the user can recover from the Drive UI.
	Trash(ctx context.Context, fileID string) error

	// Move moves a file to a new parent folder. Multi-parent semantics:
	// Drive allows a file in multiple folders; the caller does NOT
	// specify the old parent — Move only ADDS newParentID. To remove a
	// parent, callers use the Admin port's RemoveParent invocation
	// (out of scope for this port per the user's spec literal).
	Move(ctx context.Context, fileID, newParentID string) error

	// Rename updates a file's display name.
	Rename(ctx context.Context, fileID, newName string) error

	// Cleanup bulk-trashes all non-trashed files matching the supplied
	// Drive search query. Pagination is exhaustive (driven by
	// nextPageToken). Caller MUST supply a query that excludes
	// 'trashed = true' to avoid re-processing already-trashed entries.
	// Returns the count of files successfully trashed.
	Cleanup(ctx context.Context, query string) (deletedCount int, err error)
}

// FileLifecycleAdapter is the only direct caller of Files.Update /
// Files.List for file-mutation purposes; the canonical Transfer/Modify
// surface. Name conflicts with the legacy *DriveFolderManagerAdapter
// (which is folder-centric) are intentionally avoided by keeping the
// thin surface scope.
type FileLifecycleAdapter struct {
	svc *driveapi.Service
	log *zap.Logger
}

// NewFileLifecycleAdapter constructs the adapter from a configured
// Drive SDK service. The composition root in
// internal/app/build_bundles_drive.go reuses the single DriveClient
// SDK handle across Uploader / FolderManager / Lifecycle so Drive
// credentials are loaded exactly once.
func NewFileLifecycleAdapter(svc *driveapi.Service, log *zap.Logger) *FileLifecycleAdapter {
	if log == nil {
		log = zap.NewNop()
	}
	return &FileLifecycleAdapter{svc: svc, log: log}
}

// Trash moves a fileID to Drive's trash. Empty fileID is rejected.
// Files.Update{Trashed:true} is idempotent at the Drive API level —
// re-trashing an already-trashed file succeeds.
func (a *FileLifecycleAdapter) Trash(ctx context.Context, fileID string) error {
	// CARD-3 (June 2026): input validation BEFORE nil-svc check so the
	// tests can deterministically surface the input message without an
	// httptest server (cheap-rejection pattern; the nil-svc-misconfig
	// check is positioned AFTER input validation so a real production
	// caller sees the same precedence: bad input short-circuits, bad
	// config surfaces next).
	if strings.TrimSpace(fileID) == "" {
		return fmt.Errorf("trash: file id is required")
	}
	if a.svc == nil {
		return fmt.Errorf("drive service not configured")
	}
	_, err := a.svc.Files.Update(fileID, &driveapi.File{Trashed: true}).
		Fields("id", "trashed").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("drive trash: %w", err)
	}
	return nil
}

// Move relocates a file to a new parent folder. The Drive API
// semantics: AddParents adds the new parent without removing the
// existing ones (multi-parent files). The caller manages the old-parent
// removal via the Admin port's RemoveParent invocation (NOT in scope
// for FileLifecycle per the user spec).
func (a *FileLifecycleAdapter) Move(ctx context.Context, fileID, newParentID string) error {
	if strings.TrimSpace(fileID) == "" {
		return fmt.Errorf("move: file id is required")
	}
	if strings.TrimSpace(newParentID) == "" {
		return fmt.Errorf("move: new parent id is required")
	}
	if a.svc == nil {
		return fmt.Errorf("drive service not configured")
	}
	_, err := a.svc.Files.Update(fileID, nil).
		AddParents(newParentID).
		Fields("id", "parents").
		Context(ctx).
		Do()
	if err != nil {
		return fmt.Errorf("drive move: %w", err)
	}
	return nil
}

// Rename updates a file's display name.
func (a *FileLifecycleAdapter) Rename(ctx context.Context, fileID, newName string) error {
	if strings.TrimSpace(fileID) == "" {
		return fmt.Errorf("rename: file id is required")
	}
	if strings.TrimSpace(newName) == "" {
		return fmt.Errorf("rename: new name is required")
	}
	if a.svc == nil {
		return fmt.Errorf("drive service not configured")
	}
	_, err := a.svc.Files.Update(fileID, &driveapi.File{Name: newName}).
		Fields("id", "name").
		Context(ctx).
		Do()
	if err != nil {
		return fmt.Errorf("drive rename: %w", err)
	}
	return nil
}

// Cleanup bulk-trashes files matching the supplied query, paginating
// exhaustively via nextPageToken. Caller MUST include
// "and trashed = false" in the query to avoid reprocessing trashed
// entries (Trash is idempotent, so re-trashing is harmless but wastes
// quota). Returns the count of files successfully trashed; partial
// failures during the loop are logged and counted as missing from
// the return value rather than aborting the operation.
func (a *FileLifecycleAdapter) Cleanup(ctx context.Context, query string) (int, error) {
	if strings.TrimSpace(query) == "" {
		return 0, fmt.Errorf("cleanup: query is required")
	}
	if a.svc == nil {
		return 0, fmt.Errorf("drive service not configured")
	}

	var deletedCount int
	var pageToken string
	for {
		req := a.svc.Files.List().Q(query).
			Fields("nextPageToken, files(id)").
			Context(ctx)
		if pageToken != "" {
			req = req.PageToken(pageToken)
		}
		res, err := req.Do()
		if err != nil {
			return deletedCount, fmt.Errorf("cleanup list (page=%q): %w", pageToken, err)
		}
		for _, f := range res.Files {
			if err := a.Trash(ctx, f.Id); err != nil {
				a.log.Warn("cleanup: failed to trash file",
					zap.String("file_id", f.Id),
					zap.String("query", query),
					zap.Error(err))
				continue
			}
			deletedCount++
		}
		if res.NextPageToken == "" {
			break
		}
		pageToken = res.NextPageToken
	}
	return deletedCount, nil
}

// Compile-time assertion: *FileLifecycleAdapter must implement
// FileLifecycle. If a method is added/removed from either side, the
// build breaks here rather than at the first consumer site.
// Mirrors the existing `var _ Admin = (*Uploader)(nil)` and
// `var _ Reader = (*Uploader)(nil)` patterns in ports.go.
var _ FileLifecycle = (*FileLifecycleAdapter)(nil)
