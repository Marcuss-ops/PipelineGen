// Package clips (ports) — typed application-layer ports that the clips
// API handler depends on.
//
// PG-005 (June 2026): the previous 7 handler files under
// internal/api/assets/clips/** reached through 6 concrete
// internal/infrastructure/* types (*config.Config,
// *assets.ClipsRepository, *assets.VoiceoversRepository,
// *assets.ImagesRepository, *drive.Uploader, *semantic.MetadataWriter,
// *clipindexer.Service, *foldermemory.Service) plus a raw hashutil
// helper. Per AGENTS.md Pattern 0 + PG-005 ticket scope, every
// infrastructure-shaped dependency now flows through a typed port
// declared here. Concrete adapters live in
// internal/app/clips_adapters.go with explicit compile-time
// `var _ <Port> = (*<Adapter>)(nil)` assertions so future port drift
// surfaces at compile time, not first runtime call.
//
// Rule: define only methods the handler actually calls — do NOT widen
// any port to expose the whole underlying concrete. New consumer sites
// land as additional methods, one PR at a time.
package clips

import (
	"context"
	"io"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
)

// ── Domain DTOs (canonical shape at the application–infra seam) ────────

// ClipUploadResultDTO mirrors the clips-relevant subset of
// *drive.UploadResult. The handler reads FileID, WebViewLink,
// DownloadLink, and MD5Checksum. The concrete drive.UploadResult
// carries more fields that are dropped at the adapter boundary.
type ClipUploadResultDTO struct {
	FileID       string
	WebViewLink  string
	DownloadLink string
	MD5Checksum  string
}

// ClipDriveFileDTO mirrors the per-row shape returned by
// drive.Service.Files.List(). Only ID + Name are read by the clips
// handler (for metadata.json reconciliation); other columns are
// dropped at the adapter boundary.
type ClipDriveFileDTO struct {
	ID   string
	Name string
}

// ClipDriveFileMetaDTO mirrors drive.File.Meta. Only MimeType is
// consumed by the clips handler (DownloadClip MIME guard). The rest
// of the SDK File struct is dropped at the adapter boundary.
type ClipDriveFileMetaDTO struct {
	MimeType string
}

// ClipMetaWriteRequest is the narrowed semantic-write request shape.
// Mirrors semantic.WriteRequest without exposing the SDK type.
type ClipMetaWriteRequest struct {
	AssetID   string
	AssetType string
	MediaType string
	Source    string
	Generator string
	Style     string
	Prompt    string
	LocalPath string
}

// ClipMetaPayload is the narrowed semantic-payload response shape.
// Mirrors semantic.Payload without exposing the SDK type.
type ClipMetaPayload struct {
	SearchText          string
	Tags                []string
	SemanticDescription string
	RetrievalScore      *float64
}

// ClipVectorAssetDTO mirrors qdrant.VectorAsset. PG-005 (June 2026):
// lives at the application layer so use cases can construct it without
// importing internal/infrastructure/qdrant. The adapter at
// internal/app/clips_adapters.go converts to qdrant.VectorAsset at
// the infra seam.
type ClipVectorAssetDTO struct {
	AssetID    string
	Source     string
	Name       string
	LocalPath  string
	DriveLink  string
	Category   string
	MediaType  string
	SearchText string
	Tags       []string
}

// ── Structural ports (signature-bearing, minimal per Pattern 0) ────────

// ClipRepositoryPort is the canonical clips-side narrowed surface of
// *assets.ClipsRepository. The 8 methods listed below are exactly
// the ones handlers + helpers + worker + clip_ops + clip_action call
// on the concrete repo. Adapter struct machinery lives in
// internal/app/clips_adapters.go.
type ClipRepositoryPort interface {
	Upsert(ctx context.Context, clip *asset.Asset) error
	UpsertClip(ctx context.Context, clip *asset.Asset) error
	Get(ctx context.Context, id string) (*asset.Asset, error)
	GetClip(ctx context.Context, id string) (*asset.Asset, error)
	ListFolders(ctx context.Context, source string) ([]*asset.ClipFolder, error)
	GetFolder(ctx context.Context, folderID string) (*asset.ClipFolder, error)
	GetFolderChildren(ctx context.Context, parentID string) ([]*asset.Asset, error)
	ListByFolderID(ctx context.Context, folderID string) ([]*asset.Asset, error)
	ListByFolderPath(ctx context.Context, folderPath string) ([]*asset.Asset, error)
	DeleteFolder(ctx context.Context, id string) error
	BulkAddTags(ctx context.Context, ids, tags []string) error
	BulkRemoveTags(ctx context.Context, ids, tags []string) error
	ListClipsPaged(ctx context.Context, source string, limit, offset int, query string) ([]*asset.Asset, error)
	FindClipsByHash(ctx context.Context, hash string) ([]*asset.Asset, error)
}

