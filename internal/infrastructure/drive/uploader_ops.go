package drive

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	pathutil "github.com/Marcuss-ops/PipelineGen/pkg/pathutil"
	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// GetOrCreateFolder looks up an existing folder under parentID with the
// canonical name; if absent, creates it. Returns the resolved folder ID
// (existing or newly-created).
//
// F1.6 invariants (June 2026, P0 #4 + #5 + #6):
//
//  1. Canonical name: input is sanitised via pkg/pathutil.SafeFolderName.
//     The canonical form is used in BOTH the Files.List query and the
//     Files.Create body. The legacy fuzzy branch
//     `fileutil.CleanFolderName(file.Name) == CleanFolderName(name)`
//     is REMOVED — exact-match only. Callers that previously relied
//     on fuzzy matching must pre-sanitise their inputs to the
//     canonical form (folder_manager.go and admin.go already do so
//     via pathutil.SafeFolderName upstream of calling *Uploader).
//
//  2. Fail-closed lookup (P0 #4 contract closes the duplicate-on-
//     -transient-error race): a non-retryable error from Files.List
//     propagates WITHOUT falling through to Create. Transient errors
//     (429/503/timeout) are retried via pkg/retry before propagating.
//     The pre-F1.6 `if err == nil && len(list.Files) > 0` short-circuit
//     swallowed transient errors into a fallthrough-to-Create path
//     that produced duplicate folders when two EnsureFolderPath
//     calls raced through both their Create branches.
//
//  3. Race-safety keyed lock (P0 #5, in-process deduplication):
//     concurrent calls for the same (parentID, canonicalName) pair
//     are deduplicated via singleflight.Group keyed by
//     `parentID + ":" + canonicalName`. The shared call observes
//     only ONE List / Create pair; concurrent callers receive the
//     same result without racing through the Create branch.
//
//  4. Cross-process race mitigation (P0 #5, second lookup after Create):
//     after a successful Create we run a SECOND Files.List ordered by
//     createdTime asc and return the OLDEST folder ID if multiple
//     folders with the canonical name exist. Drive does not natively
//     de-duplicate folder names within one parent; this defends against
//     another server instance racing through its own Create branch
//     between our List and Create. We intentionally do NOT trash the
//     duplicate here — campaign-level cleanup is a separate follow-up
//     sweep tracked in architecture/cleanup-sweep.md (TBD).
//
// Migration note: AdminAdapter.GetOrCreateFolder (admin.go) and
// DriveFolderManagerAdapter.EnsureFolder (folder_manager.go) already
// meet this contract via their own folderLookupFunc seams. They are
// UNCHANGED in this commit and continue to satisfy the contract via
// their parallel paths.
// Single-source-of-truth consolidation across the three is a follow-up
// wave (DRY-but-not-F1.6 work).
func (u *Uploader) GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	if u.Service == nil {
		return "", fmt.Errorf("drive service not configured")
	}

	canonicalName := pathutil.SafeFolderName(name)
	if canonicalName == "" {
		return "", fmt.Errorf("GetOrCreateFolder: canonical name is empty after sanitisation (input=%q)", name)
	}

	// F1.6 P0 #5: keyed lock keyed by `parentID+":"+canonicalName`.
	// Concurrent calls (in-process) for the same (parent, name) collapse
	// into a single canonical algorithm execution; callers receive the
	// same result without each racing through Create.
	key := parentID + ":" + canonicalName
	result, err, _ := u.folderOps.Do(key, func() (any, error) {
		return u.findOrCreateFolderSerialized(ctx, parentID, canonicalName)
	})
	if err != nil {
		return "", err
	}
	return result.(string), nil
}

