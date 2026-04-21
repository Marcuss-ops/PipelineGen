// Package postgres is the future primary persistence backend for PipelineGen.
//
// Scope:
//   - jobs
//   - workers
//   - orchestration state
//   - durable events
//
// SQLite should remain cache-only and JSON should remain fallback/dev-only.
package postgres
