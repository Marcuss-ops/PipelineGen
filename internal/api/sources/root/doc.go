// Package root holds the cross-cutting HTTP handlers from the legacy
// api/sources/ package that do not belong to any specific domain
// subpackage. After PR-A this package retains:
//   - SourcesHandler root struct (with sub-handler refs)
//   - SourcesHandler.RegisterRoutes (entry point)
//   - Shared types: Request structs, response shapes, helpers
//   - The "Handler = SourcesHandler" alias for backward compat with
//     any caller still using *Handler as a method receiver.
//
// Phase 1 of PR-A: scaffold only. The actual SourcesHandler moves from
// internal/api/sources/handler_sources_source_handlers.go to here in
// PR-A phase 5.
package root

// Placeholder. SourcesHandler moves here in PR-A phase 5.
