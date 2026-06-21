package images

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/ollama"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	sqlite "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite"
	"github.com/Marcuss-ops/PipelineGen/internal/media/generation"
	"github.com/Marcuss-ops/PipelineGen/internal/media/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/media/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/media/vectorstore"
	"github.com/Marcuss-ops/PipelineGen/internal/upload/drive"
	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"

type Service struct {
	cfg        *config.Config
	repo       *sqlite.ImagesRepository
	stockRepo  *sqlite.ClipsRepository
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

	// Animations directory
	animationsDir string

	// Unified media store for Drive operations (replaces raw driveSvc calls)
	mediaStore *drive.Store

	// NEW: Intelligence & Search
	llmGen    *ollama.Generator
	vectorSvc *vectorstore.Service

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

func NewService(cfg *config.Config, repo *sqlite.ImagesRepository, stockRepo *sqlite.ClipsRepository, driveSvc *driveapi.Service, styleRegistry *generation.StyleRegistry, log *zap.Logger) *Service {
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

		scriptsDir:        cfg.Paths.PythonScriptsDir,
		nvidiaModel:       cfg.External.NvidiaModel,
		nvidiaLocalNIMURL: cfg.External.NvidiaLocalNIMURL,
		animationsDir:     cfg.Storage.AnimationsPath(),
		styleRegistry:     styleRegistry,
	}

	return s
}

func (s *Service) SetNvidiaConfig(apiKey, model string) {
	s.nvidiaAPIKey = apiKey
	s.nvidiaModel = model
}

// SetRemoteImageEndpointURL sets the remote image generation endpoint URL.
func (s *Service) SetRemoteImageEndpointURL(url string) {
	s.remoteImageEndpointURL = url
}

// SetVeloxBaseURL sets the base URL of this server, used to construct webhook_url for remote image generation.
func (s *Service) SetVeloxBaseURL(url string) {
	s.veloxBaseURL = url
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

func (s *Service) SetIngestService(svc *ingest.Service) {
	s.ingestSvc = svc
}

// SetMediaStore sets the unified media store for Drive operations.
func (s *Service) SetMediaStore(store *drive.Store) {
	s.mediaStore = store
}

// SetMetadataWriter sets the unified metadata writer for ALL media types.
// Handles semantic tagging + fallback + metadata.json creation.
func (s *Service) SetMetadataWriter(w *semantic.MetadataWriter) {
	s.metaWriter = w
}

// SetLLMGenerator sets the Ollama generator for rich descriptions.
func (s *Service) SetLLMGenerator(gen *ollama.Generator) {
	s.llmGen = gen
}

// SetVectorStore sets the vector store service for indexing.
func (s *Service) SetVectorStore(svc *vectorstore.Service) {
	s.vectorSvc = svc
}

// Log restituisce il logger interno per logging da altre componenti.
func (s *Service) Log() *zap.Logger {
	return s.log
}

// SetGoogleAccountingConfig sets the configuration for Google Vids image generation via sidecar.
func (s *Service) SetGoogleAccountingConfig(serverURL, downloadDir, vidsProjectID string) {
	s.gaServerURL = serverURL
	s.gaDownloadDir = downloadDir
	s.vidsProjectID = vidsProjectID

	// Usa downloadDir come base per risolvere path relativi restituiti dal server Python.
	// downloadDir è relativo al project root (es. "./data/google_vids"), non a imagesDir.
	absDir := downloadDir
	if absDir != "" && !filepath.IsAbs(absDir) {
		// Assolutizza usando il working directory (coincide col project root)
		if wd, err := os.Getwd(); err == nil {
			absDir = filepath.Join(wd, absDir)
		}
	}
	// Resolve eventuali elementi ".." o "." nel path
	absDir = filepath.Clean(absDir)
}

// Repo returns the underlying images repository.
func (s *Service) Repo() *sqlite.ImagesRepository {
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
