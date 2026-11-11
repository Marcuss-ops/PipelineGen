// Package app — ProcessBundle orchestration (FASE 2.B PR2-followup, June 2026).
//
// Originally build_process_qdrant.go owned BuildProcessBundle AND its
// inline body (media-processor init + VLMClient init + Qdrant runtime
// subsystem sourcing) AND the 2 QDRANT-003-style compile-time port
// assertions. The PR2-followup extraction splits these blocks into
// dedicated helper hosts so that:
//
//   - build_media_processor.go owns ONLY wireMediaProcessor (canonical
//     mutations.AssetMutationDispatcher adapter + InitMediaProcessor
//     FFmpeg-backed wiring) + newVLMClient (cfg.VLM → *vlm.Client).
//   - build_qdrant_runtime.go owns ONLY initQdrantProcessSubsystems
//     (Qdrant runtime → ProcessBundle mapping via named returns) +
//     the 2 QDRANT-003-style composition-time port assertions (clipindexer
//   - jobsoutbox/sqassets).
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
package wiring

import (
	"context"
	"database/sql"
	"fmt"

	"go.uber.org/zap"

	processwiring "github.com/Marcuss-ops/PipelineGen/internal/app/wiring/process"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/indexing/clipindexer"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	regsql "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/mediaregistry"
)
import (
	qdrantschema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// BuildProcessBundle builds the media-processing adapters and assembles
// the canonical *ProcessBundle. publisher is passed in directly —
// the Publisher is the canonical Drive upload canal, and
// processor.NewProcessor panics on nil publisher so a wiring gap is
// loud at boot.
//
// F2.8 (June 2026): the trailing arg swaps from `*drive.Uploader` to
// `delivery.Publisher`. The Publisher routes every Drive write from
// the processor through the DestinationRegistry + RequireSubpath +
// ConflictPolicy belt; the legacy direct-uploader bypass is closed.
// See DriveBundle.Publisher doc for the canonical one-owner-per-fact
// story (godlike/06).
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
// wireMediaProcessor (which delegates to InitMediaProcessor for FFmpeg
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
	dbs *Databases,
	log *zap.Logger,
	repos *RepoBundle,
	publisher delivery.Publisher,
	outbox *OutboxBundle,
	qd *QdrantDeps,
	mediaPG *sql.DB,
	mediaConfig mediaexec.ExecutionConfig,
) (*ProcessBundle, error) {
	_ = ctx

	if qd == nil {
		return nil, fmt.Errorf("BuildProcessBundle: qdrantDeps is nil (QDRANT-002 PR8 fail-closed; composition forgot to call buildQdrantDeps first?)")
	}

	mediaProcessor, err := wireMediaProcessor(outbox, repos, dbs, cfg, publisher, log, mediaPG, mediaConfig)
	if err != nil {
		return nil, err
	}

	vlmClient := newVLMClient(cfg)

	// The indexing service is a producer of media_assets mutations (embedding
	// and index-state sidecars). Bind it to the canonical committer before the
	// process bundle becomes visible; it must never fall back to db.Exec.
	if qd.ClipIndexerService != nil && outbox != nil && outbox.CanonicalWriter != nil {
		mutationCommitter, ok := outbox.CanonicalWriter.(persistence.AssetMutationCommitter)
		if !ok || mutationCommitter == nil {
			return nil, fmt.Errorf("BuildProcessBundle: canonical writer does not implement AssetMutationCommitter")
		}
		qd.ClipIndexerService.SetAssetMutationCommitter(mutationCommitter)
	}

	processQdrant := processwiring.BuildQdrantSubsystems(qd, cfg, log)

	return &ProcessBundle{
		ProcessQdrantBundle: processQdrant,
		MediaProcessor:      mediaProcessor,
		ClipIndexerService:  qd.ClipIndexerService,
		VLMClient:           vlmClient,
	}, nil
}

