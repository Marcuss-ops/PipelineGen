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
package outbox

import (
	"database/sql"
	"net/http"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/database/sqlite/outboxevents"
)

// Deps bundles the optional dependencies RegisterAll forwards to the
// real outbox event handlers. Each field is optional; a nil field
// means the corresponding handler is skipped from registration.
//
// DB: *sql.DB backing store. Required for the DeliveryHandler
// (delivery_log writes). Also feeds the ProviderSyncHandler fallback
// paths when jobs is nil. The MetadataExportHandler (Step 2, June
// 2026) no longer reaches through Deps.DB — the composition root
// constructs the typed-port adapter (metadataexport.NewSQLiteAdapter)
// and the FileWriter adapter (internal/infrastructure/files/
// metadataexport), then passes the pre-built handler to
// RegisterOptionalHandlers via the metadataExportHandler arg.
//
// HTTPClient: *http.Client used by DeliveryHandler for outbound POSTs.
// Defaults to 30s-timeout client if nil.
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
//	// VectorPointDeleter: outbox.VectorPointDeleter for the
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
// internal/infrastructure/database/sqlite/assets/source_version.go).
// PR 11 follow-up (June 2026) replaced the legacy AssetSourceChecker
// port — both the producer-side (cmd/admin/reconcile_qdrant.go) and
// consumer-side (this handler) priority chains now share that single
// function so future drift is structurally impossible. nil →
// IndexingHandler is wired WITHOUT the source_version supersede gate.
type Deps struct {
	DB                   *sql.DB
	HTTPClient           *http.Client
	HMACSecrets          [][]byte
	InsecureDev          bool
	Jobs                 JobsEnqueuer
	VectorPointDeleter   VectorPointDeleter
	AssetDeleter         AssetDeleter
	SourceVersionQuerier SourceVersionQuerier
}

// IndexClipper is declared in indexing.go (canonical owner) — do NOT
// redeclare here.

// RegisterAll wires the canonical set of handlers into the registry.
//
// Deprecated: production code MUST call RegisterCoreHandlers (when
// cfg.Qdrant.Enabled) and RegisterOptionalHandlers directly so a
// missing core dep aborts boot rather than producing a runtime
// dead-letter on the first indexed event. RegisterAll is retained as
// a back-compat wrapper for legacy tests that exercise partial wiring
// without fail-closed semantics — pre-PR-3 callers get the same
// best-effort behavior (missing core deps are LOGGED at Info and
// skipped, never returned as an error). See PR 3
// (fix/qdrant-outbox-fail-closed) for the failure-mode context that
// drove the split.
//
// Step 2 (June 2026) signature change: RegisterAll no longer
// constructs MetadataExportHandler. The composition root builds the
// metadataexport.MetadataExportHandler (with typed-port adapter) and
// passes it via metadataExportHandler. Legacy RegisterAll callers
// can pass nil — the handler is then skipped, matching the pre-Step-2
// behaviour when deps.MetadataDir was empty.
//
// Parameters:
//   - registry               : the HandlerRegistry to populate.
//   - log                    : zap logger — nil-safe.
//   - indexer                : IndexClipper dependency for the IndexingHandler.
//   - deps                   : Deps bundle (DB + HTTPClient + ...).
//   - metadataExportHandler  : pre-built metadataexport.MetadataExportHandler
//     (Step 2); nil → MetadataExportHandler skipped.
//
// Returns: the error from RegisterOptionalHandlers (core registration
// errors are swallowed so legacy callers observe the original
// "Warning-only" behaviour).
func RegisterAll(registry *outboxevents.HandlerRegistry, log *zap.Logger, indexer IndexClipper, deps *Deps, metadataExportHandler outboxevents.Handler) error {
	if registry == nil {
		return fmtError("outbox RegisterAll: registry is nil")
	}
	// Best-effort core registration: legacy callers expect no fatal
	// error so missing deps are LOGGED not returned.
	coreHandlers := []outboxevents.Handler{}
	if indexer != nil && deps != nil && deps.SourceVersionQuerier != nil {
		coreHandlers = append(coreHandlers, NewIndexingHandler(indexer, deps.SourceVersionQuerier, log))
	} else {
		log.Info("outbox RegisterAll (legacy): IndexingHandler skipped (missing indexer or SourceVersionQuerier)")
	}
	if deps != nil && deps.VectorPointDeleter != nil && deps.AssetDeleter != nil {
		coreHandlers = append(coreHandlers, NewIndexDeleteHandler(log, deps.VectorPointDeleter, deps.AssetDeleter))
	} else {
		log.Info("outbox RegisterAll (legacy): IndexDeleteHandler skipped (missing VectorPointDeleter or AssetDeleter)")
	}
	for _, h := range coreHandlers {
		if err := registry.Register(h); err != nil {
			return err
		}
	}
	return RegisterOptionalHandlers(registry, log, deps, metadataExportHandler)
}

