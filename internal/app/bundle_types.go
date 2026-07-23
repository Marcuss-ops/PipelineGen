// Package app — bundle_types.go: typed bundle aggregator definitions.
//
// PR-MOD-SOURCES-SPLIT (June 2026, follow-up to the Step 9 Channel Monitor
// consolidation): this file is the canonical home for the 6 typed bundle
// aggregators + their matching wiring result structs. They were carved out of
// `module_sources.go` (now 642-line Wire*-only file) so a reader looking for
// "what shape does an *ArtlistBundle have?" or "what does *ArtlistWiring look
// like?" can land on the dedicated single-purpose file without sorting
// ComposeRoot + bundle-builders + Wire* all under one roof.
//
// Per AGENTS.md / godlike/06 §"Database and config ownership": bundles are
// typed cross-capability dependency holders. They MUST stay aggregated (no
// individual per-dependency ctor args) so the Wave-1 wiring jitter that
// produced the 12-file constructor cap (architecture/policy.yaml Check ~28)
// cannot regress.
//
// Each type below carries its PR-history doc comment verbatim from
// module_sources.go so the audit trail is unbroken:
//
//   - ArtlistBundle        (PR4d-chunk2, June 2026) — 10 typed fields
//   - ArtlistWiring        (PR4d-chunk2, June 2026; Blocco C1-Step 3 removed
//     the Resolver + Handler fields)
//   - StockBundle          (PR4d-chunk2, June 2026) — 9 typed fields
//   - StockPipelineWiring  (Blocco C1-Step 6, June 2026 — Handler removed)
//   - YouTubeClipWiring    (Blocco C1-Step 4, June 2026 — Handler removed)
//   - FullImagesWiring     (PR3 Wave 14, June 2026)
//
// The Wire* functions that CONSUME these bundles live in module_sources.go.
// Same-package consumption means the types remain referenceable across both
// files without any re-export dance.
//
// Note: the `Jobs *JobsBundle` field on ArtlistBundle references the
// JobsBundle type declared in composition.go (same `app` package). Same-
// package access — no import cycle concern.
package app

import (
	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	module "github.com/Marcuss-ops/PipelineGen/internal/api"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/catalogsync"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providerassets"
	artlistPkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	ytService "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/usecase"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	storage "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/assetindex"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	driveup "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	jobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	gdrive "google.golang.org/api/drive/v3"
)

// ArtlistBundle is the capability bundle for the Artlist module.
// Moved from artlist_bundle.go (Phase 5 consolidation, June 2026).
//
// PR4d-chunk2 (June 2026): wraps the 25 cross-bundle reads of WireArtlist
// into 10 typed fields.
type ArtlistBundle struct {
	DB            *storage.SQLiteDB
	Assets        *asset.Service
	ClipsRepo     *assets.ClipsRepository
	DriveClient   *gdrive.Service
	DriveUploader *driveup.Uploader
	Publisher     delivery.Publisher
	// ClipResolver is the Recommend-shaped adapter (PR-ARTLIST-RECOMMEND-ADAPTER,
	// closed 2026-07-04) that bridges the handler-side artlist.ClipResolverPort
	// (Recommend method) to the canonical *scripts.ClipResolver (Resolve method)
	// + a real field-weighted Jaccard scoring layer. Constructed in WireArtlist
	// via NewClipResolverRecommendAdapter(scripts_usecase.NewClipResolver(ClipsRepo, log), log);
	// nil when the canonical resolver is unavailable (godlike/07 fail-closed
	// fast path — the handler returns 503 on /recommend in that case).
	ClipResolver       *clipResolverRecommendAdapter
	AssetIndexService  *assetindex.Service
	ClipIndexerService *clipindexer.Service
	MediaProcessor     asset.Processor
	Jobs               *JobsBundle
	CatalogSyncService *catalogsync.Service
	// TextTrackRepo persists audio transcripts for downloaded clips.
	// Wired into Artlist so every clip can be transcribed and the
	// transcript stored in asset_text_tracks (PR-ARTLIST-MANDATORY-
	// TRANSCRIPTION, July 2026).
	TextTrackRepo asset.TextTrackRepository
}

