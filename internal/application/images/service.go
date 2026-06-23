package images

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/media/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/media/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"

type Service struct {
	cfg        *config.Config
	repo       *assets.ImagesRepository
	stockRepo  *assets.ClipsRepository
	driveSvc   *driveapi.Service
	log        *zap.Logger
	tempDir    string
	scriptsDir string

	// NVIDIA AI image generation
	nvidiaAPIKey      string
	nvidiaModel       string
	nvidiaLocalNIMURL string

	// Remote image generation (Google Flow)
	remoteImageEndpointURL string
	veloxBaseURL           string

	// Ingest pipeline (optional, fallback to direct)
	ingestSvc *ingest.Service

	// Google Accounting integration
	gaServerURL   string
	gaDownloadDir string
	vidsProjectID string

	// Image storage
	imagesDir     string
	driveFolderID string

	// HTTP client for external API calls
	client *http.Client

	// Mutex per evitare download duplicati dello stesso soggetto
	mu sync.Mutex

	// Semaphore for concurrent NVIDIA image generation, configured via ConcurrencyConfig.
	nvidiaSem chan struct{}

	// Animations directory
	animationsDir string

	// Unified media store for Drive operations (replaces raw driveSvc calls)
	mediaStore *drive.Store

	// NEW: Intelligence & Search
	llmGen    *ollama.Generator
	vectorSvc *qdrant.Service

	// Centralized style registry
	styleRegistry *generation.StyleRegistry

	// Unified metadata writer for ALL media types
	// Replaces separate callSemanticTagger + fallback + upload logic per file
	metaWriter *semantic.MetadataWriter
}

type DiagnosticsReport struct {
	OK               bool     `json:"ok"`
	Services         []string `json:"services"`
	RepoConfigured   bool     `json:"repo_configured"`
	DriveConfigured  bool     `json:"drive_configured"`
	NvidiaConfigured bool     `json:"nvidia_configured"`
	IngestConfigured bool     `json:"ingest_configured"`
	WikidataWorks    bool     `json:"wikidata_works"`
}

// NewService constructs an images.Service with all optional dependencies
// wired at construction time. The 8 post-construction setters
// (SetNvidiaConfig, SetRemoteImageEndpointURL, SetVeloxBaseURL,
// SetGoogleAccountingConfig, SetMediaStore, SetLLMGenerator,
// SetVectorStore, SetMetadataWriter) have been removed in PR4-H Commit 3 —
// their fields are initialised directly in this ctor. PR4.D (June 2026) then
// collapsed the loose `nvidiaAPIKey/nvidiaModel/gaServerURL/gaDownloadDir/
// vidsProjectID` scalars into two group structs (NvidiaConfig +
// GoogleAccountingConfig) so the ctor signature stays readable as
// integrations grow.
//
// PR3 (June 2026): ingestSvc is now wired via constructor injection.
// The SetIngestService setter has been removed — ingestService is built
// during BuildDomainBundle and passed directly to NewService.
func NewService(
	cfg *config.Config,
	repo *assets.ImagesRepository,
	stockRepo *assets.ClipsRepository,
	driveSvc *driveapi.Service,
	styleRegistry *generation.StyleRegistry,
	nvidiaCfg NvidiaConfig,
	remoteImageEndpointURL string,
	veloxBaseURL string,
	gaCfg GoogleAccountingConfig,
	mediaStore *drive.Store,
	llmGen *ollama.Generator,
	vectorSvc *qdrant.Service,
	metaWriter *semantic.MetadataWriter,
	ingestSvc *ingest.Service,
	log *zap.Logger,
) *Service {
	maxNvidia := cfg.Concurrency.MaxConcurrentNvidiaGenerations
	if maxNvidia <= 0 {
		maxNvidia = 1
	}

	s := &Service{
		cfg:           cfg,
		repo:          repo,
		stockRepo:     stockRepo,
		driveSvc:      driveSvc,
		driveFolderID: cfg.Drive.RootFolder(),
		log:           log,
		imagesDir:     cfg.Storage.ImagesPath(),
		tempDir:       cfg.Storage.TempPath(),
		client: &http.Client{
			Timeout: 10 * time.Minute, // AI generation and browser automation can be slow
		},
		nvidiaSem: make(chan struct{}, maxNvidia),

		scriptsDir:             cfg.Paths.PythonScriptsDir,
		nvidiaAPIKey:           nvidiaCfg.APIKey,
		nvidiaModel:            nvidiaCfg.Model,
		nvidiaLocalNIMURL:      cfg.External.NvidiaLocalNIMURL,
		remoteImageEndpointURL: remoteImageEndpointURL,
		veloxBaseURL:           veloxBaseURL,
		gaServerURL:            gaCfg.ServerURL,
		gaDownloadDir:          gaCfg.DownloadDir,
		vidsProjectID:          gaCfg.VidsProjectID,
		animationsDir:          cfg.Storage.AnimationsPath(),
		styleRegistry:          styleRegistry,
		mediaStore:             mediaStore,
		llmGen:                 llmGen,
		vectorSvc:              vectorSvc,
		metaWriter:             metaWriter,
		ingestSvc:              ingestSvc,
	}

	return s
}

