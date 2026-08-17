// Package clips (ports) — typed application-layer ports that the clips
// API handler depends on.
//
// PG-005 (June 2026): the previous 7 handler files under
// internal/api/assets/clips/** reached through 6 concrete
// internal/infrastructure/* types (*config.Config,
// *assets.ClipsRepository, *assets.VoiceoversRepository,
// *imagesrepo.ImagesRepository, *drive.Uploader, semantic.MetadataWriterPort,
// *clipindexer.Service, *foldermemory.Service) plus a raw hashutil
// helper. Per AGENTS.md Pattern 0 + PG-005 ticket scope, every
// infrastructure-shaped dependency now flows through a typed port
// declared here. Concrete adapters live in
// internal/app/clips_adapters.go with explicit compile-time
// `var _ <Port> = (*<Adapter>)(nil)` assertions so future port drift
// surfaces at compile time, not first runtime call.
package clips

import (
	"context"
	"io"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ErrLegacySurfaceRetired was retired in DRIVE-008 CUTOVER (June 2026,
// commit 0fa8c065) at the application-layer (clips) side. The
// sentinel is preserved as a comment-only historical audit-pin (no
// live var) so future agents can trace the DRIVE-008 fail-closed
// stub lineage. FASE 0.3 (July 2026): the 3 fail-closed stubs
// (UploadFile + UploadFileWithDescription + sourcing.DrivePort.
// UploadFileWithDescription) retired via PR-YT-DRIVE-LEGACY-RETIRE.

// ── Domain DTOs (canonical shape at the application–infra seam) ────────

// ClipUploadResultDTO retired in DRIVE-008 CUTOVER (July 2026).
// Was the return type of the removed UploadFile/UploadFileWithDescription methods.

// ClipDriveFileDTO mirrors the per-row shape returned by
// drive.Service.Files.List. Only ID + Name are read by the clips
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

// ClipVectorAssetDTO was retired. The vector
// capability was deleted; the clip indexer
// (internal/infrastructure/indexing/clipindexer) is now the single
// canonical semantic-search backend.

// ClipVoiceoverRecordDTO mirrors sqlite/assets.Record. PG-005
// (June 2026): inlined here so ports.go has zero infrastructure
// imports. The adapter at internal/app/clips_adapters.go converts
// *assets.Record ↔ *ClipVoiceoverRecordDTO at the infra seam. Field
// set is the 22-column voiceovers SELECT projection — fields can be
// added safely by widening both the adapter and this DTO in lockstep.
type ClipVoiceoverRecordDTO struct {
	ID              string
	RequestID       string
	TextHash        string
	TextPreview     string
	Language        string
	Voice           string
	Filename        string
	LocalPath       string
	CleanedPath     string
	FolderID        string
	FolderPath      string
	DriveFileID     string
	DriveLink       string
	DownloadLink    string
	FileHash        string
	DurationSeconds float64
	Status          string
	Error           string
	Strategy        string
	Metadata        string
	CreatedAtRFC    string // RFC3339 serialised — keeps ports.go infra-free
	UpdatedAtRFC    string
}

// ── Structural ports (signature-bearing, minimal per Pattern 0) ────────

// ClipRepositoryPort is the canonical clips-side narrowed surface of
// *assets.ClipsRepository. The 11 methods listed below are exactly
// the ones handlers + helpers + worker + clip_ops + clip_action call
// on the concrete repo. Adapter struct machinery lives in
// internal/app/clips_adapters.go.
//
// QDRANT-asset-mutation isolation (June 2026): UpsertClip was REMOVED
// from this port. Production callers (clips.ClipOpsService +
// api/assets/clips) MUST use ClipIndexDispatcherPort to emit media_assets
// writes via outbox.Dispatcher.EnqueueAndIndex; the lower-level UpsertClip
// method stays on *assets.ClipsRepository for the dispatcher's exclusive
// use. The CI lint scripts/ci-architectural-checks.sh enforces this
// boundary by failing on `UpsertClip\(` matches in
// internal/application + internal/api production paths.
type ClipRepositoryPort interface {
	Upsert(ctx context.Context, clip *asset.Asset) error
	Get(ctx context.Context, id string) (*asset.Asset, error)
	GetClip(ctx context.Context, id string) (*asset.Asset, error)
	ListFolders(ctx context.Context, source string) ([]*asset.ClipFolder, error)
	GetFolder(ctx context.Context, folderID string) (*asset.ClipFolder, error)
	GetFolderChildren(ctx context.Context, parentID string) ([]*asset.Asset, error)
	ListByFolderID(ctx context.Context, folderID string) ([]*asset.Asset, error)
	ListByFolderPath(ctx context.Context, folderPath string) ([]*asset.Asset, error)
	DeleteFolder(ctx context.Context, id string) error
	ListClipsPaged(ctx context.Context, source string, limit, offset int, query string) ([]*asset.Asset, error)
	FindClipsByHash(ctx context.Context, hash string) ([]*asset.Asset, error)
}

// VoiceoverRepositoryPort is the canonical narrow surface of
// *assets.VoiceoversRepository used by the clips handler. Only 3
// methods are exposed because those are the only ones the clips
// handler dispatches voiceover source through. PG-005 (June 2026):
// GetByID/ListAll return *ClipVoiceoverRecordDTO (domain shape, in this
// ports file). Upsert takes *ClipVoiceoverRecordDTO. Adapter at
// internal/app/clips_adapters.go converts at the infra seam so this
// file has zero infra imports.
type VoiceoverRepositoryPort interface {
	GetByID(ctx context.Context, id string) (*ClipVoiceoverRecordDTO, error)
	ListAll(ctx context.Context) ([]*ClipVoiceoverRecordDTO, error)
	Upsert(ctx context.Context, rec *ClipVoiceoverRecordDTO) error
}

// ImageRepositoryPort is the canonical narrow surface of
// *imagesrepo.ImagesRepository. Only ListAll is exposed because Cleanup()
// is the only callsite.
type ImageRepositoryPort interface {
	ListAll(ctx context.Context) ([]*asset.ImageAsset, error)
}

// ClipDriveUploaderPort is the canonical narrow surface of
// *drive.Uploader used by the clips handler. ListFiles propagates the
// raw Drive query string (caller-side filtering keeps trashed entries
// out, same pattern as artlist.DriveFolderManager.ListByQuery).
//
// DRIVE-008 CUTOVER (July 2026): UploadFile and UploadFileWithDescription
// removed — migration to delivery.Publisher.Publish is complete.
type ClipDriveUploaderPort interface {
	GetOrCreateFolder(ctx context.Context, name, parentFolderID string) (string, error)
	GetFolderName(ctx context.Context, folderID string) (string, error)
	TrashFolder(ctx context.Context, folderID string) error
	DeleteFolder(ctx context.Context, folderID string) error
	DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error)
	GetFileMD5(ctx context.Context, fileID string) (string, error)
	GetFileMeta(ctx context.Context, fileID string) (*ClipDriveFileMetaDTO, error)
	TrashFile(ctx context.Context, fileID string) error
	ListFiles(ctx context.Context, query string) ([]ClipDriveFileDTO, error)
}

// ClipMetaWriterPort is the canonical narrow surface of
// semantic.MetadataWriterPort. GeneratePayload returns the parsed
// payload + status string; matches the existing EnrichUseCase
// signature so the use case can swap to the port without semantic
// drift.
type ClipMetaWriterPort interface {
	GeneratePayload(ctx context.Context, req ClipMetaWriteRequest) (*ClipMetaPayload, string, error)
}

// ClipConfigPort is the canonical clips-side narrow surface of
// *config.Config. Each method exposes exactly the field the handler
// reads (Pattern 0: never return the whole *Config).
//
// HC-1 (June 2026): adds JobTimeout(t) — the typed config-port for
// per-job-type execution timeouts. Consumed by the bulk_upload worker
// (`internal/application/clips/bulk_upload_worker.go::HandleJob` —
// was `context.WithTimeout(ctx, 2*time.Hour)` pre-HC-1, now
// `w.cfg.JobTimeout(job.TypeBulkUploadYouTubeClips)`). The concrete
// adapter at internal/app/clips_adapters_cfg.go delegates to
// jobs.TimeoutResolver (canonical impl: *jobs.Registry via
// jobs.Compose()).
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
	JobTimeout(jobType string) time.Duration
}

