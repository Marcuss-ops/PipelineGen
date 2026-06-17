package artlist

import (
	"context"
	"database/sql"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	"velox/go-master/internal/config"
	"velox/go-master/internal/core/destination"
	"velox/go-master/internal/core/lifecycle"
	"velox/go-master/internal/core/processor"
	"velox/go-master/internal/jobs"
	jobservice "velox/go-master/internal/jobs"
	"velox/go-master/internal/media/clipindexer"
	"velox/go-master/internal/media/models"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/internal/repository/outbox"
)

// Service è un facade leggero che delega a componenti specializzati.
// Non implementa direttamente la logica, ma coordina i servizi sottostanti.
type Service struct {
	cfg       *config.Config
	mainDB    *sql.DB
	artlistDB *sql.DB
	log       *zap.Logger

	// L1: in-memory cache per risultati live (evita rilanci di Playwright per term già ricercati di recente)
	liveCache *liveSearchCache

	// Componenti delegati
	searchService      *SearchService
	runOrchestrator    *RunOrchestratorService
	destinationService *DestinationService
	jobAdapter         *JobAdapter
	diagnosticsService *DiagnosticsService

	// Dipendenze condivise
	artlistRepo       *clips.Repository
	mediaProcessor    processor.Processor
	lifecycleService  *lifecycle.Service
	assetDestResolver destination.Resolver
	jobsSvc           *jobservice.Service
	clipIndexer       *clipindexer.Service
	driveSvc          *driveapi.Service
	dispatcher        *outbox.Dispatcher

	// Arricchimento semantico: popola search_text + embedding_json dopo ogni salvataggio
	semanticEnricher *SemanticEnricher
}

// NewService crea una nuova istanza del servizio Artlist come facade.
func NewService(cfg *config.Config, mainDB *sql.DB, artlistDB *sql.DB, artlistRepo *clips.Repository, mediaProcessor processor.Processor, lifecycleService *lifecycle.Service, assetDestResolver destination.Resolver, clipIndexer *clipindexer.Service, jobsSvc *jobservice.Service, driveSvc *driveapi.Service, log *zap.Logger) (*Service, error) {
	s := &Service{
		cfg:               cfg,
		mainDB:            mainDB,
		artlistDB:         artlistDB,
		artlistRepo:       artlistRepo,
		mediaProcessor:    mediaProcessor,
		lifecycleService:  lifecycleService,
		assetDestResolver: assetDestResolver,
		clipIndexer:       clipIndexer,
		jobsSvc:           jobsSvc,
		driveSvc:          driveSvc,
		log:               log,
		liveCache:         newPersistentLiveSearchCache(mainDB, log),
	}

	// Inizializza i componenti delegati
	s.searchService = NewSearchService(s)
	s.runOrchestrator = NewRunOrchestratorService(s)
	s.destinationService = NewDestinationService(s)
	s.jobAdapter = NewJobAdapter(s)
	s.diagnosticsService = NewDiagnosticsService(s)

	return s, nil
}

// SetSemanticEnricher imposta l'enricher semantico per i clip Artlist.
// Deve essere chiamato dopo NewService(), durante il wiring.
func (s *Service) SetSemanticEnricher(enricher *SemanticEnricher) {
	s.semanticEnricher = enricher
}

// SetDispatcher injects the canonical media_index_outbox dispatcher so
// runOrchestrator and semanticEnricher route UpsertClip + IndexClip
// atomically through the outbox instead of using SafeGoFunc(clipIndexer.IndexClip).
// Mirrors the youtube.Service contract: production wiring sets it once
// at composition time; a nil dispatcher falls back to the legacy indexer.
func (s *Service) SetDispatcher(d *outbox.Dispatcher) {
	s.dispatcher = d
	if s.semanticEnricher != nil {
		s.semanticEnricher.dispatcher = d
	}
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

// DiscoverAndQueueRun scopre clip e accoda un'esecuzione.
func (s *Service) DiscoverAndQueueRun(ctx context.Context, term string, limit int) (*SearchResponse, *RunTagResponse, error) {
	return s.searchService.DiscoverAndQueueRun(ctx, term, limit)
}

// GetRunTag ottiene lo stato di un run.
func (s *Service) GetRunTag(ctx context.Context, runID string) (*RunTagResponse, error) {
	return s.runOrchestrator.GetRunTag(ctx, runID)
}

// GetStats ottiene statistiche sul catalogo Artlist.
func (s *Service) GetStats(ctx context.Context) (*Stats, error) {
	return s.diagnosticsService.GetStats(ctx)
}

// Diagnostics ottiene informazioni diagnostiche per un termine.
func (s *Service) Diagnostics(ctx context.Context, term string) (*DiagnosticsResponse, error) {
	return s.diagnosticsService.Diagnostics(ctx, term)
}

// SearchClips cerca clip nel database locale.
func (s *Service) SearchClips(ctx context.Context, term string) []*models.MediaAsset {
	return s.searchService.SearchClips(ctx, term)
}

// HandleJob gestisce un job dalla coda.
func (s *Service) HandleJob(ctx context.Context, job *models.Job, tools *jobs.JobTools) (map[string]any, error) {
	return s.jobAdapter.HandleJob(ctx, job, tools)
}

// GetJobByRunID ottiene un job per run ID.
func (s *Service) GetJobByRunID(ctx context.Context, runID string) (*models.Job, error) {
	return s.jobAdapter.GetJobByRunID(ctx, runID)
}
