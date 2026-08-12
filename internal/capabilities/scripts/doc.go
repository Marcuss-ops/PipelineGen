// Package scriptgeneration owns the canonical scripts capability boundary.
//
// The implementation preserves the existing scriptgeneration package API so
// transport, composition, and infrastructure callers can migrate by import
// path without changing workflow behavior. This package owns the pure
// generation model, ports, durable runner, and run starter; SQLite persistence
// is backed by the canonical observability run tables.
//
// The former application-layer facade has been removed after all production
// and test references were moved to this capability package.
package scriptgeneration
