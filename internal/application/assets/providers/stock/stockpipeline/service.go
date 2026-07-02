package stockpipeline

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	appassets "github.com/Marcuss-ops/PipelineGen/internal/application/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	youtube "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	jobdomain "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	downloader "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

var rng = rand.New(rand.NewSource(time.Now().UnixNano()))

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

// RegisterHandler registers the stock pipeline job handler with the jobs
// system.
//
// Register propagates wiring errors — composition root MUST fail-closed on non-nil return.
//
// P1 #1 (July 2026): wraps appjobs.ErrMissingDeps via %w so the
// composition root + tests can assert via errors.Is(err, appjobs.ErrMissingDeps)
// regardless of which handler-specific prefix the future maintainer
// adds or removes. The handler-specific diagnostic prefix is preserved
// for operator logs. The error-return signature (refactored in
// Audit P0 #2 cont. — PR-VALIDATOR-LITERAL-REGISTER, July 2026)
// closes the silent-success class of "if jobsSvc != nil { log.Info }"
// that pre-P0 #2 swallowed nil-typed-dispatcher + duplicate-bind
// failures.
func (s *Service) RegisterHandler(jobsSvc *appjobs.Service) error {
	if jobsSvc == nil {
		return fmt.Errorf("stockpipeline.Service.RegisterHandler: jobsSvc is nil (composition root must wire jobs.Service before calling Register): %w", appjobs.ErrMissingDeps)
	}
	if err := jobsSvc.RegisterHandler(appjobs.TypeMediaStock, s.HandleJob); err != nil {
		return fmt.Errorf("stockpipeline.Service.RegisterHandler: bind %q to dispatcher: %w", appjobs.TypeMediaStock, err)
	}
	s.log.Info("registered media.stock job handler", zap.String("type", appjobs.TypeMediaStock))
	return nil
}

