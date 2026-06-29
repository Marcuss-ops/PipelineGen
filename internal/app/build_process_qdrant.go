// Package app — Process (BuildProcessBundle) + Qdrant compile-time
// assertions (FASE 2.B PR2, June 2026).
//
// Originally build_bundles_process.go owned three bundle constructors +
// composition-time Qdrant port assertions (BuildDriveBundle +
// startDriveBackgroundFolders were also inline originally):
//
//   - BuildProcessBundle
//   - BuildOutboxBundle
//   - startOutboxEventsPool
//   - Qdrant compile-time assertions
//
// PR1 (June 2026) extracted the Drive bundle construction to
//   - internal/app/build_bundles_drive.go   (BuildDriveBundle — Drive
//     client + folder resolver init, MediaStore derivation, StyleRegistry load)
//   - internal/app/build_drive_startup.go  (startDriveBackgroundFolders —
//     Drive folder bootstrap, AC validation, retry warmup)
//
// PR2 (June 2026) extracts BuildProcessBundle + the Qdrant compile-time
// assertions from build_bundles_process.go to THIS file so that:
//
//   - this file    owns ONLY BuildProcessBundle + Qdrant
//     compile-time port assertions (the Qdrant-derivable media-
//     processing bundle + the typed-port conformance gates).
//   - build_bundles_process.go is reduced to ONLY BuildOutboxBundle
//     + startOutboxEventsPool (the canonical ingestion-path outbox +
//     SafeGo launchers).
//
// Each bundle constructor corresponds to ONE bundle concept per
// AGENTS.md Pattern 5 (no half-bundles, no `Build*And*` composites).
// PR2 is MOVE-only: zero logic changes here, zero call-site changes
// anywhere in the codebase.
package app

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/ai/vlm"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// TODO #8 (June 2026) drift-fix: the canonical
// `internal/infrastructure/database/sqlite/assets` package is
// imported here as `sqassets` (matching the existing convention in
// build_bundles_core.go and the rest of the composition root). The
// previously-bare `assets.ClipsRepository` reference at line 205
// was an UNIMPORTED symbol — this file pre-existing-did-not-compile
// because no local `assets` or `sqassets` alias was set. Renaming
// the assertion to `sqassets.ClipsRepository` plus adding the
// import pins static conformance for jobsoutbox.AssetDeleter exactly
// like the previous code intended. The corresponding direct
// assignment at the AssetDeleter wiring site (`outboxDeps.AssetDeleter
// = repos.ClipsRepo`, gated on `cfg.Qdrant.Enabled && repos.ClipsRepo
// != nil`) is type-safe because the assertion below proves the
// static conformance.
// Compile-time assertions for QDRANT-003 wiring + PR 3
// (fix/qdrant-outbox-fail-closed). Per AGENTS.md Pattern 0 the
// composition root is where the typed-port contract is enforced: every
// port referenced from outbox.Deps must statically implement its
// concrete so a future refactor misses the compile, not the first
// outbox replay.
// Compile-time assertions for QDRANT-003 wiring + PR 3
// (fix/qdrant-outbox-fail-closed). Per AGENTS.md Pattern 0 the
// composition root is where the typed-port contract is enforced: every
// port referenced from outbox.Deps must statically implement its
// concrete so a future refactor misses the compile, not the first
// outbox replay.
var (
	_ clipindexer.VectorStoreIndexer = (*qdrant.IndexWriter)(nil)
	_ jobsoutbox.AssetDeleter        = (*sqassets.ClipsRepository)(nil)
)

