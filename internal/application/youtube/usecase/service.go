// Package youtube holds the application-layer orchestrator for the YouTube
// clip-extraction pipeline. Persistence, IO, and external-process execution
// are delegated to ports declared in the ports sub-package and implemented
// under internal/infrastructure/youtube.
//
// Per PR1.7 (June 2026):
//   - The setter cascade has been collapsed into a single
//     NewService(ServiceDeps) constructor. Callers wire every port exactly
//     once at composition time; missing deps are surfaced via nil guard
//     errors at first use.
//   - Persistence has exactly ONE canonical writer: `AssetRepository` on
//     ServiceDeps. The previous triple fallback has been removed in PR1.6.
//   - Drive operations go exclusively through DriveFolderManagerPort.
//   - Concrete imports of outbox / drive SDK / clipsRepo have been removed
//     from this package; concrete wiring belongs to composition + infra.
//   - Asset-processing/version callbacks were removed from the extraction
//     flow; the canonical asset writer is AssetRepo.
//
// CPR-CC-6 (June 2026): split from mega-package service_orchestrator.go.
// This file holds only the Service struct, ServiceDeps, NewService
// constructor, and ValidateServiceDeps. Public methods are in
// orchestrator.go. ExtractionCallbacks are in callbacks.go.
package usecase

import (
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/portutil"
)

// ServiceDeps is the FULL set of dependencies the YouTube orchestrator
// requires. Wiring happens exactly once via NewService(ServiceDeps);
// setters are intentionally absent.
type ServiceDeps struct {
	// Core collaborators (always required).
	Cfg               youtubetypes.RuntimeConfig
	Log               *zap.Logger
	MediaProcessor    asset.Processor
	VideoPipeline     youtubeports.VideoPipelinePort
	LifecycleService  *lifecycle.Service
	AssetDestResolver asset.Resolver

	// PR1.6 — canonical persistence writer (asset.Repository).
	// Required: dispatchOrIndex refuses to persist without it.
	AssetRepo asset.Repository

	// Port dependencies.
	SearchRunner    youtubeports.SearchRunnerPort
	SubtitleFetcher youtubeports.SubtitleFetcherPort
	Whisper         youtubeports.WhisperTranscriberPort
	ClipFiles       youtubeports.ClipFilesPort
	MetaFetcher     youtubeports.VideoMetadataFetcherPort
	DriveFolderMgr  youtubeports.DriveFolderManagerPort
	HashSvc         youtubeports.HashServicePort

	// P1.3: TranscriptReader is the Pattern 0 port for reading on-disk
	// transcript files. When nil, enrichment skips transcript-based
	// features (sponsor detection, quality scoring). Tests inject an
	// in-memory reader; production wires OSTranscriptReader.
	TranscriptReader TranscriptReader

	// PR1.5 — port-backed store/cache/index collaborators.
	Clips        youtubeports.ClipStorePort
	Cache        youtubeports.CachePort
	Monitors     youtubeports.MonitorsStorePort
	Indexer      youtubeports.ClipIndexerPort
	FolderMemory youtubeports.FolderMemoryPort
	Ollama       youtubeports.OllamaClientPort

	// PR-GODOBJ-1 (July 2026): REQUIRED via panic fail-closed in
	// NewExtractionService (godlike/07 no-fake-availability).
	// Composition (build_bundles_domain.go) must wire
	// ProcessYouTubeSegmentUseCase — a nil wiring triggers a
	// ctor-panic that surfaces the missing port at boot. The
	// legacy inline per-seg loop was physically removed in
	// PR-GODOBJ-1 (the previous Commit 1/6 "Post-Commit-H
	// removal" ratchet is now realized). Concrete wiring:
	// build_bundles_domain.go constructs ProcessSeg from
	// canonical ClipCacheAdapter + ClipAtomicWriterAdapter.
	ProcessSeg *ProcessYouTubeSegmentUseCase
}

// Service is the YouTube orchestrator. Construct it once via NewService
// (no setters). Methods received on nil-receiver port fields surface an
// explicit error rather than silently no-op'ing.
type Service struct {
	cfg               youtubetypes.RuntimeConfig
	log               *zap.Logger
	mediaProcessor    asset.Processor
	videoPipeline     youtubeports.VideoPipelinePort
	lifecycleService  *lifecycle.Service
	assetDestResolver asset.Resolver
	assetRepo         asset.Repository

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
}

