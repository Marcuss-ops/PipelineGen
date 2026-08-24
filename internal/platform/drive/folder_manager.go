// Package drive — folder_manager.go (PR2.7, F3.14)
//
// DriveFolderManagerAdapter wraps the concrete *driveapi.Service to satisfy
// the narrow drive.FolderManagerPort defined in publisher.go (the
// composition-time contract that *delivery.Publisher* consumes via
// `folders FolderManagerPort`). After the F3.14 (June 2026) dead-method
// retirement, the adapter is intentionally single-method on the public
// surface — `EnsureFolder` is the only method callers exercise.
//
// F2.11 (June 2026) + F3.14: this adapter is the only point in the system
// that calls Files.Create + Files.List for folder operations; all file
// operations (ListByQuery / Download / Upload) were retired and migrated to
// their canonical owners per godlike/06 "one owner per fact":
//
//   - drive.Reader       — read surface (Files.List, Files.Get.Download)
//     owned by *drive.Uploader
//   - delivery.Publisher — write surface (folder-ensure + PutFile),
//     published here via FolderManagerPort
//   - drive.FileLifecycle — Trash / Move / Rename / Cleanup
//     owned by *FileLifecycleAdapter
//
// The PR2.7 wide-port surface (EnsureFolder + ListByQuery + Download +
// Upload) that confl ated those three is gone. Compile-time assertion in
// folder_manager_test.go pins the narrow contract:
//
//	var _ drivepkg.FolderManagerPort = (*drivepkg.DriveFolderManagerAdapter)(nil)
//
// Pattern 0 (port abstraction layer) — the application layer does not
// import this adapter. Instead, delivery.Publisher's composition-time
// contract is the narrow FolderManagerPort interface declared in
// publisher.go; *DriveFolderManagerAdapter happens to satisfy it via
// its EnsureFolder method. drive.Reader is a SEPARATE interface (the
// read surface — ListByQuery + DownloadFile) owned by *drive.Uploader;
// it does NOT satisfy FolderManagerPort (the two are intentionally
// distinct: FolderManagerPort owns folder operations, drive.Reader
// owns file reads, drive.FileLifecycle owns file-lifecycle commands,
// and delivery.Publisher owns the write surface). DRIVE-005 pinned
// these four canonical ports per Pattern 0 + godlike/06 "one owner
// per fact".
//
// Retry policy mirrors the existing drive.Uploader behaviour: 3
// attempts on transient errors (429, 503, timeout) with exponential
// backoff (2s, 4s) via pkg/retry. Non-retryable errors short-circuit
// immediately via the IsRetryable predicate.
package drive

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
	driveapi "google.golang.org/api/drive/v3"

	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// DriveFolderManagerAdapter wraps *driveapi.Service to satisfy the
// narrow drive.FolderManagerPort. On the FolderManagerPort surface
// (the only contract consumers import) the adapter exposes exactly ONE
// method — EnsureFolder — per the F2.11 + F3.14 narrow-port commitment.
// Folder operations only — the read surface (ListByQuery + Download)
// and the write surface (Upload + PutFile) live on different adapters
// (drive.Reader for read, delivery.Publisher for write).
//
// PR2.7: introduced alongside the retired-wide-port artlist.DriveFolderManager
// to retire the raw SDK reach-through previously done in
// semantic_enricher.go::updateCumulativeMetadataJSON.
//
// F3.14 (June 2026): the wide-port methods (ListByQuery, Download, Upload,
// findFileByName) were retired — zero callers remain after F2.11 closed
// the artlist + voiceover + cards paths to drive.Reader + delivery.Publisher.
// The single-method surface here is the canonical F3.14 end-state; future
// additions should be justified against drive.Reader / delivery.Publisher
// / drive.FileLifecycle ownerships before they land here.
type DriveFolderManagerAdapter struct {
	svc *driveapi.Service
	log *zap.Logger
	// lookup is the P0.4 seam (folderLookupFunc). Production wiring
	// wraps Files.List in retry-with-jitter; tests inject a stub via
	// WithLookup to verify the no-duplicate contract without spinning
	// up an httptest server.
	lookup folderLookupFunc

	// reLookup is the P0.7 seam for post-create duplicate detection.
	// Production wiring performs a full Files.List (no retry — the
	// creation just succeeded so Drive is known-reachable) and returns
	// the count of matching folders + the oldest folder ID. Tests
	// inject a stub via WithReLookup to simulate duplicate-detected
	// scenarios without spinning up a Drive httptest server.
	reLookup folderReLookupFunc

	// folderOps is the P0.7 singleflight keyed lock deduplicating
	// concurrent EnsureFolder calls for the same (parentID, name)
	// pair. Key = parentID + ":" + canonicalName. Mirrors Uploader's
	// folderOps field (uploader.go:58).
	folderOps singleflight.Group

	// folderCache stores resolved folder IDs for completed path
	// segments so repeated EnsureFolder calls can return immediately
	// without re-running lookup.
	folderCache sync.Map
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
// F3.14 + DRIVE-005: this method is the SINGLE public surface on the
// narrow FolderManagerPort. delivery.Publisher.Publish + ResolveFolder
// route exclusively through here for folder-hierarchy creation; the
// file-level operations (read / write / lifecycle) are owned by separate
// adapters per godlike/06.
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

		// P0.7: singleflight keyed lock deduplicates concurrent calls
		// for the same (parentID, name) pair. Key = parent + ":" + seg.
		// Mirrors Uploader.GetOrCreateFolder's folderOps pattern
		// (uploader_ops.go:87).
		key := currentParent + ":" + seg
		if cached, ok := a.folderCache.Load(key); ok {
			folderID := cached.(string)
			leafID = folderID
			currentParent = folderID
			continue
		}
		result, sfErr, _ := a.folderOps.Do(key, func() (any, error) {
			if cached, ok := a.folderCache.Load(key); ok {
				return cached.(string), nil
			}
			folderID, err := a.findOrCreateFolder(ctx, currentParent, seg)
			if err == nil && folderID != "" {
				a.folderCache.Store(key, folderID)
			}
			return folderID, err
		})
		if sfErr != nil {
			return "", fmt.Errorf("ensureFolder: segment %q: %w", seg, sfErr)
		}
		folderID := result.(string)
		if folderID != "" {
			a.folderCache.Store(key, folderID)
		}
		leafID = folderID
		currentParent = folderID
	}
	return leafID, nil
}

