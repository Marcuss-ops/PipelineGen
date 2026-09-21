// Package app — helper functions extracted from registry.go.
//
// Per AGENTS.md Pattern 5 (June 2026): one concept per file. This file holds
// standalone helpers used by WireRegistry: media processor construction,
// sync target building, and Drive folder definition.
//
// Wave A Item 15 (June 2026): ensureStyleDriveFolders + style-folder
// pre-creation REMOVED — the canonical StyleRegistry already serves
// style metadata at the composition boundary, and the legacy
// drive-side pre-creation step was a hard-coded single point of
// failure that masked style discovery drift. The driveup + generation
// imports are no longer needed here (this file's remaining helpers
// never reach the Drive SDK directly or the StyleRegistry).
package wiring

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/catalogsync"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/processor"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"

	"go.uber.org/zap"
)

// ── Drive destinations ──────────────────────────────────────────────────────

// DriveDestinations groups the canonical Drive folder IDs for media assets.
type DriveDestinations struct {
	MediaRoot, SoundEffectsRoot, ImagesFolderID string
}

// ── Media processor initialisation ──────────────────────────────────────────

// InitMediaProcessor wires the media processor. PG-011: db is
// *storage.SQLiteDB (the typed canonical handle) instead of raw *sql.DB.
// MEDIA-SSOT (Sept 2026): the ClipsRegistry no longer takes a database handle
// at all — its media reads resolve from the canonical committer's engine — so
// this function no longer dereferences db.DB for the registry.
//
// PR 8 (June 2026, codex/qdrant-app-writers-fail-closed): mutationsDisp
// is the 8th positional arg so the embedded artifacts.NewClipsRegistry
// routes its media_assets UPSERT through the canonical outbox+tx
// writer. The PR-7 deferred-hydration strategy (hydrateMediaProcessor +
// MediaProcessor=nil) is gone; BuildProcessBundle now consumes
// outbox.OutboxBundle inline and constructs MediaProcessor directly
// (see composition.go::buildQdrantDeps + BuildProcessBundle for the
// strict-DAG shape: qdrantDeps -> outbox -> process).
//
// Fail-closed at the composition root: BuildProcessBundle returns
// MediaProcessor=nil if outbox.Dispatcher is nil so worker / reprocess
// / ingest paths surface the missing dep rather than silently defaulting
// to the legacy path.
// F2.8 (June 2026): the trailing arg swaps from `*driveup.Uploader`
// to `delivery.Publisher`. processor.NewProcessor now panics on nil
// publisher (composition-time fail-fast), so a wiring gap becomes
// loud at boot rather than silent on first upload. The Publisher
// canonically routes every Drive write through the DestinationRegistry
// + RequireSubpath + ConflictPolicy belt; the legacy direct-uploader
// bypass is closed. The driveup import is no longer needed in this
// file (Wave A Item 15, June 2026) — the ensureStyleDriveFolders
// helper that used it has been removed.
// The legacy `querySvc *detail.Service` parameter was REMOVED here on
// 2026-09-13 (MEDIA-SSOT P2-9 step 2): the only MediaProcessor media read is
// the ClipsRegistry's MediaRecord hydration, which now derives its reader from
// the canonical committer's engine. Keeping the parameter would have let a
// caller pass the SQLite service again, and an unused parameter is how that
// drift starts.
// MEDIA-SSOT write-bridge (September 2026): the legacy
// `locations detail.LocationRepository` parameter was DELETED here. It was a
// dead seam — no code path in this function ever read it — and an unused
// generic repository parameter is how a media-plane split-brain starts (a
// later edit reaches for the convenient unnamed engine). The processor's only
// asset_processing write now resolves from the canonical committer, so the
// location seam had no honest caller left.
func InitMediaProcessor(cfg *config.Config, db *storage.SQLiteDB, cacheDB *storage.SQLiteDB, processing assetspersistence.AssetProcessingWriter, committer assetspersistence.AssetCommitter, log *zap.Logger, publisher delivery.Publisher, mediaConfig mediaexec.ExecutionConfig) detail.Processor {
	ytDLPDownloader := downloader.NewYTDLP(cfg)
	httpDL := downloader.NewHTTPDownloader(5 * time.Minute)
	ffmpegProc := rustexec.NewConfiguredVideoProcessor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, mediaConfig.Policy, mediaConfig.Profile, log)
	// MEDIA-SSOT P2-9 step 2: MediaProcessor hydrates clip records from the
	// canonical committer's engine, so it cannot read a divergent media mirror.
	clipsRegistry := artifacts.NewClipsRegistryWithLogger(mediaDetailsReaderFromCommitter(committer), processing, committer, log)
	profile := mediaConfig.Profile
	policy := mediaConfig.Policy
	videoCfg := mediaexec.NormalizeOptions{Profile: profile, Policy: policy, Duration: cfg.Video.CanonicalClip().Duration,
		Width: profile.Width, Height: profile.Height, FPSNum: profile.FPSNum, FPSDen: profile.FPSDen, Codec: policy.Codec, Preset: policy.Preset,
		CRF: policy.CRF, KeyframeInterval: profile.KeyframeInterval}
	proc := processor.NewProcessor(ytDLPDownloader, httpDL, ffmpegProc, log, processor.ProcessorConfig{DataDir: cfg.Storage.DataDir, TempDir: cfg.Storage.TempDir, VideoCfg: videoCfg, EmbeddingServerURL: cfg.ClipIndexer.ServerURL}, clipsRegistry, publisher)
	// Derived-artifact cache: source bytes + normalize operation + encoder
	// config + processor version identify the artifact. Without it every
	// reprocess re-executes Rust/FFmpeg (warm runs would be cold).
	// Fail-soft: a cache wiring failure degrades to uncached processing.
	if cacheDB != nil && cacheDB.DB != nil {
		if cache, cacheErr := NewArtifactCache(cfg, cacheDB.DB, db.DB, log); cacheErr == nil {
			proc.SetArtifactCache(cache)
			log.Info("media processor artifact cache wired")
		} else {
			log.Warn("media processor artifact cache unavailable; media runs uncached", zap.Error(cacheErr))
		}
	}
	return proc
}

