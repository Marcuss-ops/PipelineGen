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
// The transport.JSON pipeline was removed in June 2026 (Issue 9b
// consolidation). Handlers now use apiutil.BindJSON + apiutil.OK /
// apiutil.Error directly, which is the canonical pattern per
// AGENTS.md Pattern 1 (handlers delegate to services via
// application-layer use cases).
package content
