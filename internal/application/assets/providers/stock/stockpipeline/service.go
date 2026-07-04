package stockpipeline

import (
	"context"
	"errors"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	downloader "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// PipelineConfig holds configuration for the stock pipeline run.
type PipelineConfig struct {
	ChunkDuration  int
	MaxResults     int
	EffectInterval int
	EffectsDir     string
}

// DefaultPipelineConfig returns a PipelineConfig with sensible defaults.
func DefaultPipelineConfig() PipelineConfig {
	return PipelineConfig{
		ChunkDuration:  25,
		MaxResults:     25,
		EffectInterval: 4,
		EffectsDir:     "assets/effects/EffettiVisiv",
	}
}

// Sentinel errors returned by NewService validation. Each error names a
// missing dependency so composition-time call sites can forward a single
// error to operators and tests can assert the precise missing dep without
// reading through the wrapped fmt chain.
var (
	ErrStockPipelineNilCfg = errors.New("stockpipeline.NewService: cfg is required")
	ErrStockPipelineNilLog = errors.New("stockpipeline.NewService: log is required")
	// F2.10: ErrStockPipelineNilDriveSvc RETIRED. The legacy
	// DriveSvc surface (driveup.Admin + its upload/folder-resolution
	// methods) was dropped entirely (override brutal). Every Drive
	// write from the stock pipeline now routes through
	// delivery.Publisher.Publish + delivery.Publisher.ResolveFolder.
	ErrStockPipelineNilClipsRepo      = errors.New("stockpipeline.NewService: storage.ClipsRepo is required (production path)")
	ErrStockPipelineNilAssetIndex     = errors.New("stockpipeline.NewService: storage.AssetIndex is required (production path)")
	ErrStockPipelineNilDispatcher     = errors.New("stockpipeline.NewService: storage.Dispatcher is required (QDRANT-002 PR7 — production canonical ingest)")
	ErrStockPipelineNilCutter         = errors.New("stockpipeline.NewService: media.Cutter is required (PR6 port)")
	ErrStockPipelineNilRenderer       = errors.New("stockpipeline.NewService: media.Renderer is required (PR6 port)")
	ErrStockPipelineNilClipIndexer    = errors.New("stockpipeline.NewService: media.ClipIndexer is required")
	ErrStockPipelineNilMetadataWriter = errors.New("stockpipeline.NewService: media.MetaWriter is required (semantic enrichment for Drive metadata.json upload)")
	ErrStockPipelineNilYouTube        = errors.New("stockpipeline.NewService: YouTube is required (provider metadata enrichment for direct URL sources)")
	ErrStockPipelineNilJobs           = errors.New("stockpipeline.NewService: Jobs is required (async job tracker for HandleJob / RegisterHandler)")

	// §12-4 (July 2026): stock pipeline no longer threads
	// `*downloader.YTDLPDownloader` directly. Every yt-dlp / HTTP / Drive
	// byte-fetch call routes through the canonical
	// acquisition.SourceStager port (Prepare / Release). Production
	// wiring supplies an `*acquisition.FilesystemStager` (or future
	// `*acquisition.YTDLPSourceStager`); nil routing is REJECTED at
	// ctor time so a missed composition-root injection fails loud.
	ErrStockPipelineNilSourceStager = errors.New("stockpipeline.NewService: storage.SourceStager is required (Stock Cutover §12-4 — yt-dlp must be hidden behind the acquisition port)")

	// ErrStockPipelineNilFinalizer is NOT a fail-fast sentinel — the
	// stock Service tolerates a nil Finalizer at ctor time (§12-1
	// §F.1 governance, July 2026) so existing composition-root
	// wiring (which doesn't yet inject the Spina Dorsale finalizer)
	// does not break. When nil:
	//   - Service.HandleJob STILL runs the gates via
	//     BuildFinalizationRequest (the gates fail-closed today
	//     on ErrStockNoChunksFinalized until Commit 4-7 wires
	//     the chunk-rendering ladder).
	//   - When gates pass, HandleJob logs a Warn + skips the
	//     finalizer.CompleteWithArtifacts call (legacy
	//     return-map path remains active).
	//
	// §F.2 follow-up: make Finalizer REQUIRED at ctor time +
	// wire the *finalizer.Finalizer concrete at the composition
	// root (currently routed via imageSvc per
	// registry_internal_modules.go::registerInternalModules).
	ErrStockPipelineNilFinalizer = errors.New("stockpipeline.NewService: Finalizer is nil — gates still fire but no spine write occurs (§12-1 §F.2 follow-up to wire production finalizer)")
)

// StorageDeps groups the canonical media_assets + Qdrant + asset-index stack.
// Three fields — under the AGENTS.md 10-per-bundle cap.
type StorageDeps struct {
	ClipsRepo  *assets.ClipsRepository
	AssetIndex *assetindex.Service
	Dispatcher *outbox.Dispatcher
}

// MediaDeps groups the PR6 ports + semantic enrichment. Four fields —
// under the 10-per-bundle cap. The Cutter / Renderer ports are PR6-defined
// (see ports.go); MetaWriter / ClipIndexer are cross-cutting enrichment.
type MediaDeps struct {
	Cutter      VideoCutter
	Renderer    StockRenderer
	ClipIndexer *clipindexer.Service
	MetaWriter  *semantic.MetadataWriter
}

// ────────────────────────────────────────────────────────────────────
// Audit P0 #6 (July 2026): narrow port types so test fakes can satisfy
// them via Go's structural subtyping without mocking the full
// *assetindex.Service (60+ methods), *assets.ClipsRepository (25+ methods),
// or *outbox.Dispatcher surface. Production wiring passes concrete
// pointers which satisfy these interfaces structurally — the
// `Deps` shape above is unchanged and module_sources.go::WireStockPipeline
// is NOT modified.
// ────────────────────────────────────────────────────────────────────

// stockAssetIndexUpserter is the narrow surface the stock pipeline
// uses from *assetindex.Service. Only Upsert is invoked
//
//nolint:audit-pin:gdl-07-14 stock-cutover-commit4-expanded
type stockAssetIndexUpserter interface {
	Upsert(ctx context.Context, rec *assetindex.AssetRecord) error
}

// stockClipsSearchTermUpdater is the narrow surface the stock pipeline
// uses from *assets.ClipsRepository. Only UpdateSearchTerms is invoked
//
//nolint:audit-pin:gdl-07-14 stock-cutover-commit4-expanded
//nolint:audit-pin:gdl-07-14 stock-cutover-commit4-expanded
type stockClipsSearchTermUpdater interface {
	UpdateSearchTerms(ctx context.Context, clipID, source, name string, tags []string, searchText string) error
}

//nolint:audit-pin:gdl-07-14 stock-cutover-commit4-expanded
//nolint:audit-pin:gdl-07-14 stock-cutover-commit4-expanded
//nolint:audit-pin:gdl-07-14 stock-cutover-commit4-expanded
type stockChunkDispatcher interface {
	EnqueueAndIndex(ctx context.Context, clip *asset.Asset, fileHash string) error
}

// Deps is the canonical constructor input for stockpipeline.Service
// (PR-D, Wave 22 §D3, June 2026). Sized at 7 top-level fields — well
// under the AGENTS.md 8-per-bundle cap. Sub-dependencies (StorageDeps
// + MediaDeps) group related concerns so the field-name list reads as
// the canonical composition pattern:
//
//	Cfg, Log, Drive         — pure data + Drive SDK
//	Storage                 — media_assets + outbox + asset-index stack
//	Media                   — PR6 ports + semantic enrichment
//	YouTube                 — provider for metadata enrichment
//	Jobs                    — async job tracker
//
// Pattern source: artlist.ServiceDeps (PR2.5, June 2026) — `ServiceDeps`
// embeds `ServicePorts + ServiceDependencies` for terse construction;
// here the sub-struct names carry semantic meaning rather than the
// "ports vs dependencies" split at the artlist boundary (the stock
// pipeline has fewer ports to lift out).
//
// PR-D: setter pattern (SetCutter, SetRenderer, SetClipsRepo,
// SetAssetIndex, SetDispatcher, SetJobsSvc, SetYoutubeService,
// SetClipIndexer, SetMetadataWriter) is REMOVED. All dependencies
// are constructor arguments on Deps — replaces the late-bind ordering
// hazard that swapped the canonical ingestion path on every
// composition-time race in WireStockPipeline.
type Deps struct {
	// F2.10: Drive field dropped — every Drive write routes through
	// delivery.Publisher (Publisher field below). The legacy
	// driveup.Admin surface (UploadFile + GetOrCreateFolder + Trash +
	// Delete etc.) was retired (override brutal). Folder resolution
	// inside the pipeline run uses publisher.ResolveFolder
	// (DestinationStock policy) instead of driveutil.EnsureFolderPath.
	Cfg       *config.Config
	Log       *zap.Logger
	Publisher delivery.Publisher
	Storage   StorageDeps
	Media     MediaDeps
	// DELIBERATELY FLAT — YouTube + Jobs are cross-cutting fields, intentionally
	// NOT nested under a sub-group. They are conceptually distinct from the
	// Storage (DB stack) and Media (PR6 ports + semantic enrichment) buckets
	// even though they share a "cross-cutting" semantic cluster. Grouping
	// them under a CrossCuttingDeps struct would add a third embedded
	// sub-group without any concrete shared-validation benefit (each field
	// has its own per-field sentinel). The Deps doc-comment explicitly
	// enumerates this bucketing; future maintainers should preserve the
	// shape rather than introduce a CrossCuttingDeps for symmetry.
	YouTube *youtube.Service
	Jobs    *appjobs.Service

	// Finalizer is the Spina Dorsale JobFinalizer (godlike/06
	// SSOT for SUCCEEDED writes). §12-1 §F.1 (this commit) makes
	// it OPTIONAL — nil routing keeps the legacy return-map path
	// alive for un-wired composition roots. §F.2 follow-up makes
	// it REQUIRED + wires the *finalizer.Finalizer concrete at
	// the composition root (currently routed via imageSvc per
	// registry_internal_modules.go::registerInternalModules).
	Finalizer finalization.JobFinalizer

	// SourceStager is the canonical acquisition.SourceStager port
	// (Stock Cutover §12-4, July 2026). Every yt-dlp / HTTP byte-fetch
	// call in stock routes through Prepare / Release on this port —
	// the underlying `*downloader.YTDLPDownloader` is HIDDEN behind
	// the acquisition abstraction so future §§ (DRIVE-005 forward-pointer,
	// Drive-stager, etc.) can swap the concrete without touching
	// stock surface. The port is REQUIRED at ctor time per godlike/06
	// SSOT — there is exactly ONE owner of "how does stock fetch its
	// source bytes?" and it's the concrete injected here.
	SourceStager acquisition.SourceStager
}

// Service orchestrates the stock video pipeline: search, download, clip extraction,
// effect overlay, chunk rendering, and Drive upload. All video parameters are read
// from config.Video to ensure consistency with other media pipelines.
//
// PR6 (June 2026) port injection: the Service no longer reaches into the
// ffmpeg.Processor directly. Instead it depends on two canonical ports
// declared in internal/application/assets/providers/stock:
//
//   - stock.VideoCutter  (extracted-clips from a single source video)
//   - stock.StockRenderer (cross-clip concatenation + transition/overlay)
//
// PR-D (June 2026): all dependencies — including the PR6 ports —
// arrive via the ctor-injected Deps struct. The 9 legacy setters
// (SetCutter / SetRenderer / SetClipsRepo / SetAssetIndex / SetDispatcher
// / SetJobsSvc / SetYoutubeService / SetClipIndexer / SetMetadataWriter)
// were removed. Production wire-up lives in WireStockPipeline at
// internal/app/module_sources.go (Deps{...} literal).
type Service struct {
	cfg       *config.Config
	log       *zap.Logger
	publisher delivery.Publisher
	ytdlp     *downloader.YTDLPDownloader
	// cutter + renderer are the PR6 ports. Initialised at ctor time so
	// every method sees either a non-nil port or an error from NewService;
	// the per-site nil-guards the setters previously required are gone.
	cutter      VideoCutter
	renderer    StockRenderer
	pcfg        PipelineConfig
	jobsSvc     *appjobs.Service
	assetIndex  stockAssetIndexUpserter
	youtubeSvc  *youtube.Service
	clipIndexer *clipindexer.Service
	metaWriter  *semantic.MetadataWriter
	clipsRepo   stockClipsSearchTermUpdater
	// dispatcher is the canonical media_index_outbox dispatcher,
	// required at ctor time per QDRANT-002 PR7. NewService rejects
	// nil dispatcher with ErrStockPipelineNilDispatcher.
	// Audit P0 #6: narrowed from `*outbox.Dispatcher` to the local
	// `stockChunkDispatcher` interface so test fakes can wire the
	// shape without dragging in the full infra surface.
	dispatcher stockChunkDispatcher
	// finalizer is the Spina Dorsale JobFinalizer (§12-1 §F.1
	// governance, July 2026). OPTIONAL in this commit (no fail-fast
	// on nil — see ErrStockPipelineNilFinalizer doc-comment). §F.2
	// follow-up promotes it to a REQUIRED dep + wires production
	// finalizer.New(...) at the composition root (currently routed
	// via imageSvc per registry_internal_modules.go).
	finalizer finalization.JobFinalizer
	// sourceStager is the canonical acquisition.SourceStager port
	// (Stock Cutover §12-4, July 2026). REQUIRED at ctor time — the
	// None-checking ErrStockPipelineNilSourceStager gate surfaces
	// composition-time wiring gaps before the first stock run hits
	// the broker. The `ytdlp *downloader.YTDLPDownloader` field is
	// RETIRED in §12-4: the Service no longer holds the
	// downloader-handle directly; production concretes inject the
	// bytes through acquisition.Prepare.
	sourceStager acquisition.SourceStager
}

// NewService creates a stock pipeline service via the canonical Deps struct
// (PR-D, June 2026). Returns *Service + error (the legacy signature returned
// only *Service + relied on per-call nil guards; the new contract surfaces
// missing deps at composition time, the only safe window).
//
// Validation order: pure data (Cfg, Log, Drive) → transport (Storage) →
// ports (Media) → cross-cutting. Each missing dep surfaces its own
// sentinel error (see ErrStockPipelineNil* above) so production wiring
// can forward a single error verbatim and tests can assert the precise
// field-name without unwrapping the chain.
//
// PR6 ports (Cutter, Renderer) are required — missing either fails ctor
// with ErrStockPipelineNilCutter / ErrStockPipelineNilRenderer. The
// legacy per-call nil-guards are gone; callers can rely on the
// invariants without re-checking.
//
// Production wire-up lives in WireStockPipeline
// (internal/app/module_sources.go::WireStockPipeline). The composition
// root pre-rejects any nil dispatcher at the wire call-site (QDRANT-002
// PR7 precedent on artlist.WireArtlist); NewService is the second
// line of defence so accidental misuse from tests still fails loud.
func NewService(deps Deps) (*Service, error) {
	if deps.Cfg == nil {
		return nil, ErrStockPipelineNilCfg
	}
	if deps.Log == nil {
		return nil, ErrStockPipelineNilLog
	}
	// F2.10: Drive validation dropped — see Deps doc-comment. The
	// legacy DriveSvc plumbing (driveup.Admin.UploadFile + friend
	// methods) is gone; Publisher is the only Drive-write canal.
	if deps.Storage.ClipsRepo == nil {
		return nil, ErrStockPipelineNilClipsRepo
	}
	if deps.Storage.AssetIndex == nil {
		return nil, ErrStockPipelineNilAssetIndex
	}
	if deps.Storage.Dispatcher == nil {
		return nil, ErrStockPipelineNilDispatcher
	}
	if deps.Media.Cutter == nil {
		return nil, ErrStockPipelineNilCutter
	}
	if deps.Media.Renderer == nil {
		return nil, ErrStockPipelineNilRenderer
	}
	if deps.Media.ClipIndexer == nil {
		return nil, ErrStockPipelineNilClipIndexer
	}
	if deps.Media.MetaWriter == nil {
		return nil, ErrStockPipelineNilMetadataWriter
	}
	// PR-D post-review (Wave 22 §D3 reviewer #2): YouTube and Jobs are
	// required at ctor time. Previously nil-tolerant; the silent
	// nil-passthrough was a regression surface — RegisterHandler(bundle.Jobs)
	// resolves the jobs.JobsFacade at handler dispatch, and processSingleVideo
	// touches youtube metadata for direct-URL sources. Validate them
	// like every other required dep so composition-time pre-rejection
	// catches the missing wiring without waiting for the first job.
	if deps.YouTube == nil {
		return nil, ErrStockPipelineNilYouTube
	}
	if deps.Jobs == nil {
		return nil, ErrStockPipelineNilJobs
	}
	// §12-4 (July 2026): SourceStager is REQUIRED. The previous
	// nil-tolerant fallback to a `*downloader.YTDLPDownloader` direct
	// field is retired; production composition roots MUST inject a
	// concrete `acquisition.SourceStager` here (typically
	// `*acquisition.FilesystemStager` wrapping a Fetch closure that
	// invokes yt-dlp subprocess). Missing injection fails loud at
	// boot — no silent-degrade to the old ytdlp field.
	if deps.SourceStager == nil {
		return nil, ErrStockPipelineNilSourceStager
	}

	v := deps.Cfg.Video.WithDefaults()
	return &Service{
		cfg:       deps.Cfg,
		log:       deps.Log,
		publisher: deps.Publisher,
		ytdlp:     downloader.NewYTDLP(deps.Cfg),
		cutter:    deps.Media.Cutter,
		renderer:  deps.Media.Renderer,
		pcfg: PipelineConfig{
			ChunkDuration:  v.ChunkDuration,
			MaxResults:     v.MaxClipsPerSource,
			EffectInterval: v.EffectInterval,
			EffectsDir:     DefaultPipelineConfig().EffectsDir,
		},
		jobsSvc:      deps.Jobs,
		assetIndex:   deps.Storage.AssetIndex,
		youtubeSvc:   deps.YouTube,
		clipIndexer:  deps.Media.ClipIndexer,
		metaWriter:   deps.Media.MetaWriter,
		clipsRepo:    deps.Storage.ClipsRepo,
		dispatcher:   deps.Storage.Dispatcher,
		finalizer:    deps.Finalizer,
		sourceStager: deps.SourceStager,
	}, nil
}

// ────────────────────────────────────────────────────────────────────
// Job handler methods (RegisterHandler / HandleJob / extractLease /
// manifestBytes) live in job_handler.go (Stock P0 split, July 2026).
//
// Run types (RunInput, ChunkResult, PipelineResult, DTOs) live in
// types_run.go (Stock P0 split, July 2026).
//
// Source staging methods (StageSource, stageSection) live in
// source_staging.go (Stock P0 split, July 2026).
// ────────────────────────────────────────────────────────────────────
