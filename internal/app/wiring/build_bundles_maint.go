// Package app — build_bundles_maint.go (split July 2026).
//
// This file owns the maintenance + deletion bundle construction.
// Extracted from build_bundles_domain.go per AGENTS.md Pattern 5.
//
// godlike/06 SSOT: BuildMaintBundle is the single canonical owner of the
// deletion + maintenance service construction.
package wiring

import (
	"context"
	"fmt"
	"net/http"
	"time"

	imagesregistry "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/deletion"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images/entitycatalog"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)
import (
	artifactfinalize "github.com/Marcuss-ops/PipelineGen/internal/capabilities/artifact_finalize"
)
import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/ingest"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/mutations"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/images"
	voservice "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/downloader"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesrepo"
)

// Compile-time cutover pins: the PostgreSQL media committer IS the
// authoritative read surface and saga dispatcher for the deletion path once
// the media plane is on PostgreSQL (MEDIA-SSOT read-side, September 2026).
// A future signature drift fails here rather than silently degrading the
// deletion entry point back to the SQLite mirror.
var (
	_ deletion.DispatcherPort            = (*pgmedia.PostgresMediaCommitter)(nil)
	_ deletion.MediaAssetLookupPort      = (*pgmedia.PostgresMediaCommitter)(nil)
	_ deletion.MediaAssetDriveLookupPort = (*pgmedia.PostgresMediaCommitter)(nil)
)

// BuildMaintBundle constructs the periodic maintenance + deletion services.
func BuildMaintBundle(ctx context.Context, cfg *config.Config, dbs *Databases, log *zap.Logger, drive *DriveBundle, repos *RepoBundle, search *SearchBundle, jobs *JobsBundle, outboxBundle *OutboxBundle, sourceCatalog *artifacts.SourceCatalog) (*MaintBundle, error) {
	_ = ctx
	// PR-WAVE-1-DRIVE-SSOT (July 2026): the driveUploader arg is
	// REMOVED from the canonical ctor — the field has been retired
	// from DeletionService (the value was unused by every service
	// method; the canonical async Drive surface is the dispatcher
	// outbox port per godlike/06 SSOT one-canonical-owner-per-fact).
	deletionDeps := deletion.DeletionServiceDeps{
		Repos: deletion.DeletionRepoDeps{
			ClipsRepo:     repos.ClipsRepo,
			StockRepo:     repos.ClipsRepo,
			VoiceoverRepo: repos.VoiceoverRepo,
			ImagesRepo:    repos.ImageRepo,
		},
		Catalog: sourceCatalog,
		Index: deletion.DeletionIndexDeps{
			AssetTreeSvc:  search.AssetTreeService,
			AssetIndexSvc: search.AssetIndexService,
		},
		Dispatcher: outboxBundle.Dispatcher,
		Log:        log,
	}
	// MEDIA-SSOT read-side (September 2026): when the canonical media writer
	// is the PostgreSQL committer, the media plane is authoritative for reads
	// as well as writes. The deletion entry point must therefore resolve the
	// asset from PostgreSQL (a SQLite lookup cannot see a post-cutover asset)
	// and route the first saga hop — the DELETE_REQUESTED stamp plus the
	// asset.drive.delete_requested emission — into the PostgreSQL media SSOT,
	// where the PG outbox worker consumes it (see
	// registerPostgresMediaOutboxHandlers / registerPostgresDeleteSagaHandlers).
	// Non-PostgreSQL deployments keep the exact legacy SQLite wiring.
	if pgCommitter, ok := outboxBundle.CanonicalWriter.(*pgmedia.PostgresMediaCommitter); ok && pgCommitter != nil {
		deletionDeps.Dispatcher = pgCommitter
		deletionDeps.Lookup = deletion.DeletionLookupDeps{
			ByID:          pgCommitter,
			ByDriveFileID: pgCommitter,
		}
	}
	deletionSvc := deletion.NewDeletionService(deletionDeps)
	maintRepo := imagesregistry.NewMaintenanceRepository(dbs.DualPool.Writer, log)
	maintenanceSvc := maintenance.NewService(cfg, log,
		search.AssetIndexService, search.AssetTreeService, deletionSvc,
		jobs.Service, maintRepo,
	)

	recertificationConfig := entitycatalog.DefaultRecertificationConfig()
	if cfg != nil {
		if parsed, err := time.ParseDuration(cfg.Jobs.EntityImageRecertificationInterval); err == nil && parsed > 0 {
			recertificationConfig.Interval = parsed
		}
		if cfg.Jobs.EntityImageRecertificationBatchSize > 0 {
			recertificationConfig.BatchSize = cfg.Jobs.EntityImageRecertificationBatchSize
		}
	}
	entityImageRecertification := entitycatalog.NewRecertificationService(
		repos.EntityImageCatalog,
		entitycatalog.NewHTTPImageCandidateValidator(&http.Client{Timeout: 30 * time.Second}),
		recertificationConfig,
	)
	// Registries-and-SSOT (June 2026): this is the canonical site for
	// the `system.cleanup` job-type registration. Spec §"Uniqueness"
	// requires composition to fail on duplicate job types; the previous
	// log-Warn-and-continue pattern silently absorbed any second-call
	// attempt (a latent bug that manifested after WireRegistry's
	// duplicate call was removed). Propagate so any future second-call
	// path fails composition rather than masking the underlying
	// Dispatcher error.
	if err := maintenanceSvc.RegisterHandler(); err != nil {
		return nil, fmt.Errorf("compose: register maintenance job handler (BuildMaintBundle): %w", err)
	}

	return &MaintBundle{
		MaintenanceSvc:             maintenanceSvc,
		DeletionSvc:                deletionSvc,
		EntityImageRecertification: entityImageRecertification,
	}, nil
}

