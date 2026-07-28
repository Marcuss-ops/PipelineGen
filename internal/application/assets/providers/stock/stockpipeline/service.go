// Package stockpipeline — service.go (PR-STOCK-SERVICE-SPLIT, July 2026).
//
// The stock pipeline Service is the typed orchestrator that drives the
// 6-step stock pipeline (plan → stage_sources → extract_clips →
// compose_chunks → publish → finalize) and the broker-side job
// entrypoint. This file is the SOLE owner of:
//
//   - The Service struct (private fields + 4 unexported port-shaped
//     accessors: cutter / renderer / dispatcher / finalizer /
//     sourceStager / channelLister / db).
//   - The NewService constructor (12-step validation ladder that
//     surfaces each missing dep as a typed sentinel from
//     service_errors.go).
//   - The factory wiring that copies Deps fields into the
//     unexported Service struct fields.
//
// godlike/06 SSOT (one canonical owner per fact):
//
//   - Sentinel errors (ErrStockPipelineNil*):  service_errors.go
//   - Constructor input types (Deps + StorageDeps + MediaDeps +
//     PipelineConfig):                          service_types.go
//   - Job handler methods (RegisterHandler /
//     HandleJob / extractLease / manifestBytes): job_handler.go
//   - Run types (RunInput, ChunkResult,
//     PipelineResult, DTOs):                     types_run.go
//   - Source staging methods (StageSource,
//     stageSection):                             source_staging.go
//   - Pattern 0 resilience ports (3) +
//     4 typed sentinels:                        upload_orchestration.go
//   - 6-step orchestrator + StepRunner + 2
//     more typed sentinels:                     orchestrator_steps.go
//   - StepRunner interface + runState + 6
//     accessors:                                step_runner.go
//   - Orchestrator defaults + compile-time
//     assertions:                               orchestrator_defaults.go
//   - Artifact ID helpers:                      orchestrator_fingerprint.go
//   - Metadata helpers:                         orchestrator_metadata.go
//
// PR-STOCK-SERVICE-SPLIT extracted (a) the sentinels to
// service_errors.go + (b) the constructor input types to
// service_types.go on 2026-07-04. The pre-split file was 380 LoC
// (the user spec referenced a 914-LoC pre-Commit-4-expanded view
// of service.go that no longer exists; the spec's
// "service_resilience.go / service_state.go / service_steps.go /
// service_metrics.go" files would have been empty per godlike/07
// no-fake-availability — the resilience ports + state machine +
// Stage 1-5 + Prometheus metrics live in upload_orchestration.go,
// job_handler.go, orchestrator_steps.go and are NOT in service.go
// post-Commit-4-expanded; see service_errors.go preamble + commit
// body for the full honest scope disclosure).
package stockpipeline

