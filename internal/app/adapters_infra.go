// Package app — adapters satisfying the typed ports declared in
// internal/application/<feature>/ports.go.
//
// PG-002 (June 2026): channelRepositoryAdapter wraps the SQLite-backed
// *assets.ChannelsRepository so the API layer can reach it through the
// channels.Repository interface (declared in
// internal/application/channels/ports.go) without importing
// internal/infrastructure/* directly. The adapter is intentionally
// thin — every method is a one-line delegate — because the channel
// surface today is pure CRUD; if/when application logic lands in this
// package, the adapter stays the boundary and orchestration lives in a
// ChannelService above it.
package app

import (
	"context"

	systemapi "github.com/Marcuss-ops/PipelineGen/internal/api/system"
	artlistPkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	sfxports "github.com/Marcuss-ops/PipelineGen/internal/application/assets/soundeffect"
	"github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/drivecleanup"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"go.uber.org/zap"
)

// Compile-time assertion: *channelRepositoryAdapter satisfies
// channels.Repository. Drift in either side fails the build.
var _ channels.Repository = (*channelRepositoryAdapter)(nil)

// channelRepositoryAdapter wraps the SQLite repository as the canonical
// port. The infrastructure type is unexported on the consumer side —
// only the Adapter is reachable from the composition root.
type channelRepositoryAdapter struct {
	repo *assets.ChannelsRepository
}

// newChannelRepositoryAdapter is a tiny factory for the composition
// root. The concrete ChannelsRepository comes from the assets package
// which is the single owner of the SQLite schema; this package does
// not re-export it.
func newChannelRepositoryAdapter(repo *assets.ChannelsRepository) *channelRepositoryAdapter {
	return &channelRepositoryAdapter{repo: repo}
}

func (a *channelRepositoryAdapter) ListAll(ctx context.Context) ([]*asset.CategoryChannel, error) {
	return a.repo.ListAll(ctx)
}

func (a *channelRepositoryAdapter) ListCategories(ctx context.Context) ([]string, error) {
	return a.repo.ListCategories(ctx)
}

func (a *channelRepositoryAdapter) GetByID(ctx context.Context, id string) (*asset.CategoryChannel, error) {
	return a.repo.GetByID(ctx, id)
}

func (a *channelRepositoryAdapter) Upsert(ctx context.Context, ch *asset.CategoryChannel) error {
	return a.repo.Upsert(ctx, ch)
}

func (a *channelRepositoryAdapter) Delete(ctx context.Context, id string) error {
	return a.repo.Delete(ctx, id)
}

// ── ClipsRepo adapter ────────────────────────────────────────────────

// sfxClipsRepoAdapter wraps *assets.ClipsRepository to satisfy
// sfxports.ClipRepositoryPort.
type sfxClipsRepoAdapter struct {
	repo *assets.ClipsRepository
}

// Compile-time assertion: sfxClipsRepoAdapter satisfies sfxports.ClipRepositoryPort.
var _ sfxports.ClipRepositoryPort = (*sfxClipsRepoAdapter)(nil)

func (a *sfxClipsRepoAdapter) Upsert(ctx context.Context, clip *asset.Asset) error {
	return a.repo.Upsert(ctx, clip)
}

// ── Drive uploader adapter ───────────────────────────────────────────

// sfxDriveUploaderAdapter wraps *drive.Uploader to satisfy
// sfxports.DriveUploaderPort. Only the fields used by sfx are mapped;
// MD5Checksum + DownloadLink from concrete drive.UploadResult are
// deliberately dropped (NOT consumed by the HTTP transport).
type sfxDriveUploaderAdapter struct {
	up *drive.Uploader
}

// Compile-time assertion: sfxDriveUploaderAdapter satisfies sfxports.DriveUploaderPort.
var _ sfxports.DriveUploaderPort = (*sfxDriveUploaderAdapter)(nil)

func (a *sfxDriveUploaderAdapter) GetOrCreateFolder(ctx context.Context, name, parentFolderID string) (string, error) {
	return a.up.GetOrCreateFolder(ctx, name, parentFolderID)
}

func (a *sfxDriveUploaderAdapter) UploadFile(ctx context.Context, localPath, folderID, filename string) (*sfxports.UploadResultDTO, error) {
	res, err := a.up.UploadFile(ctx, localPath, folderID, filename)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return &sfxports.UploadResultDTO{}, nil
	}
	return &sfxports.UploadResultDTO{FileID: res.FileID, WebViewLink: res.WebViewLink}, nil
}

// ── Semantic writer adapter ──────────────────────────────────────────

// sfxSemanticWriterAdapter wraps *semantic.MetadataWriter to satisfy
// sfxports.SemanticMetadataWriterPort. Translates between the narrow
// sfxports.MetadataWriteRequest / MetadataWriteResponse DTOs and the
// concrete semantic.WriteRequest / *semantic.WriteResult (with inner
// *Payload).
type sfxSemanticWriterAdapter struct {
	w *semantic.MetadataWriter
}

// Compile-time assertion: sfxSemanticWriterAdapter satisfies sfxports.SemanticMetadataWriterPort.
var _ sfxports.SemanticMetadataWriterPort = (*sfxSemanticWriterAdapter)(nil)

