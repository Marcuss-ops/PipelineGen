// Package app — wiring.ProcessBundle orchestration (FASE 2.B PR2-followup, June 2026).
//
// Originally build_process_qdrant.go owned BuildProcessBundle AND its
// inline body (media-processor init + VLMClient init + Qdrant runtime
// subsystem sourcing) AND the 2 QDRANT-003-style compile-time port
// assertions. The PR2-followup extraction splits these blocks into
// dedicated helper hosts so that:
//
//   - build_media_processor.go owns ONLY wireMediaProcessor (canonical
//     mutations.AssetMutationDispatcher adapter + wiring.InitMediaProcessor
//     FFmpeg-backed wiring) + newVLMClient (cfg.VLM → *vlm.Client).
//   - build_qdrant_runtime.go owns ONLY initQdrantProcessSubsystems
//     (Qdrant runtime → wiring.ProcessBundle mapping via named returns) +
//     the 2 QDRANT-003-style composition-time port assertions (clipindexer
//   - jobsoutbox/sqassets).
//   - THIS file is reduced to a thin BuildProcessBundle orchestrator
//     that calls the 3 helpers above and assembles the canonical
//     *wiring.ProcessBundle return value.
//
// Why an orchestrator (not a no-op file): BuildProcessBundle owns the
// composition-time nil check on `qd *wiring.QdrantDeps` (fail-closed, QDRANT-002
// PR8) and the wiring.ProcessBundle field-assembly (mapping helper outputs to
// the canonical struct). Both responsibilities live here because they
// are the bundle's invariant contract, not a per-helper concern.
//
// PR2-followup is MOVE-only: zero logic changes in any of the 3 files,
// zero call-site changes across the codebase.
package app

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/app/wiring"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/application/mediaexec"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/collections"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/disasterrecovery"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/indexing"
	qdrantmaintenance "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/maintenance"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"
	qdrantsearch "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/search"
	qdranttransport "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	regsql "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/mediaregistry"
)

// BuildProcessBundle builds the media-processing adapters and assembles
// the canonical *wiring.ProcessBundle. publisher is passed in directly —
// the Publisher is the canonical Drive upload canal, and
// processor.NewProcessor panics on nil publisher so a wiring gap is
// loud at boot.
//
// F2.8 (June 2026): the trailing arg swaps from `*drive.Uploader` to
// `delivery.Publisher`. The Publisher routes every Drive write from
// the processor through the DestinationRegistry + RequireSubpath +
// ConflictPolicy belt; the legacy direct-uploader bypass is closed.
// See wiring.DriveBundle.Publisher doc for the canonical one-owner-per-fact
// story (godlike/06).
//
// The body is intentionally thin: it calls the 3 cross-file helpers
// (wireMediaProcessor, newVLMClient, initQdrantProcessSubsystems) and
// maps their outputs to the wiring.ProcessBundle field set. The fail-closed
// contract on qd nil is enforced HERE (composition root, BEFORE the
// helpers run) so a missing qdrantDeps is surfaced as a typed error
// rather than silently downgrading to the disabled-Qdrant fallback.
//
// QDRANT-003 (June 2026): Qdrant vector-store capability reintroduced.
// IndexWriter + ClipIndexerService are constructed in the canonical
// pre-phase (composition.go::buildQdrantDeps) so BuildOutboxBundle can
// run BEFORE BuildProcessBundle, and threaded back here via the qd
// *wiring.QdrantDeps input. EnsureSchema is deferred to wire_services.go
// startup plan (startup-time).
//
// PR 8 (June 2026, codex/qdrant-app-writers-fail-closed):
// BuildProcessBundle gains `outbox *wiring.OutboxBundle` + `qd *wiring.QdrantDeps` as
// the last 2 positional args. MediaProcessor is now constructed via
// wireMediaProcessor (which delegates to wiring.InitMediaProcessor for FFmpeg
// wiring). Composition graph is now a strict DAG:
//
//	qdrantDeps(no deps) -> outbox(reads qd) -> process(reads outbox+qd) ->
//	  domains(reads process+outbox)
//
// Fail-closed at the composition root: a nil outbox.Dispatcher leaves
// MediaProcessor=nil so worker / reprocess / ingest paths surface the
// missing dep rather than silently defaulting to the legacy path. A
// nil qd fails composition immediately (composition forgot to call
// buildQdrantDeps first?). F2.8 widens this to a nil publisher — a
// missing publisher surfaces in processor.NewProcessor as a typed
// panic at composition time (loud in operator log) rather than
// silent nil-deref on first upload.
func BuildProcessBundle(
	ctx context.Context,
	cfg *config.Config,
	dbs *wiring.Databases,
	log *zap.Logger,
	repos *wiring.RepoBundle,
	publisher delivery.Publisher,
	outbox *wiring.OutboxBundle,
	qd *wiring.QdrantDeps,
	mediaConfig mediaexec.ExecutionConfig,
) (*wiring.ProcessBundle, error) {
	_ = ctx

	if qd == nil {
		return nil, fmt.Errorf("BuildProcessBundle: qdrantDeps is nil (QDRANT-002 PR8 fail-closed; composition forgot to call buildQdrantDeps first?)")
	}

	mediaProcessor, err := wireMediaProcessor(outbox, repos, dbs, cfg, publisher, log, mediaConfig)
	if err != nil {
		return nil, err
	}

	vlmClient := newVLMClient(cfg)

	collectionMgr, vectorSvc, qdrantClient, qdrantHealthProbe, locatorCleaner, qdrantSearcher := initQdrantProcessSubsystems(qd, cfg, log)

	return &wiring.ProcessBundle{
		ProcessQdrantBundle: wiring.ProcessQdrantBundle{
			CollectionManager: collectionMgr,
			QdrantDeleter:     qd.QdrantDeleter,
			QdrantRuntime:     qd.Runtime, // PR 4: first-class facade exposed at wiring.ProcessBundle level
			VectorSvc:         vectorSvc,
			QdrantClient:      qdrantClient,
			QdrantHealthProbe: qdrantHealthProbe,
			LocatorCleaner:    locatorCleaner,
			QdrantSearcher:    qdrantSearcher,
		},
		MediaProcessor:     mediaProcessor,
		ClipIndexerService: qd.ClipIndexerService,
		VLMClient:          vlmClient,
	}, nil
}

