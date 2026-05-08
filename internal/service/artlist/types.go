package artlist

import (
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

