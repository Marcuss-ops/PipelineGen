package artlist

import (
	"context"
	"database/sql"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ServicePorts collects the four canonical ports PR2.1-PR2.7 lifted out
// of the legacy concrete dependencies. Sized at 4 fields — well under
// the AGENTS.md 10-per-bundle cap.
//
// The composition root in module_artlist.go builds the SemanticEnricher
// first (so its dispatcher hookup is captured at creation) and wires it
// here as MetadataWriter. AssetStore is satisfied by *assets.ClipsRepository;
// Indexer is satisfied by *clipindexer.Service directly (the port declares
// IndexClip + IsEnabled which both implementations provide);
// DriveFolderManager (PR2.7) is satisfied by *drive.DriveFolderManagerAdapter
// which wraps *assetsapi.Service so callers never see SDK types.
type ServicePorts struct {
	AssetStore     AssetStore
	Indexer        Indexer
	MetadataWriter MetadataWriter
	// DriveFolderManager is the wide Drive port (PR2.7). Replaces
	// the raw *assetsapi.Service concrete that previously lived in
	// ServiceDependencies as "DriveClient". Hides the SDK from
	// callers (semantic_enricher, destination_service) so the
	// application layer no longer reaches through a concrete to
	// call Files.List/Trash/Download/Create.
	DriveFolderManager DriveFolderManager
	// PR2: Searcher implementations injected from infrastructure.
	// Nil means that level is skipped in the fallback chain.
	ScraperSearcher Searcher
	PixabaySearcher Searcher
	PexelsSearcher  Searcher
}

// ServiceDependencies collects the cross-cutting dependencies that are
// not yet portified: tracker/Oracle scopes concrete domain services
// that pre-date the ports effort. Sized at 11 fields — slightly above
// AGENTS.md's 10-per-bundle cap. The PR2.7 directive accepts this
// because Dispatcher is the only remaining concrete integration point
// (the media_index_outbox dispatcher) and any portification would
// just rename it; PR2.7 retired DriveClient, the previous concrete
// transport integration point, by lifting it into ServicePorts as
// DriveFolderManager.
//
// Cfg, MainDB, Log are pure data. Dispatcher is a transport integration
// point. MediaProcessor, LifecycleService, AssetDestResolver, JobsSvc
// are cross-cutting domain services that already implement interfaces
// in internal/core but whose concrete instances the application holds
// directly. AssetProcRepo / AssetVerRepo / AssetLocRepo are the
// canonical asset-lifecycle repositories from internal/domain/asset.
//
// ArtlistDB was removed (PR2.6): after the media.db.sqlite unification,
// MainDB is the only DB handle in the system.
//
// PR2.5+PR2.6+PR2.7 notes:
//   - No setters; all dependencies are constructor arguments.
//   - Field promotion makes the embedded-syntax construction
//     `ServiceDeps{AssetStore: ..., Cfg: ..., MainDB: ...}` work without
//     explicitly naming ServicePorts / ServiceDependencies at the call
//     site, which keeps the test fixtures terse.
type ServiceDependencies struct {
	Cfg               *config.Config
	MainDB            *sql.DB
	Log               *zap.Logger
	Dispatcher        Dispatcher
	MediaProcessor    asset.Processor
	LifecycleService  *lifecycle.Service
	AssetDestResolver asset.Resolver
	JobsSvc           jobs.Service
	AssetProcRepo     asset.ProcessingRepository
	AssetVerRepo      asset.VersionRepository
	AssetLocRepo      asset.LocationRepository
}

// ServiceDeps is the canonical constructor input for artlist.Service.
//
// PR2.6: split into ServicePorts (3) + ServiceDependencies (12) so the
// per-bundle field budget from AGENTS.md is respected at the port level
// (3/10) and the cross-cutting surface is grouped separately (12/10,
// accepted by the PR2.6 directive because the cross-cutting surface
// mixes data, transport, and Domain repos that don't fit a single
// coherent "port" abstraction). ServiceDeps embeds both via field
// promotion so callers can construct it in two equivalent shapes:
//
//	NewService(ServiceDeps{
//	    AssetStore: repo, Cfg: cfg, MainDB: db, Log: log,
//	    // explicit field promotion; terse for tests
//	})
//
//	NewService(ServiceDeps{
//	    ServicePorts:        ServicePorts{AssetStore: repo, ...},
//	    ServiceDependencies: ServiceDependencies{Cfg: cfg, ...},
//	    // named sub-structs; explicit for production wiring
//	})
//
// PR2.5: SetSemanticEnricher + SetDispatcher setters removed; Dispatcher
// is a constructor argument wired through the composition root.
type ServiceDeps struct {
	ServicePorts
	ServiceDependencies
}

// Service è un facade leggero che delega a componenti specializzati.
// Non implementa direttamente la logica, ma coordina i servizi sottostanti.
//
// PR2.5: the legacy setters SetSemanticEnricher + SetDispatcher are
// removed. All dependencies (AssetStore, Indexer, MetadataWriter,
// Dispatcher) are constructor arguments on ServiceDeps.
//
// PR2.6: artlistDB field dropped — after the media.db.sqlite
// consolidation it equals MainDB and the extra pointer duplicated state.
type Service struct {
	cfg    *config.Config
	mainDB *sql.DB
	log    *zap.Logger

	// L1: in-memory cache per risultati live (evita rilanci di Playwright per term già ricercati di recente)
	liveCache *liveSearchCache

	// Componenti delegati
	searchService      *SearchService
	runOrchestrator    *RunOrchestratorService
	destinationService *DestinationService
	jobAdapter         *JobAdapter
	diagnosticsService *DiagnosticsService

	// Canonical ports — wired via ServiceDeps.ServicePorts. Application
	// owns the policy; infrastructure owns the SDK / DB connection
	// behind them.
	assetStore     AssetStore
	indexer        Indexer
	metadataWriter MetadataWriter

	// Dispatcher is the canonical outbox dispatcher; nil means
	// dispatchBridge falls back to the legacy UpsertClip + IndexClip
	// pair (see dispatch_bridge.go). Wired via ServiceDeps.Dispatcher.
	dispatcher Dispatcher

	// driveFolderManager is the canonical DriveFolderManager port
	// (PR2.7). Replaces the raw *assetsapi.Service concrete that
	// lived here pre-PR2.7. The adapter
	// (DriveFolderManagerAdapter in internal/infrastructure/drive)
	// wraps the SDK so callers (semantic_enricher,
	// destination_service) never see *driveapi types. Wired via
	// ServiceDeps.ServicePorts.DriveFolderManager.
	driveFolderManager DriveFolderManager

	// PR2: infrastructure Searcher implementations for the fallback chain.
	scraperSearcher Searcher
	pixabaySearcher Searcher
	pexelsSearcher  Searcher

	// Cross-cutting domain services.
	mediaProcessor    asset.Processor
	lifecycleService  *lifecycle.Service
	assetDestResolver asset.Resolver
	jobsSvc           jobs.Service

	// Asset lifecycle repositories (canonical model — wired per codex/wire-asset-lifecycle)
	assetProcessing asset.ProcessingRepository
	assetVersions   asset.VersionRepository

	// Asset locations: canonical source of truth for local/drive paths.
	assetLocRepo asset.LocationRepository
}

// NewService crea una nuova istanza del servizio Artlist come facade.
// All dependencies are reachable through ServiceDeps via field promotion
// from ServicePorts + ServiceDependencies, so callers can construct it
// either with terse flat construction (tests) or explicit named
// sub-structs (production wiring in module_artlist.go).
func NewService(deps ServiceDeps) (*Service, error) {
	s := &Service{
		cfg:                deps.Cfg,
		mainDB:             deps.MainDB,
		log:                deps.Log,
		assetStore:         deps.AssetStore,
		indexer:            deps.Indexer,
		metadataWriter:     deps.MetadataWriter,
		dispatcher:         deps.Dispatcher,
		driveFolderManager: deps.DriveFolderManager,
		mediaProcessor:     deps.MediaProcessor,
		lifecycleService:   deps.LifecycleService,
		assetDestResolver:  deps.AssetDestResolver,
		jobsSvc:            deps.JobsSvc,
		liveCache:          newPersistentLiveSearchCache(deps.MainDB, deps.Log),
		assetProcessing:    deps.AssetProcRepo,
		assetVersions:      deps.AssetVerRepo,
		assetLocRepo:       deps.AssetLocRepo,
	}

	// Inizializza i componenti delegati
	s.searchService = NewSearchService(s, deps.Dispatcher)
	s.runOrchestrator = NewRunOrchestratorService(s)
	s.destinationService = NewDestinationService(s)
	s.jobAdapter = NewJobAdapter(s)
	s.diagnosticsService = NewDiagnosticsService(s)

	return s, nil
}

// Close è un no-op poiché la connessione DB è gestita esternamente
// dal composition root (vedi internal/app/composition.go::NewComposition).
func (s *Service) Close() error {
	return nil
}

// Search esegue una ricerca di clip.
func (s *Service) Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error) {
	return s.searchService.Search(ctx, req)
}

// SearchLive esegue una ricerca live tramite scraper.
func (s *Service) SearchLive(ctx context.Context, term string, limit int) ([]Candidate, error) {
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
