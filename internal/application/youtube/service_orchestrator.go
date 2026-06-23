// Package youtube holds the application-layer orchestrator for the YouTube
// clip-extraction pipeline. Persistence, IO, and external-process execution
// are delegated to ports declared in this same package (ports.go) and
// implemented under internal/infrastructure/youtube.
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
//   - PR2 cascade followup: legacy optional fields `assetProcessing` +
//     `assetVersions` were re-added on ServiceDeps + Service (NOT removed)
//     because the application still calls them as nil-safe parallel
//     writers during segment cutting (assetProcessing for cron-status
//     trails + assetVersions for content-hash version writes). Composition
//     may pass nil for either; callers nil-check each before use.
package youtube

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	jobtools "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	ytcache "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/cache"
	ytextraction "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/extraction"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/metadata"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/types"
	ytsearch "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/search"
	ytsegments "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/segments"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/portutil"
)

// PR5 Phase 3 (June 2026): VideoCutRequest, VideoCutResult, and VideoPipeline
// moved to youtube/ports/ so the extraction capability service can import
// them without an import cycle. These aliases preserve backward compatibility.
type VideoCutRequest = youtubeports.VideoCutRequest
type VideoCutResult = youtubeports.VideoCutResult
type VideoPipeline = youtubeports.VideoPipelinePort

// ServiceDeps is the FULL set of dependencies the YouTube orchestrator
// requires. Wiring happens exactly once via NewService(ServiceDeps);
// setters are intentionally absent.
type ServiceDeps struct {
	// Core collaborators (always required).
	Cfg               *config.Config
	Log               *zap.Logger
	MediaProcessor    asset.Processor
	VideoPipeline     VideoPipeline
	LifecycleService  *lifecycle.Service
	AssetDestResolver asset.Resolver

	// PR1.6 — canonical persistence writer (asset.Repository).
	// Required: dispatchOrIndex refuses to persist without it.
	AssetRepo asset.Repository

	// PR2 cascade followup: re-added legacy optional repositories that
	// `segment.go::135-191` still references for upsert + version-write
	// operations on freshly-cut clips. Composition passes nil for both
	// when the canonical `AssetRepo` (PR1.6) is the only writer in scope;
	// `segment.go` callers nil-check each before use.
	AssetProcessing asset.ProcessingRepository
	AssetVersions   asset.VersionRepository

	// Port dependencies.
	SearchRunner    youtubeports.SearchRunnerPort
	SubtitleFetcher youtubeports.SubtitleFetcherPort
	Whisper         youtubeports.WhisperTranscriberPort
	ClipFiles       youtubeports.ClipFilesPort
	MetaFetcher     youtubeports.VideoMetadataFetcherPort
	DriveFolderMgr  youtubeports.DriveFolderManagerPort
	HashSvc         youtubeports.HashServicePort
	TempFiles       youtubeports.TempFileManagerPort

	// PR1.5 — port-backed store/cache/index collaborators.
	Clips        youtubeports.ClipStorePort
	Monitors     youtubeports.MonitorsStorePort
	CacheStore   youtubeports.YouTubeCacheStorePort
	Indexer      youtubeports.ClipIndexerPort
	FolderMemory youtubeports.FolderMemoryPort
	Ollama       youtubeports.OllamaClientPort
}

// Service is the YouTube orchestrator. Construct it once via NewService
// (no setters). Methods received on nil-receiver port fields surface an
// explicit error rather than silently no-op'ing.
type Service struct {
	cfg               *config.Config
	log               *zap.Logger
	mediaProcessor    asset.Processor
	videoPipeline     VideoPipeline
	lifecycleService  *lifecycle.Service
	assetDestResolver asset.Resolver
	assetRepo         asset.Repository

	// PR2 cascade followup — optional repositories still referenced from
	// processSegment for upsert + version-write on freshly-cut clips.
	// Composition may pass nil for both; callers nil-check each before use.
	assetProcessing asset.ProcessingRepository
	assetVersions   asset.VersionRepository

	// Capability services (PR5 — June 2026).
	cache      *ytcache.Service
	search     *ytsearch.Service
	metadata   *ytmetadata.Service
	segSvc     *ytsegments.Service
	extraction *ytextraction.Service

	// Port-backed dependencies (no setters).
	searchRunner    youtubeports.SearchRunnerPort
	subtitleFetcher youtubeports.SubtitleFetcherPort
	whisper         youtubeports.WhisperTranscriberPort
	clipFiles       youtubeports.ClipFilesPort
	metaFetcher     youtubeports.VideoMetadataFetcherPort
	driveFolderMgr  youtubeports.DriveFolderManagerPort
	hashSvc         youtubeports.HashServicePort
	tempFiles       youtubeports.TempFileManagerPort

	clips        youtubeports.ClipStorePort
	monitors     youtubeports.MonitorsStorePort
	cacheStore   youtubeports.YouTubeCacheStorePort
	indexer      youtubeports.ClipIndexerPort
	folderMemory youtubeports.FolderMemoryPort
	ollama       youtubeports.OllamaClientPort

	// Capacity-bound semaphores configured via ConcurrencyConfig.
	videoExtractSem chan struct{}
	ollamaSem       chan struct{}
}