// HandleJob handles a stock pipeline job from the job queue.
//
// Stock Cutover Commit 2 (July 2026): the handler no longer calls
// s.Run (the legacy ~280-line body that called resolveQuery /
// Instead it calls s.runOrchestrator directly so it has access to
// the typed *job.ArtifactManifest, which is the canonical wire
// artefact for the broker's downstream runner (the worker runner
// at internal/application/jobs/worker/runner.go::uploadManifest
// reads the result map's "__artifact_manifest" key per
// domain/job.ManifestKey).
//
// Result-map shape (Stock Cutover Commit 2):
//
//	"__artifact_manifest" -> *job.ArtifactManifest (canonical wire artefact)
//	"total_clips"          -> int                      (legacy field, projected from manifest; zero in Commit 2, hydrated in Commit 4-7)
//	"total_chunks"         -> int                      (legacy field, projected from manifest; zero in Commit 2)
//	"chunks"               -> []ChunkResult            (legacy field, projected from manifest; nil in Commit 2)
//	"metadata_link"        -> string                   (legacy field, projected from manifest; empty in Commit 2)
//	"metadata_file_id"     -> string                   (legacy field, projected from manifest; empty in Commit 2)
//
// Legacy fields are kept so dashboards reading the JobStatusResponse
// continue to render without a schema break; the canonical manifest
// is the new source of truth. Commit 4-7 hydrates the legacy fields
// from the committed RunOutput metadata once the chunk ladder ships.
//
//nolint:audit-pin:gdl-07-14 stock-cutover-commit4-expanded
func (s *Service) HandleJob(ctx context.Context, job *appjobs.Job, tools *appjobs.JobTools) (map[string]any, error) {
	var payload StockRunPayload
	if len(job.Payload) > 0 {
		if err := json.Unmarshal(job.Payload, &payload); err != nil {
			return nil, fmt.Errorf("failed to unmarshal stock payload: %w", err)
		}
	}

	s.log.Info("stock job payload received",
		zap.String("job_id", job.ID),
		zap.Int("search_queries", len(payload.SearchQueries)),
		zap.Int("direct_urls", len(payload.DirectURLs)),
		zap.Int("total_minutes", payload.TotalMinutes),
		zap.Int("chunk_duration", payload.ChunkDuration),
		zap.String("subfolder", payload.Subfolder),
		zap.String("folder_name", payload.FolderName),
		zap.String("folder_id", payload.FolderID),
	)

	input := &RunInput{
		SearchQueries: payload.SearchQueries,
		DirectURLs:    payload.DirectURLs,
		TotalMinutes:  payload.TotalMinutes,
		ChunkDuration: payload.ChunkDuration,
		ClipDuration:  payload.ClipDuration,
		NoAudio:       payload.NoAudio,
		NoEffects:     payload.NoEffects,
		NoTransitions: payload.NoTransitions,
		MaxVideos:     payload.MaxVideos,
		Subfolder:     payload.Subfolder,
		FolderName:    payload.FolderName,
		FolderID:      payload.FolderID,
	}
	if payload.Metadata != nil {
		input.Metadata = &ChunkMetadataInput{
			Title:       payload.Metadata.Title,
			Description: payload.Metadata.Description,
			Tags:        payload.Metadata.Tags,
			Category:    payload.Metadata.Category,
			Author:      payload.Metadata.Author,
			Extra:       payload.Metadata.Extra,
		}
	}

	if tools.Progress != nil {
		input.Progress = tools.Progress
		tools.Progress(5, "Starting stock orchestrator")
	}

	// Stock Cutover Commit 4-expanded: route through
	// runOrchestratorResilient so the typed *RunSummary carries the
	// per-run FinalStatus that the broker JobFinalizer stamps on the
	// job row (resilience contract: artifacts emitted + Qdrant
	// projection deferred ⇒ INDEX_PENDING; artifacts emitted + Qdrant
	// OK ⇒ SUCCEEDED; manifest-gate/atomic-dispatch failure ⇒ typed
	// sentinel ⇒ JobFailed).
	summary, err := s.runOrchestratorResilient(ctx, input, job.ID)
	if err != nil {
		return nil, err
	}
	manifest := summary.Manifest

	if tools.Progress != nil {
		tools.Progress(80, "Stock orchestrator complete")
	}

	// Stock Cutover §12-1 §F (July 2026): thread HandleJob through
	// the canonical Spina Dorsale. Build the OrchestrationResult
	// envelope (already defined in finalizer_gates.go) carrying
	// the typed manifest + the per-chunk + per-metadata gate
	// inputs. Today (Commit 4-7 not landed) Chunks and Metadata
	// are EMPTY so BuildFinalizationRequest's VerifyChunks raises
	// ErrStockNoChunksFinalized — the gate fires before any
	// finalizer call, propagating the typed error to the broker
	// which marks the job FAILED (closing the silent-success
	// class per user spec P0 2.1).
	orchestration := &OrchestrationResult{
		Manifest: manifest,
		Chunks:   []ChunkState{},  // pre-Commit-4-7: empty
		Metadata: MetadataState{}, // pre-Commit-4-7: empty
	}

	// §F.1 (this commit): the Spine is OPTIONAL. When wired
	// (production case in §F.2 follow-up) the atomic
	// single-TX SUCCEEDED write happens; when unwired (today's
	// composition root, which doesn't yet thread a finalizer)
	// the gate-fail-fast path still propagates and the legacy
	// return-map shape is preserved so dashboards keep rendering.
	manifestData, marshalErr := manifestBytes(manifest)
	if marshalErr != nil {
		return nil, fmt.Errorf("stockpipeline.Service.HandleJob: marshal manifest: %w", marshalErr)
	}
	lease := extractLease(job)
	finReq, buildErr := BuildFinalizationRequest(
		job.ID,
		lease,
		manifestData,
		orchestration.Chunks,
		orchestration.Metadata,
	)
	if buildErr != nil {
		// Gate failed — propagate the typed sentinel so the
		// broker's downstream runner marks the job FAILED.
		// Today this returns ErrStockNoChunksFinalized on EVERY
		// stock run (pre-Commit-4-7). §F.2 does NOT change this
		// fail-closed behavior — it just enables the
		// post-gate-pass path through finalizer.CompleteWithArtifacts.
		return nil, fmt.Errorf("stockpipeline.Service.HandleJob: gates failed (job cannot SUCCEED): %w", buildErr)
	}

	var finResult *finalization.FinalizationResult
	if s.finalizer != nil {
		var finalErr error
		finResult, finalErr = s.finalizer.CompleteWithArtifacts(ctx, *finReq)
		if finalErr != nil {
			return nil, fmt.Errorf("stockpipeline.Service.HandleJob: finalizer spine write: %w", finalErr)
		}
		s.log.Info("stock finaliser spine SUCCEEDED",
			zap.String("job_id", job.ID),
			zap.Int("attempt", lease.Attempt),
			zap.Int("artifact_count", len(finResult.ArtifactRefs)),
		)
	} else {
		// §F.1 fallback: composition root hasn't wired the
		// production finalizer yet. The legacy return-map shape
		// preserves JobStatusResponse rendering and the broker's
		// downstream runner still sees the manifest. The job is
		// NOT marked SUCCEEDED in this branch (finalizer is the
		// single writer per godlike/06 SSOT).
		s.log.Warn("stock Service.HandleJob finalizer NOT wired (§12-1 §F.1 OPTIONAL gate) — gates passed but no spine write occurred; legacy return-map path is active",
			zap.String("job_id", job.ID),
			zap.Int("attempt", lease.Attempt),
		)
	}

	if tools.Progress != nil {
		tools.Progress(100, "Stock pipeline finalised")
	}

	// Project the typed manifest into the legacy shape for
	// dashboards that read the top-level fields (zero in Commit 2;
	// post-cutover chunks populate them in Commit 4-7).
	projected := projectManifestToPipelineResult(manifest)

	// Note on `jobdomain` (alias vs HandleJob's `job` parameter): the
	// HandleJob parameter is named `job *appjobs.Job` so the bare
	// identifier `job` resolves to the broker job, NOT to a package
	// alias. We therefore import domain/job as `jobdomain` so the
	// artifact-manifest constants (jobdomain.ManifestKey,
	// jobdomain.SchemaVersionArtifactManifestV1) are unambiguous.
	result := map[string]any{
		jobdomain.ManifestKey: manifest,                    // "__artifact_manifest" — canonical wire artefact
		"final_status":        string(summary.FinalStatus), // "SUCCEEDED" | "INDEX_PENDING" | "FAILED" | ...
		"total_clips":         projected.TotalClips,
		"total_chunks":        projected.TotalChunks,
		"chunks":              projected.Chunks,
		"metadata_link":       projected.MetadataLink,
		"metadata_file_id":    projected.MetadataFileID,
	}
	if finResult != nil {
		result["__finalization_status"] = finResult.Status
		result["__finalization_completed_at"] = finResult.CompletedAt
	}
	return result, nil
}

