// Package drive — folder_manager.go (PR2.7)
//
// DriveFolderManagerAdapter wraps the concrete *driveapi.Service to
// satisfy the artlist.DriveFolderManager port declared at
// internal/application/assets/providers/artlist/ports.go. The adapter owns the raw SDK;
// the port hides it from callers. PR2.7 introduced this adapter so the
// application layer no longer reaches through a concrete dependency
// (*drive.Uploader.Service.Files.List...) to call raw Google Drive SDK
// methods.
//
// The adapter lives in internal/infrastructure because Drive IS a
// transport/storage mechanism, not part of the artlist "chain" pipeline
// (scraper → downloader → indexer → searcher) that the chain policy
// keeps in internal/application/assets/providers/artlist/. Chain policy therefore does
// NOT apply here.
//
// Retry policy mirrors the existing drive.Uploader behaviour: 3
// attempts on transient errors (429, 503, timeout) with exponential
// backoff (2s, 4s) via pkg/retry. Non-retryable errors short-circuit
// immediately via the IsRetryable predicate.
package drive

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// DriveFolderManagerAdapter wraps *driveapi.Service to satisfy the
// artlist.DriveFolderManager port. It is the only point in the system
// that calls Files.List / Files.Trash / Files.Get.Download /
// Files.Create.Media for artlist purposes; all other code paths use
// *drive.Uploader (the legacy narrow adapter) or this adapter via the
// port.
//
// PR2.7: introduced alongside the artlist.DriveFolderManager port to
// retire the raw SDK reach-through previously done in
// semantic_enricher.go::updateCumulativeMetadataJSON.
type DriveFolderManagerAdapter struct {
	svc *driveapi.Service
	log *zap.Logger
	// lookup is the P0.4 seam (folderLookupFunc). Production wiring
	// wraps Files.List in retry-with-jitter; tests inject a stub via
	// WithLookup to verify the no-duplicate contract without spinning
	// up an httptest server.
	lookup folderLookupFunc
}

// NewDriveFolderManagerAdapter constructs the adapter from a configured
// Drive SDK service. The composition root in internal/app/module_sources.go::WireArtlist
// builds the SDK once and reuses it across multiple consumers (this
// adapter + the existing Uploader), so Drive credentials are loaded
// exactly once at composition time.
func NewDriveFolderManagerAdapter(svc *driveapi.Service, log *zap.Logger) *DriveFolderManagerAdapter {
	if log == nil {
		log = zap.NewNop()
	}
	return &DriveFolderManagerAdapter{
		svc:    svc,
		log:    log,
		lookup: newDefaultFolderLookup(svc, log),
	}
}

// EnsureFolder creates (or reuses) a folder whose path is composed from
// parent + segments. The segments are nested: when segments = ["a", "b",
// "c"], "b" lives under "a" and "c" lives under "b". Each level is
// matched against existing folders first; missing folders are created.
// Returns the resolved folder ID for the final (leaf) segment.
//
// Special case: a single segment under an empty parent creates a
// top-level folder. Empty segments slice is rejected.
//
// PR2.7 narrowing note: this adapter matches folder names *exactly*.
// The legacy *drive.Uploader.GetOrCreateFolder fallback used
// fileutil.CleanFolderName for fuzzy matching (e.g. "My Folder" vs
// "my_folder"). Callers that previously relied on fuzzy matching
// (destination_service.go::ResolveDestination) MUST pre-sanitise the
// name before passing it here — destination_service uses
// textutil.SafeName(term) before constructing the segments, which
// produces a canonical form. Future Drive-folder callers should do the
// same (use pkg/textutil.SafeName / SafeFolderName and not pass
// user-supplied raw strings).
func (a *DriveFolderManagerAdapter) EnsureFolder(ctx context.Context, parent string, segments ...string) (string, error) {
	if a.svc == nil {
		return "", fmt.Errorf("drive service not configured")
	}
	if len(segments) == 0 {
		return "", fmt.Errorf("ensureFolder: at least one segment required")
	}

	currentParent := parent
	var leafID string
	for _, seg := range segments {
		if seg == "" {
			return "", fmt.Errorf("ensureFolder: empty segment in path")
		}
		folderID, err := a.findOrCreateFolder(ctx, currentParent, seg)
		if err != nil {
			return "", fmt.Errorf("ensureFolder: segment %q: %w", seg, err)
		}
		leafID = folderID
		currentParent = folderID
	}
	return leafID, nil
}

// folderLookupFunc is the seam through which findOrCreateFolder
// resolves "is there already a Drive folder with this name under
// parent?".
//
// The contract:
//   - (id, nil):       folder exists; id is the existing folder ID
//   - ("", nil):       folder does not exist; caller MUST create
//   - ("", err):       lookup failed; findOrCreateFolder propagates err
//     WITHOUT falling through to Create (P0.4 fix)
//
// Production wiring wraps the SDK's Files.List call with pkg/retry
// (3 attempts, ±30% jitter, 200ms initial backoff, IsRetryable gating
// on transient errors). Tests inject a stub via WithLookup to drive
// transient / retry-failure / non-retryable failure modes.
//
// P0.4 (June 2026): the pre-fix implementation called Files.List
// directly and fell through to Files.Create on ANY error, producing
// duplicate folders on Drive when a transient error masked an
// existing-folder match. The seam makes that race structurally
// impossible because the contract puts the "does it exist?" decision
// in a single retry-aware function.
type folderLookupFunc func(ctx context.Context, parent, name string) (id string, err error)