// NewService is the sole canonical constructor. Pass every dependency a
// component of the YouTube pipeline touches; missing nothing means no
// surrogate setters are needed. Composition root (internal/app/composition.go)
// is the only intended caller.
//
// PR5 (June 2026): the L2 cache is extracted to youtube/cache/. If deps.Clips
// provides a *sql.DB, NewService wires the cache service automatically.
func NewService(deps ServiceDeps) *Service {
	maxVideo := 1
	maxOllama := 1
	if deps.Cfg != nil {
		if v := deps.Cfg.Concurrency.MaxConcurrentVideoExtracts; v > 0 {
			maxVideo = v
		}
		if v := deps.Cfg.Concurrency.MaxConcurrentOllamaCalls; v > 0 {
			maxOllama = v
		}
	}
	svc := &Service{
		cfg:               deps.Cfg,
		log:               deps.Log,
		mediaProcessor:    deps.MediaProcessor,
		videoPipeline:     deps.VideoPipeline,
		lifecycleService:  deps.LifecycleService,
		assetDestResolver: deps.AssetDestResolver,
		assetRepo:         deps.AssetRepo,
		assetProcessing:   deps.AssetProcessing,
		assetVersions:     deps.AssetVersions,

		searchRunner:    deps.SearchRunner,
		subtitleFetcher: deps.SubtitleFetcher,
		whisper:         deps.Whisper,
		clipFiles:       deps.ClipFiles,
		metaFetcher:     deps.MetaFetcher,
		driveFolderMgr:  deps.DriveFolderMgr,
		hashSvc:         deps.HashSvc,
		tempFiles:       deps.TempFiles,

		clips:        deps.Clips,
		monitors:     deps.Monitors,
		cacheStore:   deps.CacheStore,
		indexer:      deps.Indexer,
		folderMemory: deps.FolderMemory,
		ollama:       deps.Ollama,

		videoExtractSem: make(chan struct{}, maxVideo),
		ollamaSem:       make(chan struct{}, maxOllama),
	}

	// Wire L2 cache service when Clips provides a *sql.DB (PR5 Phase 1).
	if clipsPort := deps.Clips; clipsPort != nil {
		if db := clipsPort.DB(); db != nil {
			svc.cache = ytcache.NewService(ytcache.Deps{DB: db, Log: deps.Log})
		}
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
		svc.search = ytsearch.NewService(ytsearch.SearchDeps{
			SearchRunner: deps.SearchRunner,
			Cache:        svc.cache,
			Log:          deps.Log,
		})
	}

	// Wire metadata service (PR5 Phase 1).
	if deps.Clips != nil && deps.Log != nil {
		svc.metadata = ytmetadata.NewService(ytmetadata.MetadataDeps{
			Clips:       deps.Clips,
			MetaFetcher: deps.MetaFetcher,
			Ollama:      deps.Ollama,
			AssetRepo:   deps.AssetRepo,
			Cfg:         deps.Cfg,
			Log:         deps.Log,
		})
	}

	// Wire segments service (PR5 Phase 4 — zero-dependency).
	svc.segSvc = ytsegments.NewService()

	// Wire extraction service (PR5 Phase 3 — thin wrapper pattern).
	// The root Service implements ExtractionCallbacks so callbacks are
	// simply method calls on the same Service instance.
	svc.extraction = ytextraction.NewService(ytextraction.ExtractionDeps{
		Cfg:               deps.Cfg,
		Log:               deps.Log,
		VideoPipeline:     deps.VideoPipeline,
		Clips:             deps.Clips,
		Monitors:          deps.Monitors,
		AssetDestResolver: deps.AssetDestResolver,
		FolderMemory:      deps.FolderMemory,
		SegmentsSvc:       svc.segSvc,
	}, svc)

	return svc
}

// ── Persistence — single canonical writer (PR1.6) ──────────────────────

