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
	"path/filepath"

	systemapi "github.com/Marcuss-ops/PipelineGen/internal/api/system"
	sfxports "github.com/Marcuss-ops/PipelineGen/internal/application/assets/soundeffect"
	searchpkg "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	artlistPkg "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
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

// ── Semantic writer adapter ──────────────────────────────────────────

// sfxSemanticWriterAdapter wraps semantic.MetadataWriterPort to satisfy
// sfxports.SemanticMetadataWriterPort. Translates between the narrow
// sfxports.MetadataWriteRequest / MetadataWriteResponse DTOs and the
// concrete semantic.WriteRequest / *semantic.WriteResult (with inner
// *Payload).
type sfxSemanticWriterAdapter struct {
	w semantic.MetadataWriterPort
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

// sfxResolverAdapter computes the on-disk destination path for a
// generated sound effect. It replaces the legacy *drive.Resolver
// reach-through (PR-IMAGES-REMOVE-DRIVE-STORE, July 2026): the path
// layout is computed locally from the request fields without calling
// Drive or importing the legacy drive.Resolver/AssetDestinationRequest
// surface. Only LocalPath is propagated (the handler drops RelativePath).
type sfxResolverAdapter struct {
	mediaRoot string
}

// Compile-time assertion: sfxResolverAdapter satisfies sfxports.DestinationResolverPort.
var _ sfxports.DestinationResolverPort = (*sfxResolverAdapter)(nil)

func (a *sfxResolverAdapter) Resolve(req sfxports.AssetDestinationRequest) (sfxports.ResolvedDest, error) {
	// Preserve the legacy drive.Resolver path layout:
	//   <mediaRoot>/<source>/<subject><ext>
	// The legacy resolver defaulted an empty Subject to "unknown";
	// the sfx handler never set Subject, so every file landed at
	// "unknown.<ext>". We use Group (the sound-effect name) as the
	// filename segment instead, which fixes the overwrite bug while
	// keeping the same directory layout.
	source := req.Source
	if source == "" {
		source = "media"
	}
	subject := req.Group
	if subject == "" {
		subject = "unknown"
	}
	ext := req.Ext
	if ext == "" {
		ext = ".bin"
	}
	rel := filepath.Join(source, subject+ext)
	localPath := ""
	if a.mediaRoot != "" {
		localPath = filepath.Join(a.mediaRoot, rel)
	}
	return sfxports.ResolvedDest{LocalPath: localPath}, nil
}

// ── Sfx dispatcher adapter ──────────────────────────────────────────────────
//
// sfxDispatcherAdapter wraps the canonical *outbox.Dispatcher to satisfy
// sfxports.DispatcherPort. Outbox.Dispatcher.EnqueueAndIndex (the production
// implementation audited per the SSOT assertion at
// internal/platform/sqlite/outbox/repository.go:720) accepts
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
	admin     drive.Admin
	reader    drive.Reader
	lifecycle drive.FileLifecycle
	log       *zap.Logger
}

// newDriveAdminAdapter constructs the DriveAdminOps port adapter. If the
// underlying admin port is nil (e.g. Drive OAuth not configured), this
// returns nil so the handler-side `h.driveOps == nil` check fires the
// documented 503 "drive uploader not configured".
func newDriveAdminAdapter(admin drive.Admin, reader drive.Reader, lifecycle drive.FileLifecycle, log *zap.Logger) systemapi.DriveAdminOps {
	if admin == nil {
		return nil
	}
	return &driveAdminAdapter{admin: admin, reader: reader, lifecycle: lifecycle, log: log}
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
	if a.lifecycle == nil {
		return fmt.Errorf("driveAdminAdapter: lifecycle not wired (P1-5 CUTOVER requires FileLifecycle)")
	}
	return a.lifecycle.Rename(ctx, fileID, newName)
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

// middlewareRateLimitAdapter + middlewareFeatureFlagsAdapter + doctorConfigFrom
// — extracted to adapters_middleware.go (PR-ADAPTERS-SPLIT, July 2026).

// ── Compile-time assertions (AGENTS.md Pattern 0) ────────────────────────────

// Compile-time guarantees that each adapter satisfies the port it claims
// to satisfy. Drift in either signature surfaces at build time, not at
// runtime panic.
var (
	_ systemapi.DriveAdminOps = (*driveAdminAdapter)(nil)
	_ searchpkg.QueryEmbedder = (*searchEmbedAdapter)(nil)
)

// embeddingRegistryAdapter compile-time assertion (PR-EMBEDDING-CHANNEL-
// REGISTRY) — see adapters_infra_embedding.go for the concrete type.
var _ searchpkg.EmbeddingChannelRegistry = (*embeddingRegistryAdapter)(nil)

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

// middlewareRateLimitAdapter + middlewareFeatureFlagsAdapter
// — extracted to adapters_middleware.go (PR-ADAPTERS-SPLIT, July 2026).