// folderLookupRetry* constants are the P0.4 production retry policy
// for the lookup pre-create step. Tighter than upload/download
// (200ms vs 2s) because the lookup is a lightweight metadata query
// (1 quota unit) — upload/download backoff must accommodate multi-MB
// content delivery.
const (
	folderLookupInitialBackoff = 200 * time.Millisecond
	folderLookupMaxBackoff     = 2 * time.Second
	folderLookupMaxAttempts    = 3
	folderLookupJitterFraction = 0.3
)

// WithLookup overrides the default folder lookup function. Production
// code never calls this method.
//
// Tests use this seam to inject transient-error stubs and verify the
// no-duplicate contract without spinning up an httptest server. The
// seam is small enough that its invariant ("returns id-or-empty, with
// err-on-failure") is easy to assert in tests.
func (a *DriveFolderManagerAdapter) WithLookup(fn folderLookupFunc) *DriveFolderManagerAdapter {
	a.lookup = fn
	return a
}

// newDefaultFolderLookup returns the production-default folderLookupFunc
// wiring the SDK's Files.List through pkg/retry with the P0.4 spec
// (retry-with-jitter on transient errors, abort-on-error-after-retry).
// Tests that need to inject custom lookup behaviour use WithLookup;
// this default is what production code runs.
//
// The closure captures (svc, log) so the seam-signature stays context-only.
func newDefaultFolderLookup(svc *driveapi.Service, log *zap.Logger) folderLookupFunc {
	return func(ctx context.Context, parent, name string) (string, error) {
		queryParts := []string{
			fmt.Sprintf("name = '%s'", strings.ReplaceAll(name, "'", "\\'")),
			"trashed = false",
			"mimeType = 'application/vnd.google-apps.folder'",
		}
		if parent != "" {
			queryParts = append(queryParts, fmt.Sprintf("'%s' in parents", parent))
		}
		query := strings.Join(queryParts, " and ")

		// result is the first match ID; the retry-loop's second return
		// carries the SDK error so pkg/retry.DoWithValue predicates on
		// it via isRetryableDriveErr. Returning ("", nil) on a successful
		// empty List is what surfaces "doesn't exist" to the caller.
		id, err := retry.DoWithValue(ctx, func() (string, error) {
			list, lerr := svc.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
			return firstFolderID(list), lerr
		}, retry.Options{
			MaxAttempts:    folderLookupMaxAttempts,
			InitialBackoff: folderLookupInitialBackoff,
			MaxBackoff:     folderLookupMaxBackoff,
			BackoffFactor:  2.0,
			JitterFraction: folderLookupJitterFraction,
			IsRetryable:    isRetryableDriveErr,
			OnRetry: func(attempt int, err error) {
				if log != nil {
					log.Warn("transient drive list error, retrying (P0.4: no fallback-to-create)",
						zap.String("folder_name", name),
						zap.Int("attempt", attempt+1),
						zap.Error(err))
				}
			},
		})
		if err != nil {
			return "", fmt.Errorf("lookup existing folder %q under %q: %w", name, parent, err)
		}
		return id, nil
	}
}

// firstFolderID returns the first folder ID from a Drive List result,
// or "" when the list is nil/empty. Used by newDefaultFolderLookup to
// map a *driveapi.FileList to the seam's string return value ("" =
// "doesn't exist", non-empty = "exists with this ID").
func firstFolderID(list *driveapi.FileList) string {
	if list == nil || len(list.Files) == 0 {
		return ""
	}
	return list.Files[0].Id
}

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
		return "", fmt.Errorf("findOrCreateFolder: create %q under %q: %w", name, parentID, err)
	}
	return created.Id, nil
}

// ListByQuery runs the supplied raw Drive search query and returns
// DriveFileRef entries. Trashed-entry filtering is the caller's
// responsibility (caller MUST include "and trashed = false" in the
// query when needed). Domain shape (DriveFileRef) keeps
// *driveapi.File out of the application layer.
func (a *DriveFolderManagerAdapter) ListByQuery(ctx context.Context, query string) ([]DriveFileRef, error) {
	if a.svc == nil {
		return nil, fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("listByQuery: query is required")
	}
	list, err := a.svc.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("drive list: %w", err)
	}
	out := make([]DriveFileRef, 0, len(list.Files))
	for _, f := range list.Files {
		out = append(out, DriveFileRef{ID: f.Id, Name: f.Name})
	}
	return out, nil
}

