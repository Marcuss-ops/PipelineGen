// Package drive — folder_find_or_create.go: folder creation with P0.4/P0.7 race-safety.
//
// 2026-07-06 (Pattern 5 split): extracted from folder_manager.go. Owns the
// findOrCreateFolder helper (singleflight-deduplicated, P0.4 lookup-before-create,
// P0.7 post-create re-lookup for cross-process duplicate detection).
package drive

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// findOrCreateFolder looks for a folder under parentID with the given
// name and returns its ID, creating it if absent. Internal helper used
// by EnsureFolder.
//
// P0.4 (June 2026): the lookup is the folderseam (folderLookupFunc)
// wired to a retry-with-jitter production implementation. Crucially,
// a lookup error is propagated without falling through to Create —
// the pre-fix soft-error fallback racing with concurrent EnsureFolder
// calls produced duplicate folders on Drive when the genuine folder
// existed but a transient error masked the lookup success.
//
// P0.7 (July 2026): after a successful Create, a re-lookup (via the
// reLookup seam) detects cross-process duplicates. If >1 folder with
// the same name+parent exists, returns ErrAmbiguousDriveFolder
// (fail-closed) instead of silently returning a possibly-colliding ID.
// Singleflight deduplication is applied one frame up in EnsureFolder.
func (a *DriveFolderManagerAdapter) findOrCreateFolder(ctx context.Context, parentID, name string) (string, error) {
	existingID, err := a.lookup(ctx, parentID, name)
	if err != nil {
		return "", fmt.Errorf("findOrCreateFolder: lookup %q under %q failed after retries (P0.4 contract: NO fallback-to-create): %w", name, parentID, err)
	}
	if existingID != "" {
		return existingID, nil
	}

	// Genuine "does not exist" path: only reached when the lookup
	// returned ("", nil). Pre-P0.4, transient errors masked
	// existing-folder matches into this branch — that misroute is
	// structurally eliminated now.
	folder := &driveapi.File{
		Name:     name,
		MimeType: "application/vnd.google-apps.folder",
	}
	if parentID != "" {
		folder.Parents = []string{parentID}
	}
	created, err := a.svc.Files.Create(folder).Fields("id").Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("findOrCreateFolder: create %q under %q: %w", name, parentID, retry.WrapTransient(err))
	}

	// ── P0.7: cross-process race re-lookup ──────────────────────
	// If our Create raced with another process's Create, the re-lookup
	// will see >1 folder with the same name+parent. Return
	// ErrAmbiguousDriveFolder (fail-closed) so callers don't silently
	// pick a possibly-colliding ID. Mirrors uploader_ops.go Stage 3
	// (firstFolderIDByCreatedTimeAsc) but fail-closed on ambiguity
	// rather than silently returning the oldest.
	count, oldestID, reLookupErr := a.doReLookup(ctx, parentID, name)
	if reLookupErr != nil {
		// Defensive: re-lookup failed transiently → return created.ID.
		// Same policy as uploader_ops.go Stage 3 ("when this lookup
		// itself fails transient, return the freshly-created ID so
		// the caller still observes a usable value").
		if a.log != nil {
			a.log.Warn("post-create re-lookup failed, returning freshly-created ID (P0.7 defensive)",
				zap.String("folder_name", name),
				zap.String("parent_id", parentID),
				zap.String("created_id", created.Id),
				zap.Error(reLookupErr))
		}
		return created.Id, nil
	}
	if count > 1 {
		return "", fmt.Errorf("findOrCreateFolder: post-create re-lookup for %q under %q found %d matching folders (oldest=%q, created=%q): %w",
			name, parentID, count, oldestID, created.Id, ErrAmbiguousDriveFolder)
	}
	return created.Id, nil
}

// doReLookup performs the P0.7 post-create re-lookup. Delegates to
// the reLookup seam if injected (test path), otherwise does a
// production Drive Files.List ordered by createdTime asc.
func (a *DriveFolderManagerAdapter) doReLookup(ctx context.Context, parent, name string) (count int, oldestID string, err error) {
	if a.reLookup != nil {
		return a.reLookup(ctx, parent, name)
	}
	return a.reLookupProduction(ctx, parent, name)
}

// reLookupProduction performs a Drive Files.List query for folders
// matching (name, parent, non-trashed, folder mimeType), ordered by
// createdTime ascending. Returns (count, oldestID, nil).
func (a *DriveFolderManagerAdapter) reLookupProduction(ctx context.Context, parent, name string) (count int, oldestID string, err error) {
	list, lerr := a.svc.Files.List().
		Q(buildFolderLookupQuery(parent, name)).
		Fields("files(id, name, createdTime)").
		OrderBy("createdTime asc").
		Context(ctx).
		Do()
	if lerr != nil {
		return 0, "", lerr
	}
	if list == nil || len(list.Files) == 0 {
		return 0, "", nil
	}
	return len(list.Files), list.Files[0].Id, nil
}
