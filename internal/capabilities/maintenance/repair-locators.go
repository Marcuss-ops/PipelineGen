// internal/platform/qdrant/maintenance/repair-locators.go — repair-locators mode handler.
//
// Strips legacy drive_link / local_path payload keys from all points in the
// active Qdrant collection via the canonical LocatorCleaner. Fast path:
// does NOT require the full composition root (no SQLite open, no
// app.InitComposition) — only the Qdrant client.
//
// FASE 1.2 PR-GODOBJ-12 closure (2026-07-04): verbatim migration from
// cmd/admin/qdrant_maintenance_repair_locators.go per godlike/06 SSOT.
// Service.Repair delegates to the injected QdrantCleaner port (defined in
// service.go). The cmd/admin layer no longer imports internal/platform/qdrant
// directly for repair — it goes through the canonical QdrantCleaner port.
package maintenance

import (
	qdrantdr "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/dr"
)

// RepairOptions is the typed-input envelope for Service.Repair.
//
//   - JSON: machine-readable JSON output (vs human-readable)
type RepairOptions struct {
	JSON bool
}

// LocatorCleanupReport is the shared pure-data report returned by the
// QdrantCleaner port. Its canonical definition lives in the dependency-free
// domain package so application and infrastructure use the same type.
type LocatorCleanupReport = qdrantdr.LocatorCleanupReport
