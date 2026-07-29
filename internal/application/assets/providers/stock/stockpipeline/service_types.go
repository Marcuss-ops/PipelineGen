// Package stockpipeline — service_types.go (PR-STOCK-SERVICE-SPLIT,
// July 2026).
//
// SOLE owner of the public-facing struct types that compose the
// stock pipeline Service constructor input (godlike/06 SSOT —
// one canonical owner per fact). The constructor body lives in
// service.go::NewService; the field-name list lives here.
//
// godlike/07 minimum-blast-radius: zero new types added; this
// file is pure code-motion from the pre-split service.go. The
// 4 types (PipelineConfig + StorageDeps + MediaDeps + Deps) are
// unchanged byte-stable; the only delta is the file path. All
// public API (ServiceDeps, NewService call sites, all internal
// readers) compile without modification because Go resolves the
// type identifier by symbol + package, not by source file.
//
// PR-STOCK-SERVICE-SPLIT extracted these from service.go on
// 2026-07-04. The pre-split file was 380 LoC (the user spec
// referenced a 914-LoC pre-Commit-4-expanded view of service.go
// that no longer exists; see service_errors.go preamble for the
// full honest scope disclosure).
package stockpipeline

import (
	"context"
	"io"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// PipelineConfig holds configuration for the stock pipeline run.
type PipelineConfig struct {
	ChunkDuration      int
	MaxResults         int
	EffectInterval     int
	EffectsDir         string
	Width              int
	Height             int
	FPS                int
	Codec              string
	Preset             string
	CRF                int
	KeyframeInterval   int
	TransitionInterval int
	OverlayOpacity     float64
	ClipDuration       int
}

// DefaultPipelineConfig returns a PipelineConfig with sensible defaults.
func DefaultPipelineConfig() PipelineConfig {
	return PipelineConfig{
		ChunkDuration:  25,
		MaxResults:     25,
		EffectInterval: 4,
		EffectsDir:     "assets/effects/EffettiVisiv",
		Width:          1920, Height: 1080, FPS: 24, Codec: "libx264", Preset: "veryfast", CRF: 23,
		KeyframeInterval: 48, TransitionInterval: 4, OverlayOpacity: 0.25, ClipDuration: 5,
	}
}

// RuntimeConfig is the minimal platform configuration translated by the
// composition root before it reaches the application service.
type RuntimeConfig struct {
	WorkDir          string
	ClipDurationSec  int
	ChunkDurationSec int
	MaxResults       int
	PolicyVersion    string
}

// StorageDeps groups the canonical media_assets + Qdrant + asset-index stack.
// P8 (July 2026): narrowed to narrow interfaces so service.go has zero
// internal/infrastructure imports. Concrete types (*assets.ClipsRepository,
// *assetindex.Service, *outbox.Dispatcher) satisfy these interfaces
// structurally at the composition root.
//
// BatchRepository is required by the production composition root so batch,
// group and artifact state is durable.
type StorageDeps struct {
	ClipsRepo       stockClipsSearchTermUpdater
	AssetIndex      stockAssetIndexUpserter
	Dispatcher      stockChunkDispatcher
	BatchRepository StockBatchRepository
}

// MediaDeps groups the PR6 ports. P8 (July 2026): ClipIndexer + MetaWriter
// fields REMOVED — they were unused dead code (zero call sites in the
// stockpipeline package).
type MediaDeps struct {
	Cutter   VideoCutter
	Renderer StockRenderer
}

// Deps is the canonical constructor input for stockpipeline.Service
// (PR-D, Wave 22 §D3, June 2026). Sized at 6 top-level fields — well
// under the AGENTS.md 8-per-bundle cap. Sub-dependencies (StorageDeps
// + MediaDeps) group related concerns so the field-name list reads as
// the canonical composition pattern:
//
//	Cfg, Log, Drive         — pure data + Drive SDK
//	Storage                 — media_assets + outbox + asset-index stack
//	Media                   — PR6 ports + semantic enrichment
//	YouTube                 — provider for metadata enrichment
//	Jobs                    — async job tracker
//
// Pattern source: artlist.ServiceDeps (PR2.5, June 2026) — `ServiceDeps`
// embeds `ServicePorts + ServiceDependencies` for terse construction;
// RuntimeDeps groups the pure data / runtime knobs so Deps stays
// under the archcheck 8-field cap.
type RuntimeDeps struct {
	Cfg        *RuntimeConfig
	Log        *zap.Logger
	JobCreator JobCreator
	StepStore  steps.Store
}

// JobCreator is the minimal durable port needed by sync-mode stock runs.
// SQLite and other brokers implement it in the composition root; the
// application service never reaches into a database handle.
type JobCreator interface {
	Create(ctx context.Context, j *job.Job) error
}

// ExecutionDeps groups the job + source-staging ports so Deps stays
// under the archcheck 8-field cap.
type ExecutionDeps struct {
	Jobs         *appjobs.Service
	SourceStager acquisition.SourceStager
	// ChannelLister is the YouTube channel listing port (P4, July 2026).
	// Optional because direct URL requests do not need channel listing — the
	// composition root (currently retired/stubbed) wires the concrete
	// `*downloader.YTDLPDownloader` which satisfies ChannelLister
	// structurally. When nil, query.go's resolveQuery fails-closed with
	// a typed nil-port error at the first search attempt.
	ChannelLister ChannelLister
}

// StockFolderCreator creates or reuses a Drive folder below a caller-supplied
// parent. The stock application owns the intent; infrastructure supplies the
// concrete Drive adapter.
type StockFolderCreator interface {
	GetOrCreateFolder(ctx context.Context, name, parentID string) (string, error)
}

// here the sub-struct names carry semantic meaning rather than the
// "ports vs dependencies" split at the artlist boundary (the stock
// pipeline has fewer ports to lift out).
//
// PR-D: setter pattern (SetCutter, SetRenderer, SetClipsRepo,
// SetAssetIndex, SetDispatcher, SetJobsSvc, SetYoutubeService,
// SetClipIndexer, SetMetadataWriter) is REMOVED. All dependencies
// are constructor arguments on Deps — replaces the late-bind ordering
// hazard that swapped the canonical ingestion path on every
// composition-time race in WireStockPipeline.

// SourceCacheDeps groups the cross-run source download cache ports.
// All fields are OPTIONAL — nil means no cache (every download is
// fresh). When both Reader and Writer are wired, the StockStager
// checks the cache before yt-dlp and populates it after a successful
// download. LocalFS is the Pattern 0 typed port for the file I/O the
// cache needs (Stat on hit; Open+Create on copy); PR-REFACTOR-P0-IO-BINDER
// keeps os.* calls out of the application layer.
// Field count: 3.
type SourceCacheDeps struct {
	Reader  SourceCacheReader
	Writer  SourceCacheWriter
	LocalFS LocalFSPort
}

// DeliveryDeps groups the ports that complete and publish a stock run.
// Keeping this boundary explicit prevents the service constructor from
// growing a flat list of cross-cutting delivery concerns.
//
// PublisherPort is the pre-constructed finalization.PublisherPort adapter
// (created at the composition root via drive.NewArtifactPublisherAdapter)
// so the application layer stays free of internal/infrastructure/drive
// imports (godlike/06 import-boundary discipline).
type DeliveryDeps struct {
	Publisher     delivery.Publisher
	PublisherPort finalization.PublisherPort
	FolderCreator StockFolderCreator
	DriveReader   DriveReaderPort
	Finalizer     finalization.JobFinalizer
}

// DriveFileInfo is the application-layer representation of a Google Drive
// file entry returned by ListFiles. Defined locally so the DriveReaderPort
// stays free of internal/infrastructure/drive imports (godlike/06
// import-boundary discipline). The composition root adapts the concrete
// drive.DriveFileInfo to this type.
type DriveFileInfo struct {
	ID       string
	MimeType string
}

// DriveReaderPort is the Google Drive read-side port used by the
// stock pipeline to download a single file and to list the contents
// of a Drive folder (so a folder URL can be expanded to the first
// video). The concrete *drive.Uploader is adapted at the composition
// root via a thin wrapper.
type DriveReaderPort interface {
	DownloadFile(ctx context.Context, fileID string) (io.ReadCloser, string, error)
	ListFiles(ctx context.Context, parentID string) ([]DriveFileInfo, error)
}

type Deps struct {
	// F2.10: Drive field dropped — every Drive write routes through
	// delivery.Publisher (Publisher field below). The legacy
	// driveup.Admin surface (UploadFile + GetOrCreateFolder + Trash +
	// Delete etc.) was retired (override brutal). Folder resolution
	// inside the pipeline run uses publisher.ResolveFolder
	// (DestinationStock policy) instead of driveutil.EnsureFolderPath.
	Runtime     RuntimeDeps
	Storage     StorageDeps
	Media       MediaDeps
	Execution   ExecutionDeps
	SourceCache SourceCacheDeps
	Delivery    DeliveryDeps
}
