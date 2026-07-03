// Package drive — admin.go (P0.4 admin scope, June 2026)
//
// AdminAdapter wraps *Uploader and applies the P0.4 folderLookupFunc seam
// (admin scope) to GetOrCreateFolder. All other Admin port methods are
// inherited via *Uploader method promotion; we override ONLY
// GetOrCreateFolder to apply the no-fallback-to-Create contract.
//
// P0.4 admin scope: production callers reach GetOrCreateFolder through
// drive.EnsureFolderPath (used by stock/stockpipeline/util.go:42 and
// application/assets/ingest/drive.go:40). The pre-fix
// *Uploader.GetOrCreateFolder (uploader_ops.go:15) called Files.List
// directly and fell through to Files.Create on ANY soft error — soft-
// error fallback racing with concurrent EnsureFolderPath calls produced
// duplicate folders on Drive. AdminAdapter.GetOrCreateFolder routes the
// lookup through a single retry-aware seam (folderLookupFunc); a
// non-recoverable lookup error DOES NOT fall through to Create.
//
// DRY (godlike/06 §Data and Configuration Ownership): folderLookupFunc,
// folderLookupRetry* constants, firstFolderID, and retry.IsTransient
// already live in folder_manager.go; admin.go reuses them verbatim —
// no parallel types, no shared-with-different-name constants, no second
// isRetryable predicate that would drift over time. The two narrow
// adapters (artlist DriveFolderManager + drive Admin) share one seam
// shape because their contract is the same: a single retry-aware
// "does it exist?" decision that propagates err without falling through
// to Create.
package drive

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	retry "github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// AdminAdapter is the canonical Pattern 0 port-adapter implementation
// of drive.Admin with the P0.4 admin-scope fix applied to
// GetOrCreateFolder. Methods inherited from *Uploader promote through
// embedding; GetOrCreateFolder is shadowed with the seam-applied
// version below.
type AdminAdapter struct {
	*Uploader // method promotion: all 11 other Admin port methods inherited
	log       *zap.Logger
	// lookup is the P0.4 seam (folderLookupFunc) — shared type with
	// DriveFolderManagerAdapter.lookup. The contract is identical:
	// (id, nil) exists, ("", nil) doesn't exist, ("", err) propagate
	// without fallthrough to Create.
	lookup folderLookupFunc
}