// dispatchOrIndex writes a freshly-cut clip to the canonical asset store.
//
// The previous triple fallback (assetRepo → disp.EnqueueAndIndex →
// clipsRepo.Upsert) has been removed in PR1.6. AssetRepo is the SOLE
// writer and emits the asset.upserted outbox event atomically (PR12b
// semantics). If AssetRepo is not wired the call returns an explicit
// error so callers see the missing dependency rather than experiencing
// a silent no-op.
func (s *Service) dispatchOrIndex(ctx context.Context, clip *asset.Asset, _ string) error {
	if clip == nil {
		return fmt.Errorf("youtube.dispatchOrIndex: nil clip")
	}
	// typed-nil guard: portutil.IsNilPort catches (*Concrete)(nil) casts
	// to interface that pass == nil. Composition audit (June 2026) confirmed
	// all adapter constructors return bare nil, so this is defensive.
	if s.assetRepo == nil || portutil.IsNilPort(s.assetRepo) {
		return fmt.Errorf("youtube: canonical assetRepo not wired — composition root must include AssetRepo in ServiceDeps")
	}
	return s.assetRepo.Upsert(ctx, clip)
}

// ── Job wiring (composition root calls this once) ─────────────────────

func (s *Service) RegisterHandler(jobsSvc *jobtools.Service) {
	if jobsSvc != nil {
		jobsSvc.RegisterHandler(jobservice.TypeYouTubeClipExtract, s.HandleJob)
		s.log.Info("registered youtube_clip.extract job handler", zap.String("type", jobservice.TypeYouTubeClipExtract))

		jobsSvc.RegisterHandler(jobservice.TypeYouTubeRebuildST, s.HandleRebuildSearchTextJob)
		s.log.Info("registered youtube.rebuild_search_text job handler", zap.String("type", jobservice.TypeYouTubeRebuildST))
	}
}

// ── Public helpers ─────────────────────────────────────────────────────

// GetOrCreateChannelFolder resolves the Drive folder for a channel via the
// DriveFolderManagerPort. The previous dummy GetOrCreateChannelFolder
// fallback to a raw driveclient (concrete Drive SDK) has been removed.
func (s *Service) GetOrCreateChannelFolder(ctx context.Context, channelName, parentFolderID string) (string, error) {
	if s.driveFolderMgr == nil {
		return parentFolderID, fmt.Errorf("youtube: drive folder manager not wired — composition root must include DriveFolderMgr in ServiceDeps")
	}
	folderID, err := s.driveFolderMgr.GetOrCreateFolder(ctx, channelName, parentFolderID)
	if err != nil {
		return parentFolderID, fmt.Errorf("failed to get/create channel folder %q: %w", channelName, err)
	}
	s.log.Info("channel folder resolved",
		zap.String("channel", channelName),
		zap.String("folder_id", folderID),
		zap.String("parent", parentFolderID))
	return folderID, nil
}

// DownloadAndCut delegates to the VideoPipeline port (no longer calls
// concrete videomuscles.Pipeline from application via this method).
func (s *Service) DownloadAndCut(ctx context.Context, req VideoCutRequest) (*VideoCutResult, error) {
	if s.videoPipeline == nil {
		return nil, fmt.Errorf("youtube: video pipeline not wired")
	}
	return s.videoPipeline.DownloadAndCutYouTubeVideo(ctx, req)
}

// Config returns the resolved runtime configuration (for callers that need
// to read it without taking a direct dependency on the config loader).
func (s *Service) Config() *config.Config {
	return s.cfg
}

// md5File returns the MD5 hex digest of the file at path via the
// HashServicePort. Best-effort: a port error falls back to the local
// helper with a debug log so operators can see when the configured port
// silently misbehaves (e.g., misconfigured remote hash service).
//
// PR5 Phase 3: exported for ExtractionCallbacks compatibility.
func (s *Service) MD5File(path string) string {
	if s.hashSvc != nil {
		if h, err := s.hashSvc.MD5File(path); err == nil {
			return h
		} else {
			s.log.Debug("hashSvc.MD5File failed, falling back to local helper",
				zap.String("path", path),
				zap.Error(err))
		}
	}
	return fallbackMD5File(path)
}

// md5File is the legacy private name kept for internal callers.
func (s *Service) md5File(path string) string { return s.MD5File(path) }

// MD5String returns the MD5 hex digest of s via the HashServicePort.
// PR5 Phase 3: exported for ExtractionCallbacks compatibility.
func (s *Service) MD5String(data string) string {
	if s.hashSvc != nil {
		return s.hashSvc.MD5String(data)
	}
	return fallbackMD5String(data)
}

// md5String is the legacy private name kept for internal callers.
func (s *Service) md5String(data string) string { return s.MD5String(data) }

