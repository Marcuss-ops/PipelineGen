// Package app — build_bundles_artlist_types.go
//
// Compile-time interface assertions + private dependency structs for the
// Artlist composition root. The Artlist capability is wired across
// 4 same-package files:
//
//   - build_bundles_artlist_types.go      — Pattern 0 compile-time pins + private structs (THIS file).
//   - build_bundles_artlist_artlist.go    — services core: WireArtlist orchestrator (now slim, calls the provider/publisher helpers) + validateArtlistScraperURL + WireArtlistJobBindings.
//   - build_bundles_artlist_providers.go  — provider wiring: ffmpeg processor, AdminSystemProber, HTTPSelfLoopProbe, downloader.Resolver (with PostValidator closure), ArtlistStager, processor adapter injection, Pexels/Pixabay searchers.
//   - build_bundles_artlist_publishers.go — delivery pipeline: AssetStoreSQLite.ProcessingRepository()/.VersionRepository() + artlist runs + download audit adapters + license/release/rendition SQLite repos.
//
// godlike/06 SSOT: this file owns the canonical Pattern-0 adapter wiring
// of the artlist capability.
// godlike/07 fail-closed: mandatory gates are checked UPFRONT and nil
// dependencies yield typed errors (see WireArtlist in artlist.go for the
// actual gate sequence).
package app

import (
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	artlist "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artlist/diagnostics"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artlist/downloader"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/artlist/fallback"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/job"

	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// Pattern 0 compile-time pins (AGENTS.md): canonical DIRECT receivers
// straight-satisfy the artlist ports. Drift in any signature surfaces as
// a build failure here rather than as a runtime panic on first dispatch.
var (
	_ artlist.AssetStore = (*assets.ClipsRepository)(nil) // 7-method set via *AssetStoreSQLite method-promotion
	_ artlist.Indexer    = (*clipindexer.Service)(nil)    // IndexClip + IsEnabled
	_ artlist.Dispatcher = (*outbox.Dispatcher)(nil)      // EnqueueAndIndex + SaveDiscoveredAsset
	_ job.Service        = (*appjobs.Service)(nil)        // cross-package alias safety (Build Deps.Jobs + ServiceDeps.JobsSvc)
)

// artlistProviders is the unexported return-bundle of
// constructArtlistProviders (build_bundles_artlist_providers.go).
// All fields are DIRECT receivers / canonical concretes; no shim
// layer is sandwiched between the composition root and the artlist
// pkg (godlike/06 SSOT).
//
// godlike/06 SSOT: PexelsSearcher / PixabaySearcher are the canonical
// CONCRETE types returned by fallback.NewPexels / fallback.NewPixabay
// respectively — they satisfy the artlist.ServicePorts.PexelsSearcher /
// PixabaySearcher INTERFACE fields downstream without an explicit
// shim layer. AssetProcRepo / AssetVerRepo are the INTERFACE types
// declared in internal/domain/asset (the SQLite-backed AssetStore
// methods return these interfaces).
type artlistProviders struct {
	IsLiveProbe       *artlist.HTTPSelfLoopProbe
	SystemProber      *diagnostics.AdminSystemProber
	ArtlistDownloader *downloader.Resolver
	ArtlistStager     *artlist.ArtlistStager
	PexelsSearcher    *fallback.Pexels
	PixabaySearcher   *fallback.Pixabay
}

// artlistRepositories is the unexported return-bundle of
// constructArtlistRepositories (build_bundles_artlist_publishers.go).
// All fields are canonical SQLite-backed repos + adapters wrapped
// through the composition-root adapter layer (godlike/06 SSOT).
// AssetProcRepo / AssetVerRepo are INTERFACES (the methods on
// *assets.AssetStoreSQLite return asset.ProcessingRepository /
// asset.VersionRepository respectively).
type artlistRepositories struct {
	AssetProcRepo asset.ProcessingRepository
	AssetVerRepo  asset.VersionRepository
	// RunsAdapter holds the canonical composition-root runs adapter (single
	// translation site between the artlist.RunRepository port and the
	// artlist.ArtlistRunsRepository concrete). The struct type is
	// intentionally unexported (godlike/06 SSOT): outside callers reach it
	// only through the constructor NewArtlistRunsRepoAdapter and the typed
	// assertion in artlist_runs_adapter.go.
	RunsAdapter *artlistRunsRepoAdapter
	// DownloadAuditAdapter is the canonical port-type returned by the
	// composition-root constructor NewArtlistDownloadAuditAdapter (which
	// returns the INTERFACE rather than the concrete struct — matches the
	// artlist-side port import cycle avoidance). The compile-time pin
	// lives in artlist_download_audit_adapter.go.
	DownloadAuditAdapter artlist.DownloadAuditRepository
	LicenseRepo          *assets.AssetLicenseRepository
	ReleaseRepo          *assets.AssetReleaseRepository
	RenditionRepo        *assets.AssetRenditionRepository
}
