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

	assetswiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	assetspersistence "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/processor"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/media/rustexec"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"

	"go.uber.org/zap"
)

import (
	registrywiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/registry"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	module "github.com/Marcuss-ops/PipelineGen/internal/platform/httpserver"
)

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
func InitMediaProcessor(cfg *config.Config, db *storage.SQLiteDB, cacheDB *storage.SQLiteDB, processing assetspersistence.AssetProcessingWriter, committer assetspersistence.AssetCommitter, log *zap.Logger, publisher delivery.Publisher, mediaConfig mediaexec.ExecutionConfig) detail.MediaProcessor {
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
		if cache, cacheErr := assetswiring.NewArtifactCache(cfg, cacheDB.DB, db.DB, log); cacheErr == nil {
			proc.SetArtifactCache(cache)
			log.Info("media processor artifact cache wired")
		} else {
			log.Warn("media processor artifact cache unavailable; media runs uncached", zap.Error(cacheErr))
		}
	}
	return proc
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

type PreparedCapabilities = registrywiring.PreparedCapabilities
type CapabilityDeps = registrywiring.CapabilityDeps
type TrackedHTTPModule = registrywiring.TrackedHTTPModule
type TrackedProviderEntry = registrywiring.TrackedProviderEntry
type ProviderKind = registrywiring.ProviderKind

const (
	ProviderKindSearch = registrywiring.ProviderKindSearch
	ProviderKindFetch  = registrywiring.ProviderKindFetch
)

type strictOption = registrywiring.StrictOption

func bootstrapProviderRegistry(provReg *providers.Registry, entries []TrackedProviderEntry, descriptors []module.DescriptorProviders) error {
	return registrywiring.BootstrapProviderRegistry(provReg, entries, descriptors)
}

func registerCapabilities(reg *module.Registry, provReg *providers.Registry, deps CapabilityDeps) error {
	return registrywiring.RegisterCapabilities(reg, provReg, deps, registrywiring.ValidateRuntimeGraph)
}

func WithRegistrationPoint(point string) strictOption {
	return registrywiring.WithRegistrationPoint(point)
}

func tryRegisterModuleStrict(reg *module.Registry, log *zap.Logger, mod module.Module, opts ...strictOption) error {
	return registrywiring.TryRegisterModuleStrict(reg, log, mod, opts...)
}

func tryRegisterModule(reg *module.Registry, log *zap.Logger, mod module.Module) error {
	return registrywiring.TryRegisterModule(reg, log, mod)
}