// ── PR5 Phase 3: ExtractionCallbacks implementation ─────────────────────
// These methods satisfy the extraction.ExtractionCallbacks interface so
// the extraction capability service can delegate external operations back
// to the root orchestrator. Each method delegates to the appropriate
// capability service or port.

func (s *Service) EnrichClip(ctx context.Context, clipID string, ym *youtubeports.DownloaderMetadata, force bool) {
	if s.metadata == nil {
		return
	}
	s.metadata.EnrichClip(ctx, clipID, ym, force)
}

func (s *Service) ClassifyCategory(ctx context.Context, title string) string {
	return s.classifyCategory(ctx, title)
}

func (s *Service) CheckExistingClip(ctx context.Context, req *youtubetypes.ExtractRequest, clipID string, item *youtubetypes.ExtractItem, outDir string) bool {
	return s.checkExistingClip(ctx, req, clipID, item, outDir)
}

func (s *Service) ProcessLifecycle(ctx context.Context, metadata *lifecycle.FinalizeInput, localPath, fileHash string, item *youtubetypes.ExtractItem) {
	ytextraction.ProcessLifecycle(ctx, s.lifecycleService, localPath, fileHash, item, metadata)
}

func (s *Service) TriggerAutoIndexing(ctx context.Context, clipID string) {
	s.triggerAutoIndexing(ctx, clipID)
}

func (s *Service) IndexClip(ctx context.Context, clipID string) error {
	if s.indexer == nil {
		return nil
	}
	return s.indexer.IndexClip(ctx, clipID)
}

func (s *Service) EnrichSkippedClip(ctx context.Context, clipID, videoURL, videoID string) {
	s.enrichSkippedClip(ctx, clipID, videoURL, videoID)
}

func (s *Service) SliceSubtitles(ctx context.Context, videoID string, startSec, endSec int, outputPath string) error {
	return s.sliceSubtitles(ctx, videoID, startSec, endSec, outputPath)
}

func (s *Service) TranscribeAudio(ctx context.Context, localPath string) (string, error) {
	if s.whisper == nil {
		return "", nil
	}
	return s.whisper.TranscribeAudio(ctx, localPath)
}

func (s *Service) AssetProcessingStart(ctx context.Context, clipID, stage string) error {
	if s.assetProcessing == nil {
		return nil
	}
	return s.assetProcessing.Start(ctx, clipID, stage)
}

func (s *Service) AssetProcessingComplete(ctx context.Context, clipID, stage string) error {
	if s.assetProcessing == nil {
		return nil
	}
	return s.assetProcessing.Complete(ctx, clipID, stage)
}

func (s *Service) AssetProcessingFail(ctx context.Context, clipID, stage, errorMsg string) error {
	if s.assetProcessing == nil {
		return nil
	}
	return s.assetProcessing.Fail(ctx, clipID, stage, errorMsg)
}

func (s *Service) AssetVersionsAppend(ctx context.Context, v *asset.Version) error {
	if s.assetVersions == nil {
		return nil
	}
	return s.assetVersions.Append(ctx, v)
}

func (s *Service) DriveUploadFileIfChanged(ctx context.Context, localPath, folderID, filename string) (*youtubeports.UploadResultDTO, bool, error) {
	if s.driveFolderMgr == nil {
		return &youtubeports.UploadResultDTO{}, false, fmt.Errorf("youtube: drive folder manager not wired")
	}
	return s.driveFolderMgr.UploadFileIfChanged(ctx, localPath, folderID, filename)
}

func (s *Service) DriveGetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	if s.driveFolderMgr == nil {
		return "", fmt.Errorf("youtube: drive folder manager not wired")
	}
	return s.driveFolderMgr.GetOrCreateFolder(ctx, name, parentID)
}

func (s *Service) OllamaSimpleGenerate(ctx context.Context, model, prompt string, timeoutSec int, opts map[string]any) (string, error) {
	if s.ollama == nil {
		return "", fmt.Errorf("youtube: ollama port not wired")
	}
	return s.ollama.SimpleGenerate(ctx, model, prompt, time.Duration(timeoutSec)*time.Second, opts)
}

func (s *Service) AcquireVideoExtractSem(ctx context.Context) (release func()) {
	select {
	case s.videoExtractSem <- struct{}{}:
		return func() { <-s.videoExtractSem }
	case <-ctx.Done():
		return nil
	}
}

func (s *Service) AcquireOllamaSem(ctx context.Context) (release func()) {
	select {
	case s.ollamaSem <- struct{}{}:
		return func() { <-s.ollamaSem }
	case <-ctx.Done():
		return nil
	}
}