// ArtlistWiring holds the Artlist module wiring.
//
// PR4d-chunk2 (June 2026): Resolver field removed (historical: at the
// time, the canonical clip-resolver lived in
// `internal/application/assets/clipresolver/`, but that package was
// subsequently completely removed in a downstream refactor — see
// architecture/deprecations.yaml#PR-ARTLIST-SYNCSERVICE, closed 2026-07-04).
// The harvest service was constructed locally in WireRegistry from
// root.Jobs.Facade (the same path used pre-PR4d); the carve-out was
// deliberate because the then-existing clipresolver.Service did not
// implement script.AutoHarvestService (no EnqueueHarvest method), not
// a side-effect.
//
// Blocco C1-Step 3 (June 2026): Handler field removed. The HTTP Handler
// is constructed inside `artsources.Build(deps)` and captured by the
// returned ArtlistDescriptor's Module closure. No caller (composition
// root, tests, internal services) needs to read the raw `*ArtlistHandler`
// outside the package — matches the channels precedent of dropping the
// explicit Handler field in favor of descriptor-only wiring.
type ArtlistWiring struct {
	Module  api.Module
	Service *artlistPkg.Service
	// ProviderAssets is the unified registry for external catalog adapters
	// (Artlist, Pexels, Pixabay). It is wired in WireArtlist and frozen
	// before the module is returned.
	ProviderAssets *providerassets.Registry
	// LicenseRepo and ReleaseRepo expose the compliance repositories for
	// license/release tracking. They are wired in WireArtlist.
	LicenseRepo asset.LicenseRepository
	ReleaseRepo asset.ReleaseRepository
	// RenditionRepo exposes the asset rendition repository. Wired in WireArtlist.
	RenditionRepo asset.RenditionRepository
}

// StockBundle is the capability bundle for the stock-pipeline module.
//
// PR4d-chunk2 (June 2026): wraps the 7 cross-bundle reads of WireStockPipeline.
type StockBundle struct {
	DriveUploader      *driveup.Uploader
	Jobs               *appjobs.Service
	JobFacade          jobs.Service
	AssetIndexService  *assetindex.Service
	ClipsRepo          *assets.ClipsRepository
	YoutubeClipService *ytService.Service
	ClipIndexerService *clipindexer.Service
	Dispatcher         *outbox.Dispatcher
	Publisher          delivery.Publisher
}

// StockPipelineWiring holds the StockPipeline module wiring.
//
// Blocco C1-Step 6 (June 2026): Handler field removed. The HTTP Handler
// is constructed inside `stock.Build(deps)` and captured by the
// returned StockDescriptor's Module closure. No caller (composition
// root, tests, internal services) needs to read the raw `*stock.Handler`
// outside the package — matches the artlist / youtube / clips precedent
// of dropping the explicit Handler field in favor of descriptor-only
// wiring. The pre-Step-6 `Handler` field has no non-HTTP consumer in
// the codebase (/run + /search-and-run are the entire public surface,
// both reachable via HTTP).
//
// Fase 3 (July 2026): BatchModule exposes the /api/stock-batches
// capability as a second, sibling route module.
type StockPipelineWiring struct {
	Module      api.Module
	BatchModule api.Module
	Service     *stockpipeline.Service
}

// YouTubeClipWiring holds the YouTube Clip module wiring.
//
// Blocco C1-Step 4 (June 2026): Handler field removed. The HTTP Handler
// is constructed inside `ytsources.Build(deps)` and captured by the
// returned YouTubeDescriptor's Module closure. No caller (composition
// root, tests, internal services) needs to read the raw
// `*YouTubeClipHandler` outside the package — matches the artlist /
// channels precedent of dropping the explicit Handler field in favor of
// descriptor-only wiring.
type YouTubeClipWiring struct {
	Module  api.Module
	Service *ytService.Service
}

// FullImagesWiring holds the FullImages module wiring.
//
// PR3 (June 2026): Wave 14 close. The handler was moved from
// `internal/api/fullimages/` to `internal/api/images/` as a sibling
// of ImagesHandler. The route prefix stays `/fullimages` (NOT
// `/images`) so the public REST URL stays unchanged — zero-change-
// contract per PR3 spec. The sub-path `/video/generate` is unchanged
// (no collision with `ImagesHandler.Generate` which mounts at
// `/generate` under the `/images` prefix).
// FullImagesWiring holds the FullImages module wiring.
// Handler field removed (June 2026): FullImagesHandler was retired during the
// LONG-FILES-SPLIT-2026-07-06; zero callers accessed the field.
type FullImagesWiring struct {
	Module module.Module
}
