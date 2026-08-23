package artlist

import (
	"database/sql"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/application/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ErrTranscriberUnavailable is returned when the mandatory Transcriber
// port is not wired at composition time.
var ErrTranscriberUnavailable = fmt.Errorf("artlist: transcriber is mandatory but not wired")

// ErrTextTrackRepoUnavailable is returned when the mandatory
// asset.TextTrackRepository port is not wired at composition time.
var ErrTextTrackRepoUnavailable = fmt.Errorf("artlist: textTrackRepo is mandatory but not wired")

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
//
// Phase 7 split (split-by-capability: lookup / normalizer / cached
// search / retry/fallback). This file holds ONLY the canonical
// Service struct definition, the fail-closed constructor NewService,
// and the lifecycle no-op Close. The constructor input shape
// (ServiceDeps + ServicePorts + ServiceDependencies + sub-bundle
// types) lives in service_deps.go. The lookup delegations live in
// service_delegates.go. The job-handler binding lives in
// service_jobs.go. The single-URL import flow lives in
// service_import.go. The import-response + URL/asset helpers live in
// service_normalizer.go. The cached-search + retry/fallback
// capability declarations live in cached_search.go + retry_fallback.go.
type Service struct {
	cfg *config.Config
	log *zap.Logger

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

	// LocalSearcher is the composed SQLite search adapter. The application
	// consumes only the Searcher port; concrete SQL stays in infrastructure.
	localSearcher Searcher

	// PR2: infrastructure Searcher implementations for the fallback chain.
	scraperSearcher Searcher
	pixabaySearcher Searcher
	pexelsSearcher  Searcher

	// detailFetcher fetches rich metadata for a single Artlist clip page.
	// Optional; when nil the import endpoint fails closed.
	detailFetcher DetailFetcher

	// Cross-cutting domain services.
	mediaProcessor    asset.Processor
	assetDestResolver asset.Resolver
	jobsSvc           *appjobs.Service

	// Asset lifecycle repositories (canonical model — wired per codex/wire-asset-lifecycle)
	assetProcessing asset.ProcessingRepository
	assetVersions   asset.VersionRepository

	// assetFinalizer is the canonical transactional asset finalizer.
	// It writes media_assets, asset_versions, asset_locations, and
	// asset_renditions inside the caller's transaction.
	assetFinalizer finalization.AssetFinalizerTx

	// mainDB is the canonical SQLite handle used to open the
	// transaction passed to assetFinalizer.
	mainDB *sql.DB

	// stager is the canonical acquisition.SourceStager port. Optional.
	stager acquisition.SourceStager

	// isLiveProbe is the canonical runtime liveness probe port
	// (PR-ARTLIST-LIVE-WIRE, July 2026). Wired via deps.IsLiveProbe.
	// When nil, callers should treat the probe capability as
	// unavailable — the WireArtlist 4 mandatory gates stay
	// unchanged per godlike/07.
	isLiveProbe IsLiveProbe

	// runRepo is the canonical writer for artlist_runs aggregate
	// stats (PR-ARTLIST-PERSIST-FIX, 2026-07-04). MANDATORY at
	// composition: NewService rejects nil with
	// ErrRunRepositoryUnavailable (fail-closed).
	runRepo RunRepository

	// conceptRepo, bindingRepo, and normalizer are optional ports used
	// to create media_concepts / media_bindings after a clip is
	// materialized. When nil, linking is skipped (graceful degradation).
	conceptRepo mediamemory.ConceptRepository
	bindingRepo mediamemory.BindingRepository
	normalizer  mediamemory.Normalizer

	// transcriber extracts the audio transcript from a downloaded clip.
	// Mandatory for all Artlist downloads (PR-ARTLIST-MANDATORY-TRANSCRIPTION,
	// July 2026); nil is rejected by NewService.
	transcriber Transcriber

	// textTrackRepo persists audio transcripts for downloaded clips.
	// Mandatory for all Artlist downloads (PR-ARTLIST-MANDATORY-TRANSCRIPTION,
	// July 2026); nil is rejected by NewService.
	textTrackRepo asset.TextTrackRepository

	// systemProber is the canonical godlike/06 SystemProber port
	// (Fase 2, July 2026). nil means the diagnostics endpoint reports
	// every probe as failed via the fallback stubSystemProber
	// (DiagnosticsService constructor wires the fallback defensively).
	// Composition-time wiring in build_bundles_artlist.go MUST
	// provide a real AdminSystemProber; the fallback exists only for
	// test fixtures / unusual composition paths.
	systemProber SystemProber

	// locationRepo persists physical asset locations (Wave C / July 2026).
	// Used by stagePersistResults to record rendition locations.
	locationRepo asset.LocationRepository

	// renditionRepo persists asset rendition metadata (July 2026).
	// Used by stagePersistResults to record generated renditions.
	renditionRepo asset.RenditionRepository

	// searchStrategy controls the Pexels/Pixabay fallback chain
	// (PR-AUDIT-5, July 2026). Wired from deps.SearchStrategy.
	// Zero-value (empty string) defaults to artlist_only.
	searchStrategy ArtlistSearchStrategy

	// hasConsumer is the operator-facing consumer-alive flag
	// (PR-P2-FAILCLOSED-JOB, July 2026). Set TRUE by RegisterHandler
	// after a successful bind; FALSE on bind failure or when never
	// bound. Read by /api/artlist/job-consumer via Service.HasConsumer.
	hasConsumer bool
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
	// PR-ARTLIST-PERSIST-FIX (2026-07-04): mandatory RunRepository at
	// composition per godlike/07 no-fake-availability. Without this
	// gate, /api/artlist/run can return SUCCEEDED counts without ever
	// writing a single row to artlist_runs (the original fake-success
	// bug). The wire site in build_bundles_artlist.go MUST provide a
	// non-nil concrete (sqlite-backed); test fixtures may pass a
	// noopRunRepo implementation.
	if deps.RunRepository == nil {
		return nil, ErrRunRepositoryUnavailable
	}
	// PR-ARTLIST-MANDATORY-TRANSCRIPTION (July 2026): the transcriber
	// and text-track repo are mandatory for all Artlist downloads.
	// Fail-closed at construction so an unwired deployment cannot
	// silently produce clips without transcripts.
	if deps.Transcriber == nil {
		return nil, ErrTranscriberUnavailable
	}
	if deps.Repos.TextTrackRepo == nil {
		return nil, ErrTextTrackRepoUnavailable
	}
	s := &Service{
		cfg:               deps.Infra.Cfg,
		log:               deps.Infra.Log,
		assetStore:        deps.AssetStore,
		indexer:           deps.Indexer,
		metadataWriter:    deps.MetadataWriter,
		dispatcher:        deps.Ports.Dispatcher,
		publisher:         deps.Publisher,
		mediaProcessor:    deps.Domain.MediaProcessor,
		assetDestResolver: deps.Domain.AssetDestResolver,
		jobsSvc:           deps.Domain.JobsSvc,
		localSearcher:     deps.LocalSearcher,
		scraperSearcher:   deps.ScraperSearcher,
		pixabaySearcher:   deps.PixabaySearcher,
		pexelsSearcher:    deps.PexelsSearcher,
		detailFetcher:     deps.DetailFetcher,
		searchStrategy:    deps.SearchStrategy,
		liveCache:         newLiveSearchCache(),
		assetProcessing:   deps.Repos.AssetProcRepo,
		assetVersions:     deps.Repos.AssetVerRepo,
		assetFinalizer:    deps.Finalizer.AssetFinalizerTx,
		mainDB:            deps.Infra.MainDB,
		stager:            deps.Stager,
		isLiveProbe:       deps.IsLiveProbe,
		runRepo:           deps.RunRepository,
		systemProber:      deps.SystemProber,
		conceptRepo:       deps.MediaMemoryConceptRepo,
		bindingRepo:       deps.MediaMemoryBindingRepo,
		normalizer:        deps.MediaMemoryNormalizer,
		locationRepo:      deps.Repos.LocationRepository,
		renditionRepo:     deps.Repos.RenditionRepository,
		transcriber:       deps.Transcriber,
		textTrackRepo:     deps.Repos.TextTrackRepo,
	}

	// Surface the QDRANT-002 nil-dispatcher guard at construction
	// time. The composition root in module_sources.go::WireArtlist
	// already pre-rejects nil dispatcher with the same sentinel;
	// if it bubbles up here it means the composition wiring is
	// wrong and we must not start the service with a half-built
	// SearchService.
	var err error
	s.searchService, err = NewSearchService(s, deps.Ports.Dispatcher)
	if err != nil {
		return nil, fmt.Errorf("NewSearchService: %w", err)
	}
	s.runOrchestrator = NewRunOrchestratorService(s)
	s.destinationService = NewDestinationService(s)
	s.jobAdapter = NewJobAdapter(s)
	// Phase 2 (Fase 2): second arg is the SystemProber port (was nil pre-Phase 2).
	// nil falls back to stubSystemProber (every probe fails) rather than allowing
	// the v1 fake-availability `OK: true` aggregate to leak into the v2 endpoint.
	s.diagnosticsService = NewDiagnosticsService(s, s.systemProber)

	return s, nil
}

// Close è un no-op poiché la connessione DB è gestita esternamente
// dal composition root (vedi internal/app/composition.go::NewComposition).
func (s *Service) Close() error {
	return nil
}
