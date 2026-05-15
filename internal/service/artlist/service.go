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
	"velox/go-master/internal/service/jobs"
	jobservice "velox/go-master/internal/service/jobs"
	"velox/go-master/internal/service/clipindexer"
	"velox/go-master/pkg/config"
	"velox/go-master/pkg/models"
)

// Service è un facade leggero che delega a componenti specializzati.
// Non implementa direttamente la logica, ma coordina i servizi sottostanti.
type Service struct {
	cfg            *config.Config
	mainDB         *sql.DB
	artlistDB      *sql.DB
	nodeScraperDir string
	log            *zap.Logger

	// Componenti delegati
	searchService      *SearchService
	runOrchestrator    *RunOrchestratorService
	destinationService *DestinationService
	clipProcessor      *ClipProcessorService
	jobAdapter         *JobAdapter
	diagnosticsService *DiagnosticsService
	clipStatusService  *ClipStatusService

	// Dipendenze condivise
	artlistRepo      *clips.Repository
	mediaProcessor   processor.Processor
	lifecycleService *lifecycle.Service
	assetDestResolver destination.Resolver
	jobsSvc          *jobservice.Service
	clipIndexer      *clipindexer.Service
	driveSvc         *driveapi.Service
}

// NewService crea una nuova istanza del servizio Artlist come facade.
func NewService(cfg *config.Config, mainDB *sql.DB, artlistDB *sql.DB, nodeScraperDir string, artlistRepo *clips.Repository, mediaProcessor processor.Processor, lifecycleService *lifecycle.Service, assetDestResolver destination.Resolver, clipIndexer *clipindexer.Service, jobsSvc *jobservice.Service, driveSvc *driveapi.Service, log *zap.Logger) (*Service, error) {
	s := &Service{
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
	}

	// Inizializza i componenti delegati
	s.searchService = NewSearchService(s)
	s.runOrchestrator = NewRunOrchestratorService(s)
	s.destinationService = NewDestinationService(s)
	s.clipProcessor = NewClipProcessorService(s)
	s.jobAdapter = NewJobAdapter(s)
	s.diagnosticsService = NewDiagnosticsService(s)
	s.clipStatusService = NewClipStatusService(s)

	return s, nil
}

// Close è un no-op poiché la connessione artlistDB è gestita esternamente.
func (s *Service) Close() error {
	return nil
}

// Search esegue una ricerca di clip.
func (s *Service) Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error) {
	return s.searchService.Search(ctx, req)
}

// SearchLive esegue una ricerca live tramite scraper.
func (s *Service) SearchLive(ctx context.Context, term string, limit int) ([]ScraperClip, error) {
	return s.searchService.SearchLive(ctx, term, limit)
}

// SearchLiveAndSave esegue una ricerca live e salva i risultati.
func (s *Service) SearchLiveAndSave(ctx context.Context, term string, limit int) (*SearchResponse, error) {
	return s.searchService.SearchLiveAndSave(ctx, term, limit)
}

// DiscoverAndQueueRun scopre clip e accoda un'esecuzione.
func (s *Service) DiscoverAndQueueRun(ctx context.Context, term string, limit int) (*SearchResponse, *RunTagResponse, error) {
	return s.searchService.DiscoverAndQueueRun(ctx, term, limit)
}

// GetRunTag ottiene lo stato di un run.
func (s *Service) GetRunTag(ctx context.Context, runID string) (*RunTagResponse, error) {
	return s.runOrchestrator.GetRunTag(ctx, runID)
}

// RunTag esegue un tag su un insieme di clip.
func (s *Service) RunTag(ctx context.Context, req *RunTagRequest) (*RunTagResponse, error) {
	return s.runOrchestrator.RunTag(ctx, req)
}

// GetStats ottiene statistiche sul catalogo Artlist.
func (s *Service) GetStats(ctx context.Context) (*Stats, error) {
	return s.diagnosticsService.GetStats(ctx)
}

// Diagnostics ottiene informazioni diagnostiche per un termine.
func (s *Service) Diagnostics(ctx context.Context, term string) (*DiagnosticsResponse, error) {
	return s.diagnosticsService.Diagnostics(ctx, term)
}

// GetClipStatus ottiene lo stato di una clip specifica.
func (s *Service) GetClipStatus(ctx context.Context, clipID string) (*ClipStatusResponse, error) {
	return s.clipStatusService.GetClipStatus(ctx, clipID)
}

// SearchClips cerca clip nel database locale.
func (s *Service) SearchClips(ctx context.Context, term string) []*models.MediaAsset {
	return s.searchService.SearchClips(ctx, term)
}

// UpsertClip inserisce o aggiorna una clip nel database.
func (s *Service) UpsertClip(ctx context.Context, clip *models.MediaAsset) error {
	return s.searchService.UpsertClip(ctx, clip)
}

// HandleJob gestisce un job dalla coda.
func (s *Service) HandleJob(ctx context.Context, job *models.Job, tools *jobs.JobTools) (map[string]any, error) {
	return s.jobAdapter.HandleJob(ctx, job, tools)
}

// UpdateJobRun aggiorna un job dopo l'esecuzione.
func (s *Service) UpdateJobRun(ctx context.Context, job *models.Job, resp *RunTagResponse) error {
	return s.jobAdapter.UpdateJobRun(ctx, job, resp)
}

// GetJobByRunID ottiene un job per run ID.
func (s *Service) GetJobByRunID(ctx context.Context, runID string) (*models.Job, error) {
	return s.jobAdapter.GetJobByRunID(ctx, runID)
}

// FindActiveJob trova un job attivo per chiave.
func (s *Service) FindActiveJob(ctx context.Context, activeKey string) (*models.Job, error) {
	return s.jobAdapter.FindActiveJob(ctx, activeKey)
}
