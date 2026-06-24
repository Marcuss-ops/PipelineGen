// Package extraction is the YouTube clip extraction capability service.
//
// It orchestrates the full extraction pipeline: segment discovery, video
// download/cut, lifecycle processing, manifest management, Drive upload,
// and intelligence enrichment. External operations (metadata enrichment,
// indexing, subtitles, classification, etc.) are delegated through the
// ExtractionCallbacks interface so the service stays focused on orchestration.
//
// PR5 Phase 3 (June 2026): extracted from the root youtube package.
// ExtractionDeps is capped at 8 fields per PR5 ≤8 rule.
package extraction

import (
	"context"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
	segments "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/segments"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/types"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
)

// ── Dependencies (≤8 fields per PR5 rule) ────────────────────────────────

// ExtractionDeps holds the 8 direct dependencies the extraction pipeline
// requires. External operations not listed here (metadata enrichment,
// indexing, classification, subtitles, Whisper, hash, Drive upload,
// Ollama, asset processing, clip cache) are accessed through the
// ExtractionCallbacks interface.
type ExtractionDeps struct {
	Cfg               *config.Config
	Log               *zap.Logger
	VideoPipeline     youtubeports.VideoPipelinePort
	Clips             youtubeports.ClipStorePort
	Cache             youtubeports.CachePort
	Monitors          youtubeports.MonitorsStorePort
	AssetDestResolver asset.Resolver
	FolderMemory      youtubeports.FolderMemoryPort
	SegmentsSvc       *segments.Service
}

// ── Callbacks interface ──────────────────────────────────────────────────

// ExtractionCallbacks is implemented by the root youtube.Service. It
// delegates each callback to the appropriate capability service or port,
// keeping the extraction service free of direct dependencies on metadata,
// search, indexing, subtitles, Whisper, hash, Drive upload, Ollama,
// asset processing, and clip cache services.
//
// All types used here come from shared packages (types/, ports/, asset/,
// lifecycle/) — never from the root youtube/ or extraction/ package — to
// avoid import cycles and type incompatibilities.
type ExtractionCallbacks interface {
	// Metadata enrichment (→ metadata.Service)
	EnrichClip(ctx context.Context, clipID string, ym *youtubeports.DownloaderMetadata, force bool)

	// Search/info (→ search.Service)
	GetVideoInfo(ctx context.Context, url string) (*youtubeports.DownloaderMetadata, error)

	// Classification (→ classifyCategory)
	ClassifyCategory(ctx context.Context, title string) string

	// Clip cache (→ checkExistingClip)
	CheckExistingClip(ctx context.Context, req *youtubetypes.ExtractRequest, clipID string, item *youtubetypes.ExtractItem, outDir string) bool

	// Lifecycle (→ processLifecycle)
	ProcessLifecycle(ctx context.Context, metadata *lifecycle.FinalizeInput, localPath, fileHash string, item *youtubetypes.ExtractItem)

	// Auto-indexing (→ indexing.go)
	TriggerAutoIndexing(ctx context.Context, clipID string)
	IndexClip(ctx context.Context, clipID string) error
	EnrichSkippedClip(ctx context.Context, clipID, videoURL, videoID string)

	// Subtitles (→ subtitleFetcher)
	SliceSubtitles(ctx context.Context, videoID string, startSec, endSec int, outputPath string) error

	// Whisper (→ whisper port)
	TranscribeAudio(ctx context.Context, localPath string) (string, error)

	// Hash (→ hashSvc)
	MD5File(path string) string
	MD5String(data string) string

	// Asset processing (optional — nil-safe)
	AssetProcessingStart(ctx context.Context, clipID, stage string) error
	AssetProcessingComplete(ctx context.Context, clipID, stage string) error
	AssetProcessingFail(ctx context.Context, clipID, stage, errorMsg string) error

	// Version tracking (optional — nil-safe)
	AssetVersionsAppend(ctx context.Context, v *asset.Version) error

	// Drive upload (→ driveFolderMgr)
	DriveUploadFileIfChanged(ctx context.Context, localPath, folderID, filename string) (*youtubeports.UploadResultDTO, bool, error)
	DriveGetOrCreateFolder(ctx context.Context, name, parentID string) (string, error)

	// Ollama (→ ollama port + semaphore)
	OllamaSimpleGenerate(ctx context.Context, model, prompt string, timeoutSec int, opts map[string]any) (string, error)

	// Concurrency semaphores
	AcquireVideoExtractSem(ctx context.Context) (release func())
	AcquireOllamaSem(ctx context.Context) (release func())
}

// ── Service ──────────────────────────────────────────────────────────────

// Service orchestrates the YouTube clip extraction pipeline.
type Service struct {
	cfg               *config.Config
	log               *zap.Logger
	videoPipeline     youtubeports.VideoPipelinePort
	clips             youtubeports.ClipStorePort
	cache             youtubeports.CachePort
	monitors          youtubeports.MonitorsStorePort
	assetDestResolver asset.Resolver
	folderMemory      youtubeports.FolderMemoryPort
	segmentsSvc       *segments.Service

	callbacks ExtractionCallbacks
}

// NewService constructs the extraction service. All deps and callbacks
// are required; nil checks at call sites surface missing wiring explicitly.
func NewService(deps ExtractionDeps, cb ExtractionCallbacks) *Service {
	return &Service{
		cfg:               deps.Cfg,
		log:               deps.Log,
		videoPipeline:     deps.VideoPipeline,
		clips:             deps.Clips,
		cache:             deps.Cache,
		monitors:          deps.Monitors,
		assetDestResolver: deps.AssetDestResolver,
		folderMemory:      deps.FolderMemory,
		segmentsSvc:       deps.SegmentsSvc,
		callbacks:         cb,
	}
}