// BuildArtifactFinalizeBundle constructs the canonical FASE 3
// Spina Dorsale finalizer bundle: the application-layer
// finalizerService (the typed port that closes the FASE 3 saga's
// Finalize step by scanning ListByJob + flipping PUBLISHED →
// SUCCEEDED via the fenced MarkSucceeded primitive, gated on
// "all REQUIRED are PUBLISHED"). Returns the FinalizerBundle
// for assignment to ComposeRoot.Finalizer.
//
// Dependency surface (intentionally minimal — 2 deps):
//   - staging.Repository: the canonical artifact.Repository port
//     exposed on StagingBundle. The Finalizer's read path
//     (ListByJob) + write path (MarkSucceeded, fenced-CAS) both
//     consume this port, so sharing the same instance is a
//     godlike/06 SSOT requirement (the artifact_stages table has
//     exactly one canonical single-writer). BuildArtifactFinalizeBundle
//     MUST run AFTER BuildStagingBundle in NewComposition — the
//     constructor validates staging.Repository != nil at the
//     boundary and aborts otherwise.
//   - log: *zap.Logger for the boot-time wiring log line + the
//     per-Finalize info log line + the
//     `finalize: no stages found for job_id` debug log line
//     (the audit-trail contract).
//
// Returns error on (a) nil staging.Repository (composition-time
// misconfiguration) or (b) artifactfinalize.NewFinalizerService
// rejection (nil repo or nil log underneath — the boundary check
// re-runs as a defense-in-depth). No log is emitted on error
// path — NewComposition wraps the error and the orchestrator
// aborts startup.
func BuildArtifactFinalizeBundle(staging *StagingBundle, log *zap.Logger) (*FinalizerBundle, error) {
	// Step 1 (godlike/07 fail-fast at construction):
	// pre-validate the staging bundle dependency. A nil
	// Repository signals that BuildStagingBundle did NOT run
	// (or returned nil) — composition-order misconfiguration.
	// The error message names the missing dependency so a
	// future debug session can pinpoint the call-order issue
	// without inspecting the bundle contract.
	if staging == nil || staging.Repository == nil {
		return nil, fmt.Errorf("build artifact_finalize: staging.Repository is nil (BuildStagingBundle must run first in NewComposition)")
	}

	// Step 2: construct the application-layer finalizerService.
	// artifactfinalize.NewFinalizerService fail-closes on nil
	// repo (impossible — staging.Repository is non-nil above)
	// or nil log (impossible — log is non-nil at the
	// composition root). The boundary check is a defense-in-
	// depth double-check.
	finalizer, err := artifactfinalize.NewFinalizerService(staging.Repository, log)
	if err != nil {
		return nil, fmt.Errorf("build artifact_finalize: NewFinalizerService: %w", err)
	}

	log.Info("artifact_finalize bundle wired (FASE 3 Spina Dorsale Push 3.1d)",
		zap.String("repository_table", "artifact_stages"),
	)

	return &FinalizerBundle{
		Finalizer: finalizer,
	}, nil
}

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
			ContentSHA256: r.ContentSHA256,
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

