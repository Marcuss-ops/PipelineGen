package health

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/pkg/portutil"
)

// fakeChecker is a minimal stub implementing DBChecker/DriveChecker/
// QdrantChecker/JobsChecker via the kind field that gates which
// CheckX method is callable. All four interfaces share the shape
// (Check*(ctx) CheckResult), so one struct suffices.
//
// We deliberately do NOT import internal/infrastructure/health here:
// the infra checkers depend on this package's ports, which would
// create an import cycle. The infra checkers' return shape is
// already verified by qdrant_checker_test.go + drive_checker_test.go.
// Here we only verify the SERVICE's aggregation contract.
type fakeChecker struct {
	kind   string // "db" | "drive" | "qdrant" | "jobs"
	result CheckResult
	err    error
}

func (f *fakeChecker) CheckDB(ctx context.Context) CheckResult {
	if f.kind != "db" {
		panic("fakeChecker.CheckDB called on non-db kind=" + f.kind)
	}
	if f.err != nil {
		return CheckResult{"ok": false, "duration_ms": 0, "error": f.err.Error()}
	}
	return f.result
}

func (f *fakeChecker) CheckDrive(ctx context.Context) CheckResult {
	if f.kind != "drive" {
		panic("fakeChecker.CheckDrive called on non-drive kind=" + f.kind)
	}
	if f.err != nil {
		return CheckResult{"ok": false, "duration_ms": 0, "error": f.err.Error()}
	}
	return f.result
}

func (f *fakeChecker) CheckQdrant(ctx context.Context) CheckResult {
	if f.kind != "qdrant" {
		panic("fakeChecker.CheckQdrant called on non-qdrant kind=" + f.kind)
	}
	if f.err != nil {
		return CheckResult{"ok": false, "duration_ms": 0, "error": f.err.Error()}
	}
	return f.result
}

func (f *fakeChecker) CheckJobs(ctx context.Context) CheckResult {
	if f.kind != "jobs" {
		panic("fakeChecker.CheckJobs called on non-jobs kind=" + f.kind)
	}
	if f.err != nil {
		return CheckResult{"ok": false, "duration_ms": 0, "error": f.err.Error()}
	}
	return f.result
}

// TestService_SkipMode_DriveAndQdrant_NilChecker: when Drive + Qdrant
// are nil and DB + Jobs are healthy, Check returns ok=true. Without
// the Commit 3 skip-mode this would return ok=false due to nil drive,
// which propagates as HTTP 503 from the /health endpoint.
func TestService_SkipMode_DriveAndQdrant_NilChecker(t *testing.T) {
	svc := NewService(ServiceDeps{
		DB:     &fakeChecker{kind: "db", result: CheckResult{"ok": true, "duration_ms": 1}},
		Drive:  nil,
		Qdrant: nil,
		Jobs:   &fakeChecker{kind: "jobs", result: CheckResult{"ok": true, "duration_ms": 1}},
	})
	resp := svc.Check(context.Background(), []string{"db", "drive", "qdrant", "jobs"})
	if !resp.OK {
		t.Fatalf("expected ok=true (drive+qdrant are optional), got status=%q checks=%v", resp.Status, resp.Checks)
	}
	if resp.Status != "healthy" {
		t.Fatalf("expected healthy, got %q", resp.Status)
	}
	for _, name := range []string{"drive", "qdrant"} {
		app, has := resp.Checks[name]["applicable"].(bool)
		if !has {
			t.Fatalf("expected %s check to carry applicable key, got %v", name, resp.Checks[name])
		}
		if app {
			t.Fatalf("expected %s applicable=false (opt-out), got true", name)
		}
	}
}

// TestService_SkipMode_QdrantNil_DriveWired: verifies the asymmetric
// capability pattern — Qdrant opted-out (nil) but Drive wired with an
// ok=true result.
func TestService_SkipMode_QdrantNil_DriveWired(t *testing.T) {
	svc := NewService(ServiceDeps{
		DB: &fakeChecker{kind: "db", result: CheckResult{"ok": true, "duration_ms": 1}},
		Drive: &fakeChecker{kind: "drive", result: CheckResult{
			"ok": true, "duration_ms": 5, "configured": true,
		}},
		Qdrant: nil,
		Jobs:   &fakeChecker{kind: "jobs", result: CheckResult{"ok": true, "duration_ms": 1}},
	})
	resp := svc.Check(context.Background(), []string{"db", "drive", "qdrant", "jobs"})
	if !resp.OK {
		t.Fatalf("expected ok=true (drive wired, qdrant opted-out), got %v", resp)
	}
	if app, has := resp.Checks["qdrant"]["applicable"].(bool); !has || app {
		t.Fatalf("expected qdrant applicable=false (opt-out), got applicable=%v hasKey=%v map=%v",
			app, has, resp.Checks["qdrant"])
	}
	// Drive, by contrast, has no applicable key — it's wired and ok.
	if _, has := resp.Checks["drive"]["applicable"]; has {
		t.Fatalf("expected drive to be wired (no applicable key), got applicable key: %v", resp.Checks["drive"])
	}
}

// TestService_MandatoryNil_DB_StillFails: even with skip-mode enabled,
// DB and Jobs nil-checkers still produce ok=false — they're mandatory.
func TestService_MandatoryNil_DB_StillFails(t *testing.T) {
	svc := NewService(ServiceDeps{
		DB:   nil,
		Jobs: &fakeChecker{kind: "jobs", result: CheckResult{"ok": true, "duration_ms": 1}},
	})
	resp := svc.Check(context.Background(), []string{"db", "jobs"})
	if resp.OK {
		t.Fatalf("expected ok=false when DB is nil (mandatory), got %v", resp)
	}
	if msg, _ := resp.Checks["db"]["error"].(string); msg != "db checker not wired" {
		t.Fatalf("expected 'db checker not wired' error, got %q", msg)
	}
}

