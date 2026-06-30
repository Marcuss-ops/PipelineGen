// Package drive — file_lifecycle.go (CARD-3, June 2026)
//
// FileLifecycleAdapter wraps the concrete *driveapi.Service to satisfy
// the drive.FileLifecycle port (declared below). It owns the raw SDK
// for file-level lifecycle commands: Trash, AddParent, Rename,
// Cleanup, plus Delete (Wave C preparation, June 2026).
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
// operations: trash a single file, permanently delete a file, add a
// parent to a file (multi-parent semantics), rename a file,
// bulk-cleanup files matching a query.
//
// Consumers: artlist.SemanticEnricher (Trash), job/cleanup_handler
// (Delete), YouTube/storage.moveFile use cases (Move), system handler
// rename endpoint (Rename), future move/rename/cleanup orchestrators.
// The composition root exposes this port as root.Drive.Lifecycle
// alongside root.Drive.Admin (folder ops) and root.Drive.Reader
// (read-only).
//
// Pattern 0 (AGENTS.md): structural port interface so the application
// layer never imports google.golang.org/api/drive/v3 or sees the
// concrete FileLifecycleAdapter.
//
// All methods are user-triggered / idempotent at the Drive API level —
// no retry-with-jitter policy inherited from folder_lookupRetry*
// (P0.4 lesson) because Trash/Delete are one-shot commands,
// AddParent/Rename are rare-user-driven, and Cleanup's pagination
// loop is bounded by explicit page tokens (no silent retry-deferral
// drift).
//
// Wave C (June 2026): DeleteFile removed from drive.Admin (per the
// P0 Admin port-tightening), reallocated to FileLifecycle.Delete.
// Delete is a hard-delete (Drive Files.Delete); it is conceptually
// distinct from Trash (Drive Files.Update{Trashed:true}). Callers
// that need a recoverable state MUST use Trash; callers that need
// permanent removal MUST use Delete.
type FileLifecycle interface {
	// Trash moves a file to Drive's trash (idempotent — re-trashing a
	// trashed file succeeds). Safer than permanent DeleteFile because
	// the user can recover from the Drive UI.
	Trash(ctx context.Context, fileID string) error

	// Delete permanently removes a file from Drive. Wave C (June
	// 2026) reallocation target for the pre-Wave-C Admin.DeleteFile
	// method. NOT idempotent: a second call on an already-deleted
	// fileID returns 404 from the Drive SDK, which the adapter wraps
	// in a typed error. Callers that need recoverable state should
	// use Trash instead.
	Delete(ctx context.Context, fileID string) error

	// AddParent adds newParentID as an additional parent of a file.
	// Multi-parent semantics: Drive allows a file in multiple folders;
	// AddParent only ADDS newParentID without removing any existing
	// parents. To remove a parent, callers use the Admin port's
	// RemoveParent invocation (out of scope for this port per the
	// user's spec literal).
	//
	// Wave D (June 2026) D1: renamed from Move to AddParent. The
	// original name was misleading because the implementation never
	// removed the old parent — it was always a multi-parent add.
	// Zero production callers existed at rename time so the rename
	// is a pure port-contract tightening. True "move" semantics
	// (read parents + add new + remove old) can be added as a
	// separate MoveTo method if a future capability requires it.
	AddParent(ctx context.Context, fileID, newParentID string) error

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

// Delete permanently removes a fileID from Drive. Wave C (June 2026)
// reallocation target for the pre-Wave-C Admin.DeleteFile method.
// Files.Delete is the SDK primitive; a 404 on an already-deleted
// fileID surfaces as a wrapped error. Empty fileID short-circuits
// with the same precedence as Trash.
func (a *FileLifecycleAdapter) Delete(ctx context.Context, fileID string) error {
	if strings.TrimSpace(fileID) == "" {
		return fmt.Errorf("delete: file id is required")
	}
	if a.svc == nil {
		return fmt.Errorf("drive service not configured")
	}
	if err := a.svc.Files.Delete(fileID).Context(ctx).Do(); err != nil {
		return fmt.Errorf("drive delete: %w", err)
	}
	return nil
}

// AddParent adds newParentID as an additional parent of a file. The
// Drive API semantics: AddParents adds the new parent without
// removing the existing ones (multi-parent files). The caller
// manages the old-parent removal via the Admin port's RemoveParent
// invocation (NOT in scope for FileLifecycle per the user spec).
//
// Wave D (June 2026) D1: renamed from Move to AddParent to match
// the actual semantics. The old name was misleading because the
// implementation never removed the old parent — it was always a
// multi-parent add. Validation precedence mirrors Trash / Delete /
// Rename: input checks first, then nil-svc check.
func (a *FileLifecycleAdapter) AddParent(ctx context.Context, fileID, newParentID string) error {
	if strings.TrimSpace(fileID) == "" {
		return fmt.Errorf("addParent: file id is required")
	}
	if strings.TrimSpace(newParentID) == "" {
		return fmt.Errorf("addParent: new parent id is required")
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
		return fmt.Errorf("drive addParent: %w", err)
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
