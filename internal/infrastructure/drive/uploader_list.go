package drive

import (
	"context"
	"fmt"
	"time"
)

// DriveFileInfo holds summary info for a file in a Drive listing.
type DriveFileInfo struct {
	ID             string
	Name           string
	MimeType       string
	Size           int64
	MD5Checksum    string
	WebViewLink    string
	WebContentLink string
	Parents        []string
}

// ListFiles lists all non-trashed files in a Drive folder.
func (u *Uploader) ListFiles(ctx context.Context, parentID string) ([]DriveFileInfo, error) {
	if u.Service == nil {
		return nil, fmt.Errorf("drive service not configured")
	}
	query := fmt.Sprintf("'%s' in parents and trashed=false", parentID)
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	list, err := u.Service.Files.List().
		Q(query).
		Fields("nextPageToken, files(id, name, mimeType, size, md5Checksum, webViewLink, webContentLink, parents)").
		PageSize(1000).
		Context(requestCtx).
		Do()
	if err != nil {
		return nil, fmt.Errorf("list Drive children for %s: %w", parentID, err)
	}
	// PR-DRIVE-LIST-NIL-GUARD (July 2026): guard against (nil, nil)
	// edge case in google-api-go-client Files.List. Without this
	// guard `len(list.Files)` below panics with nil deref. The
	// typed ErrDriveListNil sentinel lets callers errors.Is to
	// distinguish the empty-result from transient errors. Mirrors
	// the nil-guard pattern already in lookupFolderExact + the
	// `if list == nil || len(list.Files) == 0` short-circuit in
	// folder_manager.go::firstFolderID.
	if list == nil {
		return nil, fmt.Errorf("ListFiles: %w", ErrDriveListNil)
	}

	result := make([]DriveFileInfo, 0, len(list.Files))
	for _, f := range list.Files {
		result = append(result, DriveFileInfo{
			ID:             f.Id,
			Name:           f.Name,
			MimeType:       f.MimeType,
			Size:           f.Size,
			MD5Checksum:    f.Md5Checksum,
			WebViewLink:    f.WebViewLink,
			WebContentLink: f.WebContentLink,
			Parents:        f.Parents,
		})
	}
	return result, nil
}

// SearchFiles lists files matching an arbitrary Drive query string.
// Unlike ListFiles (which filters by parent folder), SearchFiles
// passes the raw query directly to Files.List().Q().
func (u *Uploader) SearchFiles(ctx context.Context, query string) ([]DriveFileInfo, error) {
	if u.Service == nil {
		return nil, fmt.Errorf("drive service not configured")
	}
	list, err := u.Service.Files.List().Q(query).
		Fields("files(id, name, mimeType, size, md5Checksum, webViewLink, webContentLink, parents)").
		Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	// PR-DRIVE-LIST-NIL-GUARD (July 2026): guard against the same
	// (nil, nil) edge case Files.List can return as ListFiles
	// above. Without this guard `len(list.Files)` below panics
	// with nil deref — the panic stack frame that triggered this
	// fix path was this exact SearchFiles function (called from
	// the async clip-enqueue adapter during POST
	// /api/media/register-batch on the wire-shape only flow
	// shipped in commit 4fda04e7).
	if list == nil {
		return nil, fmt.Errorf("SearchFiles: %w", ErrDriveListNil)
	}
	result := make([]DriveFileInfo, 0, len(list.Files))
	for _, f := range list.Files {
		result = append(result, DriveFileInfo{
			ID:             f.Id,
			Name:           f.Name,
			MimeType:       f.MimeType,
			Size:           f.Size,
			MD5Checksum:    f.Md5Checksum,
			WebViewLink:    f.WebViewLink,
			WebContentLink: f.WebContentLink,
			Parents:        f.Parents,
		})
	}
	return result, nil
}