// TestService_MandatoryNil_Jobs_StillFails: symmetric to DB above.
func TestService_MandatoryNil_Jobs_StillFails(t *testing.T) {
	svc := NewService(ServiceDeps{
		DB:   &fakeChecker{kind: "db", result: CheckResult{"ok": true, "duration_ms": 1}},
		Jobs: nil,
	})
	resp := svc.Check(context.Background(), []string{"db", "jobs"})
	if resp.OK {
		t.Fatalf("expected ok=false when Jobs is nil (mandatory), got %v", resp)
	}
	if msg, _ := resp.Checks["jobs"]["error"].(string); msg != "jobs checker not wired" {
		t.Fatalf("expected 'jobs checker not wired' error, got %q", msg)
	}
}

// TestService_Aggregation_ApplicableIgnored: when a concrete check
// returns applicable=false (capability opted out at the infra layer),
// the aggregator must NOT flip allOK=false. This is the critical
// invariant for Commit 3: the Service honours applicable across BOTH
// nil-checker and concrete-checker codes.
func TestService_Aggregation_ApplicableIgnored(t *testing.T) {
	svc := NewService(ServiceDeps{
		DB: &fakeChecker{kind: "db", result: CheckResult{"ok": true, "duration_ms": 1}},
		Drive: &fakeChecker{kind: "drive", result: CheckResult{
			"ok": false, "applicable": false, "duration_ms": 0,
			"note": "Drive credentials not configured",
		}},
		Jobs: &fakeChecker{kind: "jobs", result: CheckResult{"ok": true, "duration_ms": 1}},
	})
	resp := svc.Check(context.Background(), []string{"db", "drive", "jobs"})
	if !resp.OK {
		t.Fatalf("drive applicable=false must keep ok=true, got %v", resp)
	}
}

// TestService_Aggregation_GenuineFailure_StillFails: a real failure
// (ok=false, no applicable key) must flip allOK=false even when other
// checks opt out.
func TestService_Aggregation_GenuineFailure_StillFails(t *testing.T) {
	svc := NewService(ServiceDeps{
		DB: &fakeChecker{kind: "db", err: errors.New("boom")},
		Drive: &fakeChecker{kind: "drive", result: CheckResult{
			"ok": true, "applicable": false, "duration_ms": 0, "note": "Drive credentials not configured",
		}},
	})
	resp := svc.Check(context.Background(), []string{"db", "drive"})
	if resp.OK {
		t.Fatalf("real DB failure must flip ok=false, got %v", resp)
	}
	if resp.Status != "unhealthy" {
		t.Fatalf("expected unhealthy, got %q", resp.Status)
	}
}

// TestService_UnknownCheckName: defensive — caller typos should not
// silently pass.
func TestService_UnknownCheckName(t *testing.T) {
	svc := NewService(ServiceDeps{
		DB: &fakeChecker{kind: "db", result: CheckResult{"ok": true, "duration_ms": 1}},
	})
	resp := svc.Check(context.Background(), []string{"db", "nonexistent"})
	if resp.OK {
		t.Fatalf("unknown check name must keep ok=false, got %v", resp)
	}
	if _, has := resp.Checks["nonexistent"]["error"]; !has {
		t.Fatalf("expected error key on unknown check, got %v", resp.Checks["nonexistent"])
	}
}

// TestService_EmptyNames: legacy callers passing no names get the
// shortcut branch (ok=true, no checks map populated).
func TestService_EmptyNames(t *testing.T) {
	svc := NewService(ServiceDeps{})
	resp := svc.Check(context.Background(), nil)
	if !resp.OK || resp.Status != "healthy" {
		t.Fatalf("empty names should produce healthy, got %v", resp)
	}
	if len(resp.Checks) != 0 {
		t.Fatalf("empty names should not populate checks, got %v", resp.Checks)
	}
}

// TestService_TypedNilPointer_Guarded: a typed-nil pointer cast to the
// interface (e.g., var c *infrahealth.SQLiteChecker = nil; ServiceDeps{DB: c})
// would otherwise panic on c.CheckDB(ctx). The portutil.IsNilPort guard
// in the Service must catch this and emit {ok: false, error: "X checker
// wired but typed-nil"} to keep the fail-closed contract intact.
func TestService_TypedNilPointer_Guarded(t *testing.T) {
	// Construct a typed-nil DB by declaring a nil pointer and assigning
	// it to the interface field via a local variable. We can simulate
	// it directly by hand-rolling an interface-bearing nil struct.
	var typedNilDB DBChecker // nil interface; no concern
	_ = typedNilDB

	// Direct case: nil interface already tested by MandatoryNil tests.
	// Here we hand-roll the typed-nil scenario via a nil faker.
	var nilFaker *fakeChecker // typed nil pointer
	svc := NewService(ServiceDeps{
		DB:   nilFaker,
		Jobs: &fakeChecker{kind: "jobs", result: CheckResult{"ok": true, "duration_ms": 1}},
	})
	// Sanity: portutil.IsNilPort catches it.
	if !portutil.IsNilPort(svc.db) {
		t.Fatalf("portutil.IsNilPort should detect typed-nil DB, got false")
	}
	resp := svc.Check(context.Background(), []string{"db", "jobs"})
	if resp.OK {
		t.Fatalf("typed-nil DB must not flip ok=true (should be wired-but-nil), got %v", resp)
	}
	if msg, _ := resp.Checks["db"]["error"].(string); msg == "" {
		t.Fatalf("expected non-empty error for typed-nil DB, got %v", resp.Checks["db"])
	}
}
