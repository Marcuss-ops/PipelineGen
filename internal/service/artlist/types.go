package artlist

import (
	"database/sql"

	"go.uber.org/zap"

	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/service/assetdestination"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/internal/service/mediaasset"
	"velox/go-master/internal/service/mediaregistry"
	"velox/go-master/pkg/config"
)

type Service struct {
	cfg               *config.Config
	mainDB            *sql.DB
	artlistDB         *sql.DB
	jobsDB            *sql.DB
	nodeScraperDir    string
	artlistRepo       *clips.Repository
	driveService      *DriveService
	mediaProcessor    *mediaasset.Processor
	mediaFinalizer    *mediaregistry.Finalizer
	assetDestResolver *assetdestination.Resolver
	jobsSvc           *jobservice.Service
	log               *zap.Logger
}

func NewService(cfg *config.Config, mainDB *sql.DB, jobsDB *sql.DB, artlistDBPath string, nodeScraperDir string, artlistRepo *clips.Repository, driveService *DriveService, mediaProcessor *mediaasset.Processor, mediaFinalizer *mediaregistry.Finalizer, jobsSvc *jobservice.Service, log *zap.Logger) (*Service, error) {
	var artlistDB *sql.DB
	var err error
	if artlistDBPath != "" {
		artlistDB, err = sql.Open("sqlite3", artlistDBPath+"?_journal_mode=WAL&_busy_timeout=5000")
		if err != nil {
			return nil, err
		}
	}

	// Create asset destination resolver if drive client is available
	var assetDestResolver *assetdestination.Resolver
	if driveService != nil && driveService.GetDriveClient() != nil {
		assetDestResolver = assetdestination.NewResolver(cfg, log, driveService.GetDriveClient())
	}

	return &Service{
		cfg:               cfg,
		mainDB:            mainDB,
		jobsDB:            jobsDB,
		artlistDB:         artlistDB,
		nodeScraperDir:    nodeScraperDir,
		artlistRepo:       artlistRepo,
		driveService:      driveService,
		mediaProcessor:    mediaProcessor,
		mediaFinalizer:    mediaFinalizer,
		assetDestResolver: assetDestResolver,
		jobsSvc:           jobsSvc,
		log:               log,
	}, nil
}

func (s *Service) Close() error {
	if s.artlistDB != nil {
		return s.artlistDB.Close()
	}
	return nil
}

// GetDriveFolderID returns the configured root Drive folder ID from the drive service.
// This is a convenience method for use by handlers and job handlers.
func (s *Service) GetDriveFolderID() string {
	if s.driveService == nil {
		return ""
	}
	return s.driveService.GetDriveFolderID()
}
