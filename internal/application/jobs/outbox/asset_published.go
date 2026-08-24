// Package outbox — asset_published.go is the slim orchestrator for
// the asset.published handler. The 538 LoC monolithic file
// (Wave 5 ship, 2026-07-06) was split per AGENTS.md Pattern 5 +
// godlike/06 SSOT one-canonical-owner-per-fact discipline:
//
//   - asset_published_errors.go   — typed terminal validation sentinels
//     with the umbrella + sub-%w wrap chain preserved.
//   - asset_published_envelope.go — SchemaVersion const +
//     AssetPublishedRequestV1 struct + the deprecated unexported
//     alias. Wire-shape contract only — NO business logic.
//   - asset_published_handler.go  — AssetPublishedHandler struct +
//     NewAssetPublishedHandler + EventType + Handle + ComposeSearchText.
//     Informational consumer surface only; no Qdrant port.
//
// godlike/06 SSOT (one canonical owner per fact): the schema version
// constant AssetPublishedSchemaVersion is re-declared as the parallel
// canonical constant outboxevents.SchemaVersionAssetPublished at
// internal/platform/sqlite/outboxevents/registry.go
// (the registry is the sole owner of the wire-shape string). Both
// constants MUST resolve to the same string literal — any drift
// surfaces as a build failure during the Compilation contract check.
//
// Schema versioning (PR-PUBLISH-V1):
//
//   - Strict v1 envelope — schema_version literal must match
//     AssetPublishedSchemaVersion. Mismatch is TERMINAL via
//     outboxevents.NewTerminalError so producers upgrade instead of
//     retrying into a repair loop.
//   - Required fields: schema_version, event_id, asset_id,
//     destination, idempotency_key.
//   - Optional: origin, category, subject, provider, drive_file_id,
//     drive_path, tags, requested_at. Defaulted at payload decoding
//     so the handler can rely on the enriched names.
//
// Cross-references (per godlike/06 SSOT, each companion file owns
// exactly one canonical capability concern):
//
//   - Typed errors:          asset_published_errors.go
//   - Wire-shape envelope:   asset_published_envelope.go
//   - Consumer (informational handler+composer): asset_published_handler.go
//
// Callers (handler test, producer emitter, composition root wiring)
// reference the symbols by their canonical names: the resolution is
// package-scope (all 4 files share the `outbox` package) so no
// import additions are required at any consumer site.
package outbox