// extractLease projects the legacy *appjobs.Job (broker-routed,
// already-claimed) into the canonical finalization.Lease struct
// the JobFinalizer validates inside its single-TX commit.
//
// Mapping rules (Stock Cutover §12-1 §F, July 2026):
//
//	LeaseID    ← job.LeaseID         (broker-assigned at Claim time)
//	JobID      ← job.ID              (canonical broker job identifier)
//	WorkerID   ← job.WorkerID        (broker-assigned worker id)
//	Attempt    ← job.RetryCount + 1  (the canonical "next attempt" formula)
//	ExpiresAt  ← job.LeaseExpiry     (broker-issued lease TTL)
//
// TOCTOU note: the broker's lease could expire between this
// pre-tx read and the finalizer's in-tx lease fence. The
// finalizer's own selectJobForFinalization runs the canonical
// re-validation against the DB row (worker_id + lease_id +
// lease_expiry + retry_count+1 == attempt); lease drift here
// surfaces as ErrLeaseExpired / ErrStaleAttempt inside the tx,
// which is the typed-error contract callers can errors.Is()
// against.
//
// Defensive fallback: if the broker hasn't populated LeaseExpiry
// (rare — usually happens only on synthetic test fixtures),
// extractLease returns a 5-minute TTL so validateRequest
// (`Lease.Valid()`) doesn't raise an empty-time false-positive.
// Production broker traffic always carries a non-nil LeaseExpiry.
func extractLease(job *appjobs.Job) finalization.Lease {
	if job == nil {
		return finalization.Lease{}
	}
	expiresAt := time.Now().UTC().Add(5 * time.Minute)
	if job.LeaseExpiry != nil && !job.LeaseExpiry.IsZero() {
		expiresAt = *job.LeaseExpiry
	}
	return finalization.Lease{
		LeaseID:   job.LeaseID,
		JobID:     job.ID,
		WorkerID:  job.WorkerID,
		Attempt:   job.RetryCount + 1,
		ExpiresAt: expiresAt,
	}
}

