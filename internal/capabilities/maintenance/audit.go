// internal/platform/qdrant/maintenance/audit.go — audit mode handler.
//
// Dry-run: classifies all points in the active Qdrant collection over the
// 8 legacy categories, then prints the report (JSON or human-readable).
// No mutations — read-only surface. Sibling to repair-locators and
// delete-invalid (the other 2 modes in qdrant-maintenance).
//
// FASE 1.2 PR-GODOBJ-12 closure (2026-07-04): verbatim migration from
// cmd/admin/qdrant_maintenance_audit.go per godlike/06 SSOT — the
// application layer is the canonical owner of use-case orchestration.
//
// Compile-drift fixup (2026-07-04, post-review): replace local helper
// `reportString(report interface{Stringify() string})` with direct
// `legacyaudit.StringifyReport(report)` package-function call (the
// canonical stringifier is a pkg-level func, not a Report method).
// Also: remove the now-unused `interface{...}` happy-indirection that
// would drift from the canonical func signature.
package maintenance

// AuditOptions is the typed-input envelope for the Service.Audit method.
// Mirrors the qdrantMaintDeps fields needed by the audit pipeline:
//
//   - JSON: machine-readable JSON output (vs human-readable)
//   - Limit: optional cap on per-page scan size (Qdrant max=1000; 0 = default 500)
type AuditOptions struct {
	JSON  bool
	Limit int
}
