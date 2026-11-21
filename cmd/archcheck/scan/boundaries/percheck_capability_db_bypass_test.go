package boundaries

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func scanDBBypass(t *testing.T, root string) *report.Report {
	t.Helper()
	r := &report.Report{}
	ScanCapabilityDBBypass(root, &policy.Policy{}, r)
	return r
}

func dbBypassViolations(r *report.Report) []report.Violation {
	var out []report.Violation
	for _, v := range r.Violations {
		if v.Rule == capabilityDBBypassRule {
			out = append(out, v)
		}
	}
	return out
}

func TestScanCapabilityDBBypassFlagsAppAndCapability(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/app/wiring/bad.go", `package wiring

type bad interface{ DB() *sql.DB }
`)
	writeGoFile(t, tmp, "internal/capabilities/bad/bad.go", `package bad

func (x *T) DB() *sql.DB { return nil }
`)
	got := dbBypassViolations(scanDBBypass(t, tmp))
	if len(got) != 2 {
		t.Fatalf("violations = %d, want 2: %+v", len(got), got)
	}
}

func TestScanCapabilityDBBypassIgnoresPlatformCommentsAndTests(t *testing.T) {
	tmp := t.TempDir()
	writeGoFile(t, tmp, "internal/platform/postgres/media/ok.go", `package media

// DB() *sql.DB is the implementation side of the port.
func (x *T) DB() *sql.DB { return nil }
`)
	writeGoFile(t, tmp, "internal/app/wiring/bad_test.go", `package wiring

type bad interface{ DB() *sql.DB }
`)
	writeGoFile(t, tmp, "internal/app/wiring/comment.go", `package wiring

// Never expose DB() *sql.DB from this package.
`)
	if got := dbBypassViolations(scanDBBypass(t, tmp)); len(got) != 0 {
		t.Fatalf("platform implementation, tests and comments must not be flagged, got %+v", got)
	}
}

func TestScanCapabilityDBBypassRepoTreeIsClean(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Skipf("repository root not resolvable: %v", err)
	}
	if got := dbBypassViolations(scanDBBypass(t, root)); len(got) != 0 {
		t.Fatalf("DB() bypass returned to app/capabilities: %+v", got)
	}
}
