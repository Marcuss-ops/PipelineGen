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
	"errors"
	"fmt"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
)

// ErrAdminAdapterUploaderNil is the composition-time fail-closed guard
// for the AdminAdapter.Uploader embedded field. Surfaced when a caller
// (typically the composition root in internal/app/wire_assets.go) types
// *AdminAdapter out of deps.Delivery.Admin but the embedded Uploader
// is nil — the panic class the pre-PR build would raise on the FIRST
// port call (FindFolder, UploadFile, …).
//
// Canonical SSOT: emitted only by the AdminAdapter branch in WireAssets.
// Construction-side guard (NewAdminAdapter returns nil if u==nil)
// catches the canonical path; this guard catches (a) the typed-NIL
// interface trap, (b) test paths that build *AdminAdapter via direct
// struct literal, (c) post-hoc mutation.
//
// godlike/06 SSOT: sentinel message does NOT carry a self-identifying
// `drive:` prefix because its canonical surfacing is always wrapped
// via WrapDriveAdminError (which adds the `WireAssets: drive.Admin:`
// subsystem label). A bare `drive:` prefix would create a double-
// prefix redundancy in the rendered output.
//
// godlike/07 typed-error contract: callers probe via
// errors.Is(err, drive.ErrAdminAdapterUploaderNil).
var ErrAdminAdapterUploaderNil = errors.New("AdminAdapter has nil embedded *Uploader (PR-ADAPTER-NIL-GUARD fail-closed)")

// ErrAdminUnknownType is the godlike/07 NO-FAKE-AVAILABILITY guard for
// Branch 3 in WireAssets (the silent-nil fallthrough): when
// deps.Delivery.Admin is neither *Uploader nor *AdminAdapter, the
// composition root MUST fail-closed rather than leave driveUploader
// nil silently. Pre-PR, a future Admin port variant or test stub
// routing to the wrong concrete type would produce a silent failure
// that surfaces later as a panic mid-flight on the first port call.
//
// godlike/06 SSOT: sentinel message does NOT carry a self-identifying
// `drive:` prefix (mirrors ErrAdminAdapterUploaderNil's rationale —
// its canonical surfacing is wrapped via WrapDriveAdminError).
var ErrAdminUnknownType = errors.New("unexpected Admin concrete type — composition wiring is wrong; expected *Uploader or *AdminAdapter (PR-ADAPTER-NIL-GUARD fail-closed)")

// WrapDriveAdminError is the canonical composition-root wrap helper
// for drive.Admin fail-closed errors. godlike/06 SSOT one-canonical-
// owner-per-fact: ALL composition-root wraps of drive.Admin typed
// sentinels MUST route through this helper so the wrap shape stays
// byte-stable across the codebase.
//
// The "WireAssets: drive.Admin:" subsystem label matches the existing
// composition-root fail-closed pattern in wire_assets.go (clips /
// storage / diagnostics / search / voiceover / soundeffect /
// register). Wrapping at the helper level (not the call site) means
// the test fixture TestDriveAdminSentinels_TypedErrorContract
// exercises the EXACT production call site, so any drift in the
// wrap shape surfaces as a test failure.
func WrapDriveAdminError(cause error) error {
	return fmt.Errorf("WireAssets: drive.Admin: %w", cause)
}

