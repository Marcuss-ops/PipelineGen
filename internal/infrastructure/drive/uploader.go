package drive

import (
	"context"
	"errors"
	"os"
	"sync"

	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
	driveapi "google.golang.org/api/drive/v3"
)

// LookupFunc is the seam signature for a file-existence lookup. The
// production wiring delegates to Uploader.FindFileByName or
// Uploader.FindFileByIdempotencyKey depending on whether idemKey is
// empty. Tests inject overrides via struct-literal field injection
// (no package-level mutation).
//
// P2.1 (July 2026): the seam was migrated from a package-level var to
// a struct field on *Uploader. Pre-P2.1 tests using `lookupFunc = ...`
// + t.Cleanup could leak between parallel runs through cross-test
// package-state contamination; the field-based surface is per-instance
// and safe under t.Parallel.
//
// P0.6 (July 2026): idemKey parameter added for idempotency-key-based
// lookup. When empty, the implementation falls back to filename-based
// lookup (backward compat).
type LookupFunc func(u *Uploader, ctx context.Context, folderID, filename, idemKey string) (ExistingFileLookup, error)

// OpenFileFunc is the seam signature for opening a local file. The
// production wiring delegates to os.Open. Tests inject overrides via
// struct-literal field injection (no package-level mutation).
//
// P2.1 (July 2026): see LookupFunc comment \u2014 same migration rationale
// (per-instance state, parallel-safe).
type OpenFileFunc func(path string) (*os.File, error)

// Uploader handles Google Drive file operations.
//
// F1.6 (June 2026, P0 #4 + #5 + #6): folderOps is the in-process race-safety
// keyed lock applied to GetOrCreateFolder. The zero value is ready to use;
// concurrent calls for the same (parentID, canonicalName) pair are
// deduplicated by singleflight.Group.Do(key=parentID+":"+canonicalName, ...).
// The shared call observes only ONE List/Create pair; concurrent callers
// receive the same result without racing through Create.
//
// P2.1 (July 2026): lookupFunc and openFile are the previous package-level
// test seams migrated to struct fields for per-instance test isolation.\n// Both default to the production helper when nil via u.lookupExisting /\n// u.openReader lazy-default paths; tests inject overrides via struct literal.
type Uploader struct {
	Service     *driveapi.Service
	Log         *zap.Logger
	folderOps   singleflight.Group // F1.6 P0 #5 keyed lock: parentID+":"+canonicalName
	folderCache sync.Map           // completed key -> folder ID
	folderLocks sync.Map           // key -> *sync.Mutex

	// lookupFunc and openFile are the P2.1 test seams (per-instance,
	// not package-level). Production code reads them through the lazy
	// helpers below, so existing callers that use `&Uploader{...}`\n	// struct literals without field overrides continue to behave as\n	// before (lazy default resolves to FindFileByName / os.Open).
	lookupFunc LookupFunc
	openFile   OpenFileFunc
}

// RemoteFile describes a file already present on Google Drive.
type RemoteFile struct {
	FileID      string
	Name        string
	WebViewLink string
	MD5Checksum string
}

// ExistingFileLookup is the multi-match result of FindFileByName.
// Pre-Wave B2 (June 2026) FindFileByName returned only the first match,
// silently truncating the second/third/... matches — which made
// overwrite/skip non-deterministic when multiple files shared the same
// name+parent (e.g. a user manually uploaded a sibling copy and the
// pipeline then uploaded another). Wave B2 makes the surface
// exhaustive: callers MUST branch on len(Matches) to distinguish
//
//	0 matches → no existing file, take the Create branch
//	1 match   → apply the chosen ConflictPolicy against Matches[0]
//	>1 match  → fail-closed: surface ErrAmbiguousDriveFile
//	            (NEVER silently pick the first match on ambiguous state)
//
// The zero value (len(Matches) == 0) is the canonical "no match" surface,
// matching the pre-Wave B2 (nil, nil) return contract semantically.
type ExistingFileLookup struct {
	Matches []RemoteFile
}

// ErrAmbiguousDriveFile is the canonical sentinel returned when
// FindFileByName reports more than one non-trashed match for the
// (folderID, filename) tuple. Callers errors.Is against this sentinel
// to distinguish "multiple omonimi on Drive" from other lookup
// failures (rate limit, network timeout, malformed query). Surfacing
// the sentinel at the port boundary is the Wave B2 contract change —
// pre-Wave B2 the truncation to first-match hid this case entirely.
//
// Mirrors the per-package sentinels already defined in publisher.go
// (ErrMissingDestinationRegistry, ErrMissingFolderManager,
// ErrMissingFileUploader) — exported, errors.Is-friendly, surface-stable.
var ErrAmbiguousDriveFile = errors.New("drive: ambiguous file match: multiple non-trashed files with the same name+parent exist on Drive")

// PutFile is the sole upload operation. Legacy path-based upload methods
// were retired with the Drive Publisher cutover.
// FindFileByName and FindFileByIdempotencyKey have moved to uploader_find.go
// (Pattern 5 split, July 2026).
// lookupExisting runs the file-existence lookup with lazy default
// injection. Production code uses this in PutFile. Tests inject
// `lookupFunc` via struct literal override; the lazy default falls
// through to Uploader.FindFileByName or FindFileByIdempotencyKey
// depending on whether idemKey is empty.
//
// Pre-P2.1 the package-level `var lookupFunc` carried the seam;
// per P2.1 the seam is per-instance.
func (u *Uploader) lookupExisting(ctx context.Context, folderID, filename, idemKey string) (ExistingFileLookup, error) {
	if u.lookupFunc != nil {
		return u.lookupFunc(u, ctx, folderID, filename, idemKey)
	}
	if idemKey != "" {
		return u.FindFileByIdempotencyKey(ctx, folderID, idemKey)
	}
	return u.FindFileByName(ctx, folderID, filename)
}

// openReader opens a local file with lazy default injection. Production
// callers (doUploadFile, doPutFile) go through this helper; tests inject
// `openFile` via struct literal override; the lazy default falls through
// to os.Open.
//
// Pre-P2.1 the package-level `var openFile` carried the seam;
// per P2.1 the seam is per-instance.
func (u *Uploader) openReader(path string) (*os.File, error) {
	if u.openFile != nil {
		return u.openFile(path)
	}
	return os.Open(path)
}
