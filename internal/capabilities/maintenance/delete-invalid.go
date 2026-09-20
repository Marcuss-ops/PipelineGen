// internal/platform/qdrant/maintenance/delete-invalid.go — delete-invalid mode handler.
//
// Dispatches canonical outbox DELETE events for assets whose points hit
// non-locator legacy categories (1–6, 8). Points whose ONLY finding is
// LegacyLocatorPayload (category 7) are excluded — those are repairable
// via `repair-locators`, not deletable (per Issue 12 policy).
//
// FASE 1.2 PR-GODOBJ-12 closure (2026-07-04): verbatim migration from
// cmd/admin/qdrant_maintenance_delete_invalid.go per godlike/06 SSOT.
// The OutboxDispatcher port (defined in service.go) replaces the direct
// mctx.root.Outbox.Dispatcher import so the application-layer code
// stays decoupled from internal/app (composition root).
//
// Compile-drift fixup (2026-07-04, post-review): replace local helper
// `reportString(report interface{Stringify() string})` with direct
// `legacyaudit.StringifyReport(report)` package-function call.
package maintenance

// DeleteOptions is the typed-input envelope for Service.Delete.
//
//   - JSON: machine-readable JSON output (vs human-readable)
//   - Limit: optional cap on per-page scan size (Qdrant max=1000; 0 = default 500)
type DeleteOptions struct {
	JSON  bool
	Limit int
}
