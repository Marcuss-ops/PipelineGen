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
//   - Asset-processing/version callbacks were removed from the extraction
//     flow; the canonical asset writer is AssetRepo.
package youtube

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	jobtools "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	ytextraction "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/extraction"
	ytjobs "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/jobs"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/metadata"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	ytsearch "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/search"
	ytsegments "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/segments"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/types"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/portutil"
)

// ServiceDeps is the FULL set of dependencies the YouTube orchestrator
// requires. Wiring happens exactly once via NewService(ServiceDeps);
// setters are intentionally absent.
type ServiceDeps struct {
	// Core collaborators (always required).
	Cfg               *config.Config
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

	// PR1.5 — port-backed store/cache/index collaborators.
	Clips        youtubeports.ClipStorePort
	Cache        youtubeports.CachePort
	Monitors     youtubeports.MonitorsStorePort
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
	videoPipeline     youtubeports.VideoPipelinePort
	lifecycleService  *lifecycle.Service
	assetDestResolver asset.Resolver
	assetRepo         asset.Repository

	// Capability services (PR5 — June 2026).
	cache      youtubeports.CachePort
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

	clips        youtubeports.ClipStorePort
	monitors     youtubeports.MonitorsStorePort
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
// PR5 (June 2026): the L2 cache is injected through CachePort; composition
// owns the SQLite-backed infrastructure adapter.
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
		Cache:             deps.Cache,
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

		// The receiver method s.HandleRebuildSearchTextJob (defined below)
		// wraps the canonical ytjobs.HandleRebuildSearchTextJob and lazily
		// reconstructs RebuildDeps from this Service's runtime state at
		// invocation time (Clips.DB → DB, log, Indexer port, metadata
		// EnrichClip forwarder). Registering the receiver directly keeps a
		// single registration site for the rebuild_search_text job type.
		if s.clips != nil {
			jobsSvc.RegisterHandler(jobservice.TypeYouTubeRebuildST, s.HandleRebuildSearchTextJob)
			s.log.Info("registered youtube.rebuild_search_text job handler", zap.String("type", jobservice.TypeYouTubeRebuildST))
		}
	}
}

// HandleRebuildSearchTextJob wraps the free function form of ytjobs.HandleRebuildSearchTextJob
// (which expects a RebuildDeps dep struct as its first arg) into the method-form signature
// expected by jobtools.Service.RegisterHandler (ctx, j, tools) -> (map, error). The dep
// surface (clip lister, Log, Indexer, Enricher closure) is reconstructed lazily from
// the orchestrator's runtime state at job-invocation time so the composition root does not
// have to thread a fifth dep through ServiceDeps for this job type specifically.
//
// TODO(wave14-followup): interface alignment between orchestrator runtime fields and the
// ytjobs.RebuildDeps struct (specifically ClipIndexerPort vs jobs.ClipIndexer, plus the
// `meta any` closure-cast to `*youtubeports.DownloaderMetadata`) needs verification once a
// real rebuild_search_text job is exercised end-to-end.
func (s *Service) HandleRebuildSearchTextJob(ctx context.Context, j *jobservice.Job, tools *jobtools.JobTools) (map[string]any, error) {
	deps := ytjobs.RebuildDeps{
		Log:     s.log,
		Indexer: s.indexer,
		Clips:   s.clips,
		Enricher: func(ctx context.Context, clipID string, meta any, force bool) {
			if s.metadata == nil {
				return
			}
			var m *youtubeports.DownloaderMetadata
			if meta != nil {
				m, _ = meta.(*youtubeports.DownloaderMetadata)
			}
			s.metadata.EnrichClip(ctx, clipID, m, force)
		},
	}
	return ytjobs.HandleRebuildSearchTextJob(deps, ctx, j, tools)
}

// ── Public helpers ─────────────────────────────────────────────────────