// Trash is intentionally NOT exposed on DriveFolderManagerAdapter
// (CARD-3, June 2026). The file-lifecycle surface (Files.Update{Trashed:true},
// File.Move / File.Rename / File.Cleanup) is the canonical owner of the
// raw SDK call, and it lives behind the drive.FileLifecycle port at
// internal/infrastructure/drive/file_lifecycle.go (see *FileLifecycleAdapter).
// godlike/06 §Data and Configuration Ownership — one owner per fact:
// the folder manager owns folder operations (EnsureFolder, ListByQuery,
// Download, Upload); the lifecycle adapter owns file-lifecycle commands
// (Trash, Move, Rename, Cleanup). Callers that previously called
// DriveFolderManagerAdapter.Trash (e.g. artlist semantic_enricher) now
// take a drive.FileLifecycle port directly from the composition root.

// Download fetches a file's content as a stream. The caller MUST close
// the returned io.ReadCloser. Retries on transient errors (429, 503,
// timeout) via pkg/retry. Returns body + content-type string for
// callers that need to branch on MIME.
func (a *DriveFolderManagerAdapter) Download(ctx context.Context, fileID string) (io.ReadCloser, string, error) {
	if a.svc == nil {
		return nil, "", fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(fileID) == "" {
		return nil, "", fmt.Errorf("download: file id is required")
	}
	type dlResult struct {
		body io.ReadCloser
		ct   string
	}
	res, err := retry.DoWithValue(ctx, func() (dlResult, error) {
		resp, err := a.svc.Files.Get(fileID).Context(ctx).Download()
		if err != nil {
			return dlResult{}, err
		}
		return dlResult{body: resp.Body, ct: resp.Header.Get("Content-Type")}, nil
	}, retry.Options{
		MaxAttempts:    3,
		InitialBackoff: 2 * time.Second,
		IsRetryable:    isRetryableDriveErr,
		OnRetry: func(attempt int, err error) {
			a.log.Warn("transient drive download error, retrying",
				zap.String("file_id", fileID), zap.Int("attempt", attempt+1), zap.Error(err))
		},
	})
	if err != nil {
		return nil, "", fmt.Errorf("drive download failed after 3 attempts: %w", err)
	}
	return res.body, res.ct, nil
}

// Upload uploads a local file to a Drive folder. Returns the
// webViewLink of the new/updated file. When a file with the same name
// already exists in the folder, the implementation updates it in
// place rather than creating a duplicate (matches legacy behaviour
// callers depend on). Retries on transient errors via pkg/retry.
//
// Soft error on the "find existing" lookup: when the lookup fails,
// the adapter logs WARN and tries Create anyway, which will produce
// a clearer Drive-side error if it really conflicts.
func (a *DriveFolderManagerAdapter) Upload(ctx context.Context, localPath, folderID, filename string) (string, error) {
	if a.svc == nil {
		return "", fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(localPath) == "" {
		return "", fmt.Errorf("upload: local path is required")
	}
	if strings.TrimSpace(filename) == "" {
		return "", fmt.Errorf("upload: filename is required")
	}
	type upResult struct {
		link string
	}
	res, err := retry.DoWithValue(ctx, func() (upResult, error) {
		f, err := os.Open(localPath)
		if err != nil {
			return upResult{}, fmt.Errorf("open local file: %w", err)
		}
		defer f.Close()

		existing, err := a.findFileByName(ctx, folderID, filename)
		if err != nil {
			a.log.Warn("failed to check for existing file", zap.String("name", filename), zap.Error(err))
		}

		file := &driveapi.File{Name: filename}
		if folderID != "" {
			file.Parents = []string{folderID}
		}
		var created *driveapi.File
		if existing != "" {
			created, err = a.svc.Files.Update(existing, file).
				Fields("id,webViewLink").
				Media(f).
				Context(ctx).
				Do()
		} else {
			created, err = a.svc.Files.Create(file).
				Fields("id,webViewLink").
				Media(f).
				Context(ctx).
				Do()
		}
		if err != nil {
			return upResult{}, err
		}
		return upResult{link: created.WebViewLink}, nil
	}, retry.Options{
		MaxAttempts:    3,
		InitialBackoff: 2 * time.Second,
		IsRetryable:    isRetryableDriveErr,
		OnRetry: func(attempt int, err error) {
			a.log.Warn("transient drive upload error, retrying",
				zap.String("filename", filename), zap.Int("attempt", attempt+1), zap.Error(err))
		},
	})
	if err != nil {
		return "", fmt.Errorf("drive upload failed after 3 attempts: %w", err)
	}
	return res.link, nil
}

// findFileByName returns the file ID of a non-trashed file in folderID
// with the given name, or empty string if none found. Private helper
// used by Upload to detect "same name already exists" → update flow.
// Callers that need richer metadata should use ListByQuery.
func (a *DriveFolderManagerAdapter) findFileByName(ctx context.Context, folderID, filename string) (string, error) {
	if folderID == "" || filename == "" {
		return "", nil
	}
	query := fmt.Sprintf("name = '%s' and '%s' in parents and trashed = false",
		strings.ReplaceAll(filename, "'", "\\'"), folderID)
	list, err := a.svc.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
	if err != nil {
		return "", err
	}
	if len(list.Files) == 0 {
		return "", nil
	}
	return list.Files[0].Id, nil
}