// ── Sync target building ────────────────────────────────────────────────────

// BuildSyncTargets wires the catalog-sync targets with a shared repository +
// indexer. The caller selects the PostgreSQL media repository
// (newCatalogSyncRepository) whenever the media SSOT is available.
func BuildSyncTargets(cfg *config.Config, repo catalogsync.CatalogRepository, indexer catalogsync.AssetIndexer) []catalogsync.Target {
	return []catalogsync.Target{
		{Name: "stock", RootFolderID: cfg.Drive.StockFolder(), Source: "stock", MediaType: "video", Repo: repo, Indexer: indexer},
		{Name: "youtube", RootFolderID: cfg.Drive.ClipsFolder(), Source: "youtube", MediaType: "video", Repo: repo, Indexer: indexer},
		{Name: "artlist", RootFolderID: cfg.Drive.ArtlistFolder(), Source: "artlist", MediaType: "video", Repo: repo, Indexer: indexer},
		// Sound effects use the canonical media_assets + transactional
		// outbox path. Audio embeddings are generated by the indexer once
		// a local copy is available.
		{Name: "sound_effects", RootFolderID: cfg.Drive.SoundEffectsFolder(), Source: "sound_effect", MediaType: "audio", Repo: repo, Indexer: indexer},
	}
}

// newCatalogSyncRepository returns the catalog-sync media repository +
// indexer. PostgreSQL is the media SSOT, so the GetClip existence check and
// GetIndexState read it whenever the handle is wired (MEDIA-SSOT read
// split-brain fix); folder operations stay on the operational SQLite
// repository until the clip_folders writers migrate.
//
// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-21, sub-wave B): the degrade
// branch keeps serving the folder/GetClip half from `legacy`, but it no longer
// serves the INDEX-STATE half from the mirror. An index state is a media fact,
// the engine decision point (mediasub.RequireMediaPostgres) declares that there
// is no SQLite fallback and that the operational mirror holds no committed
// media rows, so the slot is answered by noMediaPlaneIndexState, which fails
// closed. The former *assets.ClipsRepository.GetIndexState implementation is
// deleted with it.
func newCatalogSyncRepository(mediaDB *sql.DB, legacy *assets.ClipsRepository) (catalogsync.CatalogRepository, catalogsync.AssetIndexer) {
	if mediaDB != nil && legacy != nil {
		router := &postgresCatalogRepository{media: pgmedia.NewMediaSearcher(mediaDB), legacy: legacy}
		// The folder list is read from PostgreSQL only once the projection is
		// actually wired (dual-write + boot backfill). An unpopulated
		// projection would under-report folders, so pruneMissingFolders would
		// silently skip pruning (stale rows survive) instead of reconciling;
		// the read therefore stays on SQLite until the projection owns the data.
		if legacy.FolderProjectionWired() {
			router.folders = pgmedia.NewFolderRepository(mediaDB)
		}
		return router, router
	}
	// No media plane: the folder/GetClip degrade stays on the operational store,
	// the index-state slot fails closed (never the mirror — see
	// noMediaPlaneIndexState). A nil legacy is returned as a true nil interface
	// so the catalogsync target validation sees the missing port instead of a
	// typed-nil it cannot detect.
	if legacy == nil {
		return nil, nil
	}
	return legacy, noMediaPlaneIndexState{}
}