// GetOrCreateChannelFolder resolves the Drive folder for a channel via the
// DriveFolderManagerPort. The previous dummy GetOrCreateChannelFolder
// fallback to a raw driveclient (concrete Drive SDK) has been removed.
func (s *Service) GetOrCreateChannelFolder(ctx context.Context, channelName, parentFolderID string) (string, error) {
	if isUnavailablePort(s.driveFolderMgr) {
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
func (s *Service) DownloadAndCut(ctx context.Context, req youtubeports.VideoCutRequest) (*youtubeports.VideoCutResult, error) {
	if isUnavailablePort(s.videoPipeline) {
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
	if !isUnavailablePort(s.hashSvc) {
		h, err := s.hashSvc.MD5File(path)
		if err == nil {
			return h
		}
		s.log.Debug("hashSvc.MD5File failed, falling back to local helper",
			zap.String("path", path),
			zap.Error(err))
	}
	return fallbackMD5File(path)
}

// md5File is the legacy private name kept for internal callers.
func (s *Service) md5File(path string) string { return s.MD5File(path) }

// ── Typed-nil guard helpers ──────────────────────────────────────────

// isUnavailablePort returns true when port is nil (either bare nil or a
// typed-nil interface holding a nil concrete pointer). Use this for
// required ports that MUST be wired at composition time.
func isUnavailablePort(port any) bool {
	return port == nil || portutil.IsNilPort(port)
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

// MD5String returns the MD5 hex digest of s via the HashServicePort.
// PR5 Phase 3: exported for ExtractionCallbacks compatibility.
func (s *Service) MD5String(data string) string {
	if !isUnavailablePort(s.hashSvc) {
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

// IndexClip is a best-effort callback (ExtractionCallbacks) that delegates
// to the ClipIndexerPort. When no indexer is wired the call returns nil
// (no-op) — indexing is a value-add, not a correctness gate. Callers that
// require indexing must check IsEnabled() before calling.
func (s *Service) IndexClip(ctx context.Context, clipID string) error {
	if isUnavailablePort(s.indexer) {
		return nil
	}
	return s.indexer.IndexClip(ctx, clipID)
}

func (s *Service) EnrichSkippedClip(ctx context.Context, clipID, videoURL, videoID string) {
	s.enrichSkippedClip(ctx, clipID, videoURL, videoID)
}

// SliceSubtitles is the public ExtractionCallbacks entry point that
// delegates to the SubtitleFetcherPort.
//
// CPR-CC-6 Phase 2 followup (June 2026): the previous body used a
// `s.sliceSubtitles` private shell that was removed in commit aee5c867,
// leaving a broken self-reference (Go method-set would not recurse, the
// call site simply didn't compile). The shell has been inlined here.
// The isUnavailablePort guard mirrors sibling forwarding methods to
// surface an explicit error rather than nil-deref-panic when the port
// is not wired at composition time.
func (s *Service) SliceSubtitles(ctx context.Context, videoID string, startSec, endSec int, outputPath string) error {
	if isUnavailablePort(s.subtitleFetcher) {
		return fmt.Errorf("youtube: subtitle fetcher port not wired")
	}
	return s.subtitleFetcher.SliceSubtitles(ctx, videoID, startSec, endSec, outputPath)
}

// TranscribeAudio is a best-effort callback (ExtractionCallbacks) that
// delegates to the WhisperTranscriberPort. When Whisper is not wired the
// call returns ("", nil) — an empty transcript signals "no transcription
// available" rather than a hard failure. Callers downstream treat an empty
// string as a missing transcript.
func (s *Service) TranscribeAudio(ctx context.Context, localPath string) (string, error) {
	if isUnavailablePort(s.whisper) {
		return "", nil
	}
	return s.whisper.TranscribeAudio(ctx, localPath)
}

func (s *Service) DriveUploadFileIfChanged(ctx context.Context, localPath, folderID, filename string) (*youtubeports.UploadResultDTO, bool, error) {
	if isUnavailablePort(s.driveFolderMgr) {
		return &youtubeports.UploadResultDTO{}, false, fmt.Errorf("youtube: drive folder manager not wired")
	}
	return s.driveFolderMgr.UploadFileIfChanged(ctx, localPath, folderID, filename)
}

func (s *Service) DriveGetOrCreateFolder(ctx context.Context, name, parentID string) (string, error) {
	if isUnavailablePort(s.driveFolderMgr) {
		return "", fmt.Errorf("youtube: drive folder manager not wired")
	}
	return s.driveFolderMgr.GetOrCreateFolder(ctx, name, parentID)
}

func (s *Service) OllamaSimpleGenerate(ctx context.Context, model, prompt string, timeoutSec int, opts map[string]any) (string, error) {
	if isUnavailablePort(s.ollama) {
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

// ── CPR-CC-6 Phase 2 (June 2026): topic search absorbed into search capability ──
//
// Before Phase 2 the canonical "topic search" entry point — TopicSearchResult
// / TopicSearchResponse types plus the score-and-rank routine — lived as a
// receiver on *Service. Phase 2 moves the implementation into the search
// capability service (ytsearch.Service.TopicSearch) so that the end-to-end
// "find me videos for this topic" request is co-located with the L1/L2
// caches that already back SearchLive + GetVideoInfo. The orchestrator
// remains the external entry point: SearchByTopicWithFilter forwards to
// the capability service and returns an explicit error if not wired.
type (
	TopicSearchResult   = ytsearch.TopicSearchResult
	TopicSearchResponse = ytsearch.TopicSearchResponse
)

// SearchByTopicWithFilter is the single canonical YouTube search entry
// point at the application-layer boundary. It ranks YouTube search
// results with an optional publishedAfter date filter.
//
// query: non-empty trimmed search string.
// limit: clamps to [1, 50]; defaults to 10 when <= 0.
// sortMode: forwarded verbatim to SearchLive; "" means "no preference".
// publishedAfter: RFC3339 date string (e.g. "2025-01-01T00:00:00Z") or
// "" for no filter. When set, only videos uploaded AFTER this date
// remain in the response.
//
// CPR-CC-6 Phase 2 (June 2026): this method is a 1-line forwarder to
// the search capability. The implementation + scorers + types live in
// internal/application/youtube/search/topic.go. Returns an explicit
// error when the search capability is not wired (composition root must
// include SearchRunner + Log in ServiceDeps for NewService to wire it).
func (s *Service) SearchByTopicWithFilter(ctx context.Context, query string, limit int, sortMode, publishedAfter string) (*ytsearch.TopicSearchResponse, error) {
	if s.search == nil {
		return nil, fmt.Errorf("youtube: search capability not wired (composition root must include SearchRunner + Log in ServiceDeps for NewService to wire the search service)")
	}
	return s.search.TopicSearch(ctx, query, limit, sortMode, publishedAfter)
}

// ── AGENT-2 build-fix (June 2026): GetVideoInfo forwarding ──────────────
//
// GetVideoInfo fetches full YouTube metadata for the given URL by
// forwarding to the canonical VideoMetadataFetcherPort (which exposes
// GetVideoMetadata(ctx, videoURL) with the same return type). The
// isUnavailablePort guard mirrors sibling forwarding methods
// (DriveUploadFileIfChanged, OllamaSimpleGenerate) to surface a typed
// error instead of nil-deref-panic when metaFetcher is not wired at
// composition time.
//
// Fulfils the extraction.ExtractionCallbacks.GetVideoInfo interface
// requirement on *Service. Merged into CPR-CC-6 Phase 2 (June 2026)
// alongside the topic-search forwarder above; both sides appended at
// end-of-file in upstream + local histories and were combined manually
// per AGENTS.md Rebase-Conflict Lesson rule 3.
func (s *Service) GetVideoInfo(ctx context.Context, url string) (*youtubeports.DownloaderMetadata, error) {
	if isUnavailablePort(s.metaFetcher) {
		return nil, fmt.Errorf("youtube: metaFetcher port not wired")
	}
	return s.metaFetcher.GetVideoMetadata(ctx, url)
}

// Extract forwards to the extraction capability service. The orchestrator remains
// the external entry point for compositional wiring; the extraction.Service owns
// the segment-cutting state machine + Drive upload orchestration.
//
// Callers (e.g. monitor/process_video.go::265) invoke m.youtubeSvc.Extract(...)
// expecting this facade. The actual Extract method was relocated to
// extraction.Service during the 03e6b8d cleanup wave but the orchestrator-level
// facade was not updated, leaving the call site orphaned. This facade restores
// the dependency surface without re-exposing extraction-side internals to callers
// outside the application package's capability boundaries.
func (s *Service) Extract(ctx context.Context, req *youtubetypes.ExtractRequest) (*youtubetypes.ExtractResponse, error) {
	if s.extraction == nil {
		return nil, fmt.Errorf("youtube: extraction capability not wired (composition root must include Cfg, Log, VideoPipeline, Clips, Monitors, AssetDestResolver, FolderMemory, and SegmentsSvc in ServiceDeps for NewService to wire the extraction service)")
	}
	return s.extraction.Extract(ctx, req)
}

// PrewarmHotVideoMetadataCache forwards to the search capability service.
// Callers (currently internal/app/lifecycle.go) call ytSvc.PrewarmHotVideoMetadataCache(ctx)
// expecting this method on the orchestrator. The actual implementation lives on
// *ytsearch.Service (PR5 Phase 2 capability extraction, set inside NewService when
// deps.SearchRunner is wired); this facade restores the dependency surface from the
// app-layer composition root's perspective.
//
// SearchRunner + Log on ServiceDeps are required; both checked in NewService before
// wiring s.search.
func (s *Service) PrewarmHotVideoMetadataCache(ctx context.Context) error {
	if s.search == nil {
		return fmt.Errorf("youtube: search capability not wired (composition root must include SearchRunner + Log in ServiceDeps for NewService to wire the search service)")
	}
	return s.search.PrewarmHotVideoMetadataCache(ctx)
}
