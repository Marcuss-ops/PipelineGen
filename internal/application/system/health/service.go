// Package health — Service orchestrates component health checks.
package health

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/portutil"
)

// ServiceDeps wires the four health checkers into the Service.
//
// Drive and Qdrant are OPTIONAL capabilities: passing a nil check
// (or a typed-nil pointer wrapped in the interface — see
// pkg/portutil.IsNilPort) makes the corresponding health check return
// {ok: true, applicable: false} without contacting the dependency.
// DB and Jobs are MANDATORY: a missing checker produces
// {ok: false, error: "<name> checker not wired"} so misconfiguration
// is surfaced loudly instead of silently passing.
type ServiceDeps struct {
	DB    DBChecker
	Drive DriveChecker
	Qdrant QdrantChecker
	Jobs  JobsChecker
}

// Service orchestrates component health checks.
type Service struct {
	db    DBChecker
	drive DriveChecker
	qdrant QdrantChecker
	jobs  JobsChecker
}

// NewService creates a new health-check orchestrator.
func NewService(deps ServiceDeps) *Service {
	return &Service{
		db:    deps.DB,
		drive: deps.Drive,
		qdrant: deps.Qdrant,
		jobs:  deps.Jobs,
	}
}

// ValidCheckNames is the set of recognised check names.
var ValidCheckNames = map[string]bool{
	"db":     true,
	"drive":  true,
	"qdrant": true,
	"jobs":   true,
}

// NormalizeCheckNames trims, lowercases, removes empty strings,
// and deduplicates while preserving order. Accepts both repeated query
// values and comma-separated strings. Names are case-insensitive ("DB" → "db").
func NormalizeCheckNames(names []string) []string {
	// First, split comma-separated entries to produce a flat list.
	flat := make([]string, 0, len(names))
	for _, name := range names {
		for _, part := range strings.Split(name, ",") {
			flat = append(flat, strings.ToLower(strings.TrimSpace(part)))
		}
	}

	// Remove empty entries and deduplicate while preserving order.
	seen := make(map[string]bool, len(flat))
	result := make([]string, 0, len(flat))
	for _, name := range flat {
		if name == "" {
			continue
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		result = append(result, name)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// ValidateCheckNames returns an *ErrUnknownCheck if any name is
// unknown. Returns nil when all names are valid (or when names is nil).
func ValidateCheckNames(names []string) error {
	for _, name := range names {
		if !ValidCheckNames[name] {
			return &ErrUnknownCheck{Name: name}
		}
	}
	return nil
}

// HealthResponse is the unified health-check payload.
type HealthResponse struct {
	OK     bool                    `json:"ok"`
	Status string                  `json:"status"`
	Checks map[string]CheckResult  `json:"checks,omitempty"`
}

// Check runs the requested component checks and returns a unified response.
// All named checks are run even if one fails — the response aggregates
// all results and sets OK=false when any check fails.
//
// fix/health-capabilities-optional Commit 3:
//   - Drive and Qdrant are OPTIONAL capabilities. When their checker is
//     nil (capability opted out at composition time) we return
//     {ok: true, applicable: false} instead of ok=false. This prevents
//     health endpoints in Drive-less / vector-search-less deployments
//     from reporting 503 solely because the capability is missing.
//   - Aggregation: a check whose result carries applicable=false is
//     treated as opt-out and does not flip allOK. DB and Jobs remain
//     mandatory (nil checker still produces ok=false with an error).
//   - Unknown check names still report unhealthy (defensive: prevents
//     callers from silently passing on a typo).
func (s *Service) Check(ctx context.Context, names []string) HealthResponse {
	if len(names) == 0 {
		return HealthResponse{OK: true, Status: "healthy"}
	}

	checks := map[string]CheckResult{}
	allOK := true

	for _, name := range names {
		var res CheckResult
		switch name {
		case "db":
			// DB is mandatory. Guard against both nil interface AND
			// typed-nil pointer (defensive — composition.go never
			// produces typed-nil today but a future caller might).
			if s.db != nil && !portutil.IsNilPort(s.db) {
				res = s.db.CheckDB(ctx)
			} else {
				res = CheckResult{"ok": false, "duration_ms": 0, "error": "db checker not wired"}
			}
		case "drive":
			// Drive is optional: nil OR typed-nil checker = capability not wired.
			if s.drive != nil && !portutil.IsNilPort(s.drive) {
				res = s.drive.CheckDrive(ctx)
			} else {
				res = CheckResult{
					"ok": true, "applicable": false, "duration_ms": 0,
					"note": "drive checker not wired",
				}
			}
		case "qdrant":
			// Qdrant is optional: nil OR typed-nil checker = vector search disabled.
			if s.qdrant != nil && !portutil.IsNilPort(s.qdrant) {
				res = s.qdrant.CheckQdrant(ctx)
			} else {
				res = CheckResult{
					"ok": true, "applicable": false, "duration_ms": 0,
					"note": "qdrant checker not wired",
				}
			}
		case "jobs":
			// Jobs is mandatory: nil OR typed-nil checker is a misconfiguration.
			if s.jobs != nil && !portutil.IsNilPort(s.jobs) {
				res = s.jobs.CheckJobs(ctx)
			} else {
				res = CheckResult{"ok": false, "duration_ms": 0, "error": "jobs checker not wired"}
			}
		default:
			// Unknown check name: report as unhealthy so callers can
			// detect misconfiguration instead of silently passing.
			res = CheckResult{
				"ok":          false,
				"duration_ms": 0,
				"error":       "unknown check: " + name,
			}
		}

		checks[name] = res
		// Aggregation rule: applicable=false → opted out, do not fail-closed.
		// No "applicable" key (or applicable=true) + ok=false → fail-closed.
		if applicable, hasApplicable := res["applicable"].(bool); hasApplicable && !applicable {
			// opted out: skip
		} else if ok, _ := res["ok"].(bool); !ok {
			allOK = false
		}
	}

	status := "healthy"
	if !allOK {
		status = "unhealthy"
	}

	return HealthResponse{
		OK:     allOK,
		Status: status,
		Checks: checks,
	}
}
