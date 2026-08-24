// Package health orchestrates component health checks.
package system

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/portutil"
)

// ServiceDeps wires the health checkers into the Service.
//
// Drive and Qdrant are optional capabilities: passing a nil check
// (or a typed-nil pointer wrapped in the interface) makes the
// corresponding health check return {ok:true, applicable:false}
// without contacting the dependency.
//
// DB and Jobs are mandatory: a missing checker produces
// {ok:false, error:"<name> checker not wired"}.
type ServiceDeps struct {
	DB     DBChecker
	Drive  DriveChecker
	Qdrant QdrantChecker
	Jobs   JobsChecker
}

// Service orchestrates component health checks.
type Service struct {
	db     DBChecker
	drive  DriveChecker
	qdrant QdrantChecker
	jobs   JobsChecker
}

// NewService creates a new health-check orchestrator.
func NewService(deps ServiceDeps) *Service {
	return &Service{
		db:     deps.DB,
		drive:  deps.Drive,
		qdrant: deps.Qdrant,
		jobs:   deps.Jobs,
	}
}

// ValidCheckNames is the set of recognized check names.
var ValidCheckNames = map[string]bool{
	"db":     true,
	"drive":  true,
	"qdrant": true,
	"jobs":   true,
}

// NormalizeCheckNames trims, lowercases, removes empty strings,
// and deduplicates while preserving order. Accepts both repeated query
// values and comma-separated strings.
func NormalizeCheckNames(names []string) []string {
	flat := make([]string, 0, len(names))
	for _, name := range names {
		for _, part := range strings.Split(name, ",") {
			flat = append(flat, strings.ToLower(strings.TrimSpace(part)))
		}
	}

	seen := make(map[string]bool, len(flat))
	result := make([]string, 0, len(flat))
	for _, name := range flat {
		if name == "" || seen[name] {
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

// ValidateCheckNames returns an *ErrUnknownCheck if any name is unknown.
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
	OK     bool                   `json:"ok"`
	Status string                 `json:"status"`
	Checks map[string]CheckResult `json:"checks,omitempty"`
}

// Check runs the requested component checks and returns a unified response.
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
			if s.db != nil && !portutil.IsNilPort(s.db) {
				res = s.db.CheckDB(ctx)
			} else {
				res = CheckResult{"ok": false, "duration_ms": 0, "error": "db checker not wired"}
			}
		case "drive":
			if s.drive != nil && !portutil.IsNilPort(s.drive) {
				res = s.drive.CheckDrive(ctx)
			} else {
				res = CheckResult{
					"ok": true, "applicable": false, "duration_ms": 0,
					"note": "drive checker not wired",
				}
			}
		case "qdrant":
			if s.qdrant != nil && !portutil.IsNilPort(s.qdrant) {
				res = s.qdrant.CheckQdrant(ctx)
			} else {
				res = CheckResult{
					"ok": true, "applicable": false, "duration_ms": 0,
					"note": "qdrant checker not wired",
				}
			}
		case "jobs":
			if s.jobs != nil && !portutil.IsNilPort(s.jobs) {
				res = s.jobs.CheckJobs(ctx)
			} else {
				res = CheckResult{"ok": false, "duration_ms": 0, "error": "jobs checker not wired"}
			}
		default:
			res = CheckResult{
				"ok":          false,
				"duration_ms": 0,
				"error":       "unknown check: " + name,
			}
		}

		checks[name] = res
		if applicable, hasApplicable := res["applicable"].(bool); hasApplicable && !applicable {
			continue
		}
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
