package artlist

import (
	"context"
	"database/sql"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"velox/go-master/internal/core/destination"
	"velox/go-master/internal/core/lifecycle"
	"velox/go-master/internal/core/processor"
	"velox/go-master/internal/repository/clips"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/internal/service/clipindexer"
	"velox/go-master/pkg/config"
)

type Service struct {
	cfg               *config.Config
	mainDB            *sql.DB
	artlistDB         *sql.DB
	nodeScraperDir    string
	artlistRepo       *clips.Repository
	mediaProcessor    processor.Processor
	lifecycleService  *lifecycle.Service
	assetDestResolver destination.Resolver
	jobsSvc           *jobservice.Service
	clipIndexer       *clipindexer.Service
	driveSvc          *driveapi.Service
	log               *zap.Logger
}

func NewService(cfg *config.Config, mainDB *sql.DB, artlistDB *sql.DB, nodeScraperDir string, artlistRepo *clips.Repository, mediaProcessor processor.Processor, lifecycleService *lifecycle.Service, assetDestResolver destination.Resolver, clipIndexer *clipindexer.Service, jobsSvc *jobservice.Service, driveSvc *driveapi.Service, log *zap.Logger) (*Service, error) {
	return &Service{
		cfg:               cfg,
		mainDB:            mainDB,
		artlistDB:         artlistDB,
		nodeScraperDir:    nodeScraperDir,
		artlistRepo:       artlistRepo,
		mediaProcessor:    mediaProcessor,
		lifecycleService:  lifecycleService,
		assetDestResolver: assetDestResolver,
		clipIndexer:       clipIndexer,
		jobsSvc:           jobsSvc,
		driveSvc:          driveSvc,
		log:               log,
	}, nil
}

// Close is a no-op since the artlistDB connection is managed externally by storage.NewSQLiteDB()
func (s *Service) Close() error {
	// Connection is managed by bootstrap/databases.go, not here
	return nil
}

// CandidateSearcher defines the interface for searching clip candidates
type CandidateSearcher interface {
	Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error)
}

// RunOrchestrator defines the interface for orchestrating run execution
type RunOrchestrator interface {
	GetRunTag(ctx context.Context, runID string) (*RunTagResponse, error)
	RunTag(ctx context.Context, req *RunTagRequest) (*RunTagResponse, error)
}

// nodeScraperResponse represents the response from the Node.js scraper
type nodeScraperResponse struct {
	OK    bool `json:"ok"`
	Clips []struct {
		Title       string `json:"title"`
		ClipPageURL string `json:"clip_page_url"`
		PrimaryURL  string `json:"primary_url"`
		ClipID      string `json:"clip_id"`
	} `json:"clips"`
}

// ClipStatusResponse represents the status of a clip
type ClipStatusResponse struct {
	ClipID       string `json:"clip_id"`
	Name         string `json:"name"`
	HasLocalFile bool   `json:"has_local_file"`
	LocalPath    string `json:"local_path"`
	DriveLink    string `json:"drive_link"`
	HasDriveLink bool   `json:"has_drive_link"`
	FileHash     string `json:"file_hash"`
	Source       string `json:"source"`
	ExternalURL  string `json:"external_url"`
}

