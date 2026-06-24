// Package app — system_adapters.go (Wave 14 PR4 cleanup, June 24 2026)
//
// NEW file created to retire the three internal/infrastructure/* imports
// previously held by api/system/{handler,handler_drive,module}.go. This
// file lives in the composition root because that's the only location
// allowed to import infrastructure packages (AGENTS.md §13, ARCHITECTURE.md §3).
//
// Each adapter exposes a minimal port surface declared by the api/system
// package, so the api layer never has to know about concrete types. The
// adapters are unexported (lowercase struct names) since they're wiring
// glue — only `internal/app/registry.go` should construct them.
//
// NIL-DEPENDENCY CONTRACT (preserves handler 503 semantics):
//   - `newDriveAdminAdapter(upload nil, log)` returns nil so the handler-side
//     `h.driveOps == nil` check fires "drive uploader not configured" 503.
//     This was a silent-degradation regression caught during PR4 review:
//     the previous version returned a non-nil adapter with nil uploader
//     inside, and the inner nil-guards silently returned zero values
//     (handler returned 200 + empty `created` map instead of 500).
//   - `newReconcilerAdapter` follows the same shape for symmetry but is
//     less critical: the reconcile endpoint's nil check is at the service
//     level, and the nil adapter yields nil result + nil error (the
//     handler's `h.reconciler == nil` check fires first).
package app

import (
	"context"

	systemapi "github.com/Marcuss-ops/PipelineGen/internal/api/system"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/drivecleanup"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"

	"go.uber.org/zap"
)

// ── DriveAdminOps adapter ────────────────────────────────────────────────────

// driveAdminAdapter wraps *drive.Uploader and satisfies systemapi.DriveAdminOps.
// The previous handler_drive.go inlined the Google Files.Get round-trip; that
// logic now lives here so the api package never reaches into the Google Drive
// SDK directly.
type driveAdminAdapter struct {
	uploader *drive.Uploader
	log      *zap.Logger
}

// newDriveAdminAdapter constructs the DriveAdminOps port adapter. If the
// underlying drive.Uploader is nil (e.g. Drive OAuth not configured), this
// returns nil so the handler-side `h.driveOps == nil` check fires the
// documented 503 "drive uploader not configured" — preserving the original
// handler semantics that the previous all-nil-guards version broke.
func newDriveAdminAdapter(u *drive.Uploader, log *zap.Logger) systemapi.DriveAdminOps {
	if u == nil {
		return nil
	}
	return &driveAdminAdapter{uploader: u, log: log}
}

// GetOrCreateFolder delegates to *drive.Uploader.GetOrCreateFolder.
func (a *driveAdminAdapter) GetOrCreateFolder(ctx context.Context, folderName, parentID string) (string, error) {
	return a.uploader.GetOrCreateFolder(ctx, folderName, parentID)
}

// MoveFile delegates to *drive.Uploader.MoveFile.
func (a *driveAdminAdapter) MoveFile(ctx context.Context, fileID, fromFolderID, toFolderID string) error {
	return a.uploader.MoveFile(ctx, fileID, fromFolderID, toFolderID)
}

// ResolveFileInfo performs the per-ID Files.Get round-trip that DriveHandler.
// ResolveByIDs fans out in parallel.
func (a *driveAdminAdapter) ResolveFileInfo(ctx context.Context, fileID string) (systemapi.ResolveByIDsItem, error) {
	file, err := a.uploader.Service.Files.Get(fileID).
		Fields("id, name, mimeType, parents, trashed, webViewLink, size").
		Context(ctx).
		Do()
	if err != nil {
		if a.log != nil {
			a.log.Warn("system_adapters driveAdminAdapter.ResolveFileInfo: Files.Get failed",
				zap.String("file_id", fileID),
				zap.Error(err))
		}
		return systemapi.ResolveByIDsItem{}, err
	}
	if file == nil {
		return systemapi.ResolveByIDsItem{}, nil
	}
	return systemapi.ResolveByIDsItem{
		ID:          file.Id,
		Name:        file.Name,
		MimeType:    file.MimeType,
		Parents:     file.Parents,
		WebViewLink: file.WebViewLink,
		Size:        file.Size,
		Trashed:     file.Trashed,
	}, nil
}