// ClipHashPort is the canonical narrow surface of hashutil.MD5File
// (the bulk_upload_worker only computes MD5; an entire hashutil
// surface import is unnecessary).
type ClipHashPort interface {
	MD5File(path string) (string, error)
}

// VectorStorePort was retired. The vector
// capability was deleted.

// ClipTreeBuilderPort is the canonical narrow surface of
// assettree.Service.UpsertNode for the bulk-tags handler. PG-005
// (June 2026): takes a *asset.Asset (domain shape) — the infra
// *assets.AssetNode conversion is encapsulated in the adapter (see
// internal/app/clips_adapters.go::clipsAssetTreeAdapter), so this
// use case has zero infra imports.
type ClipTreeBuilderPort interface {
	UpsertFromAsset(ctx context.Context, clip *asset.Asset) error
}

// ClipPublisherPort is the canonical narrow surface of delivery.Publisher
// consumed by clips use cases (upload, bulk_upload). The concrete adapter
// wraps the composition-root's delivery.Publisher.
type ClipPublisherPort interface {
	Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error)
}

// ClipIndexDispatcherPort is the canonical narrow surface of
// outbox.Dispatcher consumed by the unified clips HTTP handler
// (UpdateClip route). The implementation atomically upserts the
// asset AND enqueues an outbox event in a single tx — the QDRANT-002
// pattern that eliminates the SQLite → Qdrant indexing gap.
//
// contentHash is the ingest-time file hash used for the dispatcher's
// supersede-gate dedup field.
//
// Pattern 8 rationale: the API layer was previously importing the
// concrete *outbox.Dispatcher directly, in violation of AGENTS.md
// Pattern 0 / Pattern 8 ("internal/api/** non deve contenere business
// orchestration, no concrete infrastructure imports"). The handler
// now depends on this interface; the concrete wiring lives in the
// composition root at `internal/app/clips_dispatcher_adapter.go` with
// a compile-time `var _ clips.ClipIndexDispatcherPort =
// (*clipsDispatcherAdapter)(nil)` assertion.
//
// Nil semantics: handler treats a nil port as "dispatcher not wired"
// (tests, partial deployments) and falls back to raw repo.UpsertClip.
// The composition root only constructs the adapter when
// outbox.Dispatcher is non-nil.
type ClipIndexDispatcherPort interface {
	EnqueueAndIndex(ctx context.Context, clip *asset.Asset, contentHash string) error
}