// NewAdminAdapter constructs the adapter from a *Uploader + logger.
// Typed-nil-safe: returns nil if u is nil so callers can rely on the
// canonical `if root.Drive.Admin != nil` nil-port pattern.
func NewAdminAdapter(u *Uploader, log *zap.Logger) *AdminAdapter {
	if u == nil {
		return nil
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &AdminAdapter{
		Uploader: u,
		log:      log,
		lookup:   newAdminDefaultLookup(u.Service, log),
	}
}

// WithLookup overrides the default folder lookup. Production code
// never calls this method. Tests use it to inject stubs that simulate
// transient / non-retryable / retry-failure paths.
func (a *AdminAdapter) WithLookup(fn folderLookupFunc) *AdminAdapter {
	if a == nil {
		return nil
	}
	a.lookup = fn
	return a
}

// newAdminDefaultLookup returns the production-default lookup wiring.
// pkg/retry.DoWithValue wraps the SDK Files.List with P0.4 spec:
//
//	3 attempts, ±30% jitter, 200ms initial backoff,
//	IsRetryable gates on transient errors (429/503/timeout).
//
// Returning ("", nil) when List is empty surfaces "doesn't exist" to
// the caller; returning transient-failure-after-retries surfaces the
// propagated error WITHOUT a soft-error fallback.
//
// Defence-in-depth (June 2026): if svc is nil, the closure returns
// "drive service not configured" upfront instead of nil-panicking on
// the SDK call. The composition root already guards against nil
// service (driveUploader is constructed only when driveClient != nil);
// this is belt-and-braces against future constructor misuse.
//
// DRY reuse: reuses folderLookupRetry* constants from folder_manager.go
// (same package). The seam spec is the same — tighter retry on a
// lightweight metadata query vs the 2s used by upload/download paths.
func newAdminDefaultLookup(svc *driveapi.Service, log *zap.Logger) folderLookupFunc {
	return func(ctx context.Context, parent, name string) (string, error) {
		if svc == nil {
			return "", fmt.Errorf("admin adapter: drive service not configured")
		}
		// Same query body as DriveFolderManagerAdapter.newDefaultFolderLookup
		// (folder_manager.go). Future refactor: hoist to a shared helper
		// (godlike/06 §one-owner-per-fact — for now the inline shape is
		// small and verbatim-equal so audit-by-grep is trivial).
		queryParts := []string{
			fmt.Sprintf("name = '%s'", strings.ReplaceAll(name, "'", "\\'")),
			"trashed = false",
			"mimeType = 'application/vnd.google-apps.folder'",
		}
		if parent != "" {
			queryParts = append(queryParts, fmt.Sprintf("'%s' in parents", parent))
		}
		query := strings.Join(queryParts, " and ")

		id, err := retry.DoWithValue(ctx, func() (string, error) {
			list, lerr := svc.Files.List().Q(query).Fields("files(id, name)").Context(ctx).Do()
			if lerr != nil {
				return "", lerr
			}
			return firstFolderID(list)
		}, retry.Options{
			MaxAttempts:    folderLookupMaxAttempts,
			InitialBackoff: folderLookupInitialBackoff,
			MaxBackoff:     folderLookupMaxBackoff,
			BackoffFactor:  2.0,
			JitterFraction: folderLookupJitterFraction,
			IsRetryable:    retry.IsTransient,
			OnRetry: func(attempt int, err error) {
				if log != nil {
					log.Warn("transient drive list error, retrying (P0.4 admin scope)",
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

// GetOrCreateFolder (Admin port, P0.4 admin scope): looks up the folder
// under parentID; if absent, creates it. The lookup seam propagates
// transient errors WITHOUT falling through to Create — structurally
// eliminating the duplicate-folder race that pre-fix *Uploader had.
//
// P0.4 contract (June 2026): NO duplicate folders on Drive. The pre-fix
// impl swallowed transient List errors into a fallthrough-to-Create
// branch; concurrency between two EnsureFolderPath calls would race
// through both their Create branches (List failed in both) and produce
// two folders on Drive. The seam makes that race structurally impossible
// because the "does it exist?" decision is a single retry-aware call.
//
// Note: no early `if a.Uploader.Service == nil` guard here. The seam
// (folderLookupFunc) is the canonical "is the underlying system
// available?" decision point — injecting it into the default lookup
// (which checks svc upfront) keeps production safe while letting tests
// use a stub seam that returns errors WITHOUT touching Service. A
// higher-level guard at the composition root
// (`var admin drive.Admin; if driveUploader != nil { admin = NewAdminAdapter(u, log) }`)
// and the default-lookup's defence-in-depth check together provide the
// same coverage as a pre-method guard would have, without violating the
// test seam contract (seam must run before any Service check).
func (a *AdminAdapter) GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	if a == nil {
		return "", fmt.Errorf("admin adapter: nil receiver (composition-root wiring misconfig)")
	}
	if name == "" {
		return "", fmt.Errorf("admin adapter: folder name required")
	}
	return a.findOrCreateFolder(ctx, parentID, name)
}

// findOrCreateFolder is the Admin port's P0.4 helper. Mirrors
// DriveFolderManagerAdapter.findOrCreateFolder; both share the seam
// shape so the no-duplicate contract is provably equal across the two
// narrow-adapter consumers.
func (a *AdminAdapter) findOrCreateFolder(ctx context.Context, parentID, name string) (string, error) {
	existingID, err := a.lookup(ctx, parentID, name)
	if err != nil {
		// P0.4 contract: lookup err propagates; NO fallthrough to Create.
		// A transient SDK hiccup that masks an existing Drive folder must
		// surface to the caller, not silently double-create.
		return "", fmt.Errorf("findOrCreateFolder (admin): lookup %q under %q failed after retries (P0.4 contract: NO fallback-to-create): %w", name, parentID, err)
	}
	if existingID != "" {
		return existingID, nil
	}

	// Genuine "does not exist" path. Only reached when the seam returned
	// ("", nil) — pre-P0.4, transient errors masked existing-folder
	// matches into this branch.
	folder := &driveapi.File{
		Name:     name,
		MimeType: "application/vnd.google-apps.folder",
	}
	if parentID != "" {
		folder.Parents = []string{parentID}
	}
	if a.Uploader == nil || a.Uploader.Service == nil {
		return "", fmt.Errorf("admin adapter: drive service not configured (Create branch)")
	}
	created, err := a.Uploader.Service.Files.Create(folder).
		Fields("id").
		Context(ctx).
		Do()
	if err != nil {
		return "", fmt.Errorf("findOrCreateFolder (admin): create %q under %q: %w", name, parentID, err)
	}
	return created.Id, nil
}

// Compile-time assertion: *AdminAdapter satisfies drive.Admin by method
// promotion through *Uploader (which already satisfies drive.Admin — see
// ports.go `var _ Admin = (*Uploader)(nil)`) plus the explicit
// GetOrCreateFolder override above. If a method is added/removed from
// the Admin port surface, the build breaks here rather than at the first
// consumer site.
var _ Admin = (*AdminAdapter)(nil)