// findOrCreateFolderSerialized is the singleflight-locked body of
// GetOrCreateFolder. It executes the canonical algorithm:
// Stage 1 — exact-match lookup via Files.List under retry-aware seam
//
//	(fail-closed on non-retryable error → no Create).
//
// Stage 2 — fresh Create using the canonical name.
// Stage 3 — cross-process race mitigation: post- Create second
//
//	Files.List ordered by createdTime asc; return oldest
//	folder if multiple match (Drive does not natively
//	de-duplicate folder names within a parent).
func (u *Uploader) findOrCreateFolderSerialized(ctx context.Context, parentID, canonicalName string) (string, error) {
	// ── Stage 1: exact-match lookup (fail-closed on error) ─────────
	existingID, err := u.lookupFolderExact(ctx, parentID, canonicalName)
	if err != nil {
		// F1.6 P0 #4 fail-closed: lookup err propagates WITHOUT
		// falling through to Create. The pre-fix `if err == nil &&
		// len(list.Files) > 0` short-circuit swallowed transient
		// errors into a fallthrough-to-Create path that produced
		// duplicate folders when two EnsureFolderPath calls' Create
		// branches both succeeded against a transient-List backdrop.
		return "", fmt.Errorf("lookup folder %q under %q: %w (F1.6 P0 #4 fail-closed: no fallthrough to Create)", canonicalName, parentID, err)
	}
	if existingID != "" {
		return existingID, nil
	}

	// ── Stage 2: fresh Create using canonical name ───────────────
	folder := &driveapi.File{
		Name:     canonicalName,
		MimeType: "application/vnd.google-apps.folder",
	}
	if parentID != "" {
		folder.Parents = []string{parentID}
	}
	created, err := u.Service.Files.Create(folder).Fields("id").Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("create folder %q under %q: %w", canonicalName, parentID, err)
	}
	if u.Log != nil {
		u.Log.Info("folder created",
			zap.String("name", canonicalName),
			zap.String("parent_id", parentID),
			zap.String("folder_id", created.Id),
		)
	}

	// ── Stage 3: cross-process race second lookup ───────────────
	// If our Create raced with another process's Create, the second
	// List will see >1 folder with the canonical name; we return the
	// OLDEST match (createdTime asc) so callers observe a stable ID
	// pointer — not the duplicate we just produced. We do NOT trash
	// the duplicate here; campaign cleanup is a separate follow-up
	// sweep.
	olderID, err := u.firstFolderIDByCreatedTimeAsc(ctx, parentID, canonicalName, created.Id)
	if err == nil && olderID != "" {
		return olderID, nil
	}
	return created.Id, nil
}

// lookupFolderExact performs an exact-match Files.List lookup wrapped in
// a retry-aware seam (transient 429/503/timeout → retry up to 3 attempts
// via pkg/retry; persistent errors propagate verbatim).
//
// Returns ("", nil) when no match is found vs ("", err) on real failures.
// The fail-closed contract lives one frame up in findOrCreateFolderSerialized.
//
// P0-1 (July 2026): body simplified to delegate to the canonical
// buildFolderLookupQuery + lookupFolderCanonical SSOT in folder_manager.go.
func (u *Uploader) lookupFolderExact(ctx context.Context, parentID, canonicalName string) (string, error) {
	fn := lookupFolderCanonical(u.Service, u.Log)
	return fn(ctx, parentID, canonicalName)
}

// firstFolderIDByCreatedTimeAsc returns the OLDEST folder ID matching the
// canonical name under parent. When multiple folders exist, the oldest
// wins (createdTime asc). When only the freshly-created folder exists,
// returns createdID (the param).
//
// Used by Stage 3 of findOrCreateFolderSerialized to mitigate a cross-
// process race: if our Create raced with another process's, we want to
// return a stable ID pointer rather than the duplicate we just produced.
// When this lookup itself fails transient, return the freshly-created
// ID so the caller still observes a usable value (the alternative —
// failing the whole call — would make this surface WORSE than the
// race it was meant to mitigate).
func (u *Uploader) firstFolderIDByCreatedTimeAsc(ctx context.Context, parentID, canonicalName, createdID string) (string, error) {
	var oldestID string
	_, err := retry.DoWithValue(ctx, func() (struct{}, error) {
		list, lerr := u.Service.Files.List().
			Q(fmt.Sprintf("name = '%s' and trashed = false and mimeType = 'application/vnd.google-apps.folder' and '%s' in parents",
				strings.ReplaceAll(canonicalName, "'", "\\'"),
				strings.ReplaceAll(parentID, "'", "\\'"))).
			Fields("files(id, name, createdTime)").
			OrderBy("createdTime asc").
			Context(ctx).
			Do()
		if lerr != nil {
			return struct{}{}, lerr
		}
		if list == nil || len(list.Files) == 0 {
			oldestID = createdID
			return struct{}{}, nil
		}
		oldestID = list.Files[0].Id
		return struct{}{}, nil
	}, folderLookupRetryOpts())
	if err != nil {
		return createdID, nil // defensive: see "When this lookup itself fails" doc-line above
	}
	return oldestID, nil
}

// folderLookupRetryOpts is the canonical retry policy shared by both
// lookup helpers (and previously the AdminAdapter lookup seam in
// admin.go). Tighter than upload/download (200ms vs 2s initial
// backoff) because the lookup is a lightweight metadata query (1
// quota unit) — upload/download backoff must accommodate multi-MB
// content delivery.
func folderLookupRetryOpts() retry.Options {
	return retry.Options{
		MaxAttempts:    3,
		InitialBackoff: 200 * time.Millisecond,
		MaxBackoff:     2 * time.Second,
		BackoffFactor:  2.0,
		JitterFraction: 0.3,
		IsRetryable:    retry.IsTransient,
	}
}