func (a *imageListRepoAdapter) ListImagesByAssetIDs(ctx context.Context, assetIDs []string) ([]images.ImageSearchResult, error) {
	if a == nil || a.repo == nil {
		return nil, nil
	}
	dbRows, err := a.repo.ListImagesByAssetIDs(ctx, assetIDs)
	if err != nil {
		return nil, err
	}
	out := make([]images.ImageSearchResult, 0, len(dbRows))
	for _, row := range dbRows {
		out = append(out, images.ImageSearchResult{
			AssetID: row.AssetID, Origin: string(row.Origin), Provider: row.Provider,
			Name: row.Name, PreviewURL: row.PreviewURL, DriveLink: row.DriveLink,
			ContentSHA256: row.ContentSHA256, SourcePageURL: row.SourcePageURL,
			Author: row.Author, License: row.License, Width: row.Width, Height: row.Height,
			Score: 1, StyleID: row.StyleID, StyleVersion: row.StyleVersion,
		})
	}
	return out, nil
}

var _ images.ImageListRepository = (*imageListRepoAdapter)(nil)
var _ images.LocalImageAssetHydrator = (*imageListRepoAdapter)(nil)

func buildImageSearchResolver(imageSvc *images.Service, imageRepo *imagesrepo.ImagesRepository, log *zap.Logger) (images.ImageSearchResolver, error) {
	if imageSvc == nil || imageSvc.RetrievalRegistry() == nil {
		return nil, fmt.Errorf("images.NewImageSearchResolver: retrieval backend is nil — image service must be constructed first")
	}
	if imageRepo == nil {
		return nil, fmt.Errorf("images.NewImageSearchResolver: image list repository is nil — repos.ImageRepo required")
	}
	resolver, err := images.NewImageSearchResolver(
		images.WithRetrievalBackend(imageSvc.RetrievalRegistry()),
		images.WithImageListRepository(&imageListRepoAdapter{repo: imageRepo}),
		images.WithLocalResolvedImageIndex(imageSvc.LocalResolvedImageIndex()),
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
	canonicalCommitter persistence.AssetCommitter,
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
	// Image media records use the same canonical PostgreSQL reader, writer and
	// registry-backed AssetRecordStore as clips. The old image-specific wiring
	// committed to PostgreSQL, then read it back from the empty SQLite mirror;
	// it also wrote a second, divergent image row through ImagesRepository.
	mediaDetails := mediaDetailsReaderFromCommitter(canonicalCommitter)
	mediaProcessing := persistence.CanonicalAssetProcessingWriter(canonicalCommitter)
	imagesRegistry := artifacts.NewClipsRegistryWithLogger(mediaDetails, mediaProcessing, canonicalCommitter, log)
	imagesLifecycle := NewLifecycleFromDeps(&AssetLifecycleDeps{Registry: imagesRegistry, Publisher: publisher, DriveReader: driveUploader, AssetIndex: search.AssetIndexService}, log)
	voiceoverRegistry := voservice.NewVoiceoverRegistryAdapter(repos.VoiceoverRepo)
	voiceoverLifecycle := NewLifecycleFromDeps(&AssetLifecycleDeps{Registry: voiceoverRegistry, Publisher: publisher, DriveReader: driveUploader, AssetIndex: search.AssetIndexService, Store: ingest.NewVoiceoverStoreAdapter(repos.VoiceoverRepo)}, log)
	// MEDIA-SSOT P2-9 step 2: the media-hydration read for both clip registries
	// and both clip stores comes from the canonical committer's engine, so the
	// ingest path reads the same media SSOT the committer writes. Passing
	// repos.Assets here made ingest hydrate from the operational SQLite mirror,
	// where a PostgreSQL-committed clip looked absent.
	// MEDIA-SSOT write-bridge: retirement resolves from the canonical
	// committer's own engine. Neither the registry nor the store receives the
	// generic detail.Repository seam, so neither can retire an asset in a
	// database the canonical writer does not own.
	mediaRetirer := persistence.CanonicalAssetSoftDeleter(canonicalCommitter)
	// MEDIA-SSOT write-bridge: asset_locations + asset_processing are
	// media-authoritative, so both write ports resolve from the canonical
	// committer's engine rather than the operational SQLite store. A nil port
	// means the media plane is closed and the ingest write fails closed
	// instead of landing on an unnamed database.
	mediaLocations := persistence.CanonicalAssetLocationWriter(canonicalCommitter)
	// MEDIA-SSOT P2-9 Phase 2: the drive-file-id listing is a MEDIA read, so it
	// resolves from the canonical committer's engine too. It used to run inside
	// the clip store as raw SQL against dbs.Main (operational SQLite), a
	// database that holds no committed media rows, so the listing was blind to
	// the SSOT. nil means the media plane is closed and the listing fails
	// closed — never dbs.Main.
	mediaDriveFileIDs := mediaDriveFileListerFromCommitter(canonicalCommitter)
	clipRegistry := artifacts.NewClipsRegistryWithLogger(mediaDetails, mediaProcessing, canonicalCommitter, log)
	clipLifecycle := NewLifecycleFromDeps(&AssetLifecycleDeps{Registry: clipRegistry, Publisher: publisher, DriveReader: driveUploader, AssetIndex: search.AssetIndexService, Store: ingest.NewClipStoreAdapter(dbs.Main.DB, mediaRetirer, mediaDetails, mediaLocations, mediaProcessing, mutationsDisp, mediaDriveFileIDs)}, log)
	stockRegistry := artifacts.NewClipsRegistryWithLogger(mediaDetails, mediaProcessing, canonicalCommitter, log)
	stockLifecycle := NewLifecycleFromDeps(&AssetLifecycleDeps{Registry: stockRegistry, Publisher: publisher, DriveReader: driveUploader, AssetIndex: search.AssetIndexService, Store: ingest.NewClipStoreAdapter(dbs.Main.DB, mediaRetirer, mediaDetails, mediaLocations, mediaProcessing, mutationsDisp, mediaDriveFileIDs)}, log)
	return ingest.NewService(cfg, log, downloader.NewMediaDownloader(10*time.Minute), map[ingest.Kind]*ingest.Pipeline{
		ingest.KindImage:     {Kind: ingest.KindImage, DefaultSource: "image", RootFolderID: cfg.Drive.ImagesFolder(), Lifecycle: imagesLifecycle},
		ingest.KindVoiceover: {Kind: ingest.KindVoiceover, DefaultSource: "voiceover", RootFolderID: cfg.Drive.VoiceoverFolder(), Lifecycle: voiceoverLifecycle},
		ingest.KindClip:      {Kind: ingest.KindClip, DefaultSource: "youtube", RootFolderID: cfg.Drive.ClipsFolder(), Lifecycle: clipLifecycle},
		ingest.KindStock:     {Kind: ingest.KindStock, DefaultSource: "stock", RootFolderID: cfg.Drive.StockFolder(), Lifecycle: stockLifecycle},
	}, nil)
}
