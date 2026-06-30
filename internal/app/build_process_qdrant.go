// Package app — ProcessBundle orchestration (FASE 2.B PR2-followup, June 2026).
//
// Originally build_process_qdrant.go owned BuildProcessBundle AND its
// inline body (media-processor init + VLMClient init + Qdrant runtime
// subsystem sourcing) AND the 2 QDRANT-003-style compile-time port
// assertions. The PR2-followup extraction splits these blocks into
// dedicated helper hosts so that:
//
//   - build_media_processor.go owns ONLY wireMediaProcessor (canonical
//     mutations.AssetMutationDispatcher adapter + initMediaProcessor
//     FFmpeg-backed wiring) + newVLMClient (cfg.VLM → *vlm.Client).
//   - build_qdrant_runtime.go owns ONLY initQdrantProcessSubsystems
//     (Qdrant runtime → ProcessBundle mapping via named returns) +
//     the 2 QDRANT-003-style composition-time port assertions (clipindexer
//     + jobsoutbox/sqassets).
//   - THIS file is reduced to a thin BuildProcessBundle orchestrator
//     that calls the 3 helpers above and assembles the canonical
//     *ProcessBundle return value.
//
// Why an orchestrator (not a no-op file): BuildProcessBundle owns the
// composition-time nil check on `qd *QdrantDeps` (fail-closed, QDRANT-002
// PR8) and the ProcessBundle field-assembly (mapping helper outputs to
// the canonical struct). Both responsibilities live here because they
// are the bundle's invariant contract, not a per-helper concern.
//
// PR2-followup is MOVE-only: zero logic changes in any of the 3 files,
// zero call-site changes across the codebase.
package app

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// BuildProcessBundle builds the media-processing adapters and assembles
// the canonical *ProcessBundle. driveUploader is passed in directly.
//
// The body is intentionally thin: it calls the 3 cross-file helpers
// (wireMediaProcessor, newVLMClient, initQdrantProcessSubsystems) and
// maps their outputs to the ProcessBundle field set. The fail-closed
// contract on qd nil is enforced HERE (composition root, BEFORE the
// helpers run) so a missing qdrantDeps is surfaced as a typed error
// rather than silently downgrading to the disabled-Qdrant fallback.
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
// the last 2 positional args. MediaProcessor is now constructed via
// wireMediaProcessor (which delegates to initMediaProcessor for FFmpeg
// wiring). Composition graph is now a strict DAG:
//
//	qdrantDeps(no deps) -> outbox(reads qd) -> process(reads outbox+qd) ->
//	  domains(reads process+outbox)
//
// Fail-closed at the composition root: a nil outbox.Dispatcher leaves
// MediaProcessor=nil so worker / reprocess / ingest paths surface the
// missing dep rather than silently defaulting to the legacy path. A
// nil qd fails composition immediately (composition forgot to call
// buildQdrantDeps first?).
func BuildProcessBundle(
	ctx context.Context,
	cfg *config.Config,
	dbs *databases,
	log *zap.Logger,
	repos *RepoBundle,
	driveUploader *drive.Uploader,
	outbox *OutboxBundle,
	qd *QdrantDeps,
) (*ProcessBundle, error) {
	_ = ctx

	if qd == nil {
		return nil, fmt.Errorf("BuildProcessBundle: qdrantDeps is nil (QDRANT-002 PR8 fail-closed; composition forgot to call buildQdrantDeps first?)")
	}

	mediaProcessor, err := wireMediaProcessor(outbox, repos, dbs, cfg, driveUploader, log)
	if err != nil {
		return nil, err
	}

	vlmClient := newVLMClient(cfg)

	collectionMgr, vectorSvc, qdrantClient, qdrantHealthProbe, locatorCleaner, qdrantSearcher := initQdrantProcessSubsystems(qd, cfg, log)

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

// ── Qdrant port assertions + subsystem init (moved from build_qdrant_runtime.go, Phase 5 consolidation, June 2026) ──

var (
	_ clipindexer.VectorStoreIndexer = (*qdrant.IndexWriter)(nil)
	_ jobsoutbox.AssetDeleter        = (*sqassets.ClipsRepository)(nil)
)

func initQdrantProcessSubsystems(
	qd *QdrantDeps,
	cfg *config.Config,
	log *zap.Logger,
) (
	collectionMgr *qdrant.CollectionManager,
	vectorSvc assetsearch.VectorStorePort,
	qdrantClient *qdrant.Client,
	qdrantHealthProbe *qdrant.HealthProbe,
	locatorCleaner *qdrant.LocatorCleaner,
	qdrantSearcher *qdrant.Searcher,
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
