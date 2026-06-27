// Package content bundles the Books + Lessons generation sub-handlers
// into a single Go package so they live next to each other in the
// source tree (per Punto 6 of the API-surface consolidation plan in
// docs/FUTURE_IMPLEMENTATIONS.md).
//
// The sub-handlers are NOT merged into a single Handler type; each
// retains the canonical pattern:
//
//	type Handler struct { ... }
//	func RegisterRoutes(group *gin.RouterGroup, handler *Handler)
//
// BooksHandler keeps the routes under /api/books/* and LessonsHandler
// keeps the routes under /api/lessons/* — preserving the existing
// client URL surface. Two RouteModule wrappers (NewBooksModule and
// NewLessonsModule) are exported so the registry can wire the two
// sub-handlers independently and each gets its own base path.
//
// Adopting transport.JSON(I,O) for these handlers is tracked as a
// follow-up (post-consolidation). The current code path uses the
// api.Error / api.OK / api.InternalError helpers directly, which is
// accepted by AGENTS.md Pattern 1 (handlers delegate to services via
// internal/media/<X>) but is not yet on the unified transport layer.
package content