// BuildProcessBundle builds media-processing adapters. driveUploader
// passed in directly.
//
// QDRANT-003 (June 2026): Qdrant vector-store capability reintroduced.
// IndexWriter + ClipIndexerService are constructed in the canonical
// pre-phase (composition.go::buildQdrantDeps) so BuildOutboxBundle can
// run BEFORE BuildProcessBundle, and threaded back here via the qd
// *QdrantDeps input. EnsureSchema is deferred to wire_services.go
// startup plan (startup-time).
//
// PR 8 (June 2026, codex/qdrant-app-writers-fail-closed):
// BuildProcessBundle gains `outbox *OutboxBundle` + `qd *QdrantDeps` as
// the last 2 positional args. MediaProcessor is now constructed INLINE
// here — the previous PR-7 deferred-hydration strategy
// (`BuildProcessBundle.MediaProcessor=nil + hydrateMediaProcessor`) is
// gone. Composition graph is now a strict DAG:
//
//	qdrantDeps(no deps) -> outbox(reads qd) -> process(reads outbox+qd) ->
//	  domains(reads process+outbox)
//
// Fail-closed at the composition root: a nil outbox.Dispatcher leaves
// MediaProcessor=nil so worker / reprocess / ingest paths surface the
// missing dep rather than silently defaulting to the legacy path. A
// nil qd fails composition immediately (composition forgot to call
// buildQdrantDeps first?).
func BuildProcessBundle(ctx context.Context, cfg *config.Config, dbs *databases, log *zap.Logger, repos *RepoBundle, driveUploader *drive.Uploader, outbox *OutboxBundle, qd *QdrantDeps) (*ProcessBundle, error) {
	_ = ctx

	if qd == nil {
		return nil, fmt.Errorf("BuildProcessBundle: qdrantDeps is nil (QDRANT-002 PR8 fail-closed; composition forgot to call buildQdrantDeps first?)")
	}

	// PR 8 (June 2026): MediaProcessor constructed INLINE here. The
	// previous `MediaProcessor=nil + hydrateMediaProcessor` deferred-
	// hydration strategy (PR 7) is gone — composition order is
	// qd -> outbox -> process so outbox.Dispatcher is available at this
	// point in NewComposition's strict-DAG orchestration. Fail-closed:
	// a nil outbox.Dispatcher leaves MediaProcessor=nil.
	var mediaProcessor asset.Processor
	if outbox != nil && outbox.Dispatcher != nil {
		mutationsDisp, err := newMutationsDispatcherAdapter(outbox.Dispatcher)
		if err != nil {
			return nil, fmt.Errorf("BuildProcessBundle: mutations dispatcher adapter: %w", err)
		}
		mediaProcessor = initMediaProcessor(cfg, dbs.main, repos.Assets.Repository(), repos.Assets,
			repos.Assets.LocationRepository(), repos.Assets.ProcessingRepository(),
			mutationsDisp, log, driveUploader)
		log.Info("PR 8: MediaProcessor constructed inline with canonical mutations.AssetMutationDispatcher (clipsRegistry UPSERT routed through outbox+tx)")
	} else {
		log.Warn("BuildProcessBundle: outbox.Dispatcher is nil — MediaProcessor left nil (QDRANT-002 PR8 fail-closed; worker + reprocess + ingest paths will surface the missing dep)")
	}

	vlmClient := vlm.NewClient(vlm.Config{
		Enabled:   cfg.VLM.Enabled,
		Endpoint:  cfg.VLM.URL,
		Model:     cfg.VLM.Model,
		TimeoutMs: cfg.VLM.TimeoutMs,
		Weight:    cfg.VLM.Weight,
	})

	// QDRANT-005 Phase 1 Blocker 2 (June 2026): Qdrant subsystems are
	// sourced from qd.Runtime (PR 4, June 2026,
	// refactor/single-qdrant-runtime) so there is exactly ONE
	// *qdrant.Client + ONE *IndexSchema per process. Pre-PR4 the
	// BuildProcessBundle body had its OWN qdrant.NewClient + DefaultV3Schema
	// call, second to the ones in composition.go::buildQdrantDeps — the
	// two *Clients were distinct pointer values (so wire_only
	// invariants like api-key header could silently drift between the
	// two) but functionally identical. After PR 4 all subsystems
	// read from the runtime. nil qd.Runtime → all subsystems nil
	// (Qdrant disabled feature flag).
	var (
		collectionMgr     *qdrant.CollectionManager
		vectorSvc         assetsearch.VectorStorePort
		qdrantClient      *qdrant.Client
		qdrantHealthProbe *qdrant.HealthProbe
		locatorCleaner    *qdrant.LocatorCleaner
		qdrantSearcher    *qdrant.Searcher
	)

	if qd.Runtime != nil {
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
	} else {
		log.Info("QDRANT-003: Qdrant disabled — no Qdrant components wired (BuildProcessBundle)")
	}

	return &ProcessBundle{
		MediaProcessor:     mediaProcessor,
		ClipIndexerService: qd.ClipIndexerService,
		VLMClient:          vlmClient,
		CollectionManager:  collectionMgr,
		QdrantDeleter:      qd.QdrantDeleter,
		QdrantRuntime:      qd.Runtime, // PR 4: first-class facade exposed at ProcessBundle level
		VectorSvc:          vectorSvc,
		QdrantClient:       qdrantClient,
		QdrantHealthProbe:  qdrantHealthProbe,
		LocatorCleaner:     locatorCleaner,
		QdrantSearcher:     qdrantSearcher,
	}, nil
}
