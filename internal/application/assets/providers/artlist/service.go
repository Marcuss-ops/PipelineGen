package artlist

import (
	"context"
	"database/sql"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ServicePorts collects the canonical ports PR2.1-PR2.7+lifted out
// of the legacy concrete dependencies. Sized at 3 fields (post-F2.11
// DriveFolderManager retirement) — well under the AGENTS.md 10-per-bundle cap.
//
// F2.11 (June 2026): DriveFolderManager port field was RETIRED
// (override brutal). The composition root no longer constructs
// *drive.DriveFolderManagerAdapter; every Drive write from artlist
// routes through delivery.Publisher (the canonical write canal per
// DRIVE-005 closure). The legacy `else if driveManager != nil` branch
// in DestinationService.ResolveDestination + the silent
// `folderID = rootFolderID` fallback are gone — a missing Publisher
// at composition is now the fail-closed wiring error
// ErrPublisherUnavailable (mirrors QDRANT-002 PR7 composition-time
// dispatcher guard).
type ServicePorts struct {
	AssetStore     AssetStore
	Indexer        Indexer
	MetadataWriter MetadataWriter
	// Publisher is the canonical Drive upload/folder-resolution canal
	// (FASE 8, June 2026; F2.11: now MANDATORY at composition per the
	// brutal-override user spec). Used by DestinationService via
	// PublisherPort for folder-only resolution. A nil Publisher fails
	// NewService with ErrPublisherUnavailable (composition-time fail-
	// closed; defense-in-depth with the WireArtlist pre-rejection).
	Publisher delivery.Publisher
	// PR2: Searcher implementations injected from infrastructure.
	// Nil means that level is skipped in the fallback chain.
	ScraperSearcher Searcher
	PixabaySearcher Searcher
	PexelsSearcher  Searcher
	// Stager is the shared SourceStager port (Step 9/12 wire-up, July 2026).
	// Optional — when non-nil, stageProcessBatch uses it as the canonical
	// source-staging surface (wrapping the Artlist Downloader port) so
	// Artlist demonstrates the same SourceStager contract used by YouTube
	// and stock. When nil, stageProcessBatch falls through to the legacy
	// mediaProcessor.Process pipeline without breaking.
	Stager assets.SourceStager
	// IsLiveProbe is the canonical runtime liveness probe port
	// (PR-ARTLIST-LIVE-WIRE, July 2026; godlike/06 SSOT owner of the
	// HTTP self-loop surface). Optional — the WireArtlist composition
	// site constructs a *HTTPSelfLoopProbe (http_live_probe.go) that
	// pings GET /api/artlist/stats with a configurable timeout; when
	// nil, callers should treat the live-probe capability as
	// unavailable (no panic — the WireArtlist 4 mandatory gates stay
	// unchanged per godlike/07). Test fixtures may pass nil.
	IsLiveProbe IsLiveProbe
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
//
// PR2.7 → F2.11 (June 2026): driveFolderManager field RETIRED
// (override brutal). The legacy DriveFolderManager port's
// EnsureFolder / ListByQuery / Download / Upload methods now fan out
// through delivery.Publisher (write surface) + drive.Reader (read
// surface) per DRIVE-005 closure's "one owner per fact" rule.
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

	// publisher is the canonical Drive upload/folder-resolution canal
	// (FASE 8 → F2.11). F2.11 made it mandatory at composition
	// time: a nil Publisher fails NewService with ErrPublisherUnavailable
	// (composition-time fail-closed). The legacy driveFolderManager
	// field (DriveFolderManager port) was retired from this struct
	// entirely; folder-only resolution routes through this Publisher
	// via PublisherPort in DestinationService, and Drive writes
	// elsewhere use Publisher.Publish directly.
	publisher delivery.Publisher

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

	// stager is the shared SourceStager port (Step 9/12 wire-up). Optional.
	stager assets.SourceStager

	// isLiveProbe is the canonical runtime liveness probe port
	// (PR-ARTLIST-LIVE-WIRE, July 2026). Wired via deps.IsLiveProbe.
	// When nil, callers should treat the probe capability as
	// unavailable — the WireArtlist 4 mandatory gates stay
	// unchanged per godlike/07.
	isLiveProbe IsLiveProbe
}

// NewService crea una nuova istanza del servizio Artlist come facade.
// All dependencies are reachable through ServiceDeps via field promotion
// from ServicePorts + ServiceDependencies, so callers can construct it
// either with terse flat construction (tests) or explicit named
// sub-structs (production wiring in module_sources.go::WireArtlist).
//
// F2.11 (June 2026): Publisher is now a mandatory composition-time
// dependency. A nil Publisher returns ErrPublisherUnavailable (the
// brutal-override fail-closed sentinel); the legacy fallback path
// that tolerated a missing Publisher (route through DriveFolderManager,
// then silently fall back to `folderID = rootFolderID`) is gone. The
// WireArtlist composition site in module_sources.go::WireArtlist
// pre-rejects the nil Publisher at the composition boundary; NewService
// is the second line of defence so accidental misuse from tests still
// fails loud at construction time rather than at first request.
func NewService(deps ServiceDeps) (*Service, error) {
	if deps.Publisher == nil {
		return nil, ErrPublisherUnavailable
	}
	s := &Service{
		cfg:                deps.Cfg,
		mainDB:             deps.MainDB,
		log:                deps.Log,
		assetStore:         deps.AssetStore,
		indexer:            deps.Indexer,
		metadataWriter:     deps.MetadataWriter,
		dispatcher:         deps.Dispatcher,
		publisher:          deps.Publisher,
		mediaProcessor:     deps.MediaProcessor,
		lifecycleService:   deps.LifecycleService,
		assetDestResolver:  deps.AssetDestResolver,
		jobsSvc:            deps.JobsSvc,
		scraperSearcher:    deps.ScraperSearcher,
		pixabaySearcher:    deps.PixabaySearcher,
		pexelsSearcher:     deps.PexelsSearcher,
		liveCache:          newPersistentLiveSearchCache(deps.MainDB, deps.Log),
		assetProcessing:    deps.AssetProcRepo,
		assetVersions:      deps.AssetVerRepo,
		assetLocRepo:       deps.AssetLocRepo,
		stager:             deps.Stager,
		isLiveProbe:        deps.IsLiveProbe,
	}

	// Inizializza i componenti delegati
	// Surface the QDRANT-002 nil-dispatcher guard at construction
	// time. The composition root in module_sources.go::WireArtlist
	// already pre-rejects nil dispatcher with the same sentinel;
	// if it bubbles up here it means the composition wiring is
	// wrong and we must not start the service with a half-built
	// SearchService.
	var err error
	s.searchService, err = NewSearchService(s, deps.Dispatcher)
	if err != nil {
		return nil, fmt.Errorf("NewSearchService: %w", err)
	}
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

// RegisterHandler registers the Artlist job handler (HandleJob) with the
// canonical jobs.Service dispatcher for TypeArtlistRun ("media.artlist").
// The composition root (WireArtlistJobBindings in build_bundles_artlist.go)
// calls this method after WireArtlist completes, mirroring the
// catalog/youtube RegisterHandler precedent in build_bundles_youtube.go.
func (s *Service) RegisterHandler(jobsSvc *appjobs.Service) error {
	return s.jobAdapter.RegisterHandler(jobsSvc)
}

// GetJobByRunID ottiene un job per run ID.
func (s *Service) GetJobByRunID(ctx context.Context, runID string) (*jobs.Job, error) {
	return s.jobAdapter.GetJobByRunID(ctx, runID)
}
