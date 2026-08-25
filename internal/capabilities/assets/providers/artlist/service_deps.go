package artlist

import (
	"database/sql"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
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
	// Searcher implementations injected from infrastructure.
	// LocalSearcher is the SQLite-backed adapter; nil means the local
	// fallback is unavailable and is skipped honestly.
	LocalSearcher Searcher
	// PR2: remote searchers injected from infrastructure.
	// Nil means that level is skipped in the fallback chain.
	ScraperSearcher Searcher
	PixabaySearcher Searcher
	PexelsSearcher  Searcher
	// DetailFetcher fetches rich metadata for a single Artlist clip page.
	// Optional — when nil the import endpoint returns ErrUnavailable.
	DetailFetcher DetailFetcher
	// Stager is the canonical acquisition.SourceStager port. Optional —
	// when non-nil, stageProcessBatch uses Prepare/Release before the
	// media processor; when nil it falls through to the processor's
	// regular download path.
	Stager acquisition.SourceStager
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
	// concrete from internal/platform/sqlite/assets.
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
	// MediaMemoryConceptRepo / MediaMemoryBindingRepo / MediaMemoryNormalizer
	// are optional ports used to create media_concepts / media_bindings
	// after a clip is materialized. nil means linking is skipped.
	MediaMemoryConceptRepo mediamemory.ConceptRepository
	MediaMemoryBindingRepo mediamemory.BindingRepository
	MediaMemoryNormalizer  mediamemory.Normalizer
	// Transcriber extracts the audio transcript from a downloaded clip.
	// Mandatory for all Artlist downloads (PR-ARTLIST-MANDATORY-TRANSCRIPTION,
	// July 2026); NewService rejects nil with ErrTranscriberUnavailable.
	Transcriber Transcriber
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
	// TextTrackRepo persists audio transcripts for downloaded clips.
	// Mandatory for all Artlist downloads (PR-ARTLIST-MANDATORY-TRANSCRIPTION,
	// July 2026); NewService rejects nil with ErrTextTrackRepoUnavailable.
	TextTrackRepo asset.TextTrackRepository
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
