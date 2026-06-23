// Package health — Service orchestrates component health checks.
package health

import "context"

// ServiceDeps wires the four health checkers into the Service.
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

// HealthResponse is the unified health-check payload.
type HealthResponse struct {
	OK     bool                    `json:"ok"`
	Status string                  `json:"status"`
	Checks map[string]CheckResult  `json:"checks,omitempty"`
}

// Check runs the requested component checks and returns a unified response.
// All named checks are run even if one fails — the response aggregates
// all results and sets OK=false when any check fails.
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
			if s.db != nil {
				res = s.db.CheckDB(ctx)
			} else {
				res = CheckResult{"ok": false, "duration_ms": 0, "error": "db checker not wired"}
			}
		case "drive":
			if s.drive != nil {
				res = s.drive.CheckDrive(ctx)
			} else {
				res = CheckResult{"ok": false, "duration_ms": 0, "error": "drive checker not wired"}
			}
		case "qdrant":
			if s.qdrant != nil {
				res = s.qdrant.CheckQdrant(ctx)
			} else {
				res = CheckResult{"ok": false, "duration_ms": 0, "error": "qdrant checker not wired"}
			}
		case "jobs":
			if s.jobs != nil {
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
		if ok, _ := res["ok"].(bool); !ok {
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
