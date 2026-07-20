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

// QdrantEndpointPort provides deep-readiness semantics for the dedicated
// /qdrant/live + /qdrant/ready transport endpoints. Distinct from
// QdrantChecker (which is the shallow liveness ping for the GENERIC
// /ready aggregator): this port owns the 4-check deep readiness
// contract (alias present, collection populated, schema ok, semantic
// canary).
//
// Sprint 3.4 step1 (godlike/06 SSOT — single canonical owner per fact):
// owns the transport-side Qdrant health concern. Adapter lives in
// internal/app/ (the only composition-root site allowed to wire
// infrastructure types into api ports per AGENTS.md).
type QdrantEndpointPort interface {
	// Live returns nil if Qdrant responds (fast liveness path).
	Live(ctx context.Context) error
	// Ready produces a deep-readiness report with 4 named sub-checks.
	// The handler maps report.OK → HTTP status (200 / 503).
	Ready(ctx context.Context) QdrantReadyReport
}

// QdrantReadyReport is the JSON-ready deep-readiness report produced
// by QdrantEndpointPort.Ready. Checks mirrors the previous handler's
// gin.H-shaped output 1:1 so /qdrant/ready is wire-stable across the
// refactor. Error is set ONLY for the not-configured (niledep) short-
// circuit so the handler can render the legacy flat shape verbatim
// ({ok:false, status:"not ready", error:"…"}); the four normal
// checks (alias/collection/schema/canary) always carry detail via
// the Checks map and leave Error empty.
type QdrantReadyReport struct {
	OK     bool           `json:"ok"`
	Status string         `json:"status"`
	Error  string         `json:"error,omitempty"`
	Checks map[string]any `json:"checks,omitempty"`
}