// VoiceoverRepositoryPort is the canonical narrowed surface of
// *assets.VoiceoversRepository. Only GetByID + ListAll + Upsert are
// exposed because those are the only methods the clips handler calls.
// PG-005 (June 2026): return type is *asset.VoiceoverRecord (the
// domain type), NOT *assets.VoiceoversRecord's *assets.Record (the
// infra alias), so this ports file has zero infra imports.
type VoiceoverRepositoryPort interface {
	GetByID(ctx context.Context, id string) (*assets.Record, error)
	ListAll(ctx context.Context) ([]*assets.Record, error)
	Upsert(ctx context.Context, rec *assets.Record) error
}

// ImageRepositoryPort is the canonical narrowed surface of
// *assets.ImagesRepository. Only ListAll is exposed because
// Cleanup() is the only callsite.
type ImageRepositoryPort interface {
	ListAll(ctx context.Context) ([]*asset.ImageAsset, error)
}

// ClipDriveUploaderPort is the canonical narrowed surface of
// *drive.Uploader used by the clips handler. ListFiles propagates the
// raw Drive query string (caller-side filtering keeps trashed entries
// out, same pattern as artlist.DriveFolderManager.ListByQuery).
type ClipDriveUploaderPort interface {
	GetOrCreateFolder(ctx context.Context, name, parentFolderID string) (string, error)
	GetFolderName(ctx context.Context, folderID string) (string, error)
	TrashFolder(ctx context.Context, folderID string) error
	DeleteFolder(ctx context.Context, folderID string) error
	UploadFile(ctx context.Context, localPath, folderID, filename string) (*ClipUploadResultDTO, error)
	UploadFileWithDescription(ctx context.Context, localPath, folderID, filename, description string) (*ClipUploadResultDTO, error)
	DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error)
	GetFileMD5(ctx context.Context, fileID string) (string, error)
	GetFileMeta(ctx context.Context, fileID string) (*ClipDriveFileMetaDTO, error)
	TrashFile(ctx context.Context, fileID string) error
	ListFiles(ctx context.Context, query string) ([]ClipDriveFileDTO, error)
}

// ClipMetaWriterPort is the canonical narrowed surface of
// *semantic.MetadataWriter. GeneratePayload returns the parsed
// payload + status string; matches the existing EnrichUseCase
// signature so the use case can swap to the port without semantic
// drift.
type ClipMetaWriterPort interface {
	GeneratePayload(ctx context.Context, req ClipMetaWriteRequest) (*ClipMetaPayload, string, error)
}

// ClipIndexerPort is the canonical narrowed surface of
// *clipindexer.Service. Both methods are exercised by the clips
// bulk-upload worker; IsEnabled() gates IndexClip().
type ClipIndexerPort interface {
	IsEnabled() bool
	IndexClip(ctx context.Context, id string) error
	BatchReindex(ctx context.Context, source, mediaType string, limit int) (*clipindexer.BatchReindexResult, error)
}

// ClipFolderMemoryPort is the canonical narrowed surface of
// *foldermemory.Service. Empty-marker for now because the handler
// stores the dependency but does not call any method on it.
// Once a future consumer appears, add LoadManifest/SaveManifest/
// UpdateManifestTXT/ComputeManifestStats one PR at a time.
type ClipFolderMemoryPort interface{}

// ClipConfigPort is the canonical clips-side narrowed surface of
// *config.Config. Each method exposes exactly the field the handler
// reads (Pattern 0: never return the whole *Config). The adapter
// delegates to cfg.Drive.X(), cfg.Storage.X(), cfg.Paths.X() in
// order.
type ClipConfigPort interface {
	ClipsDriveFolder() string
	RootFolder() string
	ArtlistDriveFolder() string
	StockDriveFolder() string
	MediaPath() string
	TempPath() string
	DataDir() string
	YoutubeClipsPath() string
	AssetsPath() string
	AssetsStoragePath() string
}

// ClipHashPort is the canonical narrowed surface of hashutil.MD5File
// (the bulk_upload_worker only computes MD5; an entire hashutil
// surface import is unnecessary).
type ClipHashPort interface {
	MD5File(path string) (string, error)
}

// SourceResolverPort is the canonical application-side narrowed
// surface of *artifacts.SourceResolver. Returns ClipRepositoryPort
// (NEVER the concrete type), so callers can stay port-pure.
type SourceResolverPort interface {
	ResolveRepo(source string) ClipRepositoryPort
}

// VectorStorePort is the canonical narrowed surface of
// *qdrant.Service for UpsertAsset calls. PG-005 (June 2026): the
// method signature takes ClipVectorAssetDTO (domain shape), not
// qdrant.VectorAsset (infra type), so the use case has zero
// infrastructure-layer reach-through. The adapter in
// internal/app/clips_adapters.go converts DTO → qdrant.VectorAsset
// at the infra seam.
type VectorStorePort interface {
	UpsertAsset(ctx context.Context, asset ClipVectorAssetDTO) error
}

// ClipTreeBuilderPort is the canonical narrowed surface of
// assettree.Service.UpsertNode for the bulk-tags handler. PG-005
// (June 2026): takes a *asset.Asset (domain shape) — the infra
// *assets.AssetNode conversion is encapsulated in the adapter (see
// internal/app/clips_adapters.go::clipsAssetTreeAdapter), so this
// use case has zero infra imports.
type ClipTreeBuilderPort interface {
	UpsertFromAsset(ctx context.Context, clip *asset.Asset) error
}
