// Package youtube holds the application-layer orchestrator for the YouTube
// clip-extraction pipeline. Persistence, IO, and external-process execution
// are delegated to ports declared in the ports sub-package and implemented
// under internal/platform/youtube
//
// Per PR1.7 (June 2026):
//   - The setter cascade has been collapsed into a single
//     NewServiceFromSubBundles(core, asset, video, storage, adapter)
//     constructor. Callers wire every port exactly once at composition
//     time; missing deps are surfaced via nil guard errors at first use.
//   - Persistence has exactly ONE canonical writer: `AssetRepository` on
//     ServiceAssetDeps. The previous triple fallback has been removed in PR1.6.
//   - Drive operations go exclusively through DriveFolderManagerPort.
//   - Concrete imports of outbox / drive SDK / clipsRepo have been removed
//     from this package; concrete wiring belongs to composition + infra.
//   - Asset-processing/version callbacks were removed from the extraction
//     flow; the canonical asset writer is AssetRepo.
//
// PR-GRUPOC-1 (July 2026): the historical monolithic ServiceDeps
// (22 fields) is RETIRED in favour of 5 capability-area sub-bundles
// (godlike/06 SSOT one-canonical-owner-per-fact, percheck_struct_deps
// ≤8 fields enforcement). Each sub-bundle groups ports by their
// real responsibility area (runtime config / asset lifecycle /
// video pipeline / persistent state / external I/O) — NOT by
// arbitrary field-position split. The composition root in
// internal/app/build_bundles_domain_media.go wires the 5 sub-bundles
// from the same canonical production dep set, so no call-site loses
// access to a port it actually consumed.
//
// CPR-CC-6 (June 2026): split from mega-package service_orchestrator.go.
// This file holds only the Service struct, sub-bundles, constructor,
// and validator. Public methods are in orchestrator.go. ExtractionCallbacks
// are in callbacks.go.
package usecase

import (
	"fmt"
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockplan"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/shared/portutil"
)

// ── Per-cluster sub-bundles (PR-GRUPOC-1, July 2026) ──────────────────
//
// godlike/06 SSOT (one canonical owner per fact): each sub-bundle
// groups ports that share a single responsibility area. The
// percheck_struct_deps enforcement is satisfied because every
// sub-bundle has ≤7 fields. NO struct-bag aliasing: each port is
// directly typed in the sub-bundle, not wrapped in a nested struct.

// ServiceCoreDeps is the runtime-config cluster: cfg + log.
//   - Cfg: the per-build YouTube runtime config (concurrency
//     limits, timeouts, hard-coded paths).
//   - Log: the canonical zap logger (composition root provides
//     a single instance per process).
type ServiceCoreDeps struct {
	Cfg youtubetypes.RuntimeConfig
	Log *zap.Logger
}

// ServiceAssetDeps is the asset-lifecycle cluster: the canonical
// asset writer, the destination resolver, the lifecycle orchestrator,
// and the per-asset media processor. These four are the "what
// happens to a single asset row" surface — the YouTube orchestrator
// delegates asset mutation through this cluster (no setters, no
// fallback paths).
type ServiceAssetDeps struct {
	AssetRepo         detail.Repository
	AssetDestResolver asset.Resolver
	LifecycleService  *lifecycle.Service
	MediaProcessor    detail.Processor
}

// ServiceVideoDeps is the video-pipeline cluster: the
// VideoPipelinePort (yt-dlp + ffmpeg facade) and the
// ProcessYouTubeSegmentUseCase (the per-segment orchestrator
// wired through PR-GODOBJ-1, godlike/07 fail-closed at boot).
type ServiceVideoDeps struct {
	VideoPipeline youtubeports.VideoPipelinePort
	ProcessSeg    *ProcessYouTubeSegmentUseCase
}

// ServiceStorageDeps is the persistent-state cluster: clip store +
// L2 cache + monitors + clip indexer + folder memory + ollama +
// transcript reader. All seven are local-state ports — the
// orchestrator reads / writes them but never holds external
// resources directly.
type ServiceStorageDeps struct {
	Clips            youtubeports.ClipStorePort
	Cache            youtubeports.CachePort
	Monitors         youtubeports.MonitorsStorePort
	Indexer          youtubeports.ClipIndexerPort
	FolderMemory     youtubeports.FolderMemoryPort
	Ollama           youtubeports.OllamaClientPort
	TranscriptReader TranscriptReader
}