// ── Reconciler adapter ───────────────────────────────────────────────────────

// reconcilerAdapter wraps *drivecleanup.Service and satisfies
// systemapi.Reconciler.
//
// DRIFT NOTE for operators/maintainers: the Reconcile result type on the
// api side is a 2-field `ReconcileResult{Deleted,Kept}` mirror of the
// canonical `drivecleanup.Result`. If the canonical Result struct grows
// new fields (e.g. dry-run listing, conflict counts), this adapter will
// silently drop them. When you add a field to drivecleanup.Result,
// also update `systemapi.ReconcileResult` and this adapter's translation.
type reconcilerAdapter struct {
	svc *drivecleanup.Service
	log *zap.Logger
}

// newReconcilerAdapter constructs the Reconciler port adapter. A nil svc
// returns a non-nil adapter — the handler's `h.reconciler == nil` check is
// bypassed in that case but the resulting port still fires through the
// canonical handler-level check (the wrapper compared equality to
// interface types, not the wrapped value).
//
// Wait — actually for symmetry with newDriveAdminAdapter, returning nil
// for nil svc is more honest. Update: this returns nil if svc is nil so
// the handler-side nil check fires the 503. (June 24, 2026 — same fix as
// driveAdminAdapter, applied symmetrically.)
func newReconcilerAdapter(svc *drivecleanup.Service, log *zap.Logger) systemapi.Reconciler {
	if svc == nil {
		return nil
	}
	return &reconcilerAdapter{svc: svc, log: log}
}

// Reconcile delegates to *drivecleanup.Service.Reconcile and translates the
// result struct to the api-side JSON-shaped mirror.
func (a *reconcilerAdapter) Reconcile(ctx context.Context, source, rootFolderID string, dryRun bool) (*systemapi.ReconcileResult, error) {
	res, err := a.svc.Reconcile(ctx, source, rootFolderID, dryRun)
	if err != nil {
		if a.log != nil {
			a.log.Warn("system_adapters reconcilerAdapter.Reconcile failed",
				zap.String("source", source),
				zap.Error(err))
		}
		return nil, err
	}
	if res == nil {
		return &systemapi.ReconcileResult{}, nil
	}
	return &systemapi.ReconcileResult{
		Deleted: res.Deleted,
		Kept:    res.Kept,
	}, nil
}

// ── DoctorConfig snapshot factory ────────────────────────────────────────────

// doctorConfigFrom reads the diagnostic-relevant fields off *config.Config
// and packs them into a value-typed snapshot. Eager path resolution
// (AssetsPath(), ImagesPath(), TempDir() etc.) means the handler holds
// plain strings, not method receivers — easier to test, easier to fake.
// Returns the zero-value DoctorConfig if cfg is nil so callers don't need
// to nil-check before passing it into NewModule.
func doctorConfigFrom(cfg *config.Config) systemapi.DoctorConfig {
	if cfg == nil {
		return systemapi.DoctorConfig{}
	}
	return systemapi.DoctorConfig{
		DataDir:                   cfg.Storage.DataDir,
		AssetsPath:                cfg.Storage.AssetsPath(),
		ImagesPath:                cfg.Storage.ImagesPath(),
		TempPath:                  cfg.Storage.TempPath(),
		AnimationsPath:            cfg.Storage.AnimationsPath(),
		YoutubeClipsPath:          cfg.Storage.YoutubeClipsPath(),
		PythonScriptsDir:          cfg.Paths.PythonScriptsDir,
		GoogleAccountingEnabled:   cfg.GoogleAccounting.Enabled,
		GoogleAccountingServerURL: cfg.GoogleAccounting.ServerURL,
	}
}

// ── Compile-time assertions (AGENTS.md Pattern 0) ────────────────────────────

// Compile-time guarantees that each adapter satisfies the port it claims
// to satisfy. Drift in either signature surfaces at build time, not at
// runtime panic. Note the construction functions return interface types
// (not *T) so the assertions live on the concrete types below; if the
// port signature drifts, the assignment `wrap → interface` fails compile.
var (
	_ systemapi.DriveAdminOps = (*driveAdminAdapter)(nil)
	_ systemapi.Reconciler    = (*reconcilerAdapter)(nil)
)
