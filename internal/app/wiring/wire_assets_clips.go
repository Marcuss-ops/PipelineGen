// Package app owns the complete clips composition pipeline. All application
// use cases are built here before the HTTP module is constructed.
package wiring

import (
	"context"
	"fmt"
	"strings"

	registrywiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/registry"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/assettree"
	clipsapi "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/clips/nonops"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/duplicates"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips/aistock"
	appupload "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips/upload"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	ytadapters "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/adapters"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing/clipindexer"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesrepo"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ClipsRepositoryDeps groups the repository ports consumed by clips.
//
// AssetRepo is the media read port: PostgreSQL is the media SSOT, so the
// production concrete is the PostgreSQL media read store and the SQLite
// adapter is selected only in the documented media-disabled degrade mode.
// It is typed as the clips capability's consumer-owned interface, NOT the
// detail.Repository type-switch bridge (which no non-SQLite store can
// satisfy).
type ClipsRepositoryDeps struct {
	ClipsRepo     *sqassets.ClipsRepository
	VoiceoverRepo *sqassets.VoiceoversRepository
	ImageRepo     *imagesrepo.ImagesRepository
	AssetRepo     clipsapi.AssetReader
	// MediaSearch answers the ListClips text-search branch from the media
	// SSOT. Nil when the media plane is closed.
	MediaSearch clipsapi.MediaClipSearcher
}

// ClipsCapabilityDeps contains only the concrete ports consumed by the clips
// capability builder. AssetsModuleDeps is projected into this bundle at the
// WireAssets boundary and does not cross into the builder.
type ClipsCapabilityDeps struct {
	Repositories       ClipsRepositoryDeps
	ArtifactService    *artifacts.Service
	AssetTreeService   *assettree.Service
	MediaProcessor     detail.Processor
	Publisher          delivery.Publisher
	ClipIndexerService *clipindexer.Service
}

type buildClipsParams struct {
	Cfg           *config.Config
	Log           *zap.Logger
	Clips         ClipsCapabilityDeps
	Jobs          *JobsBundle
	Dispatcher    *outbox.Dispatcher
	DriveUploader *driveutil.Uploader
	MetaWriter    semantic.MetadataWriterPort
	DeletionSvc   *deletion.DeletionService
	IdemHandler   gin.HandlerFunc
}

// clipsAssetJobStatus adapts the canonical job execution ledger to the clips
// capability's optional download-status port.
//
// The ledger field is the write-only capjobregistry.Registry interface, so the
// concrete read method is reached by a narrow structural assertion: a ledger
// implementation without it (or a nil bundle) simply leaves the port nil, and
// the download handler falls back to its generic 409 answer. No fake status is
// ever produced.
func clipsAssetJobStatus(jobs *JobsBundle) clipsapi.AssetJobStatusLookup {
	if jobs == nil || jobs.JobLedger == nil {
		return nil
	}
	reader, ok := jobs.JobLedger.(interface {
		LatestAssetJobStatus(context.Context, string) (string, int, bool)
	})
	if !ok {
		return nil
	}
	return clipsAssetJobStatusReader{reader: reader}
}

type clipsAssetJobStatusReader struct {
	reader interface {
		LatestAssetJobStatus(context.Context, string) (string, int, bool)
	}
}

func (a clipsAssetJobStatusReader) LatestJobStatus(ctx context.Context, assetID string) (string, int, bool) {
	return a.reader.LatestAssetJobStatus(ctx, assetID)
}

// ── clips text-search media SSOT adapter ─────────────────────────────

// clipsMediaSearcher is the narrow read surface the adapter needs. It is
// satisfied by *pgmedia.MediaSearcher — the single media read authority.
type clipsMediaSearcher interface {
	SearchLocal(ctx context.Context, req pgmedia.LocalMediaSearchRequest) ([]pgmedia.MediaAssetRecord, error)
}

// postgresClipMediaSearch answers the clips ListClips text-search branch
// (`GET /:source/clips?q=...`) from the PostgreSQL media SSOT.
//
// P2-9 (September 2026): the retired branch called
// imagesregistry.AssetStoreSQLite.SearchClips, whose fast path queried the
// operational clip_search_terms inverted index and re-hydrated the operational
// media_assets mirror — which the canonical PostgreSQL committer never
// populates. The handler could only ever grade a stale pre-cutover catalog.
// SearchLocal already carries the canonical term corpus (name / search_text /
// search_terms) with the same per-term AND semantics, so no secondary term
// table is needed and the ranking stays canonical (detail.ScoreClips).
type postgresClipMediaSearch struct {
	searcher clipsMediaSearcher
}

var _ clipsapi.MediaClipSearcher = (*postgresClipMediaSearch)(nil)

// newPostgresClipMediaSearch returns nil for a nil searcher (or one whose
// handle is closed) so the handler FAILS CLOSED instead of degrading onto the
// retired operational index. It never panics on a closed media plane: the
// caller gates on deps.MediaPostgres, and this guard covers the typed-nil case.
func newPostgresClipMediaSearch(searcher clipsMediaSearcher) clipsapi.MediaClipSearcher {
	if searcher == nil {
		return nil
	}
	return &postgresClipMediaSearch{searcher: searcher}
}

func (p *postgresClipMediaSearch) SearchClipsByTerms(ctx context.Context, source string, terms []string, limit int) ([]*asset.Asset, error) {
	cleaned := make([]string, 0, len(terms))
	for _, term := range terms {
		if trimmed := strings.TrimSpace(term); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	if len(cleaned) == 0 {
		return []*asset.Asset{}, nil
	}
	records, err := p.searcher.SearchLocal(ctx, pgmedia.LocalMediaSearchRequest{
		AllTerms: cleaned,
		Source:   source,
		Limit:    limit,
	})
	if err != nil {
		return nil, err
	}
	clips := make([]*asset.Asset, 0, len(records))
	for i := range records {
		if hydrated := records[i].HydrateAsset(); hydrated != nil {
			clips = append(clips, hydrated)
		}
	}
	return detail.ScoreClips(clips, cleaned), nil
}

func buildClipsBundle(params buildClipsParams) (*clipsapi.ClipsModule, appclips.ClipEnricher, error) {
	var clipsDispatcherPort appclips.ClipIndexDispatcherPort
	if params.Dispatcher != nil {
		clipsDispatcherPort = &clipsDispatcherAdapter{disp: params.Dispatcher}
	}
	mutationsDisp, err := registrywiring.NewMutationsDispatcherAdapter(params.Dispatcher)
	if err != nil {
		return nil, nil, fmt.Errorf("clips: mutations dispatcher: %w", err)
	}

	duplicateFinder := duplicates.NewFinder(
		NewClipsRepoDuplicateSource("local", params.Clips.Repositories.ClipsRepo),
	)
	enrichUC, err := appclips.NewEnrichUseCase(
		params.Clips.Repositories.AssetRepo,
		params.MetaWriter,
		mutationsDisp,
		params.Log,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("clips: NewEnrichUseCase: %w", err)
	}

	bulkUploadWorker := appclips.NewBulkUploadWorker(
		params.Clips.Publisher,
		newClipsRepoAdapter(params.Clips.Repositories.ClipsRepo),
		newClipsHashAdapter(),
		newClipsCfgAdapter(params.Cfg, appjobs.Compose()),
		mutationsDisp,
		params.Log,
	)

	uploadUC, err := appupload.NewUseCase(appupload.UseCaseDeps{
		Artifact:      NewArtifactServiceAdapter(params.Clips.ArtifactService),
		Publisher:     params.Clips.Publisher,
		Dispatcher:    clipsDispatcherPort,
		Config:        newClipsCfgAdapter(params.Cfg, appjobs.Compose()),
		TreeBuilder:   newClipsAssetTreeAdapter(params.Clips.AssetTreeService),
		JobsSvc:       params.Jobs.Facade,
		ProcessRunner: processRunnerAdapter,
		Log:           params.Log,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("clips: upload.NewUseCase: %w", err)
	}

	aiStockUC, err := aistock.NewUseCase(aistock.UseCaseDeps{
		DriveReader: newAistockDriveReaderAdapter(params.DriveUploader),
		Artifact:    NewArtifactServiceAdapter(params.Clips.ArtifactService),
		Dispatcher:  clipsDispatcherPort,
		Log:         params.Log,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("clips: aistock.NewUseCase: %w", err)
	}

	reuploadFolderRoots := map[string]appclips.ReuploadFolderRoot{
		"clips":   {RootID: params.Cfg.Drive.ClipsFolder(), PathMarker: params.Cfg.Storage.YoutubeClipsPath()},
		"youtube": {RootID: params.Cfg.Drive.ClipsFolder(), PathMarker: params.Cfg.Storage.YoutubeClipsPath()},
		"artlist": {RootID: params.Cfg.Drive.ArtlistFolder(), PathMarker: params.Cfg.Storage.ArtlistPath()},
		"stock":   {RootID: params.Cfg.Drive.StockFolder(), PathMarker: params.Cfg.Storage.FullPath("stock")},
	}
	reuploadUC := appclips.NewReuploadUseCase(
		params.Clips.Repositories.AssetRepo,
		params.Clips.Publisher,
		clipsDispatcherPort,
		reuploadFolderRoots,
		params.Log,
	)

	clipsOpsPorts := buildClipOpsPorts(
		newClipsRepoAdapter(params.Clips.Repositories.ClipsRepo),
		params.Jobs,
	)
	clipOpsSvc := appclips.NewClipOpsService(
		clipsOpsPorts.clipRepo,
		clipsOpsPorts.voiceoverRepo,
		clipsOpsPorts.imageRepo,
		clipsOpsPorts.driveUploader,
		clipsOpsPorts.jobsPort,
		clipsDispatcherPort,
		params.Log,
	)

	// These use cases previously lived in clips.NewHandler. Building them here
	// makes the API package transport-only and gives NonOps/Actions precisely
	// the operations they execute.
	downloadUC := appclips.NewDownloadUseCase(
		params.Clips.Repositories.AssetRepo,
		params.Clips.Repositories.VoiceoverRepo,
	)
	reprocessUC := appclips.NewReprocessUseCase(
		params.Clips.Repositories.AssetRepo,
		params.Clips.MediaProcessor,
		mutationsDisp,
		params.Cfg.Drive.ClipsFolder(),
	)
	if params.DriveUploader != nil {
		reprocessUC.SetRemoteAssetReader(params.DriveUploader)
	}

	clipsDrive := ytadapters.NewClipsDriveAdapter(params.DriveUploader, params.DriveUploader, nil)

	descriptor, err := clipsapi.Build(clipsapi.Dependencies{
		Handlers: clipsapi.Deps{
			Search: clipsapi.SearchDeps{
				ClipsRepo:     newClipsRepoAdapter(params.Clips.Repositories.ClipsRepo),
				AssetRepo:     params.Clips.Repositories.AssetRepo,
				VoiceoverRepo: newVoiceoverRepoAdapter(params.Clips.Repositories.VoiceoverRepo),
				ImagesRepo:    params.Clips.Repositories.ImageRepo,
				MediaSearch:   params.Clips.Repositories.MediaSearch,
			},
			Ingest: clipsapi.IngestDeps{
				Dispatcher:   clipsDispatcherPort,
				AssetTreeSvc: params.Clips.AssetTreeService,
				JobsSvc:      params.Jobs.Facade,
				ClipsRepo:    newClipsRepoAdapter(params.Clips.Repositories.ClipsRepo),
				EnrichUC:     enrichUC,
				UploadUC:     uploadUC,
				AIStockUC:    aiStockUC,
				Log:          params.Log,
			},
			Operations: clipsapi.OpsDeps{
				ClipOpsService: clipOpsSvc,
				DeletionSvc:    params.DeletionSvc,
				ClipsRepo:      newClipsRepoAdapter(params.Clips.Repositories.ClipsRepo),
				DriveAdmin:     clipsDrive,
				AssetTreeSvc:   params.Clips.AssetTreeService,
				Log:            params.Log,
			},
			NonOps: nonops.Deps{
				ReprocessUC:      reprocessUC,
				EnrichUC:         enrichUC,
				JobsSvc:          params.Jobs.Facade,
				BulkUploadWorker: bulkUploadWorker,
				Log:              params.Log,
			},
			Bulk: clipsapi.BulkTransportDeps{
				JobsSvc:          params.Jobs.Facade,
				MediaPath:        params.Cfg.Storage.MediaPath(),
				TempPath:         params.Cfg.Storage.TempPath(),
				DataDir:          params.Cfg.Storage.AbsDataDir(),
				BulkUploadWorker: bulkUploadWorker,
				Log:              params.Log,
			},
			Actions: clipsapi.ActionDeps{
				AssetRepo:       params.Clips.Repositories.AssetRepo,
				DriveAdmin:      clipsDrive,
				DuplicateFinder: duplicateFinder,
				DownloadUC:      downloadUC,
				ReuploadUC:      reuploadUC,
				JobStatus:       clipsAssetJobStatus(params.Jobs),
				Log:             params.Log,
			},
		},
		Transport: clipsapi.TransportDeps{
			Idempotency: params.IdemHandler,
			EnabledFunc: func() bool { return true },
			Logger:      params.Log,
		},
	})
	if err != nil {
		return nil, nil, err
	}
	if err := ClassifyDepGet(
		"WireAssets: clips: *clipsapi.ClipsModule is nil",
		descriptor == nil,
		DepRequired,
		params.Log,
	); err != nil {
		return nil, nil, err
	}
	return descriptor, enrichUC, nil
}
