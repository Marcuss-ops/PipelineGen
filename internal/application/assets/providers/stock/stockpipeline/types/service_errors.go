// Package stockpipeline — service_errors.go (PR-STOCK-SERVICE-SPLIT,
// July 2026).
//
// SOLE owner of the typed sentinel errors surfaced by the shared
// production/test dependency validation per godlike/06 SSOT (one canonical owner
// per fact). Each sentinel names the missing dependency so
// composition-time call sites can forward a single error to
// operators and tests can assert the precise missing dep without
// reading through the wrapped fmt chain.
//
// godlike/07 typed-error contract: every sentinel is a typed
// errors.New(...). Callers probe via errors.Is(...) from any
// seam. The wrapping chain in NewService is "%w" so the typed
// sentinel propagates verbatim through composition-root error
// forwarding.
//
// PR-STOCK-SERVICE-SPLIT extracted these from service.go on
// 2026-07-04. The pre-split file was 380 LoC (the user spec
// referenced a 914-LoC pre-Commit-4-expanded view of service.go
// that no longer exists; the spec's "service_resilience.go /
// service_state.go / service_steps.go / service_metrics.go" files
// would have been empty per godlike/07 no-fake-availability —
// the resilience ports + state machine + Stage 1-5 + Prometheus
// metrics live in upload_orchestration.go, job_handler.go,
// orchestrator_steps.go and are NOT in service.go post-Commit-4-
// expanded; see architecture/action-plans/2026-07-04-stock-architecture
// -improvement.md and the commit body for the full honest scope
// disclosure).
package types

import "errors"

// Sentinel errors returned by the shared production/test validation ladder.
var (
	ErrStockPipelineNilCfg = errors.New("stockpipeline.NewService: cfg is required")
	ErrStockPipelineNilLog = errors.New("stockpipeline.NewService: log is required")
	// F2.10: ErrStockPipelineNilDriveSvc RETIRED. The legacy
	// DriveSvc surface (driveup.Admin + its upload/folder-resolution
	// methods) was dropped entirely (override brutal). Every Drive
	// write from the stock pipeline now routes through
	// delivery.Publisher.Publish + delivery.Publisher.ResolveFolder.
	ErrStockPipelineNilClipsRepo     = errors.New("stockpipeline.NewService: storage.ClipsRepo is required (production path)")
	ErrStockPipelineNilAssetIndex    = errors.New("stockpipeline.NewService: storage.AssetIndex is required (production path)")
	ErrStockPipelineNilDispatcher    = errors.New("stockpipeline.NewService: storage.Dispatcher is required (QDRANT-002 PR7 — production canonical ingest)")
	ErrStockPipelineNilCutter        = errors.New("stockpipeline.NewService: media.Cutter is required (PR6 port)")
	ErrStockPipelineNilRenderer      = errors.New("stockpipeline.NewService: media.Renderer is required (PR6 port)")
	ErrStockPipelineNilJobs          = errors.New("stockpipeline.NewService: Jobs is required (async job tracker for HandleJob / RegisterHandler)")
	ErrStockPipelineNilPublisher     = errors.New("stockpipeline.NewService: delivery.Publisher is required")
	ErrStockPipelineNilPublisherPort = errors.New("stockpipeline.NewService: delivery.PublisherPort is required (godlike/06: wire drive.NewArtifactPublisherAdapter at the composition root)")
	ErrStockPipelineNilFolderCreator = errors.New("stockpipeline.NewService: delivery.FolderCreator is required")
	ErrStockPipelineNilStepStore     = errors.New("stockpipeline.NewService: Runtime.StepStore is required for durable stock state")

	// P8 (July 2026): ErrStockPipelineNilYouTube + ErrStockPipelineNilClipIndexer +
	// ErrStockPipelineNilMetadataWriter RETIRED. YouTube was never wired at
	// the composition root; ClipIndexer + MetaWriter were dead code (zero
	// call sites in the stockpipeline package).

	// Audit P0 (July 2026): LocalFS is REQUIRED — the composition root
	// MUST inject a concrete LocalFSPort (filesystem.NewLocal() or
	// filesystem.NewTempDirFS()). A nil LocalFS would previously
	// fail-closed at the cache copy site; now it fails at construction
	// time so the wiring gap is caught before the first stock run.
	ErrStockPipelineNilLocalFS = errors.New("stockpipeline.NewService: SourceCache.LocalFS is required (audit P0: no implicit fallback to real filesystem — inject filesystem.NewLocal() or filesystem.NewTempDirFS())")

	// §12-4 (July 2026): stock pipeline no longer threads
	// `*downloader.YTDLPDownloader` directly. Every yt-dlp / HTTP / Drive
	// byte-fetch call routes through the canonical
	// acquisition.SourceStager port (Prepare / Release). Production
	// wiring supplies an `*acquisition.FilesystemStager` (or future
	// `*acquisition.YTDLPSourceStager`); nil routing is REJECTED at
	// ctor time so a missed composition-root injection fails loud.
	ErrStockPipelineNilSourceStager = errors.New("stockpipeline.NewService: storage.SourceStager is required (Stock Cutover §12-4 — yt-dlp must be hidden behind the acquisition port)")

	// ErrStockProductionDBMissing surfaces when the stock pipeline is
	// wired for production (Publisher + Finalizer) but no SQLite DB is
	// available. Batch/group/artifact persistence is mandatory for
	// production stock runs.
	ErrStockProductionDBMissing = errors.New("stockpipeline: SQLite DB is mandatory for production stock pipeline (batch/group/artifact persistence)")

	// ErrStockProductionBatchRepositoryMissing surfaces when the stock
	// pipeline is wired for production but the composition root did not
	// supply a StockBatchRepository adapter.
	ErrStockProductionBatchRepositoryMissing = errors.New("stockpipeline: StockBatchRepository is mandatory for production stock pipeline")
	ErrStockProductionProjectionMissing      = errors.New("stockpipeline: ProjectionPort is mandatory for production stock pipeline")
	ErrStockProductionSourceProbeMissing     = errors.New("stockpipeline: SourceDurationProbe is mandatory for production stock pipeline")

	ErrStockPipelineNilFinalizer = errors.New("stockpipeline.NewService: Finalizer is nil — gates still fire but no spine write occurs (§12-1 §F.2 follow-up to wire production finalizer)")

	// ErrStockPipelineAllQueriesFailed surfaces when every text
	// search query in resolveInputQueries fails to resolve to a
	// YouTube URL via yt-dlp. Without at least one resolved URL,
	// the orchestrator has no video source to plan against.
	//
	// Common root causes: yt-dlp n-challenge, missing cookies,
	// network failure, or rate limiting.
	//
	// godlike/07 typed-error contract: callers probe via
	// errors.Is(err, ErrStockPipelineAllQueriesFailed).
	ErrStockPipelineAllQueriesFailed = errors.New("stockpipeline: all search queries failed to resolve via yt-dlp")
)
