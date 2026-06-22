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
	"sync"

	"go.uber.org/zap"

	jobtools "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobservice "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/portutil"
)

// VideoCutRequest contains all parameters for downloading and cutting a video segment.
// Application-layer DTO that replaces videomuscles.YouTubeCutRequest.
type VideoCutRequest struct {
	URL               string
	VideoID           string
	Start             float64
	Duration          float64
	OutputName        string
	ForceKeyframes    bool
	KeepAudio         bool
	Normalize         bool
	Strategy          string
	OutputDir         string
	PreDownloadedPath string
}

// VideoCutResult wraps the output of a video cut operation with the local file path
// and the full video metadata captured from yt-dlp.
type VideoCutResult struct {
	LocalPath string
	Metadata  *DownloaderMetadata
}

// VideoPipeline is the port for downloading + cutting YouTube video segments.
type VideoPipeline interface {
	DownloadAndCutYouTubeVideo(ctx context.Context, req VideoCutRequest) (*VideoCutResult, error)
}

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
	SearchRunner    SearchRunnerPort
	SubtitleFetcher SubtitleFetcherPort
	Whisper         WhisperTranscriberPort
	ClipFiles       ClipFilesPort
	MetaFetcher     VideoMetadataFetcherPort
	DriveFolderMgr  DriveFolderManagerPort
	HashSvc         HashServicePort
	TempFiles       TempFileManagerPort

	// PR1.5 — port-backed store/cache/index collaborators.
	Clips        ClipStorePort
	Monitors     MonitorsStorePort
	CacheStore   YouTubeCacheStorePort
	Indexer      ClipIndexerPort
	FolderMemory FolderMemoryPort
	Ollama       OllamaClientPort
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

	// Port-backed dependencies (no setters).
	searchRunner    SearchRunnerPort
	subtitleFetcher SubtitleFetcherPort
	whisper         WhisperTranscriberPort
	clipFiles       ClipFilesPort
	metaFetcher     VideoMetadataFetcherPort
	driveFolderMgr  DriveFolderManagerPort
	hashSvc         HashServicePort
	tempFiles       TempFileManagerPort

	clips       ClipStorePort
	monitors    MonitorsStorePort
	cacheStore  YouTubeCacheStorePort
	indexer     ClipIndexerPort
	folderMemory FolderMemoryPort
	ollama      OllamaClientPort

	searchL1 sync.Map
	metadataL1 sync.Map

	// Capacity-bound semaphores configured via ConcurrencyConfig.
	videoExtractSem chan struct{}
	ollamaSem       chan struct{}
}

// NewService is the sole canonical constructor. Pass every dependency a
// component of the YouTube pipeline touches; missing nothing means no
// surrogate setters are needed. Composition root (internal/app/composition.go)
// is the only intended caller.
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
	return &Service{
		cfg:               deps.Cfg,
		log:               deps.Log,
		mediaProcessor:    deps.MediaProcessor,
		videoPipeline:     deps.VideoPipeline,
		lifecycleService:  deps.LifecycleService,
		assetDestResolver: deps.AssetDestResolver,
		assetRepo:         deps.AssetRepo,
		assetProcessing:  deps.AssetProcessing,
		assetVersions:    deps.AssetVersions,

		searchRunner:    deps.SearchRunner,
		subtitleFetcher: deps.SubtitleFetcher,
		whisper:         deps.Whisper,
		clipFiles:       deps.ClipFiles,
		metaFetcher:     deps.MetaFetcher,
		driveFolderMgr:  deps.DriveFolderMgr,
		hashSvc:         deps.HashSvc,
		tempFiles:       deps.TempFiles,

		clips:       deps.Clips,
		monitors:    deps.Monitors,
		cacheStore:  deps.CacheStore,
		indexer:     deps.Indexer,
		folderMemory: deps.FolderMemory,
		ollama:      deps.Ollama,

		videoExtractSem: make(chan struct{}, maxVideo),
		ollamaSem:       make(chan struct{}, maxOllama),
	}
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

// md5String returns the MD5 hex digest of s via the HashServicePort.
func (s *Service) md5String(data string) string {
	if s.hashSvc != nil {
		return s.hashSvc.MD5String(data)
	}
	return fallbackMD5String(data)
}

// md5File returns the MD5 hex digest of the file at path via the
// HashServicePort. Best-effort: a port error falls back to the local
// helper with a debug log so operators can see when the configured port
// silently misbehaves (e.g., misconfigured remote hash service).
func (s *Service) md5File(path string) string {
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