func (s *Service) Diagnostics() DiagnosticsReport {
	return DiagnosticsReport{
		OK:               s.repo != nil,
		Services:         []string{"repo", "drive", "nvidia"},
		RepoConfigured:   s.repo != nil,
		DriveConfigured:  s.driveSvc != nil,
		NvidiaConfigured: s.nvidiaAPIKey != "" && s.nvidiaAPIKey != "PASTE_YOUR_NVIDIA_API_KEY_HERE",
		IngestConfigured: s.ingestSvc != nil,
	}
}

// Log restituisce il logger interno per logging da altre componenti.
func (s *Service) Log() *zap.Logger {
	return s.log
}

// Repo returns the underlying images repository.
func (s *Service) Repo() *assets.ImagesRepository {
	return s.repo
}

// SemanticMetadataPayload is the cross-package carrier returned by
// uploadVideoMetadata. google_vids_assets.go mixes `semantic.Payload` and
// the legacy `SemanticMetadataPayload` identifier at the same call site
// (line 71 assigns, lines 159+167 return), so they MUST be the same
// underlying type. We declare this as a Go type alias of semantic.Payload
// rather than a parallel struct to avoid any field-drift footgun: if
// semantic.Payload gains a new column tomorrow, both names pick it up.
type SemanticMetadataPayload = semantic.Payload

// generateGoogleSlidesImage is a temporary scaffold stub. The downstream
// caller (google_generate.go::generateGoogleVidsImage) invokes this
// signature with (ctx, cleanPrompt, styledPrompt, subject, style, tags,
// width, height, skipDrive). The real Google Vids / Slides image path is
// being tracked separately (track: image-google-slides); for now we fail
// fast with an explicit error so the operator can see the gap instead of
// silently rendering an empty asset. The caller already wraps this error
// with "%w" so the message remains visible in the script job log.
func (s *Service) generateGoogleSlidesImage(ctx context.Context, cleanPrompt, styledPrompt, subject, style string, tags []string, width, height int, skipDrive bool) (*asset.ImageAsset, error) {
	if s == nil {
		return nil, fmt.Errorf("generateGoogleSlidesImage: nil service")
	}
	// Respect context cancellation for parity with the other generators.
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("generateGoogleSlidesImage: %w", ctx.Err())
	default:
	}
	if s.log != nil {
		s.log.Warn("generateGoogleSlidesImage stub hit (google-slides integration not yet wired)",
			zap.String("subject", subject),
			zap.String("style", style),
			zap.Int("width", width),
			zap.Int("height", height),
		)
	}
	return nil, fmt.Errorf("generateGoogleSlidesImage: not yet implemented (stubbed to unblock build; integrate Google Vids / Slides image path in a follow-up PR)")
}

// TriggerPrewarm satisfies the ImageSearchService interface so the script
// job handler can request a pre-warm of the Playwright tab pool before the
// first AI-image call. The actual tab-pool wiring lives behind a future PR
// (the pool is not yet plumbed to this service); today this is a no-op that
// logs at debug so operators can see when prewarm requests are flowing.
//
// Behaviour notes:
//   - Best-effort: returns immediately on context cancellation so callers that
//     race the pipeline shutdown do not block.
//   - Nil-safe so tests / partial wiring can call it without a panic.
//   - The Playwright-side cache saves ~30s first-scene cold-start per
//     AGENTS.md. Until the pool is plumbed, this stub returns straight away
//     and the cold-start cost stays — that's acceptable; the build break is
//     not.
func (s *Service) TriggerPrewarm(ctx context.Context, jobID string, count int) {
	if s == nil {
		return
	}
	select {
	case <-ctx.Done():
		return
	default:
	}
	if s.log == nil {
		return
	}
	s.log.Debug("trigger_prewarm_noop", zap.String("job_id", jobID), zap.Int("count", count), zap.String("note", "playwright tab pool not yet wired to this service; future PR will plumb prewarm hooks"))
}

// GenerateVideoAI is a stub method on *images.Service so the fullimages
// pipeline can compile while the real video-from-prompt generator lands in
// a follow-up PR. The only caller today is fullimages/service.go:176, which
// expects signature (ctx, prompt string, style string) (string, error).
// Always errors for now — fullimages should treat this as a 501-not-yet-
// wired signal rather than silently producing empty videos.
func (s *Service) GenerateVideoAI(ctx context.Context, prompt, style string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("GenerateVideoAI: nil service")
	}
	select {
	case <-ctx.Done():
		return "", fmt.Errorf("GenerateVideoAI: %w", ctx.Err())
	default:
	}
	if s.log != nil {
		s.log.Debug("generate_video_ai_stub", zap.String("style", style), zap.Int("prompt_len", len(prompt)))
	}
	return "", fmt.Errorf("GenerateVideoAI: not yet implemented (stubbed to unblock build; integrate video-from-prompt generator in a follow-up PR)")
}
