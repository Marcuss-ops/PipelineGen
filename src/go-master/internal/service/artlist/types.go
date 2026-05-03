package artlist

import (
	"database/sql"

	"go.uber.org/zap"

	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/service/mediaasset"
	"velox/go-master/internal/service/mediaregistry"
	"velox/go-master/pkg/config"
)

type Service struct {
	cfg              *config.Config
	mainDB           *sql.DB
	artlistDB        *sql.DB
	jobsDB           *sql.DB
	nodeScraperDir   string
	clipsRepo        *clips.Repository
	driveService     *DriveService
	mediaProcessor   *mediaasset.Processor
	mediaFinalizer   *mediaregistry.Finalizer
	log              *zap.Logger
}

func NewService(cfg *config.Config, mainDB *sql.DB, jobsDB *sql.DB, artlistDBPath string, nodeScraperDir string, clipsRepo *clips.Repository, driveService *DriveService, mediaProcessor *mediaasset.Processor, mediaFinalizer *mediaregistry.Finalizer, log *zap.Logger) (*Service, error) {
	var artlistDB *sql.DB
	var err error
	if artlistDBPath != "" {
		artlistDB, err = sql.Open("sqlite3", artlistDBPath+"?_journal_mode=WAL&_busy_timeout=5000")
		if err != nil {
			return nil, err
		}
	}
	return &Service{
		cfg:              cfg,
		mainDB:           mainDB,
		jobsDB:           jobsDB,
		artlistDB:        artlistDB,
		nodeScraperDir:   nodeScraperDir,
		clipsRepo:        clipsRepo,
		driveService:     driveService,
		mediaProcessor:   mediaProcessor,
		mediaFinalizer:   mediaFinalizer,
		log:              log,
	}, nil
}

func (s *Service) Close() error {
	if s.artlistDB != nil {
		return s.artlistDB.Close()
	}
	return nil
}
