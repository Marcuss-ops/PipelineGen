package artlist

import (
	"context"
	"database/sql"

	"go.uber.org/zap"
	driveapi "google.golang.org/api/drive/v3"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/config"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
)

// ServiceDeps is the canonical constructor input for artlist.Service.
//
// PR2.5: the previous NewService took 14 positional arguments, mirroring
// legacy composition. The argument list contained a mix of:
//
//   - pure data (cfg, log, *sql.DB) — kept as plain fields
//   - canonical Ports (AssetStore, Indexer, MetadataWriter) — promoted
//     from concrete dependencies so the application layer no longer imports
//     the infrastructure types it used to embed
//   - cross-cutting Domain services (MediaProcessor, LifecycleService,
//     Resolver, jobs service, asset-lifecycle repos) — kept concrete for
//     now; their portification is tracked as a follow-up in PR2.6+
//
// Dispatcher is the canonical media_index_outbox dispatcher used by the
// dispatchBridge to perform UpsertClip + IndexClip atomically. Service
// holds a reference so RunOrchestrator.stagePersistResults can route
// results through the canonical enqueue path; SemanticEnricher holds an
// independent reference for the same reason (its Enrich() method must
// keep the dispatcher reachable even when called outside of the
// orchestrator's stage flow). Both refs point at the same instance in
// production wiring.
type ServiceDeps struct {
	Cfg       *config.Config
	MainDB    *sql.DB
	ArtlistDB *sql.DB
	Log       *zap.Logger

	// Canonical ports — implementations are wired by the composition root.
	AssetStore     AssetStore
	Indexer        Indexer
	MetadataWriter MetadataWriter

	// Dispatcher is the canonical outbox dispatcher used by both
	// dispatchBridge (in Service) and SemanticEnricher.Enrich. The
	// duplication is intentional: each path constructs its own
	// dispatchBridge{} and both end up at dispatcher.EnqueueAndIndex.
	Dispatcher *outbox.Dispatcher

	// DriveClient is the raw Google Drive SDK service. Stays concrete
	// because semantic_enricher uses raw SDK calls (Files.List,
	// TrashFile, DownloadFile, UploadFile) that don't fit the
	// narrow Uploader port surface. PR2.6 will lift the wider surface
	// into a dedicated port if it stabilises.
	DriveClient *driveapi.Service

	// Domain services not yet portified.
	MediaProcessor    asset.Processor
	LifecycleService  *lifecycle.Service
	AssetDestResolver asset.Resolver
	JobsSvc           *jobs.Service
	AssetProcRepo     asset.ProcessingRepository
	AssetVerRepo      asset.VersionRepository
	AssetLocRepo      asset.LocationRepository
}

// Service è un facade leggero che delega a componenti specializzati.
// Non implementa direttamente la logica, ma coordina i servizi sottostanti.
//
// PR2.5: the legacy setters SetSemanticEnricher + SetDispatcher are
// removed. All dependencies (AssetStore, Indexer, MetadataWriter,
// Dispatcher) are constructor arguments on ServiceDeps. NewService is
// idempotent — every dependency lives on the struct from the start.
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

	// Canonical ports — wired via ServiceDeps. Application owns the
	// policy; infrastructure owns the SDK / DB connection behind them.
	assetStore     AssetStore
	indexer        Indexer
	metadataWriter MetadataWriter

	// Dispatcher is the canonical outbox dispatcher; nil means
	// dispatchBridge falls back to the legacy UpsertClip + IndexClip
	// pair (see dispatch_bridge.go). Mirrors the contract that the
	// pre-PR2.5 SetDispatcher setter used; now constructed once at
	// composition-time instead of late-bound.
	dispatcher *outbox.Dispatcher

	// driveClient is the raw Google Drive SDK service. Concrete
	// dependency used by DestinationService (to build the
	// *drive.Uploader wrapper for EnsureFolder calls) and by
	// SemanticEnricher (for Files.List/TrashFile/DownloadFile/
	// UploadFile during metadata.json cumulative sync). No setter —
	// injected at construction time via ServiceDeps.
	driveClient *driveapi.Service

	// Cross-cutting domain services.
	mediaProcessor    asset.Processor
	lifecycleService  *lifecycle.Service
	assetDestResolver asset.Resolver
	jobsSvc           *jobs.Service

	// Asset lifecycle repositories (canonical model — wired per codex/wire-asset-lifecycle)
	assetProcessing asset.ProcessingRepository
	assetVersions   asset.VersionRepository

	// Asset locations: canonical source of truth for local/drive paths.
	assetLocRepo asset.LocationRepository
}

// NewService crea una nuova istanza del servizio Artlist come facade.
// All dependencies (ports, dispatcher, domain services) are injected via
// ServiceDeps — no setters, no late-binding. The composition root in
// module_artlist.go is the single place that constructs the
// SemanticEnricher first (so its dispatcher hookup is captured at
// creation) and then wires it as MetadataWriter on ServiceDeps.
func NewService(deps ServiceDeps) (*Service, error) {
	s := &Service{
		cfg:               deps.Cfg,
		mainDB:            deps.MainDB,
		artlistDB:         deps.ArtlistDB,
		log:               deps.Log,
		assetStore:        deps.AssetStore,
		indexer:           deps.Indexer,
		metadataWriter:    deps.MetadataWriter,
		dispatcher:        deps.Dispatcher,
		driveClient:       deps.DriveClient,
		mediaProcessor:    deps.MediaProcessor,
		lifecycleService:  deps.LifecycleService,
		assetDestResolver: deps.AssetDestResolver,
		jobsSvc:           deps.JobsSvc,
		liveCache:         newPersistentLiveSearchCache(deps.MainDB, deps.Log),
		assetProcessing:   deps.AssetProcRepo,
		assetVersions:     deps.AssetVerRepo,
		assetLocRepo:      deps.AssetLocRepo,
	}

	// Inizializza i componenti delegati
	s.searchService = NewSearchService(s)
	s.runOrchestrator = NewRunOrchestratorService(s)
	s.destinationService = NewDestinationService(s)
	s.jobAdapter = NewJobAdapter(s)
	s.diagnosticsService = NewDiagnosticsService(s)

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
func (s *Service) SearchClips(ctx context.Context, term string) []*asset.Asset {
	return s.searchService.SearchClips(ctx, term)
}

// HandleJob gestisce un job dalla coda.
func (s *Service) HandleJob(ctx context.Context, j *jobs.Job, tools *appjobs.JobTools) (map[string]any, error) {
	return s.jobAdapter.HandleJob(ctx, j, tools)
}

// GetJobByRunID ottiene un job per run ID.
func (s *Service) GetJobByRunID(ctx context.Context, runID string) (*jobs.Job, error) {
	return s.jobAdapter.GetJobByRunID(ctx, runID)
}
