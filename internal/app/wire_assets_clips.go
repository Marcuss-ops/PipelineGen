// Package app — WireAssets clips capability builder (PR-WIRE-ASSETS-CAPABILITY-SPLIT, July 2026).
//
// The clips capability is the largest of the 7 WireAssets capabilities.
// It is the only one that requires application-layer use cases to be
// constructed at composition time (vs. the other 6 which only need the
// canonical api.Build entrypoint). All clips-specific helpers
// (clipsDispatcherPort, mutationsDisp, enrichUC, bulkUploadWorker,
// uploadUC, reuploadUC, clipOpsSvc) are constructed INSIDE this
// function so the caller (WireAssets) only passes the shared
// infrastructure (cfg, log, deps, jobs, dispatcher, driveUploader,
// assetRepo, searchAggregator, metaWriter, folderMemSvc, deletionSvc,
// idemHandler).
//
// godlike/06 SSOT: this file is the canonical owner of the clips
// build pipeline. The canonical clips handler lives in
// internal/api/assets/clips/; this file is composition-root glue only.
//
// PR-WIRE-ASSETS-NIL-CLASSIFICATION (2026-07-25): the descriptor
// type-assertion goes through ClassifyDepGet (DepRequired, production
// fail-closed).
package app

import (
	"fmt"

	clipsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/duplicates"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	appupload "github.com/Marcuss-ops/PipelineGen/internal/application/clips/upload"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	search "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files/foldermemory"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// buildClipsParams groups the dependencies required to wire the clips
// capability bundle.
//
// PR-YAGNI-CLIPS-WIRING (July 2026): replaces the 14 positional arguments
// of buildClipsBundle with a single struct.
type buildClipsParams struct {
	Cfg              *config.Config
	Log              *zap.Logger
	Deps             *AssetsModuleDeps
	Jobs             *JobsBundle
	Dispatcher       *outbox.Dispatcher
	DriveUploader    *driveutil.Uploader
	AssetRepo        asset.Repository
	SearchAggregator *search.Aggregator
	MetaWriter       semantic.MetadataWriterPort
	FolderMemSvc     *foldermemory.Service
	DeletionSvc      *deletion.DeletionService
	IdemHandler      gin.HandlerFunc
}

// buildClipsBundle constructs the canonical *clipsapi.ClipsModule
// (PR-CLIPS-7-MODULES-UPPER-CLIPSMODULE, July 2026: replaced the
// legacy *clipsapi.ClipsDescriptor with the new upper-ClipsModule
// type) by:
//  1. Constructing the dispatcher adapters (clipsDispatcherPort +
//     mutationsDisp) from the raw *outbox.Dispatcher
//  2. Building the application-layer use cases (enrichUC, bulkUploadWorker,
//     uploadUC, reuploadUC, clipOpsSvc) from the typed-port adapters
//  3. Calling clipsapi.Build with the 20-field Dependencies struct —
//     Build returns *clipsapi.ClipsModule directly (the upper
//     ClipsModule{Catalog, Ingest, Processing, Operations} struct
//     with 4 EXPOSED sub-descriptor fields per the user directive;
//     3 PRIVATE routing-only fields {publication, indexing, bulk}
//     are not exposed cross-package per godlike/06 SSOT
//     minimum-needed surface).
//
// Card 10 (July 2026): returns the ClipEnricher typed port alongside
// the module so the composition root can thread it into the
// register-side sourcingEnrichmentAdapter without reaching through
// any internal/api/assets/clips/* field.
func buildClipsBundle(params buildClipsParams) (*clipsapi.ClipsModule, appclips.ClipEnricher, error) {
	// (1) Dispatcher adapters
	//
	// PR12c (June 2026): wire the dispatcher's port, NOT the concrete
	// *outbox.Dispatcher, into the unified clips handler. Adapter is
	// constructed only when dispatcher is non-nil (partial deploy path);
	// when dispatcher is nil the handler falls back to raw repo.UpsertClip
	// via the `if h.dispatcher != nil` guard in UpdateClip.
	var clipsDispatcherPort appclips.ClipIndexDispatcherPort
	if params.Dispatcher != nil {
		clipsDispatcherPort = &clipsDispatcherAdapter{disp: params.Dispatcher}
	}
	// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): wire the
	// canonical mutations.AssetMutationDispatcher SSOT for the wider
	// application-layer producers (BulkUploadWorker + ReprocessUseCase
	// constructed inside NewHandler). Strict fail-closed — composition
	// errors when dispatcher is nil, mirroring WireArtlist's strict
	// composition-time check (QDRANT-002 PR7 invariant).
	mutationsDisp, err := newMutationsDispatcherAdapter(params.Dispatcher)
	if err != nil {
		return nil, nil, fmt.Errorf("clips: mutations dispatcher: %w", err)
	}

	// (1b) DuplicateFinder
	//
	// Wave 4 (July 2026): the canonical duplicate-detection service.
	// It fans out hash lookups to registered sources. Today the only
	// source is the SQLite clips repository; future sources plug in via
	// the duplicates.Source port.
	duplicateFinder := duplicates.NewFinder(
		NewClipsRepoDuplicateSource("local", params.Deps.Core.ClipsRepo),
	)

	// (2a) EnrichUseCase (card 10: returns *EnrichUseCase, error)
	//
	// S1a (June 2026): construct the shared EnrichUseCase ONCE at
	// composition time. The clipsHandler receives it via Deps.EnrichUC
	// so the same instance is used by:
	//   (a) the handler's EnrichMedia / CreateClip / UploadVideoClip /
	//       ReindexClip paths, and
	//   (b) the media.enrich worker registered below, which the
	//       handlers now enqueue to instead of spawning goroutines
	//       with context.WithoutCancel.
	//
	// Wave 2 (Asset commit + Qdrant, July 2026): EnrichUseCase now
	// depends on the canonical mutations.AssetMutationDispatcher so
	// enriched metadata is persisted and re-indexed through the outbox
	// pipeline. Direct clipIndexer.IndexClip calls are removed.
	//
	// Card 10 (July 2026): NewEnrichUseCase returns (*UseCase, error)
	// — nil dispatcher is a hard error (godlike/07 fail-closed). The
	// legacy assetRepo fallback inside the use case is retired; the
	// composition root MUST pass the canonical dispatcher (WireAssets
	// already does).
	enrichUC, err := appclips.NewEnrichUseCase(params.AssetRepo, params.MetaWriter, mutationsDisp, params.Log)
	if err != nil {
		return nil, nil, fmt.Errorf("clips: NewEnrichUseCase: %w", err)
	}

	// (2b) BulkUploadWorker (W14 PR2 slice 3, June 2026)
	//
	// Constructed from typed ports so the handler never imports infra
	// for the bulk-upload job path. PR 7 (June 2026,
	// codex/qdrant-app-writers-fail-closed): the mutations.AssetMutationDispatcher
	// SSOT is the 7th positional arg so the worker routes media_assets
	// UPSERT through the canonical outbox+tx writer (QDRANT-002
	// atomicity invariant). HC-1 (June 2026): the `cfg` 5th positional
	// arg is now constructed with appjobs.Compose() as the typed
	// TimeoutResolver — the bulk_upload worker uses
	// cfg.JobTimeout(TypeBulkUploadYouTubeClips) to derive its
	// 2*time.Hour literal via the canonical Registry (replaces the
	// pre-HC-1 hard-coded context.WithTimeout(ctx, 2*time.Hour) in
	// bulk_upload_worker.go::HandleJob).
	bulkUploadWorker := appclips.NewBulkUploadWorker(
		params.Deps.Delivery.Publisher,
		newClipsRepoAdapter(params.Deps.Core.ClipsRepo),
		newClipsHashAdapter(),
		newClipsCfgAdapter(params.Cfg, appjobs.Compose()),
		mutationsDisp,
		params.Log,
	)

	// (2c) Upload UseCase (P1.5, June 2026 CUTOVER)
	//
	// Constructed from the canonical typed ports declared in
	// internal/application/clips/upload/ports.go. DriveUploader,
	// IndexDispatcher, Config, and TreeBuilder are type aliases of the
	// parent clips.* ports (ClipDriveUploaderPort, etc.) so the
	// existing adapters satisfy them directly. P0.1 (June 2026):
	// Artifact is now mandatory — the composition root wires
	// *artifacts.Service (constructed in BuildDomainBundle) via
	// artifactServiceAdapter (clips_adapters_artifact.go). A nil
	// ArtifactService in CoreDeps causes NewUseCase to return error
	// at boot time instead of silently producing HTTP 500 at request
	// time. F2.9 (June 2026): DriveUploader dropped — Publisher is
	// the single canonical Drive-write canal.
	uploadUC, err := appupload.NewUseCase(appupload.UseCaseDeps{
		Artifact:      NewArtifactServiceAdapter(params.Deps.Core.ArtifactService),
		Publisher:     params.Deps.Delivery.Publisher,
		Dispatcher:    clipsDispatcherPort,
		Config:        newClipsCfgAdapter(params.Cfg, appjobs.Compose()),
		TreeBuilder:   newClipsAssetTreeAdapter(params.Deps.Core.AssetTreeService),
		JobsSvc:       params.Jobs.Facade,
		ProcessRunner: processRunnerAdapter,
		Log:           params.Log,
	})
	if err != nil {
		return nil, fmt.Errorf("clips: upload.NewUseCase: %w", err)
	}

	// (2d) Reupload UseCase (F2.9, June 2026)
	//
	// Wired to the canonical delivery.Publisher. The legacy composition
	// wired driveUploader (ClipDriveUploaderPort) which has been
	// retired; NewReuploadUseCase panics on nil publisher as a
	// composition-time fail-fast (mirrors processor.NewProcessor,
	// F2.8 closure). Folder-root mapping is config-driven; the artlist
	// + stock path markers use the Storage methods discovered via
	// cfg audit — empty PathMarker for a source disables dynamic
	// resolution for that source (clip.FolderID() is then required).
	reuploadFolderRoots := map[string]appclips.ReuploadFolderRoot{
		"clips":   {RootID: params.Cfg.Drive.ClipsFolder(), PathMarker: params.Cfg.Storage.YoutubeClipsPath()},
		"youtube": {RootID: params.Cfg.Drive.ClipsFolder(), PathMarker: params.Cfg.Storage.YoutubeClipsPath()},
		"artlist": {RootID: params.Cfg.Drive.ArtlistFolder(), PathMarker: params.Cfg.Storage.ArtlistPath()},
		"stock":   {RootID: params.Cfg.Drive.StockFolder(), PathMarker: params.Cfg.Storage.FullPath("stock")},
	}
	reuploadUC := appclips.NewReuploadUseCase(
		params.AssetRepo,
		params.Deps.Delivery.Publisher,
		clipsDispatcherPort,
		reuploadFolderRoots,
		params.Log,
	)

	// (2e) ClipOpsService (PR 2, June 2026)
	//
	// The HTTP handler delegates Reconcile / Cleanup / VerifyClip to a
	// single canonical service instead of duplicating the business logic
	// locally. The clipsAdapterBundle exposes typed ports for every dep
	// the service takes; the new clipsJobsPortAdapter bridges
	// domain/job.Service.Enqueue into the service's narrowed DTO. HC-1 (June
	// 2026): pass the typed TimeoutResolver (canonical impl:
	// appjobs.Compose() — *jobs.Registry) so the cfg adapter in the
	// bundle has the timeouts port wired.
	// PR-CLIPS-DAPTER-BUNDLE-SLIM (July 2026): buildClipOpsPorts is the
	// strict 2-arg canonical constructor (clipRepo, jobs). The 4
	// cross-domain deps (VoiceoverRepo + ImagesRepo + DriveUploader +
	// DriveLifecycle) are pulled off JobsBundle (polluted per the
	// godlike/06 SSOT pollute-at-the-bundle-stem trade-off; see
	// internal/app/module_jobs.go for the JobsBundle surfacing).
	// The clipsDispatcherPort and metaWriter + deps.Core.AssetTreeService
	// construction sites stay INLINE here — the buildClipOpsPorts
	// contract is strictly minimal per the user spec literal.
	clipsOpsPorts := buildClipOpsPorts(newClipsRepoAdapter(params.Deps.Core.ClipsRepo), params.Jobs)
	clipOpsSvc := appclips.NewClipOpsService(
		// The clipsOpsPorts struct fields map 1:1 to ClipOpsService
		// deps per the canonical surface contract. SourceResolver
		// (retired in PR-CLIPS-DAPTER-RESOLVER-RETIRE) and the 5-dead-
		// weight fields (Cfg / StockRepo / ArtlistRepo / MetaWriter /
		// HashSvc / TreeBuilderSvc) are no longer threaded.
		clipsOpsPorts.clipRepo,
		clipsOpsPorts.voiceoverRepo,
		clipsOpsPorts.imageRepo,
		clipsOpsPorts.driveUploader,
		clipsOpsPorts.jobsPort,
		clipsDispatcherPort,
		params.Log,
	)

	// (3) Canonical Build call
	//
	// Blocco C1-Step 5 (June 2026): Clips capability is now built via
	// the canonical clips.Build(deps) (api.Descriptor, error) contract,
	// matching the artlist / youtube precedent. The orchestrator
	// *Handler is constructed inside Build and captured by the
	// returned Module's closure. clipsDescriptor.Handler stays
	// accessible to the one non-HTTP consumer
	// (newAssetRegisterService → sourcingEnrichmentAdapter →	// handler.EnrichAndIndexClip).
	descriptor, err := clipsapi.Build(clipsapi.Dependencies{
		ClipsRepo:        params.Deps.Core.ClipsRepo,
		AssetRepo:        params.AssetRepo,
		DeletionSvc:      params.DeletionSvc,
		DriveAdmin:       params.DriveUploader,
		MediaProcessor:   params.Deps.Core.MediaProcessor,
		AssetTreeSvc:     params.Deps.Core.AssetTreeService,
		MetaWriter:       params.MetaWriter,
		ClipIndexer:      params.Deps.Search.ClipIndexerService,
		JobsSvc:          params.Jobs.Facade,
		Cfg:              params.Cfg,
		Log:              params.Log,
		VoiceoverRepo:    params.Deps.Core.VoiceoverRepo,
		ImagesRepo:       params.Deps.Core.ImageRepo,
		FolderMemSvc:     params.FolderMemSvc,
		ProcessRunner:    processRunnerAdapter,
		Dispatcher:       clipsDispatcherPort,
		EnrichUC:         enrichUC,
		DuplicateFinder:  duplicateFinder,
		SearchSvc:        params.SearchAggregator,
		BulkUploadWorker: bulkUploadWorker,
		ClipOpsService:   clipOpsSvc,
		UploadUC:         uploadUC,
		ReuploadUC:       reuploadUC, // F2.9: wired via delivery.Publisher (was nil pre-F2.9)
		Publisher:        params.Deps.Delivery.Publisher,
		Idempotency:      params.IdemHandler,
		EnabledFunc:      func() bool { return true },
	})
	if err != nil {
		return nil, nil, err
	}

	// (4) ClipsModule returned directly by Build (PR-CLIPS-7-MODULES-UPPER-CLIPSMODULE, July 2026).
	//
	// PR-WIRE-ASSETS-NIL-CLASSIFICATION (2026-07-25): DepRequired fail-closed.
	// No more type-assertion — Build returns *clipsai.ClipsModule directly.
	// *ClipsModule satisfies api.Descriptor structurally (compile-time
	// pinned in clips/module.go via `var _ api.Descriptor = (*ClipsModule)(nil)`),
	// so the assetsapi.NewModule caller in wire_assets.go accepts it as
	// the api.Descriptor field directly.
	if err := ClassifyDepGet("WireAssets: clips: *clipsapi.ClipsModule is nil (godlike/07 fail-closed; PR-CLIPS-7-MODULES-UPPER-CLIPSMODULE invariant)", descriptor == nil, DepRequired, params.Log); err != nil {
		return nil, nil, err
	}
	// Card 10 (July 2026): return the canonical ClipEnricher typed
	// port alongside the module so WireAssets threads it into
	// the register-side sourcingEnrichmentAdapter (the one non-HTTP
	// consumer) without reaching through any internal/api/assets/
	// clips/* Handler field.
	return descriptor, enrichUC, nil
}