// ProbeFolderAccess verifies that a Drive folder with the given ID is
// reachable on Drive without creating any folder. Implements the
// FolderManagerPort.ProbeFolderAccess method added in P1.3 (July 2026)
// for the registry-driven startup validation in delivery package.
//
// Probe policy: Files.Get + retry-with-jitter (3 attempts, 200ms → 2s
// initial backoff, ±30% jitter, IsRetryable gating on transient errors).
// The probe IS side-effect-free (read-only) — it does NOT call
// Files.Create. Empty rootID is rejected with an explicit error
// (not a panic / not a Files.Get("") which Drive would reject with a
// 400 Bad Request).
//
// Why a separate method (vs. EnsureFolder with sentinel segment):
// EnsureFolder rejects empty segments, and any non-empty segment
// would either reuse the existing child (masking a root probe
// failure) or CREATE a child folder (side-effect we want to avoid
// at startup). Files.Get is the cleanest side-effect-free reach
// check that Drive offers — same retry policy as the production
// lookup, same transient classifier.
func (a *DriveFolderManagerAdapter) ProbeFolderAccess(ctx context.Context, rootID string) error {
	if a.svc == nil {
		return fmt.Errorf("drive service not configured")
	}
	if strings.TrimSpace(rootID) == "" {
		return fmt.Errorf("probeFolderAccess: root ID is empty")
	}

	_, err := retry.DoWithValue(ctx, func() (struct{}, error) {
		// Files.Get with a "trashed = false" soft-check so a folder
		// that exists but is in the Trash bin surfaces as "not found"
		// rather than "exists but trashed" — startup wants the
		// active folder, not the audit trail. Drive's default
		// Files.Get returns the folder regardless of trashed state;
		// the Fields("id,name,trashed") projection + the trashed-check
		// below closes the "root in trash ⇒ false positive" gap.
		f, gerr := a.svc.Files.Get(rootID).
			Fields("id,name,trashed").
			SupportsAllDrives(true).
			Context(ctx).
			Do()
		if gerr != nil {
			return struct{}{}, retry.WrapTransient(gerr)
		}
		if f.Trashed {
			return struct{}{}, fmt.Errorf("root folder %q is in the Drive Trash bin", rootID)
		}
		return struct{}{}, nil
	}, retry.Options{
		MaxAttempts:    folderLookupMaxAttempts,
		InitialBackoff: folderLookupInitialBackoff,
		MaxBackoff:     folderLookupMaxBackoff,
		BackoffFactor:  2.0,
		JitterFraction: folderLookupJitterFraction,
		IsRetryable:    retry.IsTransient,
		OnRetry: func(attempt int, err error) {
			if a.log != nil {
				a.log.Warn("transient drive root probe error, retrying (P1.3 StartupDriveRootsValidator)",
					zap.String("root_id", rootID),
					zap.Int("attempt", attempt+1),
					zap.Error(err))
			}
		},
	})
	return err
}

// folderLookupFunc and folderReLookupFunc type definitions have moved
// to folder_lookup.go (Pattern 5 split, July 2026).

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

// WithReLookup overrides the default post-create re-lookup function.
// Production code never calls this method.
//
// P0.7 (July 2026): tests inject a stub via this seam to simulate
// duplicate-detected (>1 match) scenarios without spinning up a
// Drive httptest server.
func (a *DriveFolderManagerAdapter) WithReLookup(fn folderReLookupFunc) *DriveFolderManagerAdapter {
	a.reLookup = fn
	return a
}