// ── Qdrant port assertions + subsystem init (moved from build_qdrant_runtime.go, Phase 5 consolidation, June 2026) ──

var (
	_ clipindexer.VectorStoreIndexer = (*indexing.IndexWriter)(nil)
	_ jobsoutbox.AssetDeleter        = (*sqassets.ClipsRepository)(nil)
)

func initQdrantProcessSubsystems(
	qd *wiring.QdrantDeps,
	cfg *config.Config,
	log *zap.Logger,
) (
	collectionMgr *collections.CollectionManager,
	vectorSvc assetsearch.VectorStorePort,
	qdrantClient *qdranttransport.Client,
	qdrantHealthProbe *disasterrecovery.HealthProbe,
	locatorCleaner *qdrantmaintenance.LocatorCleaner,
	qdrantSearcher *qdrantsearch.Searcher,
) {
	if qd.Runtime == nil {
		log.Info("QDRANT-003: Qdrant disabled — no Qdrant components wired (BuildProcessBundle)")
		return
	}
	collectionMgr = qd.Runtime.Manager
	vectorSvc = qd.Runtime.SearchAdapter
	qdrantClient = qd.Runtime.Client
	qdrantHealthProbe = qd.Runtime.Health
	locatorCleaner = qd.Runtime.Cleaner
	qdrantSearcher = qd.Runtime.Searcher
	log.Info("QDRANT-005 PR4: HealthProbe + LocatorCleaner + Searcher + CollectionManager sourced from single QdrantRuntime (BuildProcessBundle)",
		zap.String("qdrant_url", cfg.Qdrant.BaseURL),
		zap.String("schema_version", qd.Runtime.Schema.Version))
	log.Info("QDRANT-004 PR4: VectorStorePort sourced from single QdrantRuntime.SearchAdapter (BuildProcessBundle)")
	return
}
func buildQdrantDeps(ctx context.Context, cfg *config.Config, dbs *wiring.Databases, repos *wiring.RepoBundle, log *zap.Logger) (*wiring.QdrantDeps, error) {
	_ = ctx

	// PR-QDRANT-CONFIG-MISMATCH-GATE (July 2026): canonical
	// godlike/06 SSOT helper. Replaces the pre-PR inline check
	// (BLOCKER #3 direction A: ClipIndexer=true AND Qdrant=false)
	// with a single canonical helper that covers BOTH directions
	// of the compatibility check. Direction B (Qdrant=true AND
	// ClipIndexer=false) is the CRITICAL RED POINT surfaced by the
	// QDRANT-CHAIN-VERIFY-2026-07-04 audit — IndexClip short-circuits
	// and outbox marks asset.index.requested as COMPLETED without
	// writing to Qdrant. godlike/07 no-fake-availability: fail-closed
	// at composition time. Cross-ref:
	// internal/app/build_bundles_qdrant_gates.go::validateQdrantIndexerCompatibility
	// (SECOND wire site; build_qdrant_gates.go is the canonical
	// godlike/06 SSOT surface, this is one of 4 wire sites).
	if err := validateQdrantIndexerCompatibility(cfg); err != nil {
		return nil, err
	}

	clipIndexerService := clipindexer.NewService(&clipindexer.Config{
		Enabled:               cfg.ClipIndexer.Enabled,
		ServerURL:             cfg.ClipIndexer.ServerURL,
		ScriptPath:            cfg.ClipIndexer.ScriptPath,
		PythonBin:             cfg.ClipIndexer.PythonBin,
		AutoIndexAfterArtlist: cfg.ClipIndexer.AutoIndexAfterArtlist,
		MaxConcurrentIndexing: cfg.ClipIndexer.MaxConcurrentIndexing,
		DBPath:                dbs.Main.Path(),
	}, dbs.Main, dbs.Main.Path(), log)

	var runtime *qdrant.QdrantRuntime
	if cfg.Qdrant.Enabled {
		registryLedger, ledgerErr := regsql.NewLedger(dbs.DualPool.Writer)
		if ledgerErr != nil {
			return nil, fmt.Errorf("buildQdrantDeps: media registry ledger: %w", ledgerErr)
		}
		var rerr error
		runtime, rerr = qdrant.NewRuntime(qdrant.RuntimeConfig{
			QdrantCfg: &schema.Config{
				BaseURL: cfg.Qdrant.BaseURL,
				APIKey:  cfg.Qdrant.APIKey,
				Timeout: cfg.Qdrant.Timeout,
			},
			DB:             dbs.DualPool.Writer,
			Logger:         log,
			RegistryLedger: registryLedger,
		})
		if rerr != nil {
			return nil, fmt.Errorf("buildQdrantDeps: qdrant.NewRuntime: %w", rerr)
		}

		// Wire the canonical language registry into the PayloadMapper so
		// youtubeStrategy can filter TextTracks by the configured
		// language capabilities instead of a second slice.
		if langsCSV, csvErr := wiring.BuildMultilingualLanguageCSV(wiring.ActiveMultilingualConfig(cfg), nil); csvErr == nil {
			runtime.Mapper.SetIndexLanguages(langsCSV)
		} else {
			return nil, fmt.Errorf("buildQdrantDeps: index languages: %w", csvErr)
		}
		// Wire TextTrackRepository so the PayloadMapper can populate
		// SearchTextInput.TextTracks at search-text construction time.
		// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5 (July 2026): the
		// TextTrackRepository is now sourced from the canonical
		// wiring.RepoBundle.TextTrackRepo (wired in BuildRepoBundle). The
		// pre-PR local construction is removed so every consumer
		// (wiring.BuildTextTrackBundle, BuildRepoBundle, this Qdrant
		// PayloadMapper, the TextTrackResolver in buildDomainMediaServices)
		// shares the SAME instance.
		if repos != nil && repos.TextTrackRepo != nil {
			runtime.Mapper.SetTextTrackQuerier(repos.TextTrackRepo)
		} else {
			log.Warn("buildQdrantDeps: TextTrackRepository nil in wiring.RepoBundle; multilingual TextTracks disabled in search text")
		}
		// PR 3 fix/qdrant-outbox-fail-closed (#3 from verdict Qdrant):
		// IndexWriter is now constructed when Qdrant is enabled, regardless
		// of the ClipIndexer sidecar's IsEnabled bit. The previous
		// `cfg.Qdrant.Enabled && clipIndexerService.IsEnabled()` AND-gate
		// silently dropped the QdrantDeleter port whenever the ClipIndexer
		// service was disabled — the IndexDeleteHandler then dead-lettered
		// every asset.index.delete_requested event because both
		// QdrantDeleter and the paired AssetDeleter slot were nil at
		// registration time. Decoupled semantics: Qdrant-enabled →
		// Qdrant and IndexWriter always present. ClipIndexer is the sidecar
		// path (writes via the AI server) and stays independent of the
		// outbox deletion path.
		if clipIndexerService.IsEnabled() {
			clipIndexerService.SetVectorStore(runtime.Writer)
			log.Info("QDRANT-003 PR4: IndexWriter (from QdrantRuntime) wired as clipindexer VectorStoreIndexer",
				zap.String("runtime_alias", runtime.Schema.RuntimeAlias))
		} else {
			log.Info("QDRANT-003 PR4: Qdrant enabled, ClipIndexer disabled — QdrantRuntime constructed for IndexDeleteHandler path; VectorStore not wired into clipindexer service")
		}
	} else {
		log.Info("QDRANT-003: Qdrant disabled — no QdrantRuntime wired (buildQdrantDeps pre-phase)")
	}

	qd := &wiring.QdrantDeps{
		Runtime:            runtime,
		ClipIndexerService: clipIndexerService,
	}
	// PR 4: VectorPointDeleter port satisfied directly by runtime.Writer
	// (compile-time assertion in internal/infrastructure/qdrant/index_writer.go
	// pins the conformance: `_ jobsoutbox.VectorPointDeleter = (*qdrant.IndexWriter)(nil)`).
	// No runtime `any` cast needed.
	if runtime != nil {
		qd.QdrantDeleter = runtime.Writer
	}
	return qd, nil
}
