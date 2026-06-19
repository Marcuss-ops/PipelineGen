// Package sources provides the HTTP transport layer for media/sources endpoints.
//
// SourcesHandler is the real HTTP handler (defined across the many
// handler_sources_*.go files in this package). The Handler type alias
// preserves backward compatibility for those files, which use *Handler as
// method receiver.
//
// In PR12 the previous RouteHandler wrapper (with Inner()) was dropped.
// The module factory takes *SourcesHandler directly.
package sources

// Handler is a type alias for SourcesHandler.
type Handler = SourcesHandler
