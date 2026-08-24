// Package app — build_bundles_ingest.go (split July 2026).
//
// This file owns the ingest service construction, the image-list-repo
// adapter, and the image-search-resolver wiring. Extracted from
// build_bundles_domain.go per AGENTS.md Pattern 5.
//
// godlike/06 SSOT: buildIngestService is the single canonical owner of
// the ingest.Service construction. buildImageSearchResolver is the single
// canonical owner of the FASE 7 routing layer wiring.
package app

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"
	"time"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/mutations"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	imgservice "github.com/Marcuss-ops/PipelineGen/internal/application/images"
	"github.com/Marcuss-ops/PipelineGen/internal/application/images/routing"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	imagesregistry "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/imagesregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets/imagesrepo"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// imageListRepoAdapter bridges *imagesrepo.ImagesRepository (the infra
// producer of routing.RepositoryListFilter + []routing.RepositoryImageRow)
// to the canonical routing.ImageListRepository interface expected by
// the FASE 7 ImageSearchResolver (which accepts routing.ImageFilter +
// []routing.ImageSearchResult). The two structs are structurally
// identical — different package names only — so the adapter does a
// field-for-field rebind with no data loss.
type imageListRepoAdapter struct {
	repo *imagesrepo.ImagesRepository
}

// ListImages satisfies routing.ImageListRepository. Repository-only
// fields (Subject/Slug/Description/Tags/CreatedAt on the row) are
// dropped since the canonical ImageSearchResult does not expose
// them; field-for-field bind for the rest.
func (a *imageListRepoAdapter) ListImages(ctx context.Context, filter routing.ImageFilter) ([]routing.ImageSearchResult, error) {
	if a == nil || a.repo == nil {
		return nil, nil
	}
	dbRows, err := a.repo.ListImages(ctx, routing.RepositoryListFilter{
		SubjectID: filter.SubjectID,
		Origins:   filter.Origins,
		Providers: filter.Providers,
		StyleIDs:  filter.StyleIDs,
		Tags:      filter.Tags,
		Limit:     filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]routing.ImageSearchResult, 0, len(dbRows))
	for _, r := range dbRows {
		out = append(out, routing.ImageSearchResult{
			AssetID:       r.AssetID,
			Origin:        r.Origin,
			Provider:      r.Provider,
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

// Compile-time assertion: imageListRepoAdapter satisfies the
// canonical routing ImageListRepository.
var _ routing.ImageListRepository = (*imageListRepoAdapter)(nil)

// buildImageSearchResolver wires the FASE 7 routing layer
// (routing.ImageSearchResolver) from the canonical image-side deps.
// Fail-closed (godlike/07): if either input is nil we surface the
// composition error so NewComposition aborts rather than silently
// mounting a half-wired resolver.
func buildImageSearchResolver(imageSvc *imgservice.Service, imageRepo *imagesrepo.ImagesRepository, log *zap.Logger) (routing.ImageSearchResolver, error) {
	if imageSvc == nil || imageSvc.RetrievalRegistry() == nil {
		return nil, fmt.Errorf("routing.NewImageSearchResolver: retrieval backend is nil — image service must be constructed first")
	}
	if imageRepo == nil {
		return nil, fmt.Errorf("routing.NewImageSearchResolver: image list repository is nil — repos.ImageRepo required")
	}
	resolver, err := routing.NewImageSearchResolver(
		routing.WithRetrievalBackend(imageSvc.RetrievalRegistry()),
		routing.WithImageListRepository(&imageListRepoAdapter{repo: imageRepo}),
	)
	if err != nil {
		return nil, fmt.Errorf("routing.NewImageSearchResolver: %w", err)
	}
	log.Info("FASE 7: ImageSearchResolver wired (retrieval backend + image list repo)")
	return resolver, nil
}

// buildIngestService constructs the ingest.Service from the same deps
// that WireMediaIngest uses.
//
// PR 7 (June 2026, codex/qdrant-app-writers-fail-closed): mutationsDisp
// is the 7th positional arg so the four
// artifacts.NewClipsRegistry + ingest.NewClipStoreAdapter ctor calls
// inside this function route their media_assets UPSERT through the
// canonical outbox+tx writer. mutationsDisp is constructed once in
// BuildDomainBundle and reused so the same SSOT instance flows into
// every caller without raising the boot-time cost of repeated
// newMutationsDispatcherAdapter wraps. The buildIngestService signature
// drops the previously-threaded *wiring.OutboxBundle arg (it was never read
// inside the function body) — the caller still constructs mutationsDisp
// from outbox.Dispatcher, so the Site-1 wiring is unchanged.
func buildIngestService(cfg *config.Config, log *zap.Logger, dbs *wiring.Databases, driveUploader *driveutil.Uploader, publisher delivery.Publisher, repos *wiring.RepoBundle, search *wiring.SearchBundle, mutationsDisp mutations.AssetMutationDispatcher, committer assetspersistence.AssetCommitter) *ingest.Service {
	if driveUploader == nil {
		return nil
	}
	if repos.ImageRepo == nil || repos.VoiceoverRepo == nil || repos.ClipsRepo == nil || search.AssetIndexService == nil {
		return nil
	}
	if mutationsDisp == nil {
		log.Warn("buildIngestService: mutationsDisp is nil — ingest will surface ErrDispatcherUnavailable on first Upsert (QDRANT-002 PR7 fail-closed)")
	}
	imagesRegistry := imagesregistry.NewRegistryAdapter(repos.ImageRepo, cfg.Storage.ImagesPath(), log, committer)
	imagesLifecycle := NewLifecycleFromDeps(&AssetLifecycleDeps{Registry: imagesRegistry, Publisher: publisher, DriveReader: driveUploader, AssetIndex: search.AssetIndexService, Store: ingest.NewImageStoreAdapter(repos.ImageRepo, cfg.Storage.ImagesPath())}, log)
	voiceoverRegistry := voiceover.NewVoiceoverRegistryAdapter(repos.VoiceoverRepo)
	voiceoverLifecycle := NewLifecycleFromDeps(&AssetLifecycleDeps{Registry: voiceoverRegistry, Publisher: publisher, DriveReader: driveUploader, AssetIndex: search.AssetIndexService, Store: ingest.NewVoiceoverStoreAdapter(repos.VoiceoverRepo)}, log)
	clipRegistry := artifacts.NewClipsRegistry(dbs.DualPool.Writer, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), committer)
	clipLifecycle := NewLifecycleFromDeps(&AssetLifecycleDeps{Registry: clipRegistry, Publisher: publisher, DriveReader: driveUploader, AssetIndex: search.AssetIndexService, Store: ingest.NewClipStoreAdapter(dbs.DualPool.Writer, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), mutationsDisp)}, log)
	stockRegistry := artifacts.NewClipsRegistry(dbs.DualPool.Writer, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), committer)
	stockLifecycle := NewLifecycleFromDeps(&AssetLifecycleDeps{Registry: stockRegistry, Publisher: publisher, DriveReader: driveUploader, AssetIndex: search.AssetIndexService, Store: ingest.NewClipStoreAdapter(dbs.DualPool.Writer, repos.Assets.Repository(), repos.Assets, repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(), mutationsDisp)}, log)
	downloader := downloader.NewMediaDownloader(90 * time.Second)
	// PR-WAVE-1-DRIVE-SSOT (July 2026): the legacy
	// `driveUploader.Admin()` arg is REMOVED from the canonical
	// NewService ctor — the application-layer Service does not
	// hold drive.Admin references; the composition root owns
	// *driveutil.Uploader for the lifecycle adapter reads.
	return ingest.NewService(cfg, log, downloader, map[ingest.Kind]*ingest.Pipeline{
		ingest.KindImage:     {Kind: ingest.KindImage, DefaultSource: "image", RootFolderID: cfg.Drive.ImagesFolder(), Lifecycle: imagesLifecycle},
		ingest.KindVoiceover: {Kind: ingest.KindVoiceover, DefaultSource: "voiceover", RootFolderID: cfg.Drive.VoiceoverFolder(), Lifecycle: voiceoverLifecycle},
		ingest.KindClip:      {Kind: ingest.KindClip, DefaultSource: "youtube", RootFolderID: cfg.Drive.ClipsFolder(), Lifecycle: clipLifecycle},
		ingest.KindStock:     {Kind: ingest.KindStock, DefaultSource: "stock", RootFolderID: cfg.Drive.StockFolder(), Lifecycle: stockLifecycle},
		// PR-ENRICHMENT-STATE-MACHINE EXPAND phase: enrichState
		// passed as nil. The ingest service flips PENDING on every
		// freshly-ingested row only when the typed state-machine
		// wrapper is wired (canonical godlike/06 SSOT). Until the
		// composition root wires the state machine at boot, the VLM
		// 15-min sweeper still recovers via the typed-state filter
		// (backfill path per godlike/07). BACKFILL wave forward-
		// pointer wires the live state-machine here.
	}, nil /* enrichState: nil for EXPAND phase */)
}