func (a *sfxSemanticWriterAdapter) Write(ctx context.Context, req sfxports.MetadataWriteRequest) (*sfxports.MetadataWriteResponse, error) {
	concreteReq := semantic.WriteRequest{
		AssetID:   req.AssetID,
		AssetType: req.AssetType,
		MediaType: req.MediaType,
		Source:    req.Source,
		Generator: req.Generator,
		Style:     req.Style,
		Prompt:    req.Prompt,
		LocalPath: req.LocalPath,
	}
	res, err := a.w.Write(ctx, concreteReq)
	if err != nil {
		return nil, err
	}
	// Preserve the pre-refactor contract: a nil inner Payload must
	// short-circuit the handler's `else if writeRes != nil` branch
	// (which gates the Drive upload). Returning an empty non-nil DTO
	// here would let the upload proceed with default searchText/tags.
	if res == nil || res.Payload == nil {
		return nil, nil
	}
	return &sfxports.MetadataWriteResponse{
		SearchText: res.Payload.SearchText,
		Tags:       res.Payload.Tags,
	}, nil
}

// ── Resolver adapter ─────────────────────────────────────────────────

// sfxResolverAdapter wraps *drive.Resolver to satisfy
// sfxports.DestinationResolverPort. Only LocalPath is propagated (the
// handler drops RelativePath).
type sfxResolverAdapter struct {
	r *drive.Resolver
}

// Compile-time assertion: sfxResolverAdapter satisfies sfxports.DestinationResolverPort.
var _ sfxports.DestinationResolverPort = (*sfxResolverAdapter)(nil)

func (a *sfxResolverAdapter) Resolve(req sfxports.AssetDestinationRequest) (sfxports.ResolvedDest, error) {
	concreteReq := drive.AssetDestinationRequest{
		Source:    drive.SourceType(req.Source),
		MediaType: drive.MediaType(req.MediaType),
		Group:     req.Group,
		Hash:      req.Hash,
		Ext:       req.Ext,
	}
	res, err := a.r.Resolve(concreteReq)
	if err != nil {
		return sfxports.ResolvedDest{}, err
	}
	if res == nil {
		return sfxports.ResolvedDest{}, nil
	}
	return sfxports.ResolvedDest{LocalPath: res.LocalPath}, nil
}

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

// artlistConfigAdapter wraps *config.Config to satisfy
// artlistPkg.ArtlistConfigPort. The composition-root keeps the
// config concrete; this adapter exposes only the narrow port
// surface to the api/ layer.
type artlistConfigAdapter struct {
	cfg *config.Config
}

// Compile-time assertion: artlistConfigAdapter satisfies artlistPkg.ArtlistConfigPort.
var _ artlistPkg.ArtlistConfigPort = (*artlistConfigAdapter)(nil)

// ArtlistRootFolderID resolves the canonical artlist root folder.
// Nil-tolerant: a nil underlying cfg returns "" matching the
// pre-refactor behaviour of artlist.ResolveRootFolderID(nil).
func (a *artlistConfigAdapter) ArtlistRootFolderID() string {
	return artlistPkg.ResolveRootFolderID(a.cfg)
}

// newArtlistConfigAdapter is the canonical composition-root constructor.
// Returns a nil interface when cfg is nil so the wiring site preserves
// the `cfgPort != nil` discipline callers can rely on (production
// wiring always passes a non-nil cfg).
func newArtlistConfigAdapter(cfg *config.Config) artlistPkg.ArtlistConfigPort {
	if cfg == nil {
		return nil
	}
	return &artlistConfigAdapter{cfg: cfg}
}

// middlewareRateLimitAdapter wraps *config.Config to satisfy
// middleware.RateLimitPort. Same one-method-per-call-site discipline.
type middlewareRateLimitAdapter struct {
	cfg *config.Config
}

// Compile-time assertion.
var _ middleware.RateLimitPort = (*middlewareRateLimitAdapter)(nil)

func newMiddlewareRateLimitAdapter(cfg *config.Config) middleware.RateLimitPort {
	if cfg == nil {
		return nil
	}
	return &middlewareRateLimitAdapter{cfg: cfg}
}

func (a *middlewareRateLimitAdapter) RateLimitEnabled() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Security.RateLimitEnabled
}

func (a *middlewareRateLimitAdapter) RateLimitRequests() int {
	if a.cfg == nil {
		return 0
	}
	return a.cfg.Security.RateLimitRequests
}

// middlewareFeatureFlagsAdapter wraps *config.Config to satisfy
// middleware.FeatureFlagsPort. The per-feature bools read on
// `cfg.Features.<X>Enabled` get one delegation method each so the
// future ScriptImagesEnabled flag lands cleanly without changing
// the port surface.
type middlewareFeatureFlagsAdapter struct {
	cfg *config.Config
}

// Compile-time assertion.
var _ middleware.FeatureFlagsPort = (*middlewareFeatureFlagsAdapter)(nil)

func newMiddlewareFeatureFlagsAdapter(cfg *config.Config) middleware.FeatureFlagsPort {
	if cfg == nil {
		return nil
	}
	return &middlewareFeatureFlagsAdapter{cfg: cfg}
}

func (a *middlewareFeatureFlagsAdapter) ArtlistEnabled() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Features.ArtlistEnabled
}

func (a *middlewareFeatureFlagsAdapter) ScriptDocsEnabled() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Features.ScriptDocsEnabled
}

func (a *middlewareFeatureFlagsAdapter) ScriptClipsEnabled() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Features.ScriptClipsEnabled
}