// NewService is the sole canonical constructor. Pass every dependency a
// component of the YouTube pipeline touches; missing nothing means no
// surrogate setters are needed. Composition root (internal/app/composition.go)
// is the only intended caller.
//
// PR5 (June 2026): the L2 cache is injected through CachePort; composition
// owns the SQLite-backed infrastructure adapter.
func NewService(deps ServiceDeps) *Service {
	maxVideo := deps.Cfg.MaxConcurrentVideoExtracts
	if maxVideo <= 0 {
		maxVideo = 1
	}
	maxOllama := deps.Cfg.MaxConcurrentOllamaCalls
	if maxOllama <= 0 {
		maxOllama = 1
	}
	svc := &Service{
		cfg:               deps.Cfg,
		log:               deps.Log,
		mediaProcessor:    deps.MediaProcessor,
		videoPipeline:     deps.VideoPipeline,
		lifecycleService:  deps.LifecycleService,
		assetDestResolver: deps.AssetDestResolver,
		assetRepo:         deps.AssetRepo,

		searchRunner:    deps.SearchRunner,
		subtitleFetcher: deps.SubtitleFetcher,
		whisper:         deps.Whisper,
		clipFiles:       deps.ClipFiles,
		metaFetcher:     deps.MetaFetcher,
		driveFolderMgr:  deps.DriveFolderMgr,
		hashSvc:         deps.HashSvc,

		clips:        deps.Clips,
		cache:        deps.Cache,
		monitors:     deps.Monitors,
		indexer:      deps.Indexer,
		folderMemory: deps.FolderMemory,
		ollama:       deps.Ollama,

		transcriptReader: deps.TranscriptReader,

		videoExtractSem: make(chan struct{}, maxVideo),
		ollamaSem:       make(chan struct{}, maxOllama),
	}

	// Wire search service (PR5 Phase 2).
	//
	// PR2 fail-closed (June 2026): typed-nil defense-in-depth. The composition
	// root wires a non-nil `*SearchRunnerAdapter` (checked in
	// composition.go::BuildDomainBundle) but a future refactor could
	// accidentally pass a typed-nil concrete pointer through an interface
	// field of ServiceDeps. The portutil.IsNilPort guard catches that case
	// and refuses to wire the search service, producing an explicit
	// failure at first use instead of a silent panic.
	if deps.SearchRunner != nil && !portutil.IsNilPort(deps.SearchRunner) && deps.Log != nil {
		svc.search = NewSearchService(SearchDeps{
			SearchRunner: deps.SearchRunner,
			Cache:        svc.cache,
			Log:          deps.Log,
		})
	}

	// Wire segments service (PR5 Phase 4 — zero-dependency).
	svc.segSvc = NewSegmentsService()

	// Wire extraction service (PR5 Phase 3 — thin wrapper pattern).
	// The root Service implements ExtractionCallbacks so callbacks are
	// simply method calls on the same Service instance.
	svc.extraction = NewExtractionService(ExtractionDeps{
		Cfg:               deps.Cfg,
		Log:               deps.Log,
		VideoPipeline:     deps.VideoPipeline,
		Clips:             deps.Clips,
		Cache:             deps.Cache,
		Monitors:          deps.Monitors,
		AssetDestResolver: deps.AssetDestResolver,
		FolderMemory:      deps.FolderMemory,
		SegmentsSvc:       svc.segSvc,
		// PR-GODOBJ-1 (July 2026): REQUIRED. NewExtractionService
		// panics if ProcessSeg is nil (godlike/07 fail-closed;
		// legacy inline loop PHYSICALLY removed). Composition MUST
		// wire ProcessYouTubeSegmentUseCase.
		ProcessSeg:          deps.ProcessSeg,
		MaxConcurrentVideos: deps.Cfg.MaxConcurrentVideoExtracts,
	}, svc)

	return svc
}

// ValidateServiceDeps checks ServiceDeps for typed-nil interfaces on
// required ports. Composition MUST call this before constructing the
// service so typed-nil wiring errors surface at startup, not at first
// invocation.
func ValidateServiceDeps(deps ServiceDeps) error {
	if isUnavailablePort(deps.SearchRunner) {
		return fmt.Errorf("youtube: SearchRunner is required but not wired (or typed-nil)")
	}
	if deps.AssetRepo == nil || portutil.IsNilPort(deps.AssetRepo) {
		return fmt.Errorf("youtube: AssetRepo is required but not wired (or typed-nil)")
	}
	if isUnavailablePort(deps.VideoPipeline) {
		return fmt.Errorf("youtube: VideoPipeline is required but not wired (or typed-nil)")
	}
	if deps.MediaProcessor == nil || portutil.IsNilPort(deps.MediaProcessor) {
		return fmt.Errorf("youtube: MediaProcessor is required but not wired (or typed-nil)")
	}
	return nil
}