// GetFolderName returns the name of a Drive folder by its ID.
func (u *Uploader) GetFolderName(ctx context.Context, folderID string) (string, error) {
	if u.Service == nil {
		return "", fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(folderID) == "" {
		return "", nil
	}
	file, err := u.Service.Files.Get(folderID).Fields("name").Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return file.Name, nil
}

// TrashFile moves a file to the trash in Google Drive.
// This is safer than permanent deletion as files can be recovered.
//
// Deprecated: use FileLifecycle.Trash instead.
func (u *Uploader) TrashFile(ctx context.Context, fileID string) error {
	if u.Service == nil {
		return fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(fileID) == "" {
		return fmt.Errorf("file id is required")
	}

	_, err := u.Service.Files.Update(fileID, &driveapi.File{
		Trashed: true,
	}).Fields("id", "trashed").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to trash drive file: %w", err)
	}

	if u.Log != nil {
		u.Log.Info("drive file moved to trash", zap.String("file_id", fileID))
	}
	return nil
}

// DeleteFile permanently deletes a file from Google Drive.
// Use TrashFile instead for safer operations.
//
// Deprecated: use FileLifecycle.Delete instead.
func (u *Uploader) DeleteFile(ctx context.Context, fileID string) error {
	if u.Service == nil {
		return fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(fileID) == "" {
		return fmt.Errorf("file id is required")
	}
	if err := u.Service.Files.Delete(fileID).Context(ctx).Do(); err != nil {
		return fmt.Errorf("failed to delete drive file: %w", err)
	}

	if u.Log != nil {
		u.Log.Info("drive file deleted", zap.String("file_id", fileID))
	}
	return nil
}

// RenameFile renames a file or folder on Google Drive.
//
// Deprecated: use FileLifecycle.Rename instead.
func (u *Uploader) RenameFile(ctx context.Context, fileID, newName string) error {
	if u.Service == nil {
		return fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(fileID) == "" {
		return fmt.Errorf("file id is required")
	}
	if strings.TrimSpace(newName) == "" {
		return fmt.Errorf("new name is required")
	}

	_, err := u.Service.Files.Update(fileID, &driveapi.File{
		Name: newName,
	}).Fields("id", "name").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to rename drive file: %w", err)
	}

	u.Log.Info("drive file renamed",
		zap.String("file_id", fileID),
		zap.String("new_name", newName))
	return nil
}

// TrashFolder moves a folder to trash in Google Drive.
func (u *Uploader) TrashFolder(ctx context.Context, folderID string) error {
	if u.Service == nil {
		return fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(folderID) == "" {
		return fmt.Errorf("folder id is required")
	}

	_, err := u.Service.Files.Update(folderID, &driveapi.File{
		Trashed: true,
	}).Fields("id", "trashed").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to trash drive folder: %w", err)
	}

	if u.Log != nil {
		u.Log.Info("drive folder moved to trash", zap.String("folder_id", folderID))
	}
	return nil
}

// DeleteFolder permanently deletes a folder from Google Drive.
func (u *Uploader) DeleteFolder(ctx context.Context, folderID string) error {
	if u.Service == nil {
		return fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(folderID) == "" {
		return fmt.Errorf("folder id is required")
	}

	if err := u.Service.Files.Delete(folderID).Context(ctx).Do(); err != nil {
		return fmt.Errorf("failed to delete drive folder: %w", err)
	}

	if u.Log != nil {
		u.Log.Info("drive folder deleted", zap.String("folder_id", folderID))
	}
	return nil
}

// GetFileMD5 retrieves the MD5 checksum of a file from Google Drive.
func (u *Uploader) GetFileMD5(ctx context.Context, fileID string) (string, error) {
	if u.Service == nil {
		return "", fmt.Errorf("drive service not configured")
	}
	file, err := u.Service.Files.Get(fileID).Fields("id,md5Checksum").Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return file.Md5Checksum, nil
}

// FileMeta holds metadata about a Drive file.
type FileMeta struct {
	ID          string
	Name        string
	MimeType    string
	Size        int64
	WebViewLink string
	Parents     []string
	Trashed     bool
}

// GetFileMeta retrieves metadata for a Drive file.
func (u *Uploader) GetFileMeta(ctx context.Context, fileID string) (*FileMeta, error) {
	if u.Service == nil {
		return nil, fmt.Errorf("drive service not configured")
	}
	f, err := u.Service.Files.Get(fileID).Fields("id, name, mimeType, size, webViewLink, parents, trashed").Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	return &FileMeta{
		ID:          f.Id,
		Name:        f.Name,
		MimeType:    f.MimeType,
		Size:        f.Size,
		WebViewLink: f.WebViewLink,
		Parents:     f.Parents,
		Trashed:     f.Trashed,
	}, nil
}

// DownloadFile downloads a file from Drive and returns the response body and content type.
// The caller must close the returned io.ReadCloser.
func (u *Uploader) DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error) {
	if u.Service == nil {
		return nil, "", fmt.Errorf("drive service not configured")
	}
	resp, err := u.Service.Files.Get(fileID).Context(ctx).Download()
	if err != nil {
		return nil, "", err
	}
	return resp.Body, resp.Header.Get("Content-Type"), nil
}

// DriveFileInfo holds summary info for a file in a Drive listing.
type DriveFileInfo struct {
	ID             string
	Name           string
	MimeType       string
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
	list, err := u.Service.Files.List().
		Q(query).
		Fields("nextPageToken, files(id, name, mimeType, webViewLink, webContentLink, parents)").
		PageSize(1000).
		Context(ctx).
		Do()
	if err != nil {
		return nil, err
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
			WebViewLink:    f.WebViewLink,
			WebContentLink: f.WebContentLink,
			Parents:        f.Parents,
		})
	}
	return result, nil
}

