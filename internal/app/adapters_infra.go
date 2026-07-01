// Package app — adapters satisfying the typed ports declared in
// internal/application/<feature>/ports.go (or contract.go, after the
// Capability Standard migration).
//
// Capability Standard migration (June 2026): channelRepositoryAdapter
// has been relocated to internal/application/channels/adapters.go so
// the channels capability is the canonical owner of its own
// infrastructure adapter. This file keeps the cross-capability
// adapters that have no obvious single owner (sfx, drive, reconciler,
// doctor, artlist config, middleware flags).
package app

import (
	"context"
	"fmt"

	systemapi "github.com/Marcuss-ops/PipelineGen/internal/api/system"
	artlistPkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	sfxports "github.com/Marcuss-ops/PipelineGen/internal/application/assets/soundeffect"
	"github.com/Marcuss-ops/PipelineGen/internal/application/middleware"
	searchpkg "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

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

// sfxDriveUploaderAdapter wraps drive.Admin to satisfy
// sfxports.DriveUploaderPort. FASE 9 Step 4 (June 2026): migrated
// from *drive.Uploader to drive.Admin (Pattern 0 port). Only the
// fields used by sfx are mapped; MD5Checksum + DownloadLink from
// concrete drive.UploadResult are deliberately dropped.

// F2.10: sfxDriveUploaderAdapter + its assertion + methods were
// retired (override brutal). The sfx capability no longer reaches
// into the legacy drive.Admin.UploadFile + GetOrCreateFolder surface;
// every Drive write from sfx routes through delivery.Publisher.Publish
// (DestinationSoundEffect). The matching sfxports.DriveUploaderPort
// interface was removed from
// internal/application/assets/soundeffect/ports.go in the same
// commit — the structural compatibility check
// `var _ sfxports.DriveUploaderPort = (*sfxDriveUploaderAdapter)(nil)`
// would now fail to compile because the interface is gone.
//
// (per `97e6b71a`-era deletion): see git log for the exact diff.

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

// ── Sfx dispatcher adapter ──────────────────────────────────────────────────
//
// sfxDispatcherAdapter wraps the canonical *outbox.Dispatcher to satisfy
// sfxports.DispatcherPort. Outbox.Dispatcher.EnqueueAndIndex (the production
// implementation audited per the SSOT assertion at
// internal/infrastructure/database/sqlite/outbox/repository.go:720) accepts
// *asset.Asset + contentHash and atomically UPSERTs media_assets + emits the
// matching asset.index.requested outbox event in a single transaction.
//
// PR 6 (June 2026, codex/qdrant-api-writers-fail-closed): the sfx handler
// is migrated off direct h.clipsRepo.Upsert to dispatcher routing so the
// QDRANT-002 atomicity invariant (media_assets write and outbox event
// emit committed in one tx) applies uniformly to sfx asset writes.
//
// Signature-drift detection: any change to (a) sfxports.DispatcherPort's
// EnqueueAndIndex signature, OR (b) the outbox.Dispatcher's same method
// signature, surfaces as a build failure at the compile-time `var _` line
// below. AGENTS.md Pattern 0 convention — compile-time over runtime checks.
type sfxDispatcherAdapter struct {
	disp *outbox.Dispatcher
}

// Compile-time assertion: sfxDispatcherAdapter satisfies sfxports.DispatcherPort.
var _ sfxports.DispatcherPort = (*sfxDispatcherAdapter)(nil)

func newSfxDispatcherAdapter(disp *outbox.Dispatcher) sfxports.DispatcherPort {
	if disp == nil {
		return nil
	}
	return &sfxDispatcherAdapter{disp: disp}
}

func (a *sfxDispatcherAdapter) EnqueueAndIndex(ctx context.Context, clip *asset.Asset, contentHash string) error {
	if a == nil || a.disp == nil {
		return nil
	}
	return a.disp.EnqueueAndIndex(ctx, clip, contentHash)
}

// ── DriveAdminOps adapter ────────────────────────────────────────────────────// driveAdminAdapter wraps drive.Admin + drive.Reader and satisfies
// systemapi.DriveAdminOps. FASE 9 Step 4 (June 2026): migrated from
// *drive.Uploader to Pattern 0 ports. ResolveFileInfo now uses
// Reader.GetFileMeta (eliminates raw SDK access).
type driveAdminAdapter struct {
	admin  drive.Admin
	reader drive.Reader
	log    *zap.Logger
}

// newDriveAdminAdapter constructs the DriveAdminOps port adapter. If the
// underlying admin port is nil (e.g. Drive OAuth not configured), this
// returns nil so the handler-side `h.driveOps == nil` check fires the
// documented 503 "drive uploader not configured".
func newDriveAdminAdapter(admin drive.Admin, reader drive.Reader, log *zap.Logger) systemapi.DriveAdminOps {
	if admin == nil {
		return nil
	}
	return &driveAdminAdapter{admin: admin, reader: reader, log: log}
}

func (a *driveAdminAdapter) GetOrCreateFolder(ctx context.Context, folderName, parentID string) (string, error) {
	return a.admin.GetOrCreateFolder(ctx, folderName, parentID)
}

func (a *driveAdminAdapter) MoveFile(ctx context.Context, fileID, fromFolderID, toFolderID string) error {
	return a.admin.MoveFile(ctx, fileID, fromFolderID, toFolderID)
}

func (a *driveAdminAdapter) ListFiles(ctx context.Context, folderID string) ([]systemapi.DriveFileInfoDTO, error) {
	files, err := a.reader.ListFiles(ctx, folderID)
	if err != nil {
		return nil, err
	}
	out := make([]systemapi.DriveFileInfoDTO, len(files))
	for i, f := range files {
		out[i] = systemapi.DriveFileInfoDTO{
			ID:             f.ID,
			Name:           f.Name,
			MimeType:       f.MimeType,
			WebViewLink:    f.WebViewLink,
			WebContentLink: f.WebContentLink,
			Parents:        f.Parents,
		}
	}
	return out, nil
}

func (a *driveAdminAdapter) RenameFile(ctx context.Context, fileID, newName string) error {
	return a.admin.RenameFile(ctx, fileID, newName)
}

// ResolveFileInfo performs a metadata lookup via Reader.GetFileMeta
// (FASE 9: eliminates raw SDK *gdrive.Service access). The Reader
// returns *drive.FileMeta which carries all needed fields.
func (a *driveAdminAdapter) ResolveFileInfo(ctx context.Context, fileID string) (systemapi.ResolveByIDsItem, error) {
	if a.reader == nil {
		return systemapi.ResolveByIDsItem{}, fmt.Errorf("driveAdminAdapter: reader not wired")
	}
	meta, err := a.reader.GetFileMeta(ctx, fileID)
	if err != nil {
		if a.log != nil {
			a.log.Warn("system_adapters driveAdminAdapter.ResolveFileInfo: GetFileMeta failed",
				zap.String("file_id", fileID), zap.Error(err))
		}
		return systemapi.ResolveByIDsItem{}, err
	}
	if meta == nil {
		return systemapi.ResolveByIDsItem{}, nil
	}
	return systemapi.ResolveByIDsItem{
		ID:          meta.ID,
		Name:        meta.Name,
		MimeType:    meta.MimeType,
		Parents:     meta.Parents,
		WebViewLink: meta.WebViewLink,
		Size:        meta.Size,
		Trashed:     meta.Trashed,
	}, nil
}

// ── Reconciler (no-op placeholder) ────────────────────────────────────────────

// noopReconciler satisfies systemapi.Reconciler with a zero-result response.
// Previously this was backed by the now-removed drivecleanup.Service compatibility
// shim; the real Drive→SQLite reconciliation logic has not been implemented yet.
// This keeps the /drive/reconcile and /drive/cleanup endpoints functional
// (returning {deleted:0,kept:0}) while the feature is built out.
type noopReconciler struct{}

func (noopReconciler) Reconcile(_ context.Context, _, _ string, _ bool) (*systemapi.ReconcileResult, error) {
	return &systemapi.ReconcileResult{}, nil
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
	_ systemapi.Reconciler    = (*noopReconciler)(nil)
	_ searchpkg.QueryEmbedder = (*searchEmbedAdapter)(nil)
)

// ── Search query embedder adapter (Fase 6 Spina Dorsale) ───────────────────
//
// searchEmbedAdapter bridges the infrastructure-layer qdrant.TextEmbedder
// to the application-layer search.QueryEmbedder port. The shape is
// identical (`Embed(ctx, text) -> ([]float32, error)`), so the adapter
// is a one-method delegation.
//
// Until compositions-everywhere migration (BACKFILL phase of
// architecture/deprecations.yaml#SEARCH-VECTORSEARCHPORT-MERGE), the
// legacy mediator `mediasearch.VectorSearchPort` continues to expose
// EmbedTextForVector directly via the qdrant concrete. THIS adapter
// is the Fase 6 path: orchestrator code that asks for
// `search.QueryEmbedder` will get this adapter wrapped around the
// same qdrant.TextEmbedder concrete — zero behavioural drift, plus
// the canonical split.
type searchEmbedAdapter struct {
	embedder qdrant.TextEmbedder
}

// Embed delegates to the underlying qdrant.TextEmbedder. The method
// name matches the application port shape so compile-time assertion
// `var _ searchpkg.QueryEmbedder = (*searchEmbedAdapter)(nil)` is
// non-trivial — drift in either signature is a build failure.
func (a *searchEmbedAdapter) Embed(ctx context.Context, text string) ([]float32, error) {
	if a == nil || a.embedder == nil {
		return nil, fmt.Errorf("searchEmbedAdapter: underlying qdrant embedder not wired")
	}
	return a.embedder.Embed(ctx, text)
}

// newSearchEmbedAdapter is the canonical composition-root constructor.
// Returns a nil interface when embedder is nil so the wiring site
// preserves the `embedPort != nil` discipline callers can rely on.
func newSearchEmbedAdapter(embedder qdrant.TextEmbedder) searchpkg.QueryEmbedder {
	if embedder == nil {
		return nil
	}
	return &searchEmbedAdapter{embedder: embedder}
}

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
