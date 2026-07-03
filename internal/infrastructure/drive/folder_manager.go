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
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
	"golang.org/x/sync/singleflight"

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
// ErrAmbiguousDriveFolder is the canonical sentinel returned when
// a post-create re-lookup finds more than one non-trashed folder
// with the same name under the same parent on Drive. This is the
// folder-level parallel to ErrAmbiguousDriveFile for files.
//
// P0.7 (July 2026): the pre-fix findOrCreateFolder created a folder
// and returned created.Id without checking whether a cross-process
// race produced a duplicate. The re-lookup after Create now detects
// the >1 case and surfaces this sentinel so callers can fail-closed
// rather than silently returning a folder ID that may collide with
// another instance's folder. Callers errors.Is against this sentinel.
var ErrAmbiguousDriveFolder = errors.New("drive: ambiguous folder match: multiple non-trashed folders with the same name+parent exist on Drive")

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
		result, sfErr, _ := a.folderOps.Do(key, func() (any, error) {
			return a.findOrCreateFolder(ctx, currentParent, seg)
		})
		if sfErr != nil {
			return "", fmt.Errorf("ensureFolder: segment %q: %w", seg, sfErr)
		}
		folderID := result.(string)
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
			return struct{}{}, retry.ClassifyGoogleAPIError(gerr)
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

// folderReLookupFunc is the P0.7 seam for post-create duplicate
// detection. Production wiring performs a full Files.List query
// (no retry — the creation just succeeded so Drive is
// known-reachable) and returns count + oldestID. Tests inject a
// stub via WithReLookup.
//
// The contract:
//   - (0, "", nil):           no matching folders (unexpected; treat as ok)
//   - (1, oldestID, nil):     exactly one match (the one we just created)
//   - (>1, oldestID, nil):    duplicate detected → ErrAmbiguousDriveFolder
//   - (0, "", err):           re-lookup failed (transient); return created.ID
//     defensively (same policy as uploader_ops.go Stage 3)
type folderReLookupFunc func(ctx context.Context, parent, name string) (count int, oldestID string, err error)

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
		// it via retry.IsTransient. Returning ("", nil) on a successful
		// empty List is what surfaces "doesn't exist" to the caller.
		id, err := retry.DoWithValue(ctx, func() (string, error) {
			list, lerr := svc.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
			return firstFolderID(list), retry.ClassifyGoogleAPIError(lerr)
		}, retry.Options{
			MaxAttempts:    folderLookupMaxAttempts,
			InitialBackoff: folderLookupInitialBackoff,
			MaxBackoff:     folderLookupMaxBackoff,
			BackoffFactor:  2.0,
			JitterFraction: folderLookupJitterFraction,
			IsRetryable:    retry.IsTransient,
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
		return "", fmt.Errorf("findOrCreateFolder: create %q under %q: %w", name, parentID, retry.ClassifyGoogleAPIError(err))
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
	queryParts := []string{
		fmt.Sprintf("name = '%s'", strings.ReplaceAll(name, "'", "\\'")),
		"trashed = false",
		"mimeType = 'application/vnd.google-apps.folder'",
	}
	if parent != "" {
		queryParts = append(queryParts, fmt.Sprintf("'%s' in parents", parent))
	}
	query := strings.Join(queryParts, " and ")

	list, lerr := a.svc.Files.List().
		Q(query).
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
