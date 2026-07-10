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

// buildClipsBundle constructs the canonical *clipsapi.ClipsDescriptor
// by:
//  1. Constructing the dispatcher adapters (clipsDispatcherPort +
//     mutationsDisp) from the raw *outbox.Dispatcher
//  2. Building the application-layer use cases (enrichUC, bulkUploadWorker,
//     uploadUC, reuploadUC, clipOpsSvc) from the typed-port adapters
//  3. Calling clipsapi.Build with the 20-field Dependencies struct
//  4. Type-asserting the returned api.Descriptor to the concrete
//     *clipsapi.ClipsDescriptor (the descriptor exposes the raw
//     *Handler via .Handler for the one non-HTTP consumer: register)
func buildClipsBundle(
	cfg *config.Config,
	log *zap.Logger,
	deps *AssetsModuleDeps,
	jobs *JobsBundle,
	dispatcher *outbox.Dispatcher,
	driveUploader *driveutil.Uploader,
	lifecycle driveutil.FileLifecycle,
	assetRepo asset.Repository,
	searchAggregator *search.Aggregator,
	metaWriter semantic.MetadataWriterPort,
	folderMemSvc *foldermemory.Service,
	deletionSvc *deletion.DeletionService,
	idemHandler gin.HandlerFunc,
) (*clipsapi.ClipsDescriptor, error) {
	// (1) Dispatcher adapters
	//
	// PR12c (June 2026): wire the dispatcher's port, NOT the concrete
	// *outbox.Dispatcher, into the unified clips handler. Adapter is
	// constructed only when dispatcher is non-nil (partial deploy path);
	// when dispatcher is nil the handler falls back to raw repo.UpsertClip
	// via the `if h.dispatcher != nil` guard in UpdateClip.
	var clipsDispatcherPort appclips.ClipIndexDispatcherPort
	if dispatcher != nil {
		clipsDispatcherPort = &clipsDispatcherAdapter{disp: dispatcher}
	}
	// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): wire the
	// canonical mutations.AssetMutationDispatcher SSOT for the wider
	// application-layer producers (BulkUploadWorker + ReprocessUseCase
	// constructed inside NewHandler). Strict fail-closed — composition
	// errors when dispatcher is nil, mirroring WireArtlist's strict
	// composition-time check (QDRANT-002 PR7 invariant).
	mutationsDisp, err := newMutationsDispatcherAdapter(dispatcher)
	if err != nil {
		return nil, fmt.Errorf("clips: mutations dispatcher: %w", err)
	}

	// (2a) EnrichUseCase
	//
	// S1a (June 2026): construct the shared EnrichUseCase ONCE at
	// composition time. The clipsHandler receives it via Deps.EnrichUC
	// so the same instance is used by:
	//   (a) the handler's EnrichMedia / CreateClip / UploadVideoClip /
	//       ReindexClip paths, and
	//   (b) the media.enrich worker registered below, which the
	//       handlers now enqueue to instead of spawning goroutines
	//       with context.WithoutCancel.
	enrichUC := appclips.NewEnrichUseCase(assetRepo, deps.Search.ClipIndexerService, metaWriter, log)

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
		deps.Delivery.Publisher,
		newClipsRepoAdapter(deps.Core.ClipsRepo),
		newClipsIndexerAdapter(deps.Search.ClipIndexerService),
		newClipsHashAdapter(),
		newClipsCfgAdapter(cfg, appjobs.Compose()),
		mutationsDisp,
		log,
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
		Artifact:      NewArtifactServiceAdapter(deps.Core.ArtifactService),
		Publisher:     deps.Delivery.Publisher,
		Dispatcher:    clipsDispatcherPort,
		Config:        newClipsCfgAdapter(cfg, appjobs.Compose()),
		TreeBuilder:   newClipsAssetTreeAdapter(deps.Core.AssetTreeService),
		JobsSvc:       jobs.Facade,
		ProcessRunner: processRunnerAdapter,
		Log:           log,
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
		"clips":   {RootID: cfg.Drive.ClipsFolder(), PathMarker: cfg.Storage.YoutubeClipsPath()},
		"youtube": {RootID: cfg.Drive.ClipsFolder(), PathMarker: cfg.Storage.YoutubeClipsPath()},
		"artlist": {RootID: cfg.Drive.ArtlistFolder(), PathMarker: cfg.Storage.ArtlistPath()},
		"stock":   {RootID: cfg.Drive.StockFolder(), PathMarker: cfg.Storage.FullPath("stock")},
	}
	reuploadUC := appclips.NewReuploadUseCase(
		assetRepo,
		deps.Delivery.Publisher,
		clipsDispatcherPort,
		reuploadFolderRoots,
		log,
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
	clipsOpsPorts := buildClipOpsPorts(newClipsRepoAdapter(deps.Core.ClipsRepo), jobs)
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
		log,
	)

	// (3) Canonical Build call
	//
	// Blocco C1-Step 5 (June 2026): Clips capability is now built via
	// the canonical clips.Build(deps) (api.Descriptor, error) contract,
	// matching the artlist / youtube precedent. The orchestrator
	// *Handler is constructed inside Build and captured by the
	// returned Module's closure. clipsDescriptor.Handler stays
	// accessible to the one non-HTTP consumer
	// (newAssetRegisterService → sourcingEnrichmentAdapter →
	// handler.EnrichAndIndexClip).
	descriptor, err := clipsapi.Build(clipsapi.Dependencies{
		ClipsRepo:        deps.Core.ClipsRepo,
		AssetRepo:        assetRepo,
		DeletionSvc:      deletionSvc,
		DriveAdmin:       driveUploader,
		MediaProcessor:   deps.Core.MediaProcessor,
		AssetTreeSvc:     deps.Core.AssetTreeService,
		MetaWriter:       metaWriter,
		ClipIndexer:      deps.Search.ClipIndexerService,
		JobsSvc:          jobs.Facade,
		Cfg:              cfg,
		Log:              log,
		VoiceoverRepo:    deps.Core.VoiceoverRepo,
		ImagesRepo:       deps.Core.ImageRepo,
		FolderMemSvc:     folderMemSvc,
		ProcessRunner:    processRunnerAdapter,
		Dispatcher:       clipsDispatcherPort,
		EnrichUC:         enrichUC,
		SearchSvc:        searchAggregator,
		BulkUploadWorker: bulkUploadWorker,
		ClipOpsService:   clipOpsSvc,
		UploadUC:         uploadUC,
		ReuploadUC:       reuploadUC, // F2.9: wired via delivery.Publisher (was nil pre-F2.9)
		Publisher:        deps.Delivery.Publisher,
		Idempotency:      idemHandler,
		EnabledFunc:      func() bool { return true },
	})
	if err != nil {
		return nil, err
	}

	// (4) Type-assert to the concrete *clipsapi.ClipsDescriptor
	//
	// The Descriptor's forwarder methods (Name/Enabled/RegisterRoutes)
	// are interface-level; the non-HTTP consumer below
	// (sourcingEnrichmentAdapter → handler.EnrichAndIndexClip) needs
	// the raw orchestrator *Handler, which is reachable only via the
	// concrete *ClipsDescriptor.Handler field. Type-assert once and
	// reuse the concrete for both consumers (the concrete
	// *ClipsDescriptor satisfies api.Descriptor structurally, so the
	// assetsapi.NewModule call in wire_assets.go accepts it).
	desc, ok := descriptor.(*clipsapi.ClipsDescriptor)
	// PR-WIRE-ASSETS-NIL-CLASSIFICATION (2026-07-25): DepRequired via helper. Also adds the missing `desc == nil` post-assertion check (the other 6 descriptor sites already had it; this site had only `!ok` — the inconsistency this PR fixes).
	if err := ClassifyDepGet(fmt.Sprintf("WireAssets: clips (got %T, want *clipsapi.ClipsDescriptor)", descriptor), !ok || desc == nil, DepRequired, log); err != nil {
		return nil, err
	}
	return desc, nil
}
