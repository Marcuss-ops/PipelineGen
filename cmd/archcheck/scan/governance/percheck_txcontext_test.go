package governance

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// TestScanTxContextBan_AllFiveMethods is the load-bearing positive
// case: each of the 5 P0 C7 wire methods, when called from a
// non-allowlisted package, must produce exactly one Violation per
// call site. The seed file has one call per method on a single
// line (separate lines) so the violation count is 5.
func TestScanTxContextBan_AllFiveMethods(t *testing.T) {
	root := t.TempDir()
	src := `package app

import "context"

func caller(ctx context.Context) {
	_ = svc.UpdateJobToSucceededCAS(ctx, "job-1", "lease-1")
	_ = svc.InsertResultOnConflict(ctx, "job-1", 1, []byte("{}"))
	_ = svc.GetPriorArtifactHashes(ctx, "job-1")
	_ = svc.PersistArtifactMap(ctx, []string{"a1", "a2"})
	_ = svc.InsertOutboxEnvelope(ctx, "ev-1", []byte("{}"))
}
`
	writeFileFixture(t, root, "internal/application/example/caller.go", src)
	r := &report.Report{}
	ScanTxContextBan(root, &policy.Policy{}, r)
	if len(r.Violations) != 5 {
		t.Fatalf("expected 5 violations (one per wire method), got %d: %+v", len(r.Violations), r.Violations)
	}
	for _, v := range r.Violations {
		if v.Rule != "percheck_txcontext_ban" {
			t.Errorf("Rule = %q, want percheck_txcontext_ban", v.Rule)
		}
		if v.Severity != string(report.SeverityError) {
			t.Errorf("Severity = %q, want %q", v.Severity, report.SeverityError)
		}
	}
}

// TestScanTxContextBan_AllowlistCompletionService is the canonical
// allowlist path: a call to .UpdateJobToSucceededCAS inside the
// canonical completion service package MUST NOT surface as a
// violation. The path match is
// internal/capabilities/jobs/policy/...
func TestScanTxContextBan_AllowlistCompletionService(t *testing.T) {
	root := t.TempDir()
	src := `package completion

import "context"

func Service_Complete(ctx context.Context) error {
	return svc.UpdateJobToSucceededCAS(ctx, "job-1", "lease-1")
}
`
	writeFileFixture(t, root, "internal/capabilities/jobs/policy/service.go", src)
	r := &report.Report{}
	ScanTxContextBan(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Errorf("completion-service caller should be allowlisted, got %d: %+v", len(r.Violations), r.Violations)
	}
}

// TestScanTxContextBan_SkipsTestFiles pins the test-file exclusion:
// a wire-method call in a *_test.go file is not a production
// violation. Mirrors the shell check's --glob '!**/*_test.go'.
func TestScanTxContextBan_SkipsTestFiles(t *testing.T) {
	root := t.TempDir()
	src := `package app

import "context"

func TestDirectCall(t *testing.T) {
	svc.UpdateJobToSucceededCAS(context.Background(), "job-1", "lease-1")
}
`
	writeFileFixture(t, root, "internal/application/example/caller_test.go", src)
	r := &report.Report{}
	ScanTxContextBan(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Errorf("test-file caller should be excluded, got %d: %+v", len(r.Violations), r.Violations)
	}
}

// TestScanTxContextBan_CommentLines pins the comment-line exclusion:
// a wire-method call in a `//`-prefixed line is descriptive prose,
// not a real call. Mirrors the shell awk
// `^[[:space:]]*//` drop.
func TestScanTxContextBan_CommentLines(t *testing.T) {
	root := t.TempDir()
	src := `package app

// svc.UpdateJobToSucceededCAS(ctx, "x", "y") is forbidden in production
// callers — route through completion.Service.
func f() {}
`
	writeFileFixture(t, root, "internal/application/example/caller.go", src)
	r := &report.Report{}
	ScanTxContextBan(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Errorf("comment-only hit should be excluded, got %d: %+v", len(r.Violations), r.Violations)
	}
}

// TestScanTxContextBan_OnlyScansApplicationAndApi pins the scope
// contract: the scanner walks ONLY internal/application/** and
// internal/api/**. A wire-method call in any other subtree (e.g.
// internal/jobs/, internal/infrastructure/) is OUT OF SCOPE for
// this check (the shell check's glob is the same).
func TestScanTxContextBan_OnlyScansApplicationAndApi(t *testing.T) {
	root := t.TempDir()
	src := `package jobs

import "context"

func caller(ctx context.Context) {
	svc.UpdateJobToSucceededCAS(ctx, "job-1", "lease-1")
}
`
	writeFileFixture(t, root, "internal/jobs/caller.go", src)
	r := &report.Report{}
	ScanTxContextBan(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Errorf("internal/jobs/ is out of scope, got %d: %+v", len(r.Violations), r.Violations)
	}
}

// TestScanTxContextBan_MultipleHitsSameLine pins the per-line
// emission contract: a line that calls multiple wire methods
// produces multiple Violations (one per method). The substring
// scan is per-line, per-method.
func TestScanTxContextBan_MultipleHitsSameLine(t *testing.T) {
	root := t.TempDir()
	src := `package app

import "context"

func caller(ctx context.Context) {
	svc.UpdateJobToSucceededCAS(ctx, "job-1", "lease-1"); svc.InsertResultOnConflict(ctx, "job-1", 1, nil)
}
`
	writeFileFixture(t, root, "internal/application/example/caller.go", src)
	r := &report.Report{}
	ScanTxContextBan(root, &policy.Policy{}, r)
	if len(r.Violations) != 2 {
		t.Fatalf("expected 2 violations (2 methods on 1 line), got %d: %+v", len(r.Violations), r.Violations)
	}
	for _, v := range r.Violations {
		if !strings.Contains(v.Note, ".") {
			t.Errorf("Note should mention the wire method, got %q", v.Note)
		}
	}
}
