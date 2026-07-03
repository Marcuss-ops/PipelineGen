// Package app — build_bundles_artlist.go: composition-root surface for the
// Artlist module (ART-001 FASE-6 reversal, July 2026).
//
// godlike/06 SSOT: this file owns the canonical Pattern-0 wiring of the
// artlist capability, including the only *artlist.SemanticEnricher
// instantiation in the process — composition root is the canonical owner
// for adapter wiring by construction.
//
// godlike/07 no-fake-availability: 4 mandatory gates are checked UPFRONT
// (Publisher / Dispatcher / ClipsRepo / Jobs.Service); nil on any yields a
// typed error, which registerArtlist downgrades to log.Warn + skip-route +
// return-nil. Operators see 404 on /api/artlist/* rather than a full-system
// boot abort.
//
// 9 forward-pointer nil fields (8 in ServiceDeps + 1 in Build(Dependencies))
// are declared explicitly with linked_issue cross-refs; see
// architecture/current.yaml#ART-001.linked_issues (godlike/07 EXPAND-phase
// discipline). Read-only endpoints (/stats, /diagnostics, /search/live)
// remain live even with forward-pointers nil; write endpoints (/run,
// /recommend, /sync-catalogs) return 503 at runtime via the handler's
// nil-tolerance.
//
// Single-function shape (WireArtlist) mirrors the existing WireMediaIngest
// precedent in registry_internal_modules.go (Blocco C1-Step 3 scope).
//
// Riuso: ArtlistBundle struct (bundle_types.go, canonical per PR4d-chunk2)
// + newArtlistConfigAdapter (adapters_infra.go, already compile-time-pinned
// against artlist.ArtlistConfigPort).
package app

import (
	"context"
	"fmt"

	artlistapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/artlist"
	artlistPkg "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	jobdomain "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/semantic"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outbox"
	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	clipindexer "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"go.uber.org/zap"
)

// Pattern 0 compile-time pins (AGENTS.md): canonical DIRECT receivers
// straight-satisfy the artlist ports. Drift in any signature surfaces as
// a build failure here rather than as a runtime panic on first dispatch.
var (
	_ artlistPkg.AssetStore = (*assets.ClipsRepository)(nil) // 7-method set via *AssetStoreSQLite method-promotion
	_ artlistPkg.Indexer    = (*clipindexer.Service)(nil)    // IndexClip + IsEnabled
	_ artlistPkg.Dispatcher = (*outbox.Dispatcher)(nil)      // EnqueueAndIndex + SaveDiscoveredAsset
	_ jobdomain.Service     = (*appjobs.Service)(nil)        // cross-package alias safety (Build Deps.Jobs + ServiceDeps.JobsSvc)
)

