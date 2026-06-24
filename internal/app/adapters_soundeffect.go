// Package app — Sound effect adapters (PR3 Wave-14 PR4 / PG-003, June 2026).
//
// PG-003 (June 2026): the sfx handler previously reached through four
// concrete infrastructure types (assets.ClipsRepository,
// drive.Uploader, semantic.MetadataWriter, drive.Resolver). Each is
// wrapped here behind its structural sfxports.<Port> with a compile-time
// `var _ … = (*…)(nil)` assertion. The composition root injects the
// adapter into soundeffect.NewHandler so the api/ layer depends only
// on the typed port (per AGENTS.md Pattern 0).
package app

import (
	"context"

	sfxports "github.com/Marcuss-ops/PipelineGen/internal/application/assets/soundeffect"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
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
