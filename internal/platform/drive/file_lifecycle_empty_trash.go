// Package drive — file_lifecycle_empty_trash.go: trash inspection and
// permanent trash purge.
//
// August 2026 (Pattern 5 split): the trash-level operations live here
// rather than in file_lifecycle.go. Trash/Delete are per-file; these two
// methods are trash-scoped: ListTrashed reads what is in the trash and
// EmptyTrash permanently removes all of it.
//
// Ownership: this adapter remains the only direct caller of the Drive
// SDK for file lifecycle (godlike/06 "one owner per fact"). The admin
// CLI reaches the purge exclusively through the FileLifecycle port so
// the Drive boundary stays in internal/platform/drive.
package drive

import (
	"context"
	"fmt"
)

// driveTrashPageSizeCap mirrors the Drive Files.List maximum page size;
// ListTrashed clamps its caller-supplied maxResults to this bound so a
// preview request can never ask for an unbounded page.
const driveTrashPageSizeCap = 1000

// ListTrashed returns up to maxResults items currently in Drive's trash.
// Read-only: it issues a single Files.List page (no pagination loop) —
// the preview contract is "show the operator what is in the trash", and
// a capped page is enough to make the decision; EmptyTrash then removes
// everything regardless of how many pages exist.
//
// A maxResults <= 0 (or above the Drive page-size cap) is clamped to the
// cap rather than rejected: the call is read-only, so a permissive
// default is safe, while a destructive call would be fail-closed.
func (a *FileLifecycleAdapter) ListTrashed(ctx context.Context, maxResults int) ([]TrashedItem, error) {
	if a.svc == nil {
		return nil, fmt.Errorf("drive service not configured")
	}
	if maxResults <= 0 || maxResults > driveTrashPageSizeCap {
		maxResults = driveTrashPageSizeCap
	}
	res, err := a.svc.Files.List().
		Q("trashed = true").
		Fields("files(id, name, mimeType)").
		PageSize(int64(maxResults)).
		Context(ctx).
		Do()
	if err != nil {
		return nil, fmt.Errorf("drive list trash: %w", err)
	}
	if res == nil {
		return []TrashedItem{}, nil
	}
	out := make([]TrashedItem, 0, len(res.Files))
	for _, f := range res.Files {
		out = append(out, TrashedItem{ID: f.Id, Name: f.Name, MimeType: f.MimeType})
	}
	return out, nil
}

// EmptyTrash permanently deletes every item currently in Drive's trash
// (Drive Files.EmptyTrash). The Drive call returns no count, so the
// method surfaces only the outcome: nil means the purge completed, a
// wrapped error means it did not. Callers that need to know how many
// items were removed should call ListTrashed BEFORE EmptyTrash — the
// count cannot be recovered afterwards.
//
// NOT idempotent-recoverable: there is no undo. The admin CLI
// drive-empty-trash subcommand is fail-closed (requires --apply) so
// this method is never reached by an unflagged invocation.
func (a *FileLifecycleAdapter) EmptyTrash(ctx context.Context) error {
	if a.svc == nil {
		return fmt.Errorf("drive service not configured")
	}
	if err := a.svc.Files.EmptyTrash().Context(ctx).Do(); err != nil {
		return fmt.Errorf("drive emptyTrash: %w", err)
	}
	return nil
}
