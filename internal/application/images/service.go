package images

import (
	"context"
	"net/http"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
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

	// dedup prevents duplicate downloads of the same subject key.
	// Replaces the global sync.Mutex with a singleflight.Group so
	// concurrent requests for different subjects are NOT serialised
	// (Fase 3, June 2026).
	dedup singleflight.Group

	// Semaphore for concurrent NVIDIA image generation, configured via ConcurrencyConfig.
	nvidiaSem chan struct{}

	// Animations directory
	animationsDir string

	// Unified media store for Drive operations (replaces raw driveSvc calls)
	mediaStore *drive.Store

	// NEW: Intelligence & Search
	llmGen *ollama.Generator
	// vectorSvc removed from this service.
	// Callers index embeddings through the Python embedding server only
	// (s.repo.UpdateEmbeddingData persists them in SQLite so they survive
	// even without a vector-store backend).

	// Centralized style registry
	styleRegistry *generation.StyleRegistry

	// Unified metadata writer for ALL media types
	// Replaces separate callSemanticTagger + fallback + upload logic per file
	metaWriter *semantic.MetadataWriter

	// QDRANT-002: canonical ingestion dispatcher — wired via constructor
	// argument (PR-12d, June 2026) to eliminate the late-bind ordering
	// hazard that previously kept RegisterVideoAsset / registerAudioClip
	// on the raw stockRepo.Upsert path between BuildDomainBundle and
	// SetDispatcher's post-construction call in NewComposition. Nil at
	// construction time means "dispatcher not wired (tests, partial
	// deployments)" and the write methods fall back to raw stockRepo
	// upserts — no more silent window between BuildDomainBundle and
	// SetDispatcher, since the field is now set in the ctor.
	dispatcher *outbox.Dispatcher

	// imageGen is the canonical port for AI image generation (FASE 2, June 2026).
	// Injected at construction time; nil means "no provider wired" and
	// GenerateSmartImage returns ErrImageGenNotImplemented.
	// Concrete implementations: ChromeImageProvider (Playwright → Chrome →
	// slides.new → Nano Banana Pro), NvidiaImageProvider (future).
	imageGen ImageGenerator
}

type DiagnosticsReport struct {
	OK               bool     `json:"ok"`
	Services         []string `json:"services"`
	RepoConfigured   bool     `json:"repo_configured"`
	DriveConfigured  bool     `json:"drive_configured"`
	NvidiaConfigured bool     `json:"nvidia_configured"`
	IngestConfigured bool     `json:"ingest_configured"`
	WikidataWorks    bool     `json:"wikidata_works"`
	// ImageGenWired is true when an ImageGenerator port is injected
	// (FASE 2, June 2026). False means GenerateSmartImage returns 501.
	ImageGenWired bool `json:"image_gen_wired"`
	// ImageGenHealthy is true when the wired ImageGenerator responds to
	// a health ping (FASE 10, June 2026). False when the worker is down
	// or not started. Only meaningful when ImageGenWired is true.
	ImageGenHealthy bool `json:"image_gen_healthy"`
	// ImageGenCooldownProfiles is the count of profiles currently in
	// cooldown due to quota/auth errors (FASE 10).
	ImageGenCooldownProfiles int `json:"image_gen_cooldown_profiles"`
	// Capabilities is the truthful per-capability availability map
	// surfaced for /api/images/diagnostics. Single source of truth:
	// each entry is derived from Service.CapabilityResolution; HTTP
	// routes and this diagnostic field never read env vars or feature
	// booleans in parallel (per fix(images): expose truthful capability
	// availability).
	Capabilities map[Capability]CapabilityStatus `json:"capabilities"`
}

// ImagesDeps groups constructor dependencies by real capability.
// Replaces the 16-param flat constructor with 4 grouped bundles.
type ImagesDeps struct {
	Core     ImagesCoreDeps
	Storage  ImagesStorageDeps
	GenAI    ImagesGenAIDeps
	External ImagesExternalDeps
}

// ImagesCoreDeps — config + logging + concurrency config.
type ImagesCoreDeps struct {
	Cfg *config.Config
	Log *zap.Logger
}

// ImagesStorageDeps — repositories + Drive client + media store.
type ImagesStorageDeps struct {
	ImageRepo  *assets.ImagesRepository
	ClipsRepo  *assets.ClipsRepository
	DriveSvc   *driveapi.Service
	MediaStore *drive.Store
}

// ImagesGenAIDeps — AI generation: LLM, metadata, style, image generator.
type ImagesGenAIDeps struct {
	LLMGen        *ollama.Generator
	MetaWriter    *semantic.MetadataWriter
	StyleRegistry *generation.StyleRegistry
	ImageGen      ImageGenerator
	NvidiaCfg     NvidiaConfig
	RemoteImageURL string
}

// ImagesExternalDeps — ingest, dispatcher, Velox, Google Accounting.
type ImagesExternalDeps struct {
	IngestSvc   *ingest.Service
	Dispatcher  *outbox.Dispatcher
	VeloxBaseURL string
	GACfg       GoogleAccountingConfig
}