// Admin returns the Uploader itself as a drive.Admin interface.
// Convenience method so callers holding *Uploader can pass it to
// functions accepting drive.Admin without a separate variable.
func (u *Uploader) Admin() Admin { return u }

// Ping verifies the Drive service is reachable by calling About.Get.
// Implemented as a single canonical API call so the readiness barrier
// can exercise the liveness contract without touching the file surface.
func (u *Uploader) Ping(ctx context.Context) error {
	if u.Service == nil {
		return fmt.Errorf("drive service not configured")
	}
	_, err := u.Service.About.Get().Fields("user").Context(ctx).Do()
	return err
}

// MoveFile moves a file from one folder to another by updating its parents.
func (u *Uploader) MoveFile(ctx context.Context, fileID, fromFolderID, toFolderID string) error {
	if u.Service == nil {
		return fmt.Errorf("drive service not configured")
	}
	f, err := u.Service.Files.Get(fileID).Fields("id,parents").Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to get file: %w", err)
	}
	var currentParents string
	if len(f.Parents) > 0 {
		currentParents = f.Parents[0]
	}
	_, err = u.Service.Files.Update(fileID, &driveapi.File{}).
		Fields("id,parents").
		AddParents(toFolderID).
		RemoveParents(currentParents).
		Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("failed to move file: %w", err)
	}
	u.Log.Info("file moved", zap.String("file_id", fileID), zap.String("to_folder", toFolderID))
	return nil
}

// FileIsNotTrashed checks that a Drive file/folder exists AND is not in the trash.
// Google Drive's Files.Get returns the resource even for trashed items, so a simple
// existence check (FileExists) is not sufficient to detect trashed folders.
func (u *Uploader) FileIsNotTrashed(ctx context.Context, fileID string) (bool, error) {
	if u.Service == nil {
		return false, fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(fileID) == "" {
		return false, nil
	}

	file, err := u.Service.Files.Get(fileID).Fields("id", "trashed").Context(ctx).Do()
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "notFound") {
			return false, nil
		}
		return false, err
	}

	return !file.Trashed, nil
}

// FileExists checks if a file exists on Google Drive.
// SearchFiles lists files matching an arbitrary Drive query string.
// Unlike ListFiles (which filters by parent folder), SearchFiles
// passes the raw query directly to Files.List().Q().
func (u *Uploader) SearchFiles(ctx context.Context, query string) ([]DriveFileInfo, error) {
	if u.Service == nil {
		return nil, fmt.Errorf("drive service not configured")
	}
	list, err := u.Service.Files.List().Q(query).
		Fields("files(id, name, mimeType, webViewLink, webContentLink, parents)").
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
			WebViewLink:    f.WebViewLink,
			WebContentLink: f.WebContentLink,
			Parents:        f.Parents,
		})
	}
	return result, nil
}

func (u *Uploader) FileExists(ctx context.Context, fileID string) (bool, error) {
	if u.Service == nil {
		return false, fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(fileID) == "" {
		return false, nil
	}

	_, err := u.Service.Files.Get(fileID).Fields("id", "trashed").Context(ctx).Do()
	if err != nil {
		// Check if it's a 404
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "notFound") {
			return false, nil
		}
		return false, err
	}

	return true, nil
}