// AdminAdapter is the canonical Pattern 0 port-adapter implementation
// of drive.Admin with the P0.4 admin-scope fix applied to
// GetOrCreateFolder. Methods inherited from *Uploader promote through
// embedding; GetOrCreateFolder is shadowed with the seam-applied
// version below.
//
// P0-1 (July 2026): added reLookup seam for post-create duplicate
// detection, mirroring DriveFolderManagerAdapter's P0.7 contract.
type AdminAdapter struct {
	*Uploader // method promotion: folder/lifecycle/read methods inherited
	log       *zap.Logger
	// lookup is the P0.4 seam (folderLookupFunc) — shared type with
	// DriveFolderManagerAdapter.lookup. The contract is identical:
	// (id, nil) exists, ("", nil) doesn't exist, ("", err) propagate
	// without fallthrough to Create.
	lookup folderLookupFunc
	// reLookup is the P0.7 seam for post-create duplicate detection.
	// Production wiring performs a Drive Files.List (no retry) and
	// returns the count of matching folders. Tests inject a stub via
	// WithReLookup.
	reLookup folderReLookupFunc
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
	a := &AdminAdapter{
		Uploader: u,
		log:      log,
		lookup:   newAdminDefaultLookup(u.Service, log),
	}
	// P0-7 (July 2026): wire the default production re-lookup so the
	// post-create duplicate detection fires in production — not just
	// when tests inject a stub via WithReLookup.
	a.reLookup = a.reLookupProduction
	return a
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

// WithReLookup overrides the default post-create re-lookup function.
// P0-7 (July 2026): tests inject a stub via this seam to simulate
// duplicate-detected scenarios without spinning up a httptest server.
// Mirrors DriveFolderManagerAdapter.WithReLookup.
func (a *AdminAdapter) WithReLookup(fn folderReLookupFunc) *AdminAdapter {
	if a == nil {
		return nil
	}
	a.reLookup = fn
	return a
}

// newAdminDefaultLookup returns the production-default lookup wiring.
// P0-1 (July 2026): delegates to the canonical lookupFolderCanonical SSOT
// in folder_manager.go, retiring the duplicated query construction.
func newAdminDefaultLookup(svc *driveapi.Service, log *zap.Logger) folderLookupFunc {
	return lookupFolderCanonical(svc, log)
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

// doReLookup performs the P0.7 post-create re-lookup. Delegates to
// the reLookup seam if injected (test path), otherwise does a
// production Drive Files.List. Mirrors DriveFolderManagerAdapter.doReLookup.
func (a *AdminAdapter) doReLookup(ctx context.Context, parent, name string) (count int, oldestID string, err error) {
	if a == nil {
		return 0, "", fmt.Errorf("admin adapter: nil receiver")
	}
	if a.reLookup != nil {
		return a.reLookup(ctx, parent, name)
	}
	return a.reLookupProduction(ctx, parent, name)
}

// reLookupProduction performs a Drive Files.List query for folders
// matching (name, parent, non-trashed, folder mimeType), ordered by
// createdTime ascending. Returns (count, oldestID, nil).
// P0-7 (July 2026): mirrors DriveFolderManagerAdapter.reLookupProduction.
func (a *AdminAdapter) reLookupProduction(ctx context.Context, parent, name string) (count int, oldestID string, err error) {
	if a.Uploader == nil || a.Uploader.Service == nil {
		return 0, "", fmt.Errorf("admin adapter: drive service not configured")
	}
	query := buildFolderLookupQuery(parent, name)
	list, lerr := a.Uploader.Service.Files.List().
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

	// ── P0.7 (July 2026): cross-process race re-lookup ─────────
	// If our Create raced with another process's Create, the re-lookup
	// will see >1 folder with the same name+parent. Fail-closed via
	// ErrAmbiguousDriveFolder — mirrors DriveFolderManagerAdapter.
	count, oldestID, reLookupErr := a.doReLookup(ctx, parentID, name)
	if reLookupErr != nil {
		// Defensive: re-lookup failed transiently → return created.ID.
		a.log.Warn("post-create re-lookup failed, returning freshly-created ID (P0.7 admin defensive)",
			zap.String("folder_name", name),
			zap.String("parent_id", parentID),
			zap.String("created_id", created.Id),
			zap.Error(reLookupErr))
		return created.Id, nil
	}
	if count > 1 {
		return "", fmt.Errorf("findOrCreateFolder (admin): post-create re-lookup for %q under %q found %d matching folders (oldest=%q, created=%q): %w",
			name, parentID, count, oldestID, created.Id, ErrAmbiguousDriveFolder)
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
