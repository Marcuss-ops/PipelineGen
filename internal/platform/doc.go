// Package platform hosts the technical, capability-agnostic implementations
// declared in docs/architecture/godlike/02_TARGET_STRUCTURE.md
// §"internal/platform". Each platform subpackage implements a single
// technical capability (database, HTTP server, drive upload, vector
// search, process execution, filesystem, observability, AI inference)
// and owns NO business semantics.
//
// EXPAND-phase placeholder (Wave 19, June 2026). Production platform code
// authoritatively lives in internal/platform/<x>/ until the
// per-platform BACKFILL/CUTOVER waves relocate it under this root. Per
// 02_TARGET_STRUCTURE.md §"internal/platform":
//
//   - platform/qdrant knows how to search vectors; the owning capability
//     decides what an asset search means.
//   - platform/drive knows how to upload and move files; the owning
//     capability decides when and why to upload.
//   - platform/sqlite owns connection, transaction, migration, and pragma
//     mechanics; capability repositories own SQL for their tables.
//
// Do not import this package from production code in Wave 19 beyond the
// existing internal/platform/config subzone, which is the only declared
// Phase-0 legal subzone per architecture/policy.yaml (platform_subzones).
package platform