// ServiceAdapterDeps is the external-I/O cluster: search +
// subtitle + whisper + clip-files + meta-fetcher + drive-folder +
// hash. All seven reach outside the process boundary (yt-dlp,
// Drive API, network, etc.) — the orchestrator delegates them
// without holding concrete clients.
type ServiceAdapterDeps struct {
	SearchRunner    youtubeports.SearchRunnerPort
	SubtitleFetcher youtubeports.SubtitleFetcherPort
	Whisper         youtubeports.WhisperTranscriberPort
	ClipFiles       youtubeports.ClipFilesPort
	MetaFetcher     youtubeports.VideoMetadataFetcherPort
	DriveFolderMgr  youtubeports.DriveFolderManagerPort
	HashSvc         youtubeports.HashServicePort
}

// Service is the YouTube orchestrator. Construct it once via
// NewServiceFromSubBundles (no setters). Methods received on
// nil-receiver port fields surface an explicit error rather than
// silently no-op'ing.
type Service struct {
	cfg               youtubetypes.RuntimeConfig
	log               *zap.Logger
	mediaProcessor    detail.Processor
	videoPipeline     youtubeports.VideoPipelinePort
	lifecycleService  *lifecycle.Service
	assetDestResolver asset.Resolver
	assetRepo         detail.Repository

	// Capability services (PR5 — June 2026; P0.3: MetadataService retired).
	cache      youtubeports.CachePort
	search     *SearchService
	segSvc     *SegmentsService
	extraction *ExtractionService

	// Port-backed dependencies (no setters).
	searchRunner    youtubeports.SearchRunnerPort
	subtitleFetcher youtubeports.SubtitleFetcherPort
	whisper         youtubeports.WhisperTranscriberPort
	clipFiles       youtubeports.ClipFilesPort
	metaFetcher     youtubeports.VideoMetadataFetcherPort
	driveFolderMgr  youtubeports.DriveFolderManagerPort
	hashSvc         youtubeports.HashServicePort

	clips        youtubeports.ClipStorePort
	monitors     youtubeports.MonitorsStorePort
	indexer      youtubeports.ClipIndexerPort
	folderMemory youtubeports.FolderMemoryPort
	ollama       youtubeports.OllamaClientPort

	// Capacity-bound semaphores configured via ConcurrencyConfig.
	videoExtractSem chan struct{}
	ollamaSem       chan struct{}

	// P1.3: Pattern 0 port for reading on-disk transcript files.
	transcriptReader TranscriptReader
	processSeg       *ProcessYouTubeSegmentUseCase
	stockService     *stockplan.StockService
}