// WireArtlist constructs *artlist.Service + *ArtlistDescriptor from the canonical
// ArtlistBundle populated by registerArtlist + the 5 ComposeRoot receiver-fields
// (Dispatcher / Drive.Reader / Drive.Lifecycle / MetaWriter / DestResolver) that
// were not pre-exposed on ArtlistBundle by PR4d-chunk2 convention. Each of the
// 5 is a DIRECT receiver from ComposeRoot — not an adapter shim (godlike/06 SSOT).
//
// godlike/07: 4 mandatory gates checked UPFRONT. nil on any returns a typed error
// which the caller (registerArtlist) downgrades to log.Warn + skip-route + return-nil.
// godlike/06: SemanticEnricher is the canonical app-layer wrapper matching
// artlist.MetadataWriter.Enrich (enrich signature verbatim).
func WireArtlist(
	ctx context.Context,
	log *zap.Logger,
	cfg *config.Config,
	bundle *ArtlistBundle,
	dispatcher *outbox.Dispatcher,
	reader drivepkg.Reader,
	lifecycle drivepkg.FileLifecycle,
	metaWriter *semantic.MetadataWriter,
	destResolver asset.Resolver,
) (*ArtlistWiring, error) {
	_ = ctx

	// godlike/07 fail-closed: 4 mandatory gates UPFRONT.
	if bundle == nil {
		return nil, fmt.Errorf("WireArtlist: bundle is nil")
	}
	if bundle.Publisher == nil {
		return nil, fmt.Errorf("WireArtlist: bundle.Publisher is nil (F2.11 mandatory; artlist.NewService rejects with ErrPublisherUnavailable so we fail-closed upstream)")
	}
	if dispatcher == nil {
		return nil, fmt.Errorf("WireArtlist: dispatcher is nil (QDRANT-002 mandatory; artlist.NewSearchService rejects with ErrAssetMutationDispatcherUnavailable)")
	}
	if bundle.ClipsRepo == nil {
		return nil, fmt.Errorf("WireArtlist: bundle.ClipsRepo is nil (AssetStore port nil at first SearchByTerms call would panic)")
	}
	if bundle.Jobs == nil || bundle.Jobs.Service == nil {
		return nil, fmt.Errorf("WireArtlist: bundle.Jobs.Service is nil (Build dep + JobsSvc; /run path unreachable)")
	}

	// jobdomain.Service alias pin is verified at compile time (Pattern 0 + AGENTS.md).
	_ = (jobdomain.Service)(bundle.Jobs.Service)

	log.Info("WireArtlist: ART-001 reversal wiring starting",
		zap.String("root_path", "/api/artlist/*"),
		zap.Bool("godlike_07_fail_closed", true),
	)

	// godlike/06 SSOT: SemanticEnricher is the canonical app-layer wrapper for
	// *semantic.MetadataWriter; its Enrich(ctx, clip, term) signature matches
	// artlist.MetadataWriter.Enrich exactly (semantic_enricher.go:147). The 8
	// constructor args are all DIRECT receivers — no shim layer.
	semanticEnricher := artlistPkg.NewSemanticEnricher(
		bundle.ClipsRepo,
		bundle.ClipIndexerService,
		metaWriter,
		bundle.Publisher,
		reader,
		dispatcher,
		lifecycle,
		log,
	)

	// 19-field ServiceDeps literal via nested named-struct init (8 ServicePorts
	// + 11 ServiceDependencies). 8 forward-pointer nil fields tagged with
	// linked_issue id per architecture/current.yaml#ART-001.linked_issues.
	service, err := artlistPkg.NewService(artlistPkg.ServiceDeps{
		ServicePorts: artlistPkg.ServicePorts{
			// ServicePorts (8) — 4 DIRECT, 4 FORWARD_POINTER nil.
			AssetStore:      bundle.ClipsRepo,
			Indexer:         bundle.ClipIndexerService,
			MetadataWriter:  semanticEnricher,
			Publisher:       bundle.Publisher,
			ScraperSearcher: nil, // forward-pointer: PR-ARTLIST-LIVE-WIRE
			PixabaySearcher: nil, // forward-pointer: PR-ARTLIST-SEARCHERS
			PexelsSearcher:  nil, // forward-pointer: PR-ARTLIST-SEARCHERS
			Stager:          nil, // forward-pointer: PR-ARTLIST-STAGER
		},
		ServiceDependencies: artlistPkg.ServiceDependencies{
			// ServiceDependencies (11) — 7 DIRECT, 4 FORWARD_POINTER nil.
			Cfg:               cfg,
			MainDB:            bundle.DB.DB,
			Log:               log,
			Dispatcher:        dispatcher,
			MediaProcessor:    bundle.MediaProcessor,
			LifecycleService:  nil, // forward-pointer: PR-ARTLIST-LIFECYCLE
			AssetDestResolver: destResolver,
			JobsSvc:           bundle.Jobs.Service,
			AssetProcRepo:     nil, // forward-pointer: PR-ARTLIST-REPOS
			AssetVerRepo:      nil, // forward-pointer: PR-ARTLIST-REPOS
			AssetLocRepo:      nil, // forward-pointer: PR-ARTLIST-REPOS
		},
	})
	if err != nil {
		return nil, fmt.Errorf("WireArtlist: artlist.NewService: %w", err)
	}

	descriptor, err := artlistapi.Build(artlistapi.Dependencies{
		Service:        service,
		CatalogSync:    bundle.CatalogSyncService,
		Jobs:           bundle.Jobs.Service,
		ClipResolver:   nil, // forward-pointer: PR-ARTLIST-SYNCSERVICE
		NodeScraperDir: cfg.External.NodeScraperDir,
		CfgPort:        newArtlistConfigAdapter(cfg),
		EnabledFunc:    func() bool { return cfg.Features.ArtlistEnabled },
		ModuleOpts:     nil,
		Logger:         log,
	})
	if err != nil {
		_ = service.Close()
		return nil, fmt.Errorf("WireArtlist: artlist.Build: %w", err)
	}

	// Type-assert the canonical concrete (Blocco C1-Step 3: descriptor is
	// api.Descriptor at the wire layer; the *artlistapi.ArtlistDescriptor
	// concrete carries Module + Service fields). Mirrors registerYouTubeClip
	// precedent (335-340 of registry_internal_modules.go).
	ad, ok := descriptor.(*artlistapi.ArtlistDescriptor)
	if !ok || ad == nil {
		_ = service.Close()
		return nil, fmt.Errorf("WireArtlist: artlist.Build returned unexpected descriptor type %T (want *artlistapi.ArtlistDescriptor)", descriptor)
	}

	log.Info("WireArtlist: ART-001 reversal wiring complete",
		zap.String("descriptor_name", ad.Name()),
		zap.Bool("godlike_06_ssot", true),
	)
	return &ArtlistWiring{Module: ad.Module, Service: ad.Service}, nil
}