// manifestBytes marshals the canonical *job.ArtifactManifest
// (C12 envelope) into finalization.ResultManifest.Data bytes.
// Errors here are typed-error contract violations (manifest
// schema drift) — a typed-error wrap is the right escalation
// since callers can't recover from a marshaller bug mid-job.
func manifestBytes(manifest *jobdomain.ArtifactManifest) ([]byte, error) {
	if manifest == nil {
		return nil, fmt.Errorf("stockpipeline.manifestBytes: nil manifest")
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("stockpipeline.manifestBytes: marshal: %w", err)
	}
	return raw, nil
}

// RunInput holds the parameters for a stock pipeline run.
type RunInput struct {
	SearchQueries []string
	DirectURLs    []string
	TotalMinutes  int
	MaxVideos     int
	ChunkDuration int
	ClipDuration  int
	NoAudio       bool
	NoEffects     bool
	NoTransitions bool
	Subfolder     string
	FolderName    string
	FolderID      string
	Metadata      *ChunkMetadataInput
	Progress      func(percent int, message string)
}

// ChunkMetadataInput holds user-provided metadata for chunks.
type ChunkMetadataInput struct {
	Title       string            `json:"title,omitempty"`
	Description string            `json:"description,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Category    string            `json:"category,omitempty"`
	Author      string            `json:"author,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"`
}

// PipelineMetadata is the single metadata JSON uploaded at the end with all chunks.
type PipelineMetadata struct {
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Source      SourceInfo        `json:"source"`
	Pipeline    PipelineInfo      `json:"pipeline"`
	Tags        []string          `json:"tags,omitempty"`
	Category    string            `json:"category,omitempty"`
	Author      string            `json:"author,omitempty"`
	Extra       map[string]string `json:"extra,omitempty"`
	Chunks      []ChunkMeta       `json:"chunks"`
}

// ChunkMeta describes a single chunk within the pipeline metadata.
type ChunkMeta struct {
	Index         int        `json:"index"`
	TimelineStart float64    `json:"timeline_start"`
	TimelineEnd   float64    `json:"timeline_end"`
	DriveLink     string     `json:"drive_link,omitempty"`
	DownloadLink  string     `json:"download_link,omitempty"`
	Clips         []ClipInfo `json:"clips"`
}

// SourceInfo describes the source video.
type SourceInfo struct {
	URL      string  `json:"url"`
	Title    string  `json:"title,omitempty"`
	Duration float64 `json:"duration_sec,omitempty"`
}

// ClipInfo describes a single clip within a chunk.
type ClipInfo struct {
	Index int    `json:"index"`
	Start string `json:"start"`
	End   string `json:"end"`
	Title string `json:"title,omitempty"`
}

// PipelineInfo describes pipeline settings used.
type PipelineInfo struct {
	ClipDuration  int  `json:"clip_duration"`
	ChunkDuration int  `json:"chunk_duration"`
	NoAudio       bool `json:"no_audio"`
	NoEffects     bool `json:"no_effects"`
	NoTransitions bool `json:"no_transitions"`
}

// PipelineResult holds the results of a stock pipeline run.
type PipelineResult struct {
	SearchTerms    []string      `json:"search_terms"`
	TotalClips     int           `json:"total_clips"`
	TotalChunks    int           `json:"total_chunks"`
	Chunks         []ChunkResult `json:"chunks"`
	MetadataLink   string        `json:"metadata_link,omitempty"`
	MetadataFileID string        `json:"metadata_file_id,omitempty"`
}

// ChunkResult represents a single rendered and uploaded video chunk.
//
// Blocco 1b (July 2026): added Rendered / Uploaded outcome fields so
// callers can distinguish which stages completed. Pre-fix callers had
// no way to know whether the chunk's DriveLink was real or
// empty-because-upload-failed.
//
// Stock Cutover Commit 4-expanded (July 2026): the previously-typed
// `internal/.../types_status.go` (deleted in Commit 4) and the
// `run_upload_indexing_test.go` for the canonical 3-test failure-mode
// contract that replaces the field-level signal. Per-job post-emission
// indexing state is now surfaced at the orchestrator level via
// `job.StatusIndexPending` (see domain/job/job.go), not at the per-
// chunk level.
//
//nolint:audit-pin:gdl-07-14 stock-cutover-commit4-expanded
//nolint:audit-pin:gdl-07-14 stock-cutover-commit4-expanded
type ChunkResult struct {
	Index         int      `json:"index"`
	TimelineStart float64  `json:"timeline_start"`
	TimelineEnd   float64  `json:"timeline_end"`
	LocalPath     string   `json:"local_path"`
	DriveLink     string   `json:"drive_link"`
	DownloadLink  string   `json:"download_link"`
	DriveFileID   string   `json:"drive_file_id"`
	Title         string   `json:"title"`
	SourceIDs     []string `json:"source_ids,omitempty"`
	// Rendered is true when the FFmpeg render step completed and the
	// chunk file exists on disk.
	Rendered bool `json:"rendered"`
	// Uploaded is true when Publisher.Publish wrote the chunk to Drive.
	Uploaded bool `json:"uploaded"`
}

// VideoSource represents a single video to be downloaded and processed.
type VideoSource struct {
	URL         string
	Title       string
	Source      string
	DurationSec float64
}

// StagedSource is the result of a lightweight StageSource call.
// It contains only the downloaded file — no render, upload, or indexing.
// The caller owns the file at LocalPath and is responsible for cleanup.
//
// Blocco 2a (July 2026): created to separate the "fetch" contract from
// the full pipeline (render → upload → index). Adapter.Fetch uses this
// instead of Run so the staged file survives the return.
type StagedSource struct {
	LocalPath string
	Bytes     int64
}

// StageSource downloads a video from a URL and returns the staged file.
// It delegates to the canonical acquisition.SourceStager port which
// owns persistent stage registry + .meta.json sidecars + TTL eviction.
//
// §12-4 (July 2026): the legacy yt-dlp-baked local implementation is
// RETIRED. The Service no longer holds a `*downloader.YTDLPDownloader`
// field directly; instead it asks `Service.sourceStager.Prepare(ctx, req)`
// for the canonical PrepareContext + LocalPath. The TempPath + MkdirTemp
// dance is gone — stagingRoot lives in the FilesystemStager so multiple
// runs share persistent state across calls (idempotency invariant).
//
// Blocco 2a (July 2026, preserved for the FetchProvider contract): the
// returned *StagedSource is the legacy dual-shape carrier; the adapter
// flattens the PrepareContext.LocalPath + PrepareContext.SizeBytes
// into the StagedSource struct so callers (Adapter.Fetch etc.) don't
// need to switch shapes mid-call. The cleanup function is a thin
// wrapper around sourceStager.Release(ctx, PrepareContext.CleanupToken).
func (s *Service) StageSource(ctx context.Context, url string) (*StagedSource, error) {
	prepared, err := s.sourceStager.Prepare(ctx, acquisition.PrepareRequest{
		Source: acquisition.SourceRef{
			URL:           url,
			PolicyVersion: "v1",
		},
		IdempotencyKey: "stock.stage." + acquisition.DeriveIdempotencyKey(acquisition.SourceRef{
			URL:           url,
			PolicyVersion: "v1",
		}),
		CallerRef: "stock.StageSource",
	})
	if err != nil {
		return nil, fmt.Errorf("stage source: prepare via acquisition.SourceStager: %w", err)
	}
	fi, statErr := os.Stat(prepared.LocalPath)
	if statErr != nil {
		return nil, fmt.Errorf("stage source: stat staged file %q: %w", prepared.LocalPath, statErr)
	}
	if fi.Size() == 0 {
		return nil, fmt.Errorf("stage source: staged file is empty: %s", prepared.LocalPath)
	}
	s.log.Info("stage source: video downloaded via acquisition port",
		zap.String("url", url),
		zap.String("local_path", prepared.LocalPath),
		zap.String("stage_id", prepared.ID),
		zap.String("cleanup_token", prepared.CleanupToken),
		zap.Int64("bytes", fi.Size()),
		zap.Time("expires_at", prepared.ExpiresAt),
	)
	return &StagedSource{
		LocalPath: prepared.LocalPath,
		Bytes:     fi.Size(),
	}, nil
}

// stageSection downloads a single time-slice of a video via the
// canonical acquisition.SourceStager port (Stock Cutover §12-4).
//
// §12-4 (July 2026): the section path no longer threads a raw
// downloader.YTDLPDownloader.Download call. Instead the section time
// range flows through the same acquisition.SourceRef envelope as the
// full-asset path; the yt-dlp invocation logic that handles yt-dlp's
// `--download-sections` lives INSIDE the production concrete
// (`*acquisition.YTDLPSourceStager`, §12-4.2 forward-pointer). Today
// the FilesystemStager concrete writes the file via its Fetch
// closure — which stock callers wire to the yt-dlp subprocess.
//
// The legacy `s.ytdlp.Download` direct call is RETIRED.
func (s *Service) stageSection(ctx context.Context, ref appassets.SourceRef) (*appassets.StagedAsset, error) {
	prepared, err := s.sourceStager.Prepare(ctx, acquisition.PrepareRequest{
		Source: acquisition.SourceRef{
			URL:             ref.URL,
			DownloadSection: ref.DownloadSection,
			ForceKeyframes:  ref.ForceKeyframes,
			MergeFormat:     ref.MergeFormat,
			PolicyVersion:   "v1",
		},
		IdempotencyKey: "stock.section." + acquisition.DeriveIdempotencyKey(acquisition.SourceRef{
			URL:             ref.URL,
			DownloadSection: ref.DownloadSection,
			PolicyVersion:   "v1",
		}),
		CallerRef: "stock.stageSection",
	})
	if err != nil {
		return nil, fmt.Errorf("stage section: prepare via acquisition.SourceStager (%q section=%q): %w", ref.URL, ref.DownloadSection, err)
	}
	fi, statErr := os.Stat(prepared.LocalPath)
	if statErr != nil {
		return nil, fmt.Errorf("stage section: stat %q: %w", prepared.LocalPath, statErr)
	}
	if fi.Size() == 0 {
		return nil, fmt.Errorf("stage section: staged file is empty: %s", prepared.LocalPath)
	}
	s.log.Info("stage section: video section downloaded via acquisition port",
		zap.String("url", ref.URL),
		zap.String("section", ref.DownloadSection),
		zap.String("local_path", prepared.LocalPath),
		zap.String("stage_id", prepared.ID),
		zap.String("cleanup_token", prepared.CleanupToken),
		zap.Int64("bytes", fi.Size()),
		zap.Time("expires_at", prepared.ExpiresAt),
	)
	return &appassets.StagedAsset{
		LocalPath: prepared.LocalPath,
		Bytes:     fi.Size(),
	}, nil
}
