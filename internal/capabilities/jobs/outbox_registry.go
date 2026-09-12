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

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/finalize"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediamemory"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/httpclient"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
)

// Deps bundles the dependencies consumed by the outbox event handlers.
// Each field is optional unless required by a handler registration; a nil
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
// MEDIA-CUTOVER (2026-09-12): the SourceVersionQuerier field is REMOVED
// with the retired IndexingHandler family — the supersede gate it fed
// consumed SQLite-outbox asset.index.requested events, a pipeline with
// zero production consumers since the PostgreSQL media cutover.
type JobDeps struct {
	Jobs                   JobsEnqueuer
	VectorPointDeleter     VectorPointDeleter
	AssetDeleter           AssetDeleter
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

// MEDIA-CUTOVER (September 2026): the retired `RegisterCoreHandlers` media/
// Qdrant surface is DELETED. The media projection plane is the PostgreSQL
// pgvector `PostgresIndexWorker` (asset.index.requested → embed →
// media_embeddings → INDEXED); the SQLite outbox registers NO media/Qdrant
// projection handler in ANY mode, and the composition root's
// `assertSingleMediaIndexOwner` fails the boot if one is registered. A stray media event
// in the SQLite outbox dead-letters loudly instead of projecting into
// Qdrant. See docs/migrations/BASELINE_PLAN.md.

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
// registered on failure; this keeps semantics compatible across the
// handler families.
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
