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
//	DB            : *sql.DB backing store. Required for the
//	                                 MetadataExportHandler (snapshot reads
//	                                 across media_assets / voiceover /
////	                                 image / delivery_log) and for the
//	                                 DeliveryHandler (delivery_log
//	                                 writes). Also feeds the
//	                                 ProviderSyncHandler fallback paths
//	                                 when jobs is nil.
//
//	HTTPClient    : *http.Client used by DeliveryHandler for outbound
//	                  POSTs. Defaults to 30s-timeout client if nil.
//
//	MetadataDir   : absolute path to the sidecar JSON output directory
//	                  (typically cfg.Storage.FullPath("asset_metadata")).
//	                  Required for MetadataExportHandler to be wired.
//
//	HMACSecrets   : rotated keys (current first, previous second).
//	                  Required for ProductionDeliveryHandler to wire
//	                  real outbound signing. nil + insecureDev=false
//	                  causes the registration to SKIP the delivery
//	                  handler (no permanent stub: a missing secret
//	                  registers a loud "refuse-everything" handler).
//
//	InsecureDev   : boolean flag for VELOX_ALLOW_INSECURE_DEV=true.
//	                  When true the DeliveryHandler emits unsigned POSTs
//	                  with an unmistakable warning per call. Never meant
//	                  for production.
//
//	Jobs          : JobsEnqueuer for the ProviderSyncHandler (real
//	                  dispatch onto jobs.Service for drive|youtube).
//	                  nil → drive|youtube events fail-open as retryable
//	                  errors so the outbox pool retries — no silent ack.
type Deps struct {
	DB          *sql.DB
	HTTPClient  *http.Client
	MetadataDir string
	HMACSecrets [][]byte
	InsecureDev bool
	Jobs        JobsEnqueuer
}

// IndexClipper is declared in indexing.go (canonical owner) — do NOT
// redeclare here.

// RegisterAll wires the canonical set of handlers into the registry.
//
// Parameters:
//   - registry : the HandlerRegistry to populate.
//   - log      : zap logger — nil-safe (handlers use zap.NewNop on nil).
//   - indexer  : IndexClipper dependency for the IndexingHandler.
//                Pass nil to skip the IndexingHandler (tests, partial
//                wiring). The handler is optional, not mandatory.
//   - deps     : Deps bundle. Each field is optional; the corresponding
//                handler is registered as long as its CRITICAL field is
//                present (DB for metadata_export, HTTPClient or DB for
//                delivery, Jobs for provider_sync's drive|youtube path,
//                HMACSecrets OR InsecureDev for delivery).
//
// Returns: the first registration error (duplicate event type, nil
// handler, empty EventType).
func RegisterAll(registry *outboxevents.HandlerRegistry, log *zap.Logger, indexer IndexClipper, deps *Deps) error {
	if registry == nil {
		return fmtError("outbox RegisterAll: registry is nil")
	}
	realHandlers := []outboxevents.Handler{
		&WorkflowStepCompletedHandler{log: log},
		&WorkflowStepFailedHandler{log: log},
	}

	// IndexClipper-backed IndexingHandler is optional.
	if indexer != nil {
		realHandlers = append(realHandlers, &IndexingHandler{
			indexer: indexer,
			log:     log,
		})
	}

	// The three real outbox handlers. Each is registered if its CRITICAL
	// dependencies are present. We never substitute a stub — a missing
	// dependency just means the handler is skipped (so the outbox pool
	// reports "no handler for event type X" in dead_letter for events
	// that arrive during the wiring window).
	if deps != nil {
		if deps.DB != nil && deps.MetadataDir != "" {
			realHandlers = append(realHandlers, NewMetadataExportHandler(log, deps.DB, deps.MetadataDir))
		}
		// DeliveryHandler wires whenever a DB OR HTTPClient is available
		// so callers opt-in by setting the critical deps; we also gate
		// on HMACSecrets OR InsecureDev to avoid silently shipping
		// unsigned POSTs in production.
		if (deps.HTTPClient != nil || deps.DB != nil) && (len(deps.HMACSecrets) > 0 || deps.InsecureDev) {
			realHandlers = append(realHandlers, NewDeliveryHandler(log, deps.HTTPClient, deps.DB, deps.HMACSecrets, deps.InsecureDev))
		}
	}

	// ProviderSyncHandler needs only the logger + jobs (jobs can be nil:
	// the handler refuses drive|youtube events with a retryable error in
	// that case, never silently acks).
	realHandlers = append(realHandlers, NewProviderSyncHandler(log, depsOrNil(deps).Jobs))

	for _, h := range realHandlers {
		if err := registry.Register(h); err != nil {
			return err
		}
	}

	log.Info("outbox handlers registered (real handlers only — no stubs)",
		zap.Int("real", len(realHandlers)),
		zap.Bool("delivery_wired", deps != nil && (deps.HTTPClient != nil || deps.DB != nil) && (len(deps.HMACSecrets) > 0 || deps.InsecureDev)),
		zap.Bool("metadata_export_wired", deps != nil && deps.DB != nil && deps.MetadataDir != ""),
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
