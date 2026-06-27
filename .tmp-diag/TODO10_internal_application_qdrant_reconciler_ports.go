// Package reconciler — canonical ports + sentinel errors (QDRANT-006 TODO 10,
// PipelineGen, June 2026).
//
// QDRANT-005 (TODO 8, June 2026) fail-closed contract settled the SCAN side
// of the reconciler: 5 AND-ed CompleteScan gates, dry-run pass-through,
// Repairer=nil fail-fast. That contract was self-contained inside the
// reconciler (no external collaborators beyond the per-call Scroller +
// AssetIDsLister).
//
// QDRANT-006 (TODO 9) drifted the producer side: the orchestrator routes BOTH
// missing-from-Qdrant (reindex-emit) AND Qdrant-orphans (delete-emit) through
// the canonical outbox dispatcher. The dispatcher already exposes
// `EnqueueAndReconcileIndex` / `EnqueueAndReconcileDelete` for those two
// flows, but the reconciler.Service struct had no field-level reference to
// either — both were hidden behind the existing `Repairer` interface.
//
// TODO 10 closes the layering gap by promoting the TWO canonical
// collaborators that the apply phase MUST talk to into first-class Service
// ports:
//
//  1. Outbox          — emits reconcile_reindex events for missing IDs.
//     Slim view of *outbox.Dispatcher (one method).
//  2. PayloadMutator  — deletes orphan Qdrant points by asset ID.
//     Slim view of *qdrant.IndexWriter (one method).
//
// In a perfect world both ports would always be wired in production. They
// MUST be wired when `DryRun=false` AND drift is present — drift means one of
// MissingCount > 0 / OrphanCount > 0, and each direction has its OWN port
// dependency. A nil port in apply mode is now an explicit refuse-and-error,
// not a silent noop: `reconciler.Repairer.Apply` returning nil used to be
// indistinguishable from "ports were nil so nothing happened" — an operator
// dashboard saw `report.Applied = false` with no actionable signal. The two
// sentinels below make the failure mode loud (errors.Is-testable, 409-mappable
// upstream).
//
// Dry-run mode (DryRun=true) tolerates nil ports because no repair phase
// runs. That is the only context-aware exception. Calling Apply directly
// with nil ports (future API) is a programming error and ALSO returns the
// sentinel — the dry-run exemption is Reconcile-flow-only, not Apply-direct
// (we keep this asymmetry because the operator-driven dry-run is the
// preview-the-drift flow documented in AGENTS.md §QDRANT-005).
package reconciler

import (
	"context"
	"errors"
)

// ErrOutboxRequired is the sentinel returned when the apply phase requires
// the Outbox port (re-emit of missing-from-Qdrant IDs) but it is nil. Tests
// assert errors.Is(err, ErrOutboxRequired); callers surface it as 409
// Conflict or a structured "apply blocked (config)" log line.
//
// Message intentionally mirrors QDRANT-006 TODO 10 spec verbatim:
// "outbox port required for reconcile apply — refusing silent noop".
// Dash in the message is the canonical ASCII form; UI / dashboard
// presentations normalise it to ": " before display.
var ErrOutboxRequired = errors.New("outbox port required for reconcile apply — refusing silent noop")

// ErrPayloadMutatorRequired mirrors ErrOutboxRequired for the
// orphan-cleanup direction. Same error-message shape; the "payload" word
// names the port and stays close to the type name so log-grep tooling
// matches.
var ErrPayloadMutatorRequired = errors.New("payload mutator port required for reconcile apply — refusing silent noop")

// Outbox is the canonical port the reconciler.Service consumes to emit
// reconcile_reindex outbox events on missing-from-Qdrant IDs. The
// production adapter is *internal/infrastructure/database/sqlite/outbox.Dispatcher;
// the unit-test adapter is a stub that records enqueue calls and an optional
// forced-error shape for the "all repairs fail" test path (TODO 10 spec
// scenario 4).
//
// Method signature mirrors `outbox.Dispatcher.EnqueueAndReconcileIndex`
// exactly so production composition wires the dispatcher directly with no
// shim layer; compile-time pinning of the production adapter contract is
// the composition root's responsibility (where the Dispatcher is
// constructed and bound via ServiceDeps), not a reconciler-package concern
// (avoids any downward import from the application layer to the
// infrastructure layer).
type Outbox interface {
	EnqueueAndReconcileIndex(ctx context.Context, assetID, targetSchema, contentHash, reason string) error
}

// PayloadMutator is the canonical port the reconciler.Service consumes to
// delete Qdrant points for orphan (asset IDs no longer in SQLite) repair.
// The production adapter is *internal/infrastructure/qdrant.IndexWriter;
// the unit-test adapter is a stub that records the IDs requested for delete
// and an optional forced-error shape for the "all repairs fail" test path.
//
// Method signature mirrors
// `(internal/infrastructure/qdrant.IndexWriter).DeletePoints` exactly so
// production composition wires the writer directly with no shim layer;
// compile-time pinning of the production adapter contract is the
// composition root's responsibility (where the IndexWriter is constructed
// and bound via ServiceDeps), not a reconciler-package concern (avoids
// any downward import from the application layer to the infrastructure
// layer).
type PayloadMutator interface {
	DeletePoints(ctx context.Context, ids []string) error
}