func buildQdrantDeps(ctx context.Context, cfg *config.Config, dbs *Databases, repos *RepoBundle, log *zap.Logger) (*QdrantDeps, error) {
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
				BaseURL:             cfg.Qdrant.BaseURL,
				APIKey:              cfg.Qdrant.APIKey,
				Timeout:             cfg.Qdrant.Timeout,
				ProjectionRetention: cfg.Qdrant.ProjectionRetention,
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
		if langsCSV, csvErr := BuildMultilingualLanguageCSV(ActiveMultilingualConfig(cfg), nil); csvErr == nil {
			runtime.Mapper.SetIndexLanguages(langsCSV)
		} else {
			return nil, fmt.Errorf("buildQdrantDeps: index languages: %w", csvErr)
		}
		// Wire TextTrackRepository so the PayloadMapper can populate
		// SearchTextInput.TextTracks at search-text construction time.
		// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 5 (July 2026): the
		// TextTrackRepository is now sourced from the canonical
		// RepoBundle.TextTrackRepo (wired in BuildRepoBundle). The
		// pre-PR local construction is removed so every consumer
		// (BuildTextTrackBundle, BuildRepoBundle, this Qdrant
		// PayloadMapper, the TextTrackResolver in buildDomainMediaServices)
		// shares the SAME instance.
		if repos != nil && repos.TextTrackRepo != nil {
			runtime.Mapper.SetTextTrackQuerier(repos.TextTrackRepo)
		} else {
			log.Warn("buildQdrantDeps: TextTrackRepository nil in RepoBundle; multilingual TextTracks disabled in search text")
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
		// MEDIA LEGACY READ-PLANE DEMOLITION (2026-09-20): the Qdrant media write
		// plane (clipindexer VectorStoreIndexer + projection-sequence advancer) is
		// RETIRED. QdrantRuntime is still constructed for the non-media
		// IndexDeleteHandler path (QdrantDeleter below); it is no longer rebound as
		// the clipindexer vector authority because clipindexer is a pure
		// PostgreSQL-outbox delegator.
		log.Info("MEDIA-CUTOVER: Qdrant media write plane retired — QdrantRuntime retained for the non-media IndexDeleteHandler path",
			zap.String("runtime_alias", runtime.Schema.RuntimeAlias))
	} else {
		log.Info("QDRANT-003: Qdrant disabled — no QdrantRuntime wired (buildQdrantDeps pre-phase)")
	}

	qd := &QdrantDeps{
		Runtime:            runtime,
		ClipIndexerService: clipIndexerService,
	}
	// PR 4: VectorPointDeleter port satisfied directly by runtime.Writer
	// (compile-time assertion in internal/platform/qdrant/index_writer.go
	// pins the conformance: `_ jobsoutbox.VectorPointDeleter = (*qdrant.IndexWriter)(nil)`).
	// No runtime `any` cast needed.
	if runtime != nil {
		qd.QdrantDeleter = runtime.Writer
	}
	return qd, nil
}

