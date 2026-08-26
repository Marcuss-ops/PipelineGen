package wiring

import (
	"context"
	"fmt"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	voservice "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/downloader"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesrepo"
	"go.uber.org/zap"
)

type imageListRepoAdapter struct {
	repo *imagesrepo.ImagesRepository
}

func (a *imageListRepoAdapter) ListImages(ctx context.Context, filter images.ImageFilter) ([]images.ImageSearchResult, error) {
	if a == nil || a.repo == nil {
		return nil, nil
	}
	providers := make([]string, len(filter.Providers))
	for i, p := range filter.Providers {
		providers[i] = string(p)
	}
	dbRows, err := a.repo.ListImages(ctx, detail.RepositoryListFilter{
		SubjectID: filter.SubjectID,
		Origins:   filter.Origins,
		Providers: providers,
		StyleIDs:  filter.StyleIDs,
		Tags:      filter.Tags,
		Limit:     filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]images.ImageSearchResult, 0, len(dbRows))
	for _, r := range dbRows {
		out = append(out, images.ImageSearchResult{
			AssetID:       r.AssetID,
			Origin:        string(r.Origin),
			Provider:      string(r.Provider),
			Name:          r.Name,
			PreviewURL:    r.PreviewURL,
			DriveLink:     r.DriveLink,
			LegacyFileMD5: r.LegacyFileMD5,
			SourcePageURL: r.SourcePageURL,
			Author:        r.Author,
			Width:         r.Width,
			Height:        r.Height,
			Score:         r.Score,
			StyleID:       r.StyleID,
			StyleVersion:  r.StyleVersion,
			License:       r.License,
		})
	}
	return out, nil
}

var _ images.ImageListRepository = (*imageListRepoAdapter)(nil)

func buildImageSearchResolver(imageSvc *imgservice.Service, imageRepo *imagesrepo.ImagesRepository, log *zap.Logger) (images.ImageSearchResolver, error) {
	if imageSvc == nil || imageSvc.RetrievalRegistry() == nil {
		return nil, fmt.Errorf("images.NewImageSearchResolver: retrieval backend is nil — image service must be constructed first")
	}
	if imageRepo == nil {
		return nil, fmt.Errorf("images.NewImageSearchResolver: image list repository is nil — repos.ImageRepo required")
	}
	resolver, err := images.NewImageSearchResolver(
		images.WithRetrievalBackend(imageSvc.RetrievalRegistry()),
		images.WithImageListRepository(&imageListRepoAdapter{repo: imageRepo}),
	)
	if err != nil {
		return nil, fmt.Errorf("images.NewImageSearchResolver: %w", err)
	}
	log.Info("FASE 7: ImageSearchResolver wired (retrieval backend + image list repo)")
	return resolver, nil
}

func buildIngestService(
	cfg *config.Config,
	log *zap.Logger,
	dbs *Databases,
	driveUploader *driveutil.Uploader,
	publisher delivery.Publisher,
	repos *RepoBundle,
	search *SearchBundle,
	mutationsDisp mutations.AssetMutationDispatcher,
	canonicalCommitter *imagesregistry.SQLiteMediaCommitter,
) *ingest.Service {
	if driveUploader == nil {
		return nil
	}
	if repos.ImageRepo == nil || repos.VoiceoverRepo == nil || repos.ClipsRepo == nil || search.AssetIndexService == nil {
		return nil
	}
	if mutationsDisp == nil {
		log.Warn("buildIngestService: mutationsDisp is nil — ingest will surface ErrDispatcherUnavailable on first Upsert (QDRANT-002 PR7 fail-closed)")
	}
	imagesRegistry := imagesregistry.NewRegistryAdapter(repos.ImageRepo, cfg.Storage.ImagesPath(), log, canonicalCommitter)
	imagesLifecycle := NewLifecycleFromDeps(&AssetLifecycleDeps{Registry: imagesRegistry, Publisher: publisher, DriveReader: driveUploader, AssetIndex: search.AssetIndexService, Store: ingest.NewImageStoreAdapter(repos.ImageRepo, cfg.Storage.ImagesPath())}, log)
	voiceoverRegistry := voservice.NewVoiceoverRegistryAdapter(repos.VoiceoverRepo)
	voiceoverLifecycle := NewLifecycleFromDeps(&AssetLifecycleDeps{Registry: voiceoverRegistry, Publisher: publisher, DriveReader: driveUploader, AssetIndex: search.AssetIndexService, Store: ingest.NewVoiceoverStoreAdapter(repos.VoiceoverRepo)}, log)
	clipRegistry := artifacts.NewClipsRegistry(dbs.Main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), canonicalCommitter)
	clipLifecycle := NewLifecycleFromDeps(&AssetLifecycleDeps{Registry: clipRegistry, Publisher: publisher, DriveReader: driveUploader, AssetIndex: search.AssetIndexService, Store: ingest.NewClipStoreAdapter(dbs.Main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), mutationsDisp)}, log)
	stockRegistry := artifacts.NewClipsRegistry(dbs.Main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), canonicalCommitter)
	stockLifecycle := NewLifecycleFromDeps(&AssetLifecycleDeps{Registry: stockRegistry, Publisher: publisher, DriveReader: driveUploader, AssetIndex: search.AssetIndexService, Store: ingest.NewClipStoreAdapter(dbs.Main.DB, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), mutationsDisp)}, log)
	return ingest.NewService(cfg, log, downloader.NewMediaDownloader(10*time.Minute), map[ingest.Kind]*ingest.Pipeline{
		ingest.KindImage:     {Kind: ingest.KindImage, DefaultSource: "image", RootFolderID: cfg.Drive.ImagesFolder(), Lifecycle: imagesLifecycle},
		ingest.KindVoiceover: {Kind: ingest.KindVoiceover, DefaultSource: "voiceover", RootFolderID: cfg.Drive.VoiceoverFolder(), Lifecycle: voiceoverLifecycle},
		ingest.KindClip:      {Kind: ingest.KindClip, DefaultSource: "youtube", RootFolderID: cfg.Drive.ClipsFolder(), Lifecycle: clipLifecycle},
		ingest.KindStock:     {Kind: ingest.KindStock, DefaultSource: "stock", RootFolderID: cfg.Drive.StockFolder(), Lifecycle: stockLifecycle},
	}, nil)
}
