package images

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
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
	llmGen *ollama.Generator
	// PG-034 (June 2026): vectorSvc removed — Qdrant capability deleted.
	// Callers index embeddings through the Python embedding server only
	// (s.repo.UpdateEmbeddingData persists them in SQLite so they survive
	// even without a vector-store backend).

	// Centralized style registry
	styleRegistry *generation.StyleRegistry

	// Unified metadata writer for ALL media types
	// Replaces separate callSemanticTagger + fallback + upload logic per file
	metaWriter *semantic.MetadataWriter
}

type DiagnosticsReport struct {
	OK               bool                              `json:"ok"`
	Services         []string                          `json:"services"`
	RepoConfigured   bool                              `json:"repo_configured"`
	DriveConfigured  bool                              `json:"drive_configured"`
	NvidiaConfigured bool                              `json:"nvidia_configured"`
	IngestConfigured bool                              `json:"ingest_configured"`
	WikidataWorks    bool                              `json:"wikidata_works"`
	// Capabilities is the truthful per-capability availability map
	// surfaced for /api/images/diagnostics. Single source of truth:
	// each entry is derived from Service.CapabilityResolution; HTTP
	// routes and this diagnostic field never read env vars or feature
	// booleans in parallel (per fix(images): expose truthful capability
	// availability).
	Capabilities map[Capability]CapabilityStatus `json:"capabilities"`
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
		metaWriter:             metaWriter,
		ingestSvc:              ingestSvc,
	}

	return s
}

func (s *Service) Diagnostics() DiagnosticsReport {
	// Single source of truth: NvidiaConfigured is derived from
	// CapabilityResolution so HTTP routes and the diagnostic field do
	// not duplicate the nvidiaAPIKey / placeholder check. See
	// capability.go for the resolver.
	return DiagnosticsReport{
		OK:               s.repo != nil,
		Services:         []string{"repo", "drive", "nvidia", "remote_image_gen"},
		RepoConfigured:   s.repo != nil,
		DriveConfigured:  s.driveSvc != nil,
		NvidiaConfigured: s.CapabilityResolution(CapImageGenNvidia) == StatusAvailable,
		IngestConfigured: s.ingestSvc != nil,
		Capabilities:     s.AllCapabilities(),
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
	s.log.Debug("trigger_prewarm_noop", zap.String("job_id", jobID), zap.Int("count", count), zap.String("note", "playwright tab pool not yet wired to this service; future PR will plumb prewarm hooks"))
}

// GenerateVideoAI has been removed (PR cleanup June 2026).
// Video-from-prompt generation was never implemented. The fullimages
// pipeline already has a ken-burns fallback that continues to work.
