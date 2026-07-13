// Package asset is one of the kernel subzones declared in
// docs/architecture/godlike/02_TARGET_STRUCTURE.md §"internal/kernel".
// It is reserved for stable asset-shape concepts shared by at least two
// real capabilities (canonical Asset type, MediaType enum, Location,
// LifecycleState, ID primitives used across capability boundaries).
//
// EXPAND-phase placeholder (Wave 19, June 2026). Production content
// authoritatively lives in internal/domain/asset/ until BACKFILL/CUTOVER
// waves move it under us. Per 02_TARGET_STRUCTURE.md §"internal/kernel":
//
//   - The kernel imports the Go standard library only.
//   - No Gin types, SQL, repository implementations, Drive/Qdrant/FFmpeg/Ollama
//     clients, configuration structs, loggers, feature flags, or application
//     services may live here.
//   - A type belongs in the kernel ONLY when at least two real capabilities
//     need the same semantic contract; convenience moves are rejected.
//
// Do not import this package from production code in Wave 19. New imports
// require an entry in architecture/deprecations.yaml (per
// docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md).
package asset
