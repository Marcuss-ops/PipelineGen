package images

import (
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"
	"velox/go-master/internal/config"
	"velox/go-master/internal/media/ingest"
	clipsRepo "velox/go-master/internal/repository/clips"
	imagesRepo "velox/go-master/internal/repository/images"
)

const userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.124 Safari/537.36"

type Service struct {
	cfg      *config.Config
	repo     *imagesRepo.Repository
	stockRepo *clipsRepo.Repository
	driveSvc *driveapi.Service
	log      *zap.Logger
	tempDir  string
	scriptsDir string

	// NVIDIA AI image generation
	nvidiaAPIKey string
	nvidiaModel  string

	// Ingest pipeline (optional, fallback to direct)
	ingestSvc *ingest.Service

	// Google Accounting integration
	gaServerURL       string
	gaDownloadDir     string
	googleAccountingURL string
	googleAccountingDir string

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
}

type wikiCacheEntry struct {
	result    string
	timestamp time.Time
}

type DiagnosticsReport struct {
	OK                bool     `json:"ok"`
	Services          []string `json:"services"`
	RepoConfigured    bool     `json:"repo_configured"`
	DriveConfigured   bool     `json:"drive_configured"`
	NvidiaConfigured  bool     `json:"nvidia_configured"`
	IngestConfigured  bool     `json:"ingest_configured"`
	WikidataWorks     bool     `json:"wikidata_works"`
}

func NewService(cfg *config.Config, repo *imagesRepo.Repository, stockRepo *clipsRepo.Repository, driveSvc *driveapi.Service, log *zap.Logger) *Service {
	return &Service{
		cfg:       cfg,
		repo:      repo,
		stockRepo: stockRepo,
		driveSvc:  driveSvc,
		log:       log,
		tempDir:   cfg.Storage.TempPath(),
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		wikiCache: make(map[string]wikiCacheEntry),
		scriptsDir: cfg.Paths.PythonScriptsDir,
		nvidiaModel: cfg.External.NvidiaModel,
		animationsDir: cfg.Storage.AnimationsPath(),
	}
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

func (s *Service) SetGoogleAccountingConfig(serverURL, downloadDir string) {
	s.gaServerURL = serverURL
	s.gaDownloadDir = downloadDir
}

func Slugify(s string) string {
	s = strings.ToLower(s)
	s = strings.TrimSpace(s)
	re := regexp.MustCompile(`[^a-z0-9]+`)
	s = re.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

var slugRegex = regexp.MustCompile(`[^a-z0-9]+`)
