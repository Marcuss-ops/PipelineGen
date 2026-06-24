// Package soundeffect ports — application-layer structural ports for the
// sound effect HTTP transport.
//
// PG-003 (June 2026): the sfx handler previously imported four concrete
// infrastructure types (*assets.ClipsRepository, *drive.Uploader,
// *semantic.MetadataWriter, *drive.Resolver) which violated AGENTS.md
// Pattern 0 (typed ports). These ports replace those reach-throughs.
// Concrete adapters live in internal/app/adapters_soundeffect.go and
// implement compile-time `var _ <Port> = (*<Adapter>)(nil)` assertions.
package soundeffect

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

// ── DTOs (minimal surface — only fields the handler reads) ───────────

// UploadResultDTO mirrors the sfx-relevant subset of
// drive.UploadResult. The concrete adapter drops MD5Checksum +
// DownloadLink (NOT consumed by the HTTP transport).
type UploadResultDTO struct {
	FileID      string `json:"file_id"`
	WebViewLink string `json:"web_view_link,omitempty"`
}

// MetadataWriteRequest mirrors the sfx-relevant subset of
// semantic.WriteRequest. The concrete adapter drops unused fields
// (SourceType, Retriever, PageURL, ImageURL, License, Author,
// Extensions, GroupID, Assets).
type MetadataWriteRequest struct {
	AssetID   string
	AssetType string
	MediaType string
	Source    string
	Generator string
	Style     string
	Prompt    string
	LocalPath string
}

// MetadataWriteResponse mirrors sfx-relevant fields of semantic.Payload:
// only SearchText + Tags are read by the handler.
type MetadataWriteResponse struct {
	SearchText string
	Tags       []string
}

// AssetDestinationRequest mirrors the sfx-relevant subset of
// drive.AssetDestinationRequest: only Source, MediaType, Group, Hash,
// Ext are set in the handler body. The remaining fields (Style,
// Subject, DriveRootOverride, GenerationID) are translated to empty
// strings at the adapter boundary.
type AssetDestinationRequest struct {
	Source    string
	MediaType string
	Group     string
	Hash      string
	Ext       string
}

// ResolvedDest mirrors the sfx-relevant subset of drive.ResolvedDest.
// Only LocalPath is consumed; RelativePath is dropped.
type ResolvedDest struct {
	LocalPath string
}

// ── Structural ports ─────────────────────────────────────────────────

// ClipRepositoryPort persists the generated sound effect asset to the
// canonical media_assets store. The sfx path generates a single clip per
// request and writes it once via Upsert; no other operations are
// required at the handler boundary.
type ClipRepositoryPort interface {
	Upsert(ctx context.Context, clip *asset.Asset) error
}

// DriveUploaderPort is the sfx-side narrowed surface of drive.Uploader.
// GetOrCreateFolder + UploadFile cover the sfx upload flow (mp3 +
// generated metadata.json sidecar).
type DriveUploaderPort interface {
	GetOrCreateFolder(ctx context.Context, name, parentFolderID string) (string, error)
	UploadFile(ctx context.Context, localPath, folderID, filename string) (*UploadResultDTO, error)
}

// SemanticMetadataWriterPort produces the semantic metadata.json for
// the generated sfx asset. The handler reads only SearchText + Tags;
// adapter discards the rest.
type SemanticMetadataWriterPort interface {
	Write(ctx context.Context, req MetadataWriteRequest) (*MetadataWriteResponse, error)
}

// DestinationResolverPort resolves the on-disk path the generated sfx
// should be saved under. Pure-path (no Drive I/O); implemented in
// internal/app/adapters_soundeffect.go around drive.Resolver.
type DestinationResolverPort interface {
	Resolve(req AssetDestinationRequest) (ResolvedDest, error)
}
