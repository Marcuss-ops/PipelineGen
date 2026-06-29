// Package app — Qdrant runtime subsystem helpers + composition-time
// port assertions (FASE 2.B PR2-followup, June 2026).
//
// Originally the Qdrant subsystem sourcing block (the 6 vars +
// if qd.Runtime != nil read-then-log block) + the 2 QDRANT-003-style
// compile-time port assertions were inline at the top + body of
// BuildProcessBundle inside build_process_qdrant.go. The PR2-followup
// extraction moves these into helpers + the canonical compile-time
// assertion host in THIS file so that:
//
//   - this file          owns ONLY initQdrantProcessSubsystems (the
//     Qdrant runtime → ProcessBundle mapping via named returns) +
//     the 2 QDRANT-003-style composition-time port assertions.
//
//   - build_media_processor.go owns ONLY wireMediaProcessor +
//     newVLMClient.
//
//   - build_process_qdrant.go is reduced to a thin BuildProcessBundle
//     orchestrator that calls the 3 helpers across the 2 files above
//     and assembles the canonical *ProcessBundle return value.
//
// The 2 compile-time port assertions pin QDRANT-003 wiring + PR 3
// (fix/qdrant-outbox-fail-closed) conformance: every port referenced
// from outbox.Deps / clipindexer must statically implement its
// concrete so a future refactor misses the compile, not the first
// outbox replay. The TODO #8 drift-fix comment about sqassets is
// verbatim-preserved from the pre-extraction build_process_qdrant.go
// (MOVE-only constraint).
//
// PR2-followup is MOVE-only: zero logic changes in any of the 3 files,
// zero call-site changes across the codebase.
package app

import (
	"go.uber.org/zap"

	assetsearch "github.com/Marcuss-ops/PipelineGen/internal/application/assets/search"
	jobsoutbox "github.com/Marcuss-ops/PipelineGen/internal/application/jobs/outbox"
	sqassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/assets"
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

// initQdrantProcessSubsystems sources the 6 Qdrant subsystem pointers
// from the single-process Qdrant runtime (`qd.Runtime`). The runtime
// is the result of composition.go::buildQdrantDeps's qdrant.NewRuntime
// call — exactly ONE *qdrant.Client + ONE *IndexSchema per process
// (QDRANT-005 PR 4, refactor/single-qdrant-runtime). Pre-PR4 the
// BuildProcessBundle body had its own qdrant.NewClient + DefaultV3Schema
// call as well, leading to two pointers that drifted on api-key
// header. The runtime consolidates them.
//
// Named returns are used so that the disabled-Qdrant path (qd.Runtime
// is nil) can `return` without zeroing 6 variables explicitly; the
// default zero of each named return is the correct nil-for-its-type
// (concrete pointers + nil interface for assetsearch.VectorStorePort),
// preserving the pre-extraction behavior verbatim.
//
// Returns are unused-by-Bundle if the bundle leaves them nil; the
// ProcessBundle's per-field consumers guard their access with nil
// checks (composition_graph: qdrantDeps → outbox → process → domains).
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
		return // named returns default to nil — matches pre-extraction inline behavior
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