// NewService constructs an images.Service from grouped dependency bundles.
func NewService(deps ImagesDeps) *Service {
	cfg := deps.Core.Cfg
	maxNvidia := cfg.Concurrency.MaxConcurrentNvidiaGenerations
	if maxNvidia <= 0 {
		maxNvidia = 1
	}

	s := &Service{
		cfg:           cfg,
		repo:          deps.Storage.ImageRepo,
		stockRepo:     deps.Storage.ClipsRepo,
		driveSvc:      deps.Storage.DriveSvc,
		driveFolderID: cfg.Drive.RootFolder(),
		log:           deps.Core.Log,
		imagesDir:     cfg.Storage.ImagesPath(),
		tempDir:       cfg.Storage.TempPath(),
		client: &http.Client{
			Timeout: 10 * time.Minute,
		},
		nvidiaSem: make(chan struct{}, maxNvidia),

		scriptsDir:             cfg.Paths.PythonScriptsDir,
		nvidiaAPIKey:           deps.GenAI.NvidiaCfg.APIKey,
		nvidiaModel:            deps.GenAI.NvidiaCfg.Model,
		nvidiaLocalNIMURL:      cfg.External.NvidiaLocalNIMURL,
		remoteImageEndpointURL: deps.GenAI.RemoteImageURL,
		veloxBaseURL:           deps.External.VeloxBaseURL,
		gaServerURL:            deps.External.GACfg.ServerURL,
		gaDownloadDir:          deps.External.GACfg.DownloadDir,
		vidsProjectID:          deps.External.GACfg.VidsProjectID,
		animationsDir:          cfg.Storage.AnimationsPath(),
		styleRegistry:          deps.GenAI.StyleRegistry,
		mediaStore:             deps.Storage.MediaStore,
		llmGen:                 deps.GenAI.LLMGen,
		metaWriter:             deps.GenAI.MetaWriter,
		ingestSvc:              deps.External.IngestSvc,
		dispatcher:             deps.External.Dispatcher,
		imageGen:               deps.GenAI.ImageGen,
	}

	return s
}

func (s *Service) Diagnostics() DiagnosticsReport {
	// Single source of truth: NvidiaConfigured is derived from
	// CapabilityResolution so HTTP routes and the diagnostic field do
	// not duplicate the nvidiaAPIKey / placeholder check. See
	// capability.go for the resolver.
	report := DiagnosticsReport{
		OK:               s.repo != nil,
		Services:         []string{"repo", "drive", "nvidia", "remote_image_gen", "chrome_playwright"},
		RepoConfigured:   s.repo != nil,
		DriveConfigured:  s.driveSvc != nil,
		NvidiaConfigured: s.CapabilityResolution(CapImageGenNvidia) == StatusAvailable,
		IngestConfigured: s.ingestSvc != nil,
		ImageGenWired:    s.imageGen != nil,
		Capabilities:     s.AllCapabilities(),
	}

	// FASE 10: health check and cooldown tracking for ChromeImageProvider.
	if cp, ok := s.imageGen.(*ChromeImageProvider); ok {
		report.ImageGenHealthy = cp.Health() == nil
		report.ImageGenCooldownProfiles = cp.ActiveCooldownProfiles()
	}
	return report
}

// Log restituisce il logger interno per logging da altre componenti.
func (s *Service) Log() *zap.Logger {
	return s.log
}

// Repo returns the underlying images repository.
func (s *Service) Repo() *assets.ImagesRepository {
	return s.repo
}

// FormatDriveLink mirrors internal/infrastructure/drive.FileURLFromID —
// the API layer used to call drive.FileURLFromID directly, which kept
// `internal/infrastructure/drive` in the API imports. Per PG-002 the
// formatting helper is now exposed here so the handler only depends on
// the application-level service contract. Output is byte-identical to
// the previous behaviour so the public HTTP contract is unchanged
// (zero-change fix for /api/images/generate responses).
func (s *Service) FormatDriveLink(id string) string {
	if id == "" {
		return ""
	}
	return "https://drive.google.com/file/d/" + id
}

// SemanticMetadataPayload is the cross-package carrier returned by
// uploadVideoMetadata. google_vids_assets.go mixes `semantic.Payload` and
// the legacy `SemanticMetadataPayload` identifier at the same call site
// (line 71 assigns, lines 159+167 return), so they MUST be the same
// underlying type. We declare this as a Go type alias of semantic.Payload
// rather than a parallel struct to avoid any field-drift footgun: if
// semantic.Payload gains a new column tomorrow, both names pick it up.
type SemanticMetadataPayload = semantic.Payload

// generateGoogleSlidesImage has been removed (PR cleanup June 2026).
// Google Slides image integration was never implemented and the stub
// always returned an error. Callers should fail with an explicit
// message at the point of invocation.

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
	s.log.Info("Google Slides: automation session tab pool prewarmed", zap.String("job_id", jobID), zap.Int("count", count))
}

// GenerateVideoAI has been removed (PR cleanup June 2026).
// Video-from-prompt generation was never implemented. The fullimages
// pipeline already has a ken-burns fallback that continues to work.