// validateQdrantIndexerCompatibility is the canonical composition-root
// fail-closed gate for the Qdrant + ClipIndexer configuration pair
// (PR-QDRANT-CONFIG-MISMATCH-GATE, July 2026). It returns a non-nil
// error iff the configuration is INCOMPATIBLE for end-to-end indexing
// (outbox asset.index.requested -> IndexingHandler -> IndexClip ->
// Qdrant.Writer). Compatible configurations return nil silently so the
// per-call site can proceed to construct its Qdrant-stack bundle.
//
// Configurations that fail-closed (with the verbatim substring contract):
//
//	(a) nil cfg                                 -> "cfg is nil"
//	(b) Qdrant.Enabled=true AND ClipIndexer.Enabled=false
//	    -> "QdrantEnabled=true ... ClipIndexerEnabled=false ...
//	        QDRANT-CHAIN-VERIFY-2026-07-04 P0 ...
//	        VELOX_FEATURE_CLIP_INDEXER_ENABLED=true ...
//	        VELOX_FEATURE_QDRANT_ENABLED=false"
//	(c) ClipIndexer.Enabled=true AND Qdrant.Enabled=false
//	    -> "ClipIndexerEnabled=true ... QdrantEnabled=false ...
//	        QDRANT-CHAIN-VERIFY-2026-07-04 P0 ...
//	        VELOX_FEATURE_CLIP_INDEXER_ENABLED=false ...
//	        VELOX_FEATURE_QDRANT_ENABLED=true"
//
// Configurations that pass silently (godlike/07 both-enabled OR both-disabled):
//
//	(d) Qdrant.Enabled=false AND ClipIndexer.Enabled=false (the canonical
//	    operator-disabled zero-state, allowed by design)
//	(e) Qdrant.Enabled=true AND ClipIndexer.Enabled=true (the canonical
//	    happy path: every end-to-end indexing hop is wired)
//
// godlike/07 no-fake-availability: case (b) is the CRITICAL RED POINT
// discovered by the QDRANT-CHAIN-VERIFY-2026-07-04 audit. With this gate
// in place, a misconfigured deployment fails at BOOT (loud + actionable)
// rather than at the first asset.index.requested event (silent + false
// success). The godlike/07 fail-fast-at-boot-vs-fail-slow-at-first-/run
// principle is preserved.
//
// godlike/06 SSOT one-owner-per-fact: this helper is the SOLE canonical
// owner of the Qdrant+ClipIndexer compatibility check. The 4 wire sites
// (build_process_qdrant/buildQdrantDeps + build_bundles_process/BuildOutboxBundle
// + wire_services/WireServices + build_bundles_core/buildHealthService)
// all delegate to this helper by exactly ONE call line at the top of
// their function body. Future Qdrant+ClipIndexer compatibility policy
// changes are made HERE; the wire sites pick them up automatically.
//
// Escape hatches (documented in the returned error message):
//
//	Configuration			Fix env-var
//	-------------------------	----------------------------------------
//	Qdrant=true, ClipIndexer=false	set VELOX_FEATURE_CLIP_INDEXER_ENABLED=true
//	OR				set VELOX_FEATURE_QDRANT_ENABLED=false
//	ClipIndexer=true, Qdrant=false	set VELOX_FEATURE_QDRANT_ENABLED=true
//	OR				set VELOX_FEATURE_CLIP_INDEXER_ENABLED=false
//
// The disable-via-env-var escape means operators who don't need the
// Qdrant vector store or who don't need the AI sidecar can choose either
// direction without spindle-and-break boot.
func validateQdrantIndexerCompatibility(cfg *config.Config) error {
	if cfg == nil {
		// godlike/06 SSOT surface: the helper itself must fail loudly when
		// invoked with nil cfg (defensive coverage for callers that have
		// not yet initialized the boot-time config struct). The Wire* callers
		// (build_process_qdrant/buildQdrantDeps, build_bundles_process/
		// BuildOutboxBundle, wire_services/WireServices, build_bundles_core/
		// buildHealthService) all dereference cfg for other reads, so this
		// nil-check is the canonical "cfg is nil" surface.
		return fmt.Errorf("validateQdrantIndexerCompatibility: cfg is nil (PR-QDRANT-CONFIG-MISMATCH-GATE fail-closed cannot evaluate Qdrant+ClipIndexer compatibility)")
	}

	// Direction B (the RED POINT): Qdrant enabled, ClipIndexer disabled.
	// IndexClip short-circuits ("clipindexer disabled, skipping") and the
	// outbox marks asset.index.requested as COMPLETED without writing to
	// Qdrant. Operators seeing this in boot logs must choose one of the
	// two escape hatches listed in the message.
	// Qdrant and the indexer are independent  When the indexer
	// is off, its event handler returns the typed retry sentinel; startup must
	// remain available for script/video workflows that do not need indexing.

	// Direction A (pre-existing inline check, July 2026): ClipIndexer
	// enabled, Qdrant disabled. UpsertVectorStore would no-op (no target
	// store) and the clipindexer would report INDEXED despite no Qdrant
	// write — same false-success class as Direction B but on the
	// sidecar->vector channel rather than the outbox vector-write.
	// With Qdrant disabled the indexer is allowed to enter registry-only /
	// disabled-projection mode. It must never report INDEXED without a vector;
	// the clipindexer service enforces that invariant at event time.

	// Dimension equality is not enough to prove that the runtime query
	// embedder and the indexed vectors share a vector space. The schema is
	// the single contract owner; reject a model drift before Qdrant is used.
	if cfg.Qdrant.Enabled {
		textSpec := qdrantschema.DefaultV3Schema().GetDense("text")
		if textSpec == nil || cfg.External.OllamaEmbedModel != textSpec.Model {
			want := "<missing>"
			if textSpec != nil {
				want = textSpec.Model
			}
			return fmt.Errorf("QDRANT_EMBEDDING_CONTRACT_MISMATCH: collection_model=%s runtime_model=%s", want, cfg.External.OllamaEmbedModel)
		}
	}

	return nil
}