// RegisterCoreHandlers wires handlers that MUST be present when
// cfg.Qdrant.Enabled is true (verdict Qdrant section #5, PR 3
// fix/qdrant-outbox-fail-closed). Missing any mandatory dep returns a
// typed error so BuildOutboxBundle aborts boot instead of warning.
//
//	Mandatory deps when cfg.Qdrant.Enabled:
//
//   - indexer (IndexClipper; production concrete is *clipindexer.Service).
//   - deps.SourceVersionQuerier (production concrete is
//     *assets.ClipsRepository — IndexingHandler source-version
//     supersede gate cannot run without it).
//   - deps.VectorPointDeleter (production concrete is
//     *qdrant.IndexWriter from QdrantRuntime.Writer — PR 4
//     consolidated the previous QdrantDeleter type into this single
//     outbox.VectorPointDeleter port; IndexDeleteHandler cannot
//     issue Qdrant DELETE-points without it).
//   - deps.AssetDeleter (production concrete is *assets.ClipsRepository
//     — IndexDeleteHandler cannot tombstone the SQLite row without
//     it).
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
	if deps.SourceVersionQuerier == nil {
		return fmtError("outbox RegisterCoreHandlers: IndexingHandler source-version gate cannot run (SourceVersionQuerier=nil; ClipsRepo was nil at BuildOutboxBundle call site despite cfg.Qdrant.Enabled=true)")
	}
	if deps.VectorPointDeleter == nil {
		return fmtError("outbox RegisterCoreHandlers: IndexDeleteHandler cannot run (VectorPointDeleter=nil; Qdrant enabled but no IndexWriter built from buildQdrantDeps)")
	}
	if deps.AssetDeleter == nil {
		// Reachable root cause: ClipsRepo was nil at the BuildOutboxBundle
		// call site despite cfg.Qdrant.Enabled=true. The compound message
		// previously listed "OR Qdrant.Enabled=false" which is unreachable
		// because BuildOutboxBundle gates RegisterCoreHandlers behind
		// `if cfg.Qdrant.Enabled`.
		return fmtError("outbox RegisterCoreHandlers: IndexDeleteHandler cannot run (AssetDeleter=nil; ClipsRepo was nil at BuildOutboxBundle call site despite cfg.Qdrant.Enabled=true)")
	}
	core := []outboxevents.Handler{
		NewIndexingHandler(indexer, deps.SourceVersionQuerier, log),
		NewIndexDeleteHandler(log, deps.VectorPointDeleter, deps.AssetDeleter),
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
//   - MetadataExportHandler (pre-built by composition root via the
//     metadataexport package's typed-port adapter; passed in via
//     metadataExportHandler; nil → skipped).
//   - DeliveryHandler (deps.HTTPClient OR deps.DB; plus HMACSecrets
//     OR InsecureDev).
//   - ProviderSyncHandler (only Jobs — nil Jobs → drive|youtube events
//     fail with retryable error inside the handler, never silently
//     ack).
//
// Step 2 (June 2026) signature change: metadataExportHandler replaces
// the pre-Step-2 deps.DB+deps.MetadataDir construction. The composition
// root constructs the typed-port adapter (metadataexport.NewSQLiteAdapter
// at internal/infrastructure/database/sqlite/metadataexport/ +
// FileWriter at internal/infrastructure/files/metadataexport/) and
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
		&WorkflowStepCompletedHandler{log: log},
		&WorkflowStepFailedHandler{log: log},
	}
	if metadataExportHandler != nil {
		optional = append(optional, metadataExportHandler)
	}
	if deps != nil {
		if (deps.HTTPClient != nil || deps.DB != nil) && (len(deps.HMACSecrets) > 0 || deps.InsecureDev) {
			optional = append(optional, NewDeliveryHandler(log, deps.HTTPClient, deps.DB, deps.HMACSecrets, deps.InsecureDev))
		}
	}
	optional = append(optional, NewProviderSyncHandler(log, depsOrNil(deps).Jobs))
	for _, h := range optional {
		if err := registry.Register(h); err != nil {
			return err
		}
	}
	log.Info("outbox optional handlers registered",
		zap.Int("registered", len(optional)),
		zap.Bool("metadata_export_wired", metadataExportHandler != nil),
		zap.Bool("delivery_wired", deps != nil && (deps.HTTPClient != nil || deps.DB != nil) && (len(deps.HMACSecrets) > 0 || deps.InsecureDev)),
		zap.Bool("provider_sync_jobs_wired", deps != nil && deps.Jobs != nil),
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
