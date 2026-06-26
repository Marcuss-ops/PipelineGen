// Package health defines the application-layer health-check contracts.
//
// The Service builds a unified health response by delegating to
// infrastructure-layer checkers (DB, Drive, Qdrant, Jobs).
// Handlers call only Check(); never import infrastructure directly.
//
// CheckResult is a generic map — each component check returns its own
// fields (must include "ok": bool and "duration_ms": int64).
package health

import "context"

// CheckResult holds component-specific health fields. Every result
// must carry "ok" (bool) and "duration_ms" (int64). Additional
// fields (error, enabled, configured, index counts, etc.) are
// component-defined.
type CheckResult map[string]any

// DBChecker verifies the primary database is reachable and migrated.
type DBChecker interface {
	CheckDB(ctx context.Context) CheckResult
}

// DriveChecker verifies the Google Drive token and API are reachable.
type DriveChecker interface {
	CheckDrive(ctx context.Context) CheckResult
}

// QdrantChecker verifies the vector index backend is reachable when enabled.
type QdrantChecker interface {
	CheckQdrant(ctx context.Context) CheckResult
}

// JobsChecker verifies the job broker DB table exists and is reachable.
type JobsChecker interface {
	CheckJobs(ctx context.Context) CheckResult
}

// ErrUnknownCheck is returned when the caller requests an unknown health
// check name. The HTTP handler maps this typed error to HTTP 400 (not 503),
// distinguishing misconfiguration from genuine unhealthiness.
type ErrUnknownCheck struct {
	Name string
}

func (e *ErrUnknownCheck) Error() string {
	return "unknown health check: " + e.Name
}