// NewServiceFromSubBundles is the sole canonical constructor (PR-GRUPOC-1,
// July 2026). Pass every dependency a component of the YouTube pipeline
// touches via 5 capability-area sub-bundles; missing nothing means no
// surrogate setters are needed. Composition root (internal/app/build_
// bundles_domain_media.go) is the only intended caller.
//
// PR5 (June 2026): the L2 cache is injected through CachePort; composition
// owns the SQLite-backed infrastructure adapter.
func NewServiceFromSubBundles(
	core ServiceCoreDeps,
	asset ServiceAssetDeps,
	video ServiceVideoDeps,
	storage ServiceStorageDeps,
	adapter ServiceAdapterDeps,
) *Service {
	maxVideo := core.Cfg.MaxConcurrentVideoExtracts
	if maxVideo <= 0 {
		maxVideo = 1
	}
	maxOllama := core.Cfg.MaxConcurrentOllamaCalls
	if maxOllama <= 0 {
		maxOllama = 1
	}
	svc := &Service{
		cfg:               core.Cfg,
		log:               core.Log,
		mediaProcessor:    asset.MediaProcessor,
		videoPipeline:     video.VideoPipeline,
		lifecycleService:  asset.LifecycleService,
		assetDestResolver: asset.AssetDestResolver,
		assetRepo:         asset.AssetRepo,

		searchRunner:    adapter.SearchRunner,
		subtitleFetcher: adapter.SubtitleFetcher,
		whisper:         adapter.Whisper,
		clipFiles:       adapter.ClipFiles,
		metaFetcher:     adapter.MetaFetcher,
		driveFolderMgr:  adapter.DriveFolderMgr,
		hashSvc:         adapter.HashSvc,

		clips:        storage.Clips,
		cache:        storage.Cache,
		monitors:     storage.Monitors,
		indexer:      storage.Indexer,
		folderMemory: storage.FolderMemory,
		ollama:       storage.Ollama,

		transcriptReader: storage.TranscriptReader,
		processSeg:       video.ProcessSeg,

		videoExtractSem: make(chan struct{}, maxVideo),
		ollamaSem:       make(chan struct{}, maxOllama),
	}
	if stock, err := stockplan.NewYouTubeStockService(svc, svc, svc, core.Cfg.ClipsFolderID); err == nil {
		svc.stockService = stock
	} else if core.Log != nil {
		core.Log.Warn("youtube stock capability unavailable", zap.Error(err))
	}

	// Wire search service (PR5 Phase 2).
	//
	// PR2 fail-closed (June 2026): typed-nil defense-in-depth. The composition
	// root wires a non-nil `*SearchRunnerAdapter` (checked in
	// composition.go::BuildDomainBundle) but a future refactor could
	// accidentally pass a typed-nil concrete pointer through an interface
	// field of ServiceAdapterDeps. The portutil.IsNilPort guard catches that case
	// and refuses to wire the search service, producing an explicit
	// failure at first use instead of a silent panic.
	if adapter.SearchRunner != nil && !portutil.IsNilPort(adapter.SearchRunner) && core.Log != nil {
		svc.search = NewSearchService(SearchDeps{
			SearchRunner: adapter.SearchRunner,
			Cache:        svc.cache,
			Log:          core.Log,
		})
	}

	// Wire segments service (PR5 Phase 4 — zero-dependency).
	svc.segSvc = NewSegmentsService()

	// Wire extraction service (PR5 Phase 3 — thin wrapper pattern).
	// The root Service implements ExtractionCallbacks so callbacks are
	// simply method calls on the same Service instance.
	svc.extraction = NewExtractionService(ExtractionDeps{
		Cfg: core.Cfg,
		Log: core.Log,
		Legacy: LegacyCompositionDeps{
			VideoPipeline: video.VideoPipeline,
			Clips:         storage.Clips,
			Cache:         storage.Cache,
			Monitors:      storage.Monitors,
		},
		AssetDestResolver: asset.AssetDestResolver,
		FolderMemory:      storage.FolderMemory,
		SegmentsSvc:       svc.segSvc,
		// PR-GODOBJ-1 (July 2026): REQUIRED. NewExtractionService
		// panics if ProcessSeg is nil (godlike/07 fail-closed;
		// legacy inline loop PHYSICALLY removed). Composition MUST
		// wire ProcessYouTubeSegmentUseCase.
		ProcessSeg:          video.ProcessSeg,
		MaxConcurrentVideos: core.Cfg.MaxConcurrentVideoExtracts,
	}, svc)

	return svc
}

// SetSegmentSelectionResolver wires the canonical segment-selection
// resolver (explicit | important) into the extraction pipeline. The
// composition root calls this once after NewServiceFromSubBundles;
// a nil resolver leaves the extraction service in explicit-only mode
// (selection.mode="important" fails closed, godlike/07).
func (s *Service) SetSegmentSelectionResolver(r *SegmentSelectionResolver) {
	if s == nil {
		return
	}
	if s.extraction != nil {
		s.extraction.SetSegmentSelectionResolver(r)
	}
}

// StockService returns the transcript-first YouTube stock capability. A nil
// return means the capability was not wired and must not be registered.
func (s *Service) StockService() *stockplan.StockService {
	if s == nil {
		return nil
	}
	return s.stockService
}

// ValidateServiceDepsFromSubBundles checks the 5 sub-bundles for
// typed-nil interfaces on required ports. Composition MUST call this
// before constructing the service so typed-nil wiring errors surface
// at startup, not at first invocation.
//
// PR-GRUPOC-1 (July 2026): mirrors the pre-refactor typed-nil guards
// (SearchRunner / AssetRepo / VideoPipeline / MediaProcessor) on the
// new sub-bundle surface. godlike/07 fail-closed: nil REQUIRED ports
// surface as typed errors at composition time, not at first request.
func ValidateServiceDepsFromSubBundles(
	core ServiceCoreDeps,
	asset ServiceAssetDeps,
	video ServiceVideoDeps,
	_ ServiceStorageDeps,
	adapter ServiceAdapterDeps,
) error {
	if isUnavailablePort(adapter.SearchRunner) {
		return fmt.Errorf("youtube: SearchRunner is required but not wired (or typed-nil)")
	}
	if asset.AssetRepo == nil || portutil.IsNilPort(asset.AssetRepo) {
		return fmt.Errorf("youtube: AssetRepo is required but not wired (or typed-nil)")
	}
	if isUnavailablePort(video.VideoPipeline) {
		return fmt.Errorf("youtube: VideoPipeline is required but not wired (or typed-nil)")
	}
	if asset.MediaProcessor == nil || portutil.IsNilPort(asset.MediaProcessor) {
		return fmt.Errorf("youtube: MediaProcessor is required but not wired (or typed-nil)")
	}
	return nil
}
