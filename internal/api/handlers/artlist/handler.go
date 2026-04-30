package artlist

// File refactored into modules:
// - handler_core.go: Handler struct, route registration, middleware
// - handler_run.go: Run pipeline and status handlers
// - handler_search.go: Stats, search, diagnostics, drive sync handlers
// - handler_clip.go: Clip lifecycle and import handlers

// Dead code removed:
// - Unused local searchLive function
// - Dead Sync/Reindex/PurgeStale handlers (service methods removed)
// - Related route registrations for dead endpoints
