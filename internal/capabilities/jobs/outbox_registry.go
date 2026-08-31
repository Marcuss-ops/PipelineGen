// Package outboxhandlers wires concrete handlers into an
// outboxevents.HandlerRegistry. Each handler is responsible for one
// event type (registered by EventType()).
//
// Conventions (Operational Readiness PR, 2026-06-20):
//   - Real handlers (workflow_step_*, asset.index.requested, delivery,
//     asset.metadata_export, provider.sync) DO useful work. They
//     parse + validate the v1 envelope strictly, dispatch to the
//     canonical long-lived service (jobs.Service, drive upload
//     pipeline, filesystem writer), emit a structured audit log, and
//     return early on terminal failures so the outbox pool marks them
//     dead-letter rather than spinning.
//   - IndexingHandler, MetadataExportHandler, DeliveryHandler,
//     ProviderSyncHandler accept structured credentials at construction
//     time (HMAC secrets, jobs.Service) and never reach back into the
//     app config — partial wiring is "test-only": nil HMAC without
//     insecureDev forces the delivery handler to refuse every event.
//
// All handlers MUST be safe for concurrent invocation. The outbox
// worker pool calls them from N goroutines; the handlers share a
// *sql.DB and a *http.Client (both documented as safe for concurrent
// use) and a zap.Logger (also safe).
package jobs

