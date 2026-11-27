package database

import (
	"testing"
	"time"
)

// DB-04: the two largest uncontrolled tables must be in the canonical
// retention plans, with the right time column and terminal-job guard.
func TestBuildRetentionPlansCoversUnboundedTables(t *testing.T) {
	telemetry := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	history := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	plans := buildRetentionPlans("/data", "/data/observability/api_requests.db.sqlite", telemetry, history)

	find := func(planName, table string) (string, bool, bool) {
		for _, plan := range plans {
			if plan.Name != planName {
				continue
			}
			for _, op := range plan.Ops {
				if op.Table == table {
					return op.TimeColumn, op.TerminalJobOnly, true
				}
			}
		}
		return "", false, false
	}

	if column, terminalOnly, ok := find("jobs", "job_results"); !ok {
		t.Fatal("job_results is missing from the jobs retention plan")
	} else if column != "created_at" || !terminalOnly {
		t.Fatalf("job_results retention = (%q, terminalOnly=%v), want (created_at, true)", column, terminalOnly)
	}

	if column, terminalOnly, ok := find("media", "render_attempt_analytics"); !ok {
		t.Fatal("render_attempt_analytics is missing from the media retention plan")
	} else if column != "recorded_at" || terminalOnly {
		t.Fatalf("render_attempt_analytics retention = (%q, terminalOnly=%v), want (recorded_at, false)", column, terminalOnly)
	}
}
