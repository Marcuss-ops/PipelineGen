package artlist

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
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
	// DetailFetcher fetches rich metadata for a single Artlist clip page.
	// Optional — when nil the import endpoint returns ErrUnavailable.
	DetailFetcher DetailFetcher
	// Stager is the shared SourceStager port (Step 9/12 wire-up, July 2026).
	// Optional — when non-nil, stageProcessBatch uses it as the canonical
	// source-staging surface (wrapping the Artlist Downloader port) so
	// Artlist demonstrates the same SourceStager contract used by YouTube
	// and stock. When nil, stageProcessBatch falls through to the legacy
	// mediaProcessor.Process pipeline without breaking.
	Stager assets.SourceStager
	// SearchStrategy controls the Pexels/Pixabay fallback chain (PR-AUDIT-5,
	// July 2026). The strategy is wired from cfg.External.ArtlistSearchStrategy
	// at composition time. Zero-value defaults to artlist_only (the safest
	// default — no external stock sources without explicit operator opt-in).
	SearchStrategy ArtlistSearchStrategy
	// IsLiveProbe is the canonical runtime liveness probe port
	// (PR-ARTLIST-LIVE-WIRE, July 2026; godlike/06 SSOT owner of the
	// HTTP self-loop surface). Optional — the WireArtlist composition
	// site constructs a *HTTPSelfLoopProbe (http_live_probe.go) that
	// pings GET /api/artlist/stats with a configurable timeout; when
	// nil, callers should treat the live-probe capability as
	// unavailable (no panic — the WireArtlist 4 mandatory gates stay
	// unchanged per godlike/07). Test fixtures may pass nil.
	IsLiveProbe IsLiveProbe
	// RunRepository is the canonical writer for the artlist_runs
	// aggregate table (PR-ARTLIST-PERSIST-FIX, 2026-07-04). MANDATORY
	// at composition: NewService rejects nil with
	// ErrRunRepositoryUnavailable (fail-closed discipline; mirrors
	// Publisher + Dispatcher). Production wires the SQLite-backed
	// concrete from internal/infrastructure/database/sqlite/assets.
	RunRepository RunRepository
	// SystemProber is the canonical godlike/06 port (Fase 2, July 2026)
	// that fans out the 10 wire-by-wire diagnostic probes
	// (scraper / browser / session / downloader / ffmpeg_binary /
	// drive_folder / sqlite_writable / outbox_dispatcher /
	// qdrant_reachable / embedding_provider). Composition root injects
	// an *AdminSystemProber concrete from
	// internal/infrastructure/artlist/diagnostics; tests can pass
	// probe stubs (or rely on the fallback stubSystemProber in
	// NewDiagnosticsService, which reports every probe as failed
	// rather than fake-availability).
	SystemProber SystemProber
}

// ServiceDependencies collects the cross-cutting dependencies that are
// not yet portified, grouped into coherent sub-bundles to respect
// AGENTS.md's per-struct field cap. Each sub-bundle is a named struct
// so production wiring and test fixtures can be explicit without
// exceeding the 8-field limit.
//
// PR2.5+PR2.6+PR2.7 notes:
//   - No setters; all dependencies are constructor arguments.
//   - Field promotion makes the embedded-syntax construction
//     `ServiceDeps{AssetStore: ..., Cfg: ..., MainDB: ...}` work without
//     explicitly naming ServicePorts / ServiceDependencies at the call
//     site, which keeps the test fixtures terse.
type ServiceDependencies struct {
	Infra     ArtlistInfraDeps
	Ports     ArtlistPortDeps
	Domain    ArtlistDomainDeps
	Repos     ArtlistRepoDeps
	Finalizer ArtlistFinalizerDeps
}

// ArtlistInfraDeps groups the infrastructure-like dependencies.
type ArtlistInfraDeps struct {
	Cfg    *config.Config
	Log    *zap.Logger
	MainDB *sql.DB
}

