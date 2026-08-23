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
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/application/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/media/processor"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"

	"go.uber.org/zap"
)

// ── Drive destinations ──────────────────────────────────────────────────────

// DriveDestinations groups the canonical Drive folder IDs for media assets.
type DriveDestinations struct {
	MediaRoot, SoundEffectsRoot, ImagesFolderID string
}

func (d *DriveDestinations) RootFolder() string   { return d.MediaRoot }
func (d *DriveDestinations) ImagesFolder() string { return d.ImagesFolderID }

// ── Media processor initialisation ──────────────────────────────────────────

// InitMediaProcessor wires the media processor. PG-011: db is now
// *storage.SQLiteDB (the typed canonical handle) instead of raw *sql.DB;
// the artifacts.NewClipsRegistry constructor still takes *sql.DB so we
// deref via db.DB at the call site — this keeps the upstream contract
// unchanged while letting the composition layer stop holding a raw
// sqlite handle in signatures.
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
func InitMediaProcessor(cfg *config.Config, db *storage.SQLiteDB, assetsRepo asset.Repository, querySvc *asset.Service, locations asset.LocationRepository, processing asset.ProcessingRepository, committer assetspersistence.AssetCommitter, log *zap.Logger, publisher delivery.Publisher, mediaConfig mediaexec.ExecutionConfig) asset.Processor {
	ytDLPDownloader := downloader.NewYTDLP(cfg)
	httpDL := downloader.NewHTTPDownloader(5 * time.Minute)
	ffmpegProc := rustexec.NewConfiguredVideoProcessor(cfg.External.RustMusclesPath, cfg.External.FfmpegPath, mediaConfig.Policy, mediaConfig.Profile, log)
	clipsRegistry := artifacts.NewClipsRegistry(db.DB, assetsRepo, querySvc, locations, processing, committer)
	profile := mediaConfig.Profile
	policy := mediaConfig.Policy
	videoCfg := mediaexec.NormalizeOptions{Profile: profile, Policy: policy, Duration: cfg.Video.CanonicalClip().Duration,
		Width: profile.Width, Height: profile.Height, FPS: profile.FPS, Codec: policy.Codec, Preset: policy.Preset,
		CRF: policy.CRF, KeyframeInterval: profile.KeyframeInterval}
	proc := processor.NewProcessor(ytDLPDownloader, httpDL, ffmpegProc, log, processor.ProcessorConfig{DataDir: cfg.Storage.DataDir, TempDir: cfg.Storage.TempDir, VideoCfg: videoCfg, EmbeddingServerURL: cfg.ClipIndexer.ServerURL}, clipsRegistry, publisher)
	// Derived-artifact cache: source bytes + normalize operation + encoder
	// config + processor version identify the artifact. Without it every
	// reprocess re-executes Rust/FFmpeg (warm runs would be cold).
	// Fail-soft: a cache wiring failure degrades to uncached processing.
	if cache, cacheErr := NewArtifactCache(cfg, db.DB, log); cacheErr == nil {
		proc.SetArtifactCache(cache)
		log.Info("media processor artifact cache wired")
	} else {
		log.Warn("media processor artifact cache unavailable; media runs uncached", zap.Error(cacheErr))
	}
	return proc
}

// ── Sync target building ────────────────────────────────────────────────────

func BuildSyncTargets(cfg *config.Config, clipsOnlyRepo *assets.ClipsRepository, clipsRepo *assets.ClipsRepository, artlistRepo *assets.ClipsRepository) []catalogsync.Target {
	return []catalogsync.Target{
		{Name: "stock", RootFolderID: cfg.Drive.StockFolder(), Source: "stock", MediaType: "video", Repo: clipsRepo, Indexer: clipsRepo},
		{Name: "youtube", RootFolderID: cfg.Drive.ClipsFolder(), Source: "youtube", MediaType: "video", Repo: clipsOnlyRepo, Indexer: clipsOnlyRepo},
		{Name: "artlist", RootFolderID: cfg.Drive.ArtlistFolder(), Source: "artlist", MediaType: "video", Repo: artlistRepo, Indexer: artlistRepo},
		// Sound effects use the canonical media_assets + transactional
		// outbox path. Audio embeddings are generated by the indexer once
		// a local copy is available.
		{Name: "sound_effects", RootFolderID: cfg.Drive.SoundEffectsFolder(), Source: "sound_effect", MediaType: "audio", Repo: clipsRepo, Indexer: clipsRepo},
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