import (
	"database/sql"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/finalize"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpclient"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// Deps bundles the dependencies consumed by the outbox event handlers.
// Each field is optional unless required by RegisterCoreHandlers; a nil
// optional field means the corresponding optional handler is skipped.
//
// DB: *sql.DB backing store. Required for the DeliveryHandler
// (delivery_log writes). Also feeds the ProviderSyncHandler fallback
// paths when jobs is nil. The MetadataExportHandler (Step 2, June
// 2026) no longer reaches through Deps.DB — the composition root
// constructs the typed-port adapter (metadataexport.NewSQLiteAdapter)
// and the FileWriter adapter (internal/platform/filesystem/
// metadataexport), then passes the pre-built handler to
// RegisterOptionalHandlers via the metadataExportHandler arg.
//
// HTTPClient: httpclient.Client used by DeliveryHandler for outbound POSTs.
// PR-REFACTOR-P0-IO-BINDER-HTTP (July 2026): the field is now the
// canonical narrow port (Do/Post/Get) rather than a direct *http.Client
// so the application layer no longer depends on net/http directly.
// Production concrete is *httpclient.DefaultClient; defaults to a
// 30s-timeout default client when nil.
//
// MetadataDir: REMOVED (Step 2, June 2026). override MetadataDir via
// the pre-built handler's HandlerDeps.OutputDir; the composition root
// reads `cfg.Storage.FullPath("asset_metadata")` and stamps it onto
// the HandlerDeps at wire time.
//
// HMACSecrets: rotated keys (current first, previous second). Required
// for ProductionDeliveryHandler to wire real outbound signing. nil +
// insecureDev=false causes the registration to SKIP the delivery
// handler (no permanent stub: a missing secret registers a loud
// "refuse-everything" handler).
//
// InsecureDev: boolean flag for VELOX_ALLOW_INSECURE_DEV=true. When
// true the DeliveryHandler emits unsigned POSTs with an unmistakable
// warning per call. Never meant for production.
//
// Jobs: JobsEnqueuer for the ProviderSyncHandler (real dispatch onto
// jobs.Service for drive|youtube). nil → drive|youtube events
// fail-open as retryable errors so the outbox pool retries — no
// silent ack.
//
//	// VectorPointDeleter: outbox.VectorPointDeleter for the
//
// IndexDeleteHandler (real DELETE-points onto Qdrant via the
// canonical *qdrant.IndexWriter from QdrantRuntime.Writer).
// nil → IndexDeleteHandler is skipped; events then dead-letter
// with "no handler for event type X" in the pool's pool log.
//
// AssetDeleter: AssetDeleter for the IndexDeleteHandler (real
// media_assets soft-delete via *assets.ClipsRepository). nil →
// IndexDeleteHandler is skipped (same effect as VectorPointDeleter nil).
//
// SourceVersionQuerier: SourceVersionQuerier for the IndexingHandler
// pre-flight supersede gate (real media_assets source_version via
// *assets.ClipsRepository.SourceVersionFor, which delegates to the
// canonical SQL helper in
// internal/platform/sqlite/assets/source_version.go).
// PR 11 follow-up (June 2026) replaced the legacy AssetSourceChecker
// port — both the producer-side (cmd/admin/reconcile_qdrant.go) and
// consumer-side (this handler) priority chains now share that single
// function so future drift is structurally impossible. nil →
// IndexingHandler is wired WITHOUT the source_version supersede gate.
//
// VoiceoverCleanupDriver: VoiceoverCleanupDriver for the
// VoiceoverCleanupHandler (P0.7 Wave 21 Step 10/12, June 2026) —
// consumes voiceover.cleanup.requested events durably emitted from
// voiceover.finalizeStage's caller-owned tx and translates them
// into Drive file delete + local file remove side-effects.
// Production concrete is drive.Admin (structurally satisfies
// InfraDeps groups the infrastructure / configuration knobs so Deps
// stays under the archcheck 8-field cap.
type InfraDeps struct {
	DB                 *sql.DB
	HTTPClient         httpclient.Client
	HMACSecrets        [][]byte
	InsecureDev        bool
	DeliveryOperations DeliveryOperation
}

// JobDeps groups the job + cleanup ports so Deps stays under the
// archcheck 8-field cap.
type JobDeps struct {
	Jobs                   JobsEnqueuer
	VectorPointDeleter     VectorPointDeleter
	AssetDeleter           AssetDeleter
	SourceVersionQuerier   SourceVersionQuerier
	VoiceoverCleanupDriver VoiceoverCleanupDriver
	// BindingIndexer + BindingConceptRepo + BindingRepo wire the
	// optional binding.index.requested handler. All three are nil
	// in environments that do not use the mediamemory capability;
	// when ALL three are non-nil the handler is registered.
	BindingIndexer     BindingIndexer
	BindingConceptRepo BindingConceptRepository
	BindingRepo        mediamemory.BindingRepository
}

// DriveDeleteDeps bundles the 4 narrow ports DriveDeleteHandler
// depends on so Deps stays under the archcheck 8-field cap.
type DriveDeleteDeps struct {
	// Blocco 3.1 commit 2/3 (June 2026) — deletion state machine.
	// Each field is optional; their presence registers
	// DriveDeleteHandler in RegisterOptionalHandlers. Production
	// wires all 4 from BuildOutboxBundle (internal/app/
	// build_bundles_process.go): ClipsLifecycleStateReader and
	// ClipsLifecycleStateWriter are the SAME *assets.ClipsRepository
	// instance, DriveDeleter is drive.FileLifecycle assigned
	// directly (structural conformance — the port is Trash + Delete,
	// drive.FileLifecycle is a method-set superset, no wrapper
	// needed), and StateAdvancer wraps *outbox.Dispatcher.
	DrivePatchLifecycle  LifecycleStateReader
	DrivePatchLifecycleW ClipsLifecycleStateWriter
	DrivePatchStateAdv   StateAdvancer
	DriveDeleteHandler   DriveDeleter
}

// VoiceoverCleanupDriver via its DeleteFile method — same surface,
// same instance, no wrapper needed). nil → VoiceoverCleanupHandler
// registered WITHOUT Drive delete capability (test-path-only; the
// local file removal branch still runs because os.Remove is
// stdlib, no port ceremony needed).
type Deps struct {
	Infra       InfraDeps
	Jobs        JobDeps
	DriveDelete DriveDeleteDeps
}

// IndexClipper is declared in indexing.go (canonical owner) — do NOT
// redeclare here.

// RegisterCoreHandlers wires handlers that MUST be present when
// cfg.Qdrant.Enabled is true (verdict Qdrant section #5, PR 3
// fix/qdrant-outbox-fail-closed). Missing any mandatory dep returns a
// typed error so BuildOutboxBundle aborts boot instead of warning.
//
//		Mandatory deps when cfg.Qdrant.Enabled:
//
//	  - indexer (IndexClipper; production concrete is *clipindexer.Service).
//	  - deps.Jobs.SourceVersionQuerier (production concrete is
//	    *assets.ClipsRepository — IndexingHandler source-version
//	    supersede gate cannot run without it).
//	  - deps.Jobs.VectorPointDeleter (production concrete is
//	    *qdrant.IndexWriter from QdrantRuntime.Writer — PR 4
//	    consolidated the previous QdrantDeleter type into this single
//	    outbox.VectorPointDeleter port; IndexDeleteHandler cannot
//	    issue Qdrant DELETE-points without it).
//	  - deps.Jobs.AssetDeleter (production concrete is *assets.ClipsRepository
//	    — IndexDeleteHandler cannot tombstone the SQLite row without
//	    it).
//
// Operators reading the error get the literal name of the missing dep
// so a grep of the boot log finds it instantly. The handler list is
// NOT registered on failure so the caller can retry without
// accumulating duplicates.
//
// Returns: nil on success, an error naming the first missing
// dependency otherwise.
func RegisterCoreHandlers(registry *outboxevents.HandlerRegistry, log *zap.Logger, indexer IndexClipper, deps *Deps) error {
	if registry == nil {
		return fmtError("outbox RegisterCoreHandlers: registry is nil")
	}
	if indexer == nil {
		return fmtError("outbox RegisterCoreHandlers: IndexingHandler mandatory dep missing (indexer=nil; buildQdrantDeps did not construct a ClipIndexer service)")
	}
	if deps == nil {
		return fmtError("outbox RegisterCoreHandlers: deps is nil (composition omitted outbox.Deps — wiring bug)")
	}
	if deps.Jobs.SourceVersionQuerier == nil {
		return fmtError("outbox RegisterCoreHandlers: IndexingHandler source-version gate cannot run (SourceVersionQuerier=nil; ClipsRepo was nil at BuildOutboxBundle call site despite cfg.Qdrant.Enabled=true)")
	}
	if deps.Jobs.VectorPointDeleter == nil {
		return fmtError("outbox RegisterCoreHandlers: IndexDeleteHandler cannot run (VectorPointDeleter=nil; Qdrant enabled but no IndexWriter built from buildQdrantDeps)")
	}
	if deps.Jobs.AssetDeleter == nil {
		// Reachable root cause: ClipsRepo was nil at the BuildOutboxBundle
		// call site despite cfg.Qdrant.Enabled=true. The compound message
		// previously listed "OR Qdrant.Enabled=false" which is unreachable
		// because BuildOutboxBundle gates RegisterCoreHandlers behind
		// `if cfg.Qdrant.Enabled`.
		return fmtError("outbox RegisterCoreHandlers: IndexDeleteHandler cannot run (AssetDeleter=nil; ClipsRepo was nil at BuildOutboxBundle call site despite cfg.Qdrant.Enabled=true)")
	}
	core := []outboxevents.Handler{
		buildIndexingHandler(indexer, deps.Jobs.SourceVersionQuerier, log),
		NewIndexDeleteHandler(log, deps.Jobs.VectorPointDeleter, deps.Jobs.AssetDeleter),
	}
	for _, h := range core {
		if err := registry.Register(h); err != nil {
			return err
		}
	}
	log.Info("outbox core handlers registered (fail-closed contract when cfg.Qdrant.Enabled)",
		zap.Int("registered", len(core)),
	)
	return nil
}

// RegisterOptionalHandlers wires handlers that tolerate missing
// dependencies (best-effort). Missing deps are logged at Info and
// skipped — registration never aborts on these.
//
// Optional handlers today:
//
//   - WorkflowStepCompletedHandler / WorkflowStepFailedHandler (no
//     deps; always wired).
//   - AssetPublishedHandler (logger only; informational receipt; never
//     writes Qdrant or media_assets.index_state).
//   - MetadataExportHandler (pre-built by composition root via the
//     metadataexport package's typed-port adapter; passed in via
//     metadataExportHandler; nil → skipped).
//   - DeliveryHandler (deps.Infra.HTTPClient required; plus HMACSecrets
//     OR InsecureDev). Explicit reference/materialization ports are supplied
//     through DeliveryOperations; absent ports make explicit operations fail
//     closed while legacy envelopes remain source-compatible.
//   - ProviderSyncHandler (only Jobs — nil Jobs → drive|youtube events
//     fail with retryable error inside the handler, never silently
//     ack).
//
// Step 2 (June 2026) signature change: metadataExportHandler replaces
// the pre-Step-2 deps.Infra.DB+deps.MetadataDir construction. The composition
// root constructs the typed-port adapter (metadataexport.NewSQLiteAdapter
// at internal/platform/sqlite/metadataexport/ +
// FileWriter at internal/platform/filesystem/metadataexport/) and
// stamps HandlerDeps{Resolver, Writer, OutputDir} onto the handler at
// wire time. The application package no longer touches *sql.DB or os.
//
// Returns: the first registration error. The handler list is NOT
// registered on failure; this keeps semantics compatible with
// RegisterCoreHandlers.
func RegisterOptionalHandlers(registry *outboxevents.HandlerRegistry, log *zap.Logger, deps *Deps, metadataExportHandler outboxevents.Handler) error {
	if registry == nil {
		return fmtError("outbox RegisterOptionalHandlers: registry is nil")
	}
	optional := []outboxevents.Handler{
		finalize.NewWorkflowStepCompletedHandler(log),
		finalize.NewWorkflowStepFailedHandler(log, nil),
		NewAssetPublishedHandler(log),
	}
	if metadataExportHandler != nil {
		optional = append(optional, metadataExportHandler)
	}
	if deps != nil {
		// The delivery handler is only registered when the composition
		// root injects a concrete httpclient.Client. There is no application-
		// layer fallback that builds an HTTP client; a missing client
		// means the webhook delivery path is intentionally skipped
		// rather than silently depending on an infrastructure driver.
		if deps.Infra.HTTPClient != nil && (len(deps.Infra.HMACSecrets) > 0 || deps.Infra.InsecureDev) {
			optional = append(optional, NewDeliveryHandlerWithOperations(log, deps.Infra.HTTPClient, deps.Infra.DB, deps.Infra.HMACSecrets, deps.Infra.InsecureDev, deps.Infra.DeliveryOperations))
		}
	}
	optional = append(optional, NewProviderSyncHandler(log, depsOrNil(deps).Jobs.Jobs))

	// binding.index.requested (Phase 1.2+): when mediamemory is wired,
	// reindex the parent concept in Qdrant after every binding
	// mutation.
	if deps != nil &&
		deps.Jobs.BindingIndexer != nil &&
		deps.Jobs.BindingConceptRepo != nil &&
		deps.Jobs.BindingRepo != nil {
		optional = append(optional, NewBindingIndexingHandler(
			deps.Jobs.BindingIndexer,
			deps.Jobs.BindingConceptRepo,
			log,
		))
	} else {
		log.Info("outbox RegisterOptionalHandlers: BindingIndexingHandler skipped (mediamemory not wired)")
	}

	// P0.7 Wave 21 Step 10/12 (June 2026): voiceover orphan cleanup
	// handler. Registered unconditionally — the handler itself is
	// nil-safe (driver == nil → log+skip the Drive delete branch,
	// still runs local file removal via stdlib os.Remove). This
	// keeps the handler's leak-free contract alive on every
	// deployment regardless of whether a Drive admin is wired at
	// composition time.
	optional = append(optional, NewVoiceoverCleanupHandler(depsOrNil(deps).Jobs.VoiceoverCleanupDriver, log))

	// Blocco 3.1 commit 2/3 (June 2026) — DriveDeleteHandler
	// (asset.drive.delete_requested.v1). Registered only when
	// ALL four narrow ports are wired, otherwise skipped at Info
	// (matching the optional-handler best-effort contract — a
	// partial Drive wiring in dev environments that don't have a
	// Qdrant cluster should not abort boot).
	if deps != nil &&
		deps.DriveDelete.DrivePatchLifecycle != nil &&
		deps.DriveDelete.DrivePatchLifecycleW != nil &&
		deps.DriveDelete.DrivePatchStateAdv != nil &&
		deps.DriveDelete.DriveDeleteHandler != nil {
		optional = append(optional, NewDriveDeleteHandler(
			log,
			deps.DriveDelete.DriveDeleteHandler,
			deps.DriveDelete.DrivePatchLifecycle,
			deps.DriveDelete.DrivePatchLifecycleW,
			deps.DriveDelete.DrivePatchStateAdv,
		))
	}
	for _, h := range optional {
		if err := registry.Register(h); err != nil {
			return err
		}
	}
	log.Info("outbox optional handlers registered",
		zap.Int("registered", len(optional)),
		zap.Bool("asset_published_informational_wired", true),
		zap.Bool("metadata_export_wired", metadataExportHandler != nil),
		zap.Bool("delivery_wired", deps != nil && deps.Infra.HTTPClient != nil && (len(deps.Infra.HMACSecrets) > 0 || deps.Infra.InsecureDev)),
		zap.Bool("provider_sync_jobs_wired", deps != nil && deps.Jobs.Jobs != nil),
		zap.Bool("voiceover_cleanup_driver_wired", deps != nil && deps.Jobs.VoiceoverCleanupDriver != nil),
		zap.Bool("drive_delete_wired", deps != nil &&
			deps.DriveDelete.DrivePatchLifecycle != nil &&
			deps.DriveDelete.DrivePatchLifecycleW != nil &&
			deps.DriveDelete.DrivePatchStateAdv != nil &&
			deps.DriveDelete.DriveDeleteHandler != nil),
	)
	return nil
}

func depsOrNil(d *Deps) *Deps {
	if d == nil {
		return &Deps{}
	}
	return d
}

// fmtError is a tiny helper that keeps imports tidy (no fmt package at
// the top of this file just for one error wrap).
func fmtError(msg string) error { return &registryError{msg: msg} }

type registryError struct{ msg string }

func (e *registryError) Error() string { return e.msg }

// buildIndexingHandler is the canonical constructor for the
// IndexingHandler with auto-wired state-updater (PR-QDRANT-INDEXCLIP-GUARD,
// July 2026).
//
// godlike/06 SSOT: the production IndexClipper concrete
// (*clipindexer.Service) is the SAME instance that satisfies
// clipindexer.IndexerStateUpdater (compile-time pinned at
// internal/capabilities/indexing/clipindexer/state_writer.go:
// `var _ IndexerStateUpdater = (*Service)(nil)`). When RegisterCoreHandlers
// RegisterCoreHandlers receives a *Service from the composition root; the
// type-assertion below auto-wires the IndexerStateUpdater port so
// the ErrIndexClipDisabledButEventRequested branch can stamp
// INDEXING_SKIPPED_NO_INDEXER on media_assets without a separate
// Deps field.
//
// godlike/07 minimum-blast-radius: test fakes that satisfy
// IndexClipper but NOT IndexerStateUpdater (e.g. mockIndexClipper in
// the test files) get a *IndexingHandler with stateUpdater=nil —
// the sentinel-detect branch still routes to retry correctly (the
// err is returned regardless of state-update success per
// godlike/07 fail-closed), but the state-update side-effect is
// skipped. The handler logs a Warn line so the production wire
// path's missing-port surface is auditable.
//
// Returns the wired handler for inclusion in the registry list.
func buildIndexingHandler(indexer IndexClipper, sourceQuerier SourceVersionQuerier, log *zap.Logger) *IndexingHandler {
	h := NewIndexingHandler(indexer, sourceQuerier, log)
	if su, ok := indexer.(IndexerStateUpdater); ok {
		h.WithStateUpdater(su)
		return h
	}
	log.Info("outbox buildIndexingHandler: indexer does not implement IndexerStateUpdater; sentinel-driven state-write will be skipped (retry path still fires)",
		zap.String("indexer_type", fmt.Sprintf("%T", indexer)),
	)
	return h
}