// ArtlistPortDeps groups the port-like dependencies.
type ArtlistPortDeps struct {
	Dispatcher Dispatcher
}

// ArtlistDomainDeps groups the cross-cutting domain services.
type ArtlistDomainDeps struct {
	MediaProcessor    asset.Processor
	AssetDestResolver asset.Resolver
	JobsSvc           *appjobs.Service
}

// ArtlistRepoDeps groups the asset lifecycle repositories.
type ArtlistRepoDeps struct {
	AssetProcRepo       asset.ProcessingRepository
	AssetVerRepo        asset.VersionRepository
	LocationRepository  asset.LocationRepository
	RenditionRepository asset.RenditionRepository
}

// ArtlistFinalizerDeps groups the transactional finalizer dependencies.
type ArtlistFinalizerDeps struct {
	// AssetFinalizerTx is the canonical transactional asset finalizer
	// (Wave C / July 2026). Artlist uses it to write media_assets,
	// asset_versions, asset_locations, and asset_renditions inside a
	// single transaction, replacing the legacy dispatchBridge path.
	AssetFinalizerTx finalization.AssetFinalizerTx
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

	// stager is the shared SourceStager port (Step 9/12 wire-up). Optional.
	stager assets.SourceStager

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
		locationRepo:      deps.Repos.LocationRepository,
		renditionRepo:     deps.Repos.RenditionRepository,
	}

	// Inizializza i componenti delegati
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

// Search esegue una ricerca di clip.
func (s *Service) Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error) {
	return s.searchService.Search(ctx, req)
}