// postgresCatalogRepository routes media reads (GetClip/GetIndexState) to the
// PostgreSQL media SSOT while folder reconciliation remains on the SQLite
// clip_folders repository.
//
// clip_folders is deliberately NOT read from PostgreSQL yet: every writer still
// persists it in SQLite, so a PG-only folder read would observe an empty
// projection. Migrating the folder write path is a prerequisite for moving the
// folder reads.
type postgresCatalogRepository struct {
	media   catalogMediaReader
	folders catalogFolderReader
	legacy  *assets.ClipsRepository
}

// catalogMediaReader is the narrow PostgreSQL read surface the catalog router
// consumes. *pgmedia.MediaSearcher implements it.
type catalogMediaReader interface {
	GetAsset(ctx context.Context, assetID string) (*pgmedia.MediaAssetRecord, error)
}

// catalogFolderReader is the narrow PostgreSQL folder projection the catalog
// router consumes. *pgmedia.FolderRepository implements it.
type catalogFolderReader interface {
	ListFolders(ctx context.Context, source string) ([]*detail.ClipFolder, error)
}

var (
	_ catalogsync.CatalogRepository = (*postgresCatalogRepository)(nil)
	_ catalogsync.AssetIndexer      = (*postgresCatalogRepository)(nil)
)

func (r *postgresCatalogRepository) GetClip(ctx context.Context, id string) (*asset.Asset, error) {
	rec, err := r.media.GetAsset(ctx, id)
	if err != nil {
		if errors.Is(err, pgmedia.ErrMediaAssetNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return catalogAssetFromRecord(rec), nil
}

func (r *postgresCatalogRepository) GetIndexState(ctx context.Context, id string) (asset.IndexState, error) {
	rec, err := r.media.GetAsset(ctx, id)
	if err != nil {
		if errors.Is(err, pgmedia.ErrMediaAssetNotFound) {
			// Preserve the legacy SQLite contract: a missing row is sql.ErrNoRows.
			return "", sql.ErrNoRows
		}
		return "", err
	}
	return asset.IndexState(rec.IndexState), nil
}

func (r *postgresCatalogRepository) ListFolders(ctx context.Context, source string) ([]*detail.ClipFolder, error) {
	if r.folders != nil {
		return r.folders.ListFolders(ctx, source)
	}
	return r.legacy.ListFolders(ctx, source)
}

// DeleteFolder writes through the operational repository, whose attached
// PostgreSQL projection mirrors the delete. Routing the delete here (rather
// than straight at the projection) keeps the two stores converged: a
// projection-only delete would leave a stale SQLite row that the next sync
// would resurrect via the folder list.
func (r *postgresCatalogRepository) DeleteFolder(ctx context.Context, id string) error {
	return r.legacy.DeleteFolder(ctx, id)
}

// catalogAssetFromRecord reconstructs the kernel asset the catalog-sync
// preservation logic consumes. Typed columns are merged over the metadata_json
// object so the legacy accessors (LocalPath/LegacyFileMD5/FolderID/…) resolve.
func catalogAssetFromRecord(rec *pgmedia.MediaAssetRecord) *asset.Asset {
	if rec == nil {
		return nil
	}
	meta := rec.MetadataMap()
	meta["local_path"] = rec.LocalPath
	meta["legacy_file_md5"] = rec.SHA256
	meta["drive_file_id"] = rec.DriveFileID
	meta["folder_id"] = rec.FolderID
	meta["parent_folder_id"] = rec.ParentFolderID
	meta["folder_path"] = rec.FolderPath
	return &asset.Asset{
		ID:             rec.ID,
		Source:         asset.Source(rec.Source),
		Name:           rec.Name,
		Filename:       rec.Filename,
		MediaType:      asset.MediaType(rec.MediaType),
		Category:       rec.Category,
		SourceURL:      rec.SourceURL,
		ThumbnailURL:   rec.ThumbnailURL,
		Duration:       time.Duration(rec.DurationMS) * time.Millisecond,
		Tags:           append([]string(nil), rec.Tags...),
		LifecycleState: asset.LifecycleState(rec.LifecycleState),
		Metadata:       meta,
		CreatedAt:      rec.CreatedAtTime(),
	}
}

// ── Style Drive folder pre-creation ─────────────────────────────────────────
//
// Wave A Item 15 (June 2026): REMOVED. The legacy ensureStyleDriveFolders
// helper (which called uploader.GetOrCreateFolder in a loop for every
// registered style) has been deleted from this file. The corresponding
// concurrent.SafeGo("drive-style-folders", ...) call site in
// build_bundles_drive.go::startDriveBackgroundFolders is also removed.
// Composition's role is to wire deps; per-style Drive folder creation
// is the operator's responsibility post-deploy via the canonical
// `reset-video-ai` admin command (which uses the drive.Admin port).
