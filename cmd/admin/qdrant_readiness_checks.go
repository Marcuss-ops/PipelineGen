// cmd/admin/qdrant_readiness_checks.go — types + readinessCheck map +
// checkDeliverySigner, the canonical surface for the production-shaped
// readiness gate.
//
// LONG-FILES-DECOMPOSITION-2026-07-06 Band C #1: per-check functions
// extracted to companion files:
//   - SQL checks      → qdrant_readiness_checks_db.go
//   - Production checks → qdrant_readiness_checks_prod.go
//   - Qdrant checks   → qdrant_readiness_checks_qdrant.go
//   - Routes check    → qdrant_readiness_checks_routes.go
//   - Semantic check  → qdrant_readiness_semantic.go (pre-existing)
//
// The orchestrator (qdrantReadiness), CLI entry (runQdrantReadiness),
// bridge (appInitCompositionForReadiness), report type (qdrantReadinessReport),
// runOneCheck, and utility functions stay in qdrant_readiness.go.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"go.uber.org/zap"

	api "github.com/Marcuss-ops/PipelineGen/internal/api"
	searchpkg "github.com/Marcuss-ops/PipelineGen/internal/application/search"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
	outboxevents "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outboxevents"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// checkStatus is the per-check tuple used by all readiness checks.
// Pass=true means the check passed; Err is the diagnostic string for
// human ops (always populated on Pass=false, omitted otherwise).
type checkStatus struct {
	Pass bool
	Err  string
}

// readinessDeps is the dependency bag each check function consumes.
// Tests inject sqlmock + Config directly without standing up the full
// composition root; production wires Root via app.InitComposition
// so the production-shaped checks (Real wiring — server, dispatcher,
// worker, sqlite reader, qdrant lister, reconciler, routes) read
// real state.
//
// PR 15 (June 2026): Root is the canonical "production constructor
// output" the readiness gate consumes. Optional in tests so unit
// tests can still exercise the SQL-only checks (dead_letter,
// legacy_audit) without standing up the full composition root.
type readinessDeps struct {
	DB   *sql.DB
	Cfg  *config.Config
	Log  *zap.Logger
	Root *compositionRoot
}

// compositionRoot is the readiness-side view of the *app.ComposeRoot
// produced by app.InitComposition + app.WireRegistry.
//
// PR 15 (June 2026): every field carries a PRODUCTION CONCRETE TYPE
// from internal/app + internal/infrastructure + internal/api. There
// are no empty-marker interfaces and no narrow-port duplicates — the
// readiness gate reads the same Go values the server reads, so
// production-shaped invariants turn into plain nil checks at the
// read site. app.InitComposition + app.WireRegistry are the canonical
// constructors; if either fails, root is nil and every production-
// shaped check emits its per-check failure message.
//
// Bundle accessors (canonical, per `internal/app/composition.go`):
//   - Dispatcher        ← root.Outbox.Dispatcher   *outbox.Dispatcher
//   - EventsPool        ← root.Outbox.EventsPool   *outboxevents.Pool
//   - OutboxHandler     ← WireRegistry().OutboxHandler (api.InternalOutboxRouter)
//   - MediasearchHandler← WireRegistry().MediasearchHandler (api.InternalMediaSearchRouter)
//   - ClipsRepo         ← root.Repos.ClipsRepo     *assets.ClipsRepository
//   - QdrantClient      ← root.Process.QdrantClient  *transport.Client
//   - SemanticSearch    ← registryWiring.SearchFanOut (search.SearchFanOut)
type compositionRoot struct {
	Dispatcher         *outbox.Dispatcher
	EventsPool         *outboxevents.Pool
	OutboxHandler      api.InternalOutboxRouter
	MediasearchHandler api.InternalMediaSearchRouter
	ClipsRepo          *assets.ClipsRepository
	QdrantClient       *transport.Client
	SemanticSearch     searchpkg.SearchFanOut
}

// readinessCheck is the testable surface for the production-shaped
// readiness gate. Each entry is a `var` (not `func`) so
// cmd/admin/qdrant_readiness_test.go can REPLACE individual checks
// with mocks/failing implementations without touching this file.
// The composition root wires the real checks at init; tests override
// only the keys they want to simulate failure for.
//
// PR 15 — 9 user-specified keys, alphabetical from the spec:
//
//	"dead_letters_zero"             (production outbox status check)
//	"dispatcher_really_built"       (root.Outbox.Dispatcher != nil)
//	"legacy_cleanup_clean"          (per-channel SQL aggregate)
//	"production_sqlite_reader"      (root.Repos.ClipsRepo != nil)
//	"qdrant_active_collection_real" (real client + GetAliasTarget +
//	                                 CompareActiveCollection)
//	"real_routes_present"           (real router built from production
//	                                 handlers, not stubs)
//	"scan_reconciler_complete"      (qdrantReconcilerListerAdapter dry-run
//	                                 against SQLite + Qdrant; the legacy
//	                                 *qdrant.Reconciler was deleted in PR 7)
//	"server_production_constructor" (root != nil AND every required
//	                                 bundle non-nil)
//	"worker_real_state"             (root.Outbox.EventsPool != nil)
//
// Plus the pre-existing "delivery_signer" check (HMAC secret >= 16)
// as a backwards-compat key because the existing test suite asserts
// it. Production deployments rely on it for webhook integrity.
var readinessCheck = map[string]func(context.Context, readinessDeps) checkStatus{
	"dead_letters_zero":             checkDeadLetter,
	"delivery_signer":               checkDeliverySigner,
	"dispatcher_really_built":       checkDispatcherBuilt,
	"legacy_cleanup_clean":          checkLegacyAudit,
	"production_sqlite_reader":      checkSQLiteReader,
	"projection_parity":             checkProjectionParity,
	"qdrant_active_collection_real": checkQdrantActiveCollection,
	"real_routes_present":           checkRoutesReal,
	"scan_reconciler_complete":      checkReconcilerProduction,
	"semantic_search_real":          checkSemanticSearchReal,
	"server_production_constructor": checkServerProductionConstructor,
	"worker_real_state":             checkWorkerRealState,
}

// checkDeliverySigner (preserved from pre-PR-15 — protects webhook HMAC
// integrity in production).
func checkDeliverySigner(_ context.Context, deps readinessDeps) checkStatus {
	if deps.Cfg == nil {
		return checkStatus{Err: "config is nil"}
	}
	secret := strings.TrimSpace(deps.Cfg.Security.DeliveryHMACSecret)
	if secret == "" {
		return checkStatus{Err: "security.delivery_hmac_secret is empty"}
	}
	if len(secret) < 16 {
		return checkStatus{Err: fmt.Sprintf("security.delivery_hmac_secret length=%d (must be >= 16)", len(secret))}
	}
	return checkStatus{Pass: true}
}
