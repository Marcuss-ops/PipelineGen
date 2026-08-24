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
// AddParent/Rename are rare-user-driven, and Cleanup (with the
// Wave D D2 structured CleanupRequest — at-least-one-filter safety
// guard) is bounded by explicit page tokens (no silent retry-deferral
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

	// Cleanup bulk-trashes all non-trashed files matching the
	// structured CleanupRequest. Pagination is exhaustive (driven by
	// nextPageToken). Caller MUST supply at least one filter in the
	// request — a request with all-zero fields would match every
	// non-trashed file on Drive (a Drive-wide wipe) and is rejected
	// upfront with a typed error.
	//
	// Wave D (June 2026) D2: signature changed from
	// `Cleanup(ctx, query string) (int, error)` to
	// `Cleanup(ctx, req CleanupRequest) (int, error)`. The legacy
	// raw-query shape is removed; callers now describe the cleanup
	// target via the structured CleanupRequest fields (ParentFolderID,
	// Name, MimeType, OlderThan). The Drive query string is
	// constructed internally from the request.
	//
	// Wave D (June 2026) D3: return type changed from `(int, error)`
	// to `(CleanupResult, error)`. The CleanupResult surfaces the
	// matched/trashed/failed counts + a FailedIDs slice so callers
	// can branch on outcome (retry failed IDs, audit per-file
	// failures, surface to ops dashboards) without re-issuing a
	// Files.List to count what happened.
	Cleanup(ctx context.Context, req CleanupRequest) (CleanupResult, error)
}

// CleanupResult is the structured return value for FileLifecycle.Cleanup
// (Wave D D3, June 2026). Surfaces the per-page iteration outcome so
// callers can audit partial-failure patterns without re-issuing a
// Files.List.
//
// Field semantics:
//   - Matched: total files found by the Drive query (counted BEFORE
//     trashing). Equal to len(FailedIDs) + Trashed on a fully-iterated
//     cleanup; may be larger if a page request failed mid-loop and
//     the iterator returned early.
//   - Trashed: count of files successfully moved to trash.
//   - Failed: count of files that failed to trash (e.g. transient
//     Drive error, 429, 503). The IDs of the failed files are in
//     FailedIDs.
//   - FailedIDs: file IDs that failed. Caller can retry these via
//     Trash in a follow-up loop. Empty when all files were trashed
//     successfully OR when the cleanup was rejected upfront
//     (at-least-one-filter guard). Initialised to an empty slice
//     (NOT nil) so JSON marshalling produces `"failed_ids": []`
//     rather than `"failed_ids": null`.
type CleanupResult struct {
	Matched   int
	Trashed   int
	Failed    int
	FailedIDs []string
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

// CleanupRequest, buildQuery, and Cleanup have moved to
// file_lifecycle_cleanup.go (Pattern 5 split, July 2026).
// Compile-time assertion: *FileLifecycleAdapter must implement
// FileLifecycle. If a method is added/removed from either side, the
// build breaks here rather than at the first consumer site.
// Mirrors the existing `var _ Admin = (*Uploader)(nil)` and
// `var _ Reader = (*Uploader)(nil)` patterns in ports.go.
var _ FileLifecycle = (*FileLifecycleAdapter)(nil)