// SearchLive esegue una ricerca live tramite scraper.
//
// preferRemote (PR-P2-SEARCH-LIVE, July 2026): when true, the chain is
// reordered so the Node ScraperSearcher is the PRIMARY provider and
// BOTH the local DB-level cache (DBSearcher, indexed terms) AND the
// in-memory TTL cache (CachedSearcher wrapper around scraper) are
// COMPLETELY DROPPED from the chain.
//
// prefer_remote defaulting is endpoint-scoped (user spec): the
// GET /api/artlist/search/live handler defaults to true (operator-
// facing live-search semantics). Internal callers must pass false:
//   - DiscoverAndQueueRun: explicit RunTag workflow → false (legacy
//     cache-first "discover fresh content" semantics is preserved).
//   - run_orchestrator_stages::stageDiscoverClips → SearchLiveAndSave
//     → SearchLive: false (orchestrator retries must not re-hit the
//     scraper for the same term).
func (s *Service) SearchLive(ctx context.Context, term string, limit int, preferRemote bool) ([]Candidate, error) {
	return s.searchService.SearchLive(ctx, term, limit, preferRemote)
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
func (s *Service) HandleJob(ctx context.Context, j *appjobs.Job, tools *appjobs.JobTools) (map[string]any, error) {
	return s.jobAdapter.HandleJob(ctx, j, tools)
}

// RegisterHandler registers the Artlist job handler (HandleJob) with the
// canonical appjobs.Service dispatcher for TypeArtlistRun ("media.artlist").
// The composition root (WireArtlistJobBindings in build_bundles_artlist.go)
// calls this method after WireArtlist completes, mirroring the
// catalog/youtube RegisterHandler precedent in build_bundles_youtube.go.
//
// PR-P2-FAILCLOSED-JOB (July 2026): on success the receiver ALSO
// tracks hasConsumer=true so the new /api/artlist/job-consumer
// health endpoint (artlist_handlers.go::JobConsumer) can read the
// consumer state without coupling to the appjobs.Service surface
// shape. The handler-side check is the source of truth for "is
// media.artlist consumed?" — operator dashboards +
// `make verify-main` gate both rely on this bool. godlike/06 SSOT:
// the bool is the SINGLE canonical ownership surface for the
// consumer-alive question scoped to artlist.
func (s *Service) RegisterHandler(jobsSvc *appjobs.Service) error {
	// PR-P2-FAILCLOSED-JOB (July 2026): reset hasConsumer=false BEFORE
	// the bind call so a panic mid-call, a failed re-bind, or any
	// side-channel that could leave stale TRUE is correctly cleared
	// regardless of prior state (defense-in-depth on top of the
	// post-bind HasHandler cross-check in WireArtlistJobBindings).
	s.hasConsumer = false
	if err := s.jobAdapter.RegisterHandler(jobsSvc); err != nil {
		return err
	}
	s.hasConsumer = true
	return nil
}

// HasConsumer reports whether the Artlist job handler is currently
// bound to a appjobs.Service dispatcher for TypeArtlistRun
// ("media.artlist"). godlike/06 SSOT — operator-facing surface for
// /api/artlist/job-consumer; composition root drives the bool via
// RegisterHandler (which is the only writer). Returns false when the
// receiver is nil (defensive godlike/06 SSOT — handler-side nil
// tolerance without a panic on dereference).
func (s *Service) HasConsumer() bool {
	if s == nil {
		return false
	}
	return s.hasConsumer
}

// Searchers returns the canonical (scraper, pixabay, pexels) Searcher triplet
// for diagnostic + observability access. Wired at composition time by
// build_bundles_artlist.go::WireArtlist from the infrastructure-layer
// concretes (internal/infrastructure/artlist/scraper + fallback); each
// satisfies the same Searcher port (godlike/06 one-canonical-owner-per-fact).
//
// Per godlike/06 SSOT the construction is the composition root's exclusive
// responsibility — callers MUST NOT call this method to discover "which
// searcher should I use" (the canonical answer is the application's
// SearchLive / DiscoverAndQueueRun fallback chain, NOT a hand-rolled
// dispatcher at the caller). Use the accessor for diagnostic surfaces
// (operator visibility into wired capabilities, build_bundles_artlist_test.go
// PR-ARTLIST-SEARCHERS wiring assertions, future health probes).
func (s *Service) Searchers() (Searcher, Searcher, Searcher) {
	return s.scraperSearcher, s.pixabaySearcher, s.pexelsSearcher
}

// GetJobByRunID ottiene un job per run ID.
func (s *Service) GetJobByRunID(ctx context.Context, runID string) (*appjobs.Job, error) {
	return s.jobAdapter.GetJobByRunID(ctx, runID)
}

// ImportClip imports a single Artlist clip by its detail page URL.
// When req.Download is false the asset is persisted as STAGING/DISCOVERED
// and returned without downloading the video. When req.Download is true
// the clip is also downloaded, normalized, uploaded to Drive, and
// prepared for indexing via the canonical outbox path.
func (s *Service) ImportClip(ctx context.Context, req *ImportClipRequest) (*ImportClipResponse, error) {
	if s.detailFetcher == nil {
		return nil, ErrUnavailable
	}
	if strings.TrimSpace(req.ClipPageURL) == "" {
		return nil, ErrEmpty
	}

	candidate, err := s.detailFetcher.FetchDetails(ctx, req.ClipPageURL)
	if err != nil {
		return nil, fmt.Errorf("fetch clip details: %w", err)
	}
	if candidate == nil {
		return nil, ErrEmptyResult
	}

	clip := candidateToAsset(candidate, req.ClipPageURL)

	// Avoid duplicate imports: if the clip already exists, return the
	// existing record.
	if s.assetStore != nil {
		if existing, getErr := s.assetStore.Get(ctx, clip.ID); getErr == nil && existing != nil {
			s.log.Info("artlist import skipped: clip already exists", zap.String("clip_id", clip.ID))
			return &ImportClipResponse{
				OK:     true,
				ClipID: existing.ID,
				Name:   existing.Name,
				Status: "already_imported",
			}, nil
		}
	}

	resp := &ImportClipResponse{
		OK:           true,
		ClipID:       clip.ID,
		Name:         clip.Name,
		ClipPageURL:  clip.ClipPageURL,
		ThumbnailURL: candidate.ThumbnailURL,
		PreviewURL:   candidate.PreviewURL,
		Tags:         candidate.Keywords,
		Categories:   candidate.Categories,
		Creator:      candidate.Creator,
		Metadata:     make(map[string]any),
	}
	if clip.Metadata != nil {
		resp.Metadata = clip.Metadata
	}
	if candidate.RawMetadata != nil {
		if country, ok := candidate.RawMetadata["country"].(string); ok {
			resp.Country = country
		}
		if loc, ok := candidate.RawMetadata["location"].(string); ok {
			resp.Location = loc
		}
	}

	if !req.Download {
		if s.dispatcher == nil {
			return nil, ErrAssetMutationDispatcherUnavailable
		}
		if err := s.dispatcher.SaveDiscoveredAsset(ctx, clip, asset.StateStaging, asset.StateDiscovered); err != nil {
			return nil, fmt.Errorf("save discovered asset: %w", err)
		}
		resp.Status = "discovered"
		return resp, nil
	}

	item, err := s.runOrchestrator.ImportSingleClip(ctx, req, clip)
	if err != nil {
		resp.OK = false
		resp.Error = err.Error()
		resp.Status = "failed"
		return resp, err
	}

	resp.Status = item.Status
	resp.DriveLink = item.DriveLink
	resp.DriveFileID = item.DriveFileID
	resp.LocalPath = item.LocalPath
	resp.FileHash = item.FileHash
	resp.DownloadLink = item.DownloadLink
	if resp.Status == "" {
		resp.Status = "completed"
	}

	return resp, nil
}

// candidateToAsset maps a provider-level candidate to the canonical
// asset model, preserving all Artlist-specific metadata in the JSON
// Metadata field.
func candidateToAsset(c *Candidate, clipPageURL string) *asset.Asset {
	id := c.ID
	if id == "" {
		id = extractClipIDFromURL(clipPageURL)
	}
	name := c.Title
	if name == "" {
		name = id
	}

	providerTags := make([]string, len(c.Keywords))
	copy(providerTags, c.Keywords)

	searchTerms := make([]string, 0, len(providerTags)+1)
	searchTerms = append(searchTerms, name)
	searchTerms = append(searchTerms, providerTags...)

	clip := &asset.Asset{
		ID:          id,
		Name:        name,
		Source:      asset.Source("artlist"),
		MediaType:   asset.MediaType("video"),
		Tags:        providerTags,
		SearchTerms: deduplicateStrings(searchTerms),
		SourceURL:   c.SourceRef,
		ClipPageURL: clipPageURL,
		Metadata: map[string]any{
			"creator":             c.Creator,
			"provider_tags":       providerTags,
			"provider_categories": c.Categories,
			"metadata_origin":     "artlist",
		},
	}
	if c.Description != "" {
		clip.Metadata["description"] = c.Description
	}
	if c.ThumbnailURL != "" {
		clip.ThumbnailURL = c.ThumbnailURL
	}
	if c.PreviewURL != "" {
		clip.Metadata["preview_url"] = c.PreviewURL
	}
	if c.PageURL != "" {
		clip.ClipPageURL = c.PageURL
	} else if clipPageURL != "" {
		clip.ClipPageURL = clipPageURL
	}
	for k, v := range c.RawMetadata {
		clip.Metadata[k] = v
	}
	return clip
}

// extractClipIDFromURL pulls the numeric clip id from an Artlist
// detail page URL. Falls back to the full URL when no numeric id is
// present.
func extractClipIDFromURL(u string) string {
	// artlist.io/stock-footage/clip/<slug>/<id>
	parts := strings.Split(strings.TrimRight(u, "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] != "" {
			return parts[i]
		}
	}
	return u
}