import (
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/execution/steps"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// Service orchestrates the stock video pipeline: search, download, clip extraction,
// effect overlay, chunk rendering, and Drive upload. All video parameters are read
// from config.Video to ensure consistency with other media pipelines.
//
// PR6 (June 2026) port injection: the Service no longer reaches into the
// ffmpeg.Processor directly. Instead it depends on two canonical ports
// declared in internal/application/assets/providers/stock:
//
//   - stock.VideoCutter  (extracted-clips from a single source video)
//   - stock.StockRenderer (cross-clip concatenation + transition/overlay)
//
// PR-D (June 2026): all dependencies — including the PR6 ports —
// arrive via the ctor-injected Deps struct (defined in
// service_types.go). The 9 legacy setters (SetCutter / SetRenderer /
// SetClipsRepo / SetAssetIndex / SetDispatcher / SetJobsSvc /
// SetYoutubeService / SetClipIndexer / SetMetadataWriter) were
// removed. Production wire-up lives in WireStockPipeline at
// internal/app/module_sources.go (Deps{...} literal).
//
// P4+P8 (July 2026): the ytdlp, clipIndexer, metaWriter, youtubeSvc fields
// are REMOVED — dead code or port-abstracted. All infra imports are
// eliminated from service.go (godlike/06 import-boundary discipline).
type Service struct {
	cfg           *config.Config
	log           *zap.Logger
	publisher     delivery.Publisher
	folderCreator StockFolderCreator
	cutter        VideoCutter
	renderer      StockRenderer
	pcfg          PipelineConfig
	jobsSvc       *appjobs.Service
	assetIndex    stockAssetIndexUpserter
	clipsRepo     stockClipsSearchTermUpdater
	// batchRepo is the durable stock batch/group/artifact repository.
	// nil means the pipeline runs in-memory/test mode (back-compat).
	batchRepo StockBatchRepository
	// dispatcher is the canonical media_index_outbox dispatcher,
	// required at ctor time per QDRANT-002 PR7. NewService rejects
	// nil dispatcher with ErrStockPipelineNilDispatcher.
	// Audit P0 #6: narrowed from `*outbox.Dispatcher` to the local
	// `stockChunkDispatcher` interface so test fakes can wire the
	// shape without dragging in the full infra surface.
	dispatcher stockChunkDispatcher
	// finalizer is the Spina Dorsale JobFinalizer (§12-1 §F.1
	// governance, July 2026). OPTIONAL in this commit (no fail-fast
	// on nil — see ErrStockPipelineNilFinalizer doc-comment). §F.2
	// follow-up promotes it to a REQUIRED dep + wires production
	// finalizer.New(...) at the composition root (currently routed
	// via imageSvc per registry_internal_modules.go).
	finalizer finalization.JobFinalizer
	// sourceStager is the canonical acquisition.SourceStager port
	// (Stock Cutover §12-4, July 2026). REQUIRED at ctor time — the
	// None-checking ErrStockPipelineNilSourceStager gate surfaces
	// composition-time wiring gaps before the first stock run hits
	// the broker.
	sourceStager acquisition.SourceStager
	// channelLister is the YouTube channel listing port (P4, July 2026).
	// The concrete `*downloader.YTDLPDownloader` satisfies ChannelLister
	// structurally. nil-tolerant at ctor time (wire-up deferred pending
	// composition-root re-enablement).
	channelLister ChannelLister
	// driveReader enables listing Google Drive folders so a Drive folder
	// URL can be expanded to its first video file, and downloads single
	// Drive files. nil-tolerant — when nil, Drive URLs fail with a typed
	// error.
	driveReader DriveReaderPort

	// db is the SQLite handle for the step store (Phase 2, July 2026).
	// nil-tolerant — when nil, the orchestrator falls back to in-memory.
	jobCreator JobCreator
	stepStore  steps.Store

	// sourceCacheReader + sourceCacheWriter are the cross-run source
	// download cache ports. When both are non-nil, the StockStager
	// checks the SQLite-backed cache before invoking yt-dlp and
	// populates it after a successful download. OPTIONAL — nil means
	// no cache (every download is fresh).
	sourceCacheReader SourceCacheReader
	sourceCacheWriter SourceCacheWriter
	// localFS is the Pattern 0 typed port for the local filesystem
	// I/O the source cache needs (Stat on hit; Open+Create on copy).
	// PR-REFACTOR-P0-IO-BINDER keeps os.* calls out of this package;
	// opt nil → fail-closed at the cache copy site.
	localFS LocalFSPort
}

// NewService creates a stock pipeline service via the canonical Deps struct
// (PR-D, June 2026). Returns *Service + error (the legacy signature returned
// only *Service + relied on per-call nil guards; the new contract surfaces
// missing deps at composition time, the only safe window).
//
// Validation order: pure data (Cfg, Log) → transport (Storage) →
// ports (Media) → cross-cutting. Each missing dep surfaces its own
// sentinel error (declared in service_errors.go) so production wiring
// can forward a single error verbatim and tests can assert the precise
// field-name without unwrapping the chain.
//
// PR6 ports (Cutter, Renderer) are required — missing either fails ctor
// with ErrStockPipelineNilCutter / ErrStockPipelineNilRenderer. The
// legacy per-call nil-guards are gone; callers can rely on the
// invariants without re-checking.
//
// Production wire-up lives in WireStockPipeline
// (internal/app/module_sources.go::WireStockPipeline). The composition
// root pre-rejects any nil dispatcher at the wire call-site (QDRANT-002
// PR7 precedent on artlist.WireArtlist); NewService is the second
// line of defence so accidental misuse from tests still fails loud.
func NewService(deps Deps) (*Service, error) {
	if deps.Runtime.Cfg == nil {
		return nil, ErrStockPipelineNilCfg
	}
	if deps.Runtime.Log == nil {
		return nil, ErrStockPipelineNilLog
	}
	// F2.10: Drive validation dropped — see Deps doc-comment. The
	// legacy DriveSvc plumbing (driveup.Admin.UploadFile + friend
	// methods) is gone; Publisher is the only Drive-write canal.
	if deps.Storage.ClipsRepo == nil {
		return nil, ErrStockPipelineNilClipsRepo
	}
	if deps.Storage.AssetIndex == nil {
		return nil, ErrStockPipelineNilAssetIndex
	}
	if deps.Storage.Dispatcher == nil {
		return nil, ErrStockPipelineNilDispatcher
	}
	if deps.Media.Cutter == nil {
		return nil, ErrStockPipelineNilCutter
	}
	if deps.Media.Renderer == nil {
		return nil, ErrStockPipelineNilRenderer
	}
	// P8 (July 2026): ClipIndexer + MetaWriter + YouTube validation RETIRED
	// — dead code (zero call sites in the stockpipeline package).
	// Jobs is required at ctor time per PR-D (Wave 22 §D3). Previously
	// nil-tolerant; the silent nil-passthrough was a regression surface.
	// Validate like every other required dep so composition-time
	// pre-rejection catches missing wiring without waiting for the first job.
	if deps.Execution.Jobs == nil {
		return nil, ErrStockPipelineNilJobs
	}
	// §12-4 (July 2026): SourceStager is REQUIRED. The previous
	// nil-tolerant fallback to a `*downloader.YTDLPDownloader` direct
	// field is retired; production composition roots MUST inject a
	// concrete `acquisition.SourceStager` here (typically
	// `*acquisition.FilesystemStager` wrapping a Fetch closure that
	// invokes yt-dlp subprocess). Missing injection fails loud at
	// boot — no silent-degrade to the old ytdlp field.
	if deps.Execution.SourceStager == nil {
		return nil, ErrStockPipelineNilSourceStager
	}

	v := deps.Runtime.Cfg.Video.WithDefaults()
	return &Service{
		cfg:           deps.Runtime.Cfg,
		log:           deps.Runtime.Log,
		publisher:     deps.Delivery.Publisher,
		folderCreator: deps.Delivery.FolderCreator,
		cutter:        deps.Media.Cutter,
		renderer:      deps.Media.Renderer,
		pcfg: PipelineConfig{
			ChunkDuration:  v.ChunkDuration,
			MaxResults:     v.MaxClipsPerSource,
			EffectInterval: v.EffectInterval,
			EffectsDir:     DefaultPipelineConfig().EffectsDir,
		},
		jobsSvc:           deps.Execution.Jobs,
		assetIndex:        deps.Storage.AssetIndex,
		clipsRepo:         deps.Storage.ClipsRepo,
		batchRepo:         deps.Storage.BatchRepository,
		dispatcher:        deps.Storage.Dispatcher,
		finalizer:         deps.Delivery.Finalizer,
		sourceStager:      deps.Execution.SourceStager,
		channelLister:     deps.Execution.ChannelLister,
		driveReader:       deps.Delivery.DriveReader,
		jobCreator:        deps.Runtime.JobCreator,
		stepStore:         deps.Runtime.StepStore,
		sourceCacheReader: deps.SourceCache.Reader,
		sourceCacheWriter: deps.SourceCache.Writer,
		localFS:           deps.SourceCache.LocalFS,
	}, nil
}
