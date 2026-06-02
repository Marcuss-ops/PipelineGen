package images

import (
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
	"velox/go-master/internal/pkg/textutil"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
	"velox/go-master/internal/config"
	"velox/go-master/internal/media/generation"
	"velox/go-master/internal/media/ingest"
	"velox/go-master/internal/media/semantic"
	"velox/go-master/internal/media/storage"
	"velox/go-master/internal/media/vectorstore"
	"velox/go-master/internal/ml/ollama"
	clipsRepo "velox/go-master/internal/repository/clips"
	imagesRepo "velox/go-master/internal/repository/images"
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"

type Service struct {
	cfg        *config.Config
	repo       *imagesRepo.Repository
	stockRepo  *clipsRepo.Repository
	driveSvc   *driveapi.Service
	log        *zap.Logger
	tempDir    string
	scriptsDir string

	// NVIDIA AI image generation
	nvidiaAPIKey string
	nvidiaModel  string

	// Ingest pipeline (optional, fallback to direct)
	ingestSvc *ingest.Service

	// Google Accounting integration
	gaServerURL         string
	gaDownloadDir       string
	googleAccountingURL string
	googleAccountingDir string
	vidsProjectID       string
	flowProjectID       string

	// Image storage
	imagesDir     string
	driveFolderID string

	// HTTP client for external API calls
	client *http.Client

	// Cache for wiki API calls
	wikiCacheMu sync.RWMutex
	wikiCache   map[string]wikiCacheEntry

	// Mutex per evitare download duplicati dello stesso soggetto
	mu sync.Mutex

	// Animations directory
	animationsDir string

	// Unified media store for Drive operations (replaces raw driveSvc calls)
	mediaStore *storage.Store

	// NEW: Intelligence & Search
	llmGen    *ollama.Generator
	vectorSvc *vectorstore.Service

	// Centralized style registry
	styleRegistry *generation.StyleRegistry

	// Unified metadata writer for ALL media types
	// Replaces separate callSemanticTagger + fallback + upload logic per file
	metaWriter *semantic.MetadataWriter
}

type wikiCacheEntry struct {
	result    string
	timestamp time.Time
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

func NewService(cfg *config.Config, repo *imagesRepo.Repository, stockRepo *clipsRepo.Repository, driveSvc *driveapi.Service, styleRegistry *generation.StyleRegistry, log *zap.Logger) *Service {
	s := &Service{
		cfg:                 cfg,
		repo:                repo,
		stockRepo:           stockRepo,
		driveSvc:            driveSvc,
		driveFolderID: cfg.Drive.RootFolder(),
		log:                 log,
		imagesDir:           cfg.Storage.ImagesPath(),
		tempDir:             cfg.Storage.TempPath(),
		client: &http.Client{
			Timeout: 5 * time.Minute, // AI generation and browser automation can be slow
		},
		wikiCache:     make(map[string]wikiCacheEntry),
		scriptsDir:    cfg.Paths.PythonScriptsDir,
		nvidiaModel:   cfg.External.NvidiaModel,
		animationsDir: cfg.Storage.AnimationsPath(),
		styleRegistry: styleRegistry,
	}

	return s
}

func (s *Service) SetNvidiaConfig(apiKey, model string) {
	s.nvidiaAPIKey = apiKey
	s.nvidiaModel = model
}

func (s *Service) SetScriptsDir(dir string) {
	s.scriptsDir = dir
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
func (s *Service) SetMediaStore(store *storage.Store) {
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

func (s *Service) SetGoogleAccountingConfig(serverURL, downloadDir, vidsProjectID, flowProjectID string) {
	s.gaServerURL = serverURL
	s.gaDownloadDir = downloadDir
	s.googleAccountingURL = serverURL // allinea anche googleAccountingURL per le immagini Google Vids
	s.vidsProjectID = vidsProjectID
	s.flowProjectID = flowProjectID

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
	s.googleAccountingDir = absDir
}

func (s *Service) effectiveImagesDriveFolderID() string {
	// Use centralized resolver: MediaRootFolder > ImagesRootFolder > ""
	return s.cfg.Drive.ImagesFolder()
}

func Slugify(s string) string {
	return textutil.Slugify(s)
}
