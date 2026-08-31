// Package job is one of the kernel subzones declared in
// docs/architecture/godlike/02_TARGET_STRUCTURE.md §"internal/kernel".
// It is reserved for stable job-execution concepts shared by capabilities
// that produce or consume background work (canonical Job shape, JobType,
// Lease, terminal states, WorkerSession contract used cross-capability).
//
// EXPAND-phase placeholder (Wave 19, June 2026). Production content
// authoritatively lives in internal/kernel/job/ until BACKFILL/CUTOVER
// waves relocate it under this import path. Per the kernel rules in
// 02_TARGET_STRUCTURE.md, no repository implementation, no Gin, no
// database/sql, no transport-specific type lives here.
package job
