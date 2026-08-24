package scan

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// TestScanMonitorInfraImport_Clean is the negative case: a
// monitor/ tree with no internal/infrastructure imports
// produces 0 violations and 0 warnings.
func TestScanMonitorInfraImport_Clean(t *testing.T) {
	root := t.TempDir()
	writeFileFixture(t, root, "internal/application/assets/monitor/clean.go", `package monitor

import "context"

type ChannelMonitor struct{}
`)
	r := &report.Report{}
	ScanMonitorInfraImport(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Errorf("clean monitor tree should produce 0 violations, got %d: %+v", len(r.Violations), r.Violations)
	}
	if len(r.Warnings) != 0 {
		t.Errorf("clean monitor tree should produce 0 warnings, got %d: %+v", len(r.Warnings), r.Warnings)
	}
}

// TestScanMonitorInfraImport_HardFail pins the load-bearing
// fail-closed case: a production import of internal/infrastructure
// in a non-test .go file without an upstream marker is a
// hard-fail violation.
func TestScanMonitorInfraImport_HardFail(t *testing.T) {
	root := t.TempDir()
	src := `package monitor

import "context"

import sqlassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"

type ChannelMonitor struct {
	_ sqlassets.SomeType
}
`
	writeFileFixture(t, root, "internal/application/assets/monitor/bad.go", src)
	r := &report.Report{}
	ScanMonitorInfraImport(root, &policy.Policy{}, r)
	if len(r.Violations) != 1 {
		t.Fatalf("hard-fail import should produce 1 violation, got %d: %+v", len(r.Violations), r.Violations)
	}
	v := r.Violations[0]
	if v.Rule != "percheck_monitor_infra_import" {
		t.Errorf("Rule = %q, want percheck_monitor_infra_import", v.Rule)
	}
	if v.Severity != string(report.SeverityError) {
		t.Errorf("Severity = %q, want %q", v.Severity, report.SeverityError)
	}
	if !strings.Contains(v.Note, "FASE 3.7") {
		t.Errorf("Note should reference FASE 3.7, got %q", v.Note)
	}
	if len(r.Warnings) != 0 {
		t.Errorf("hard-fail import should produce 0 warnings, got %d: %+v", len(r.Warnings), r.Warnings)
	}
}

// TestScanMonitorInfraImport_MarkerSingleLine pins the canonical
// single-line import allowlist pattern:
//
//	// ARCH-ALLOWLIST: monitor-infra-import
//	_ "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/..."
//
// The marker is on line N-1; the import is on line N. The
// scanner MUST NOT surface a violation AND MUST record a
// warning (the marker site is audit-pin residue).
func TestScanMonitorInfraImport_MarkerSingleLine(t *testing.T) {
	root := t.TempDir()
	src := `package monitor

// ARCH-ALLOWLIST: monitor-infra-import
import sqlassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/foo"

type ChannelMonitor struct {
	_ sqlassets.SomeType
}
`
	writeFileFixture(t, root, "internal/application/assets/monitor/allowed.go", src)
	r := &report.Report{}
	ScanMonitorInfraImport(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Errorf("single-line marker should allow the import, got %d violations: %+v", len(r.Violations), r.Violations)
	}
	if len(r.Warnings) != 1 {
		t.Fatalf("marker site should produce 1 warning, got %d: %+v", len(r.Warnings), r.Warnings)
	}
	if !strings.Contains(r.Warnings[0], "ARCH-ALLOWLIST: monitor-infra-import") {
		t.Errorf("warning should mention the marker, got %q", r.Warnings[0])
	}
}

// TestScanMonitorInfraImport_MarkerMultiLine pins the canonical
// multi-line import block allowlist pattern with the marker above
// the enclosing import declaration.
func TestScanMonitorInfraImport_MarkerMultiLine(t *testing.T) {
	root := t.TempDir()
	src := `package monitor

// ARCH-ALLOWLIST: monitor-infra-import
import (
	"context"

	sqlassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/foo"
)

type ChannelMonitor struct {
	_ sqlassets.SomeType
}
`
	writeFileFixture(t, root, "internal/application/assets/monitor/allowed_multi.go", src)
	r := &report.Report{}
	ScanMonitorInfraImport(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Errorf("multi-line marker should allow the import, got %d violations: %+v", len(r.Violations), r.Violations)
	}
}

// TestScanMonitorInfraImport_MarkerInsideMultiLineBlock pins the retained
// shell Check 54 form used by the monitor SQLite tests: the marker sits
// directly above the concrete infrastructure import inside import (...).
func TestScanMonitorInfraImport_MarkerInsideMultiLineBlock(t *testing.T) {
	root := t.TempDir()
	src := `package monitor

import (
	"context"

	_ "github.com/mattn/go-sqlite3"

	// ARCH-ALLOWLIST: monitor-infra-import — owner=@monitor-team; deadline=2026-09-15
	sqlassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
)

type ChannelMonitor struct {
	_ sqlassets.SomeType
}
`
	writeFileFixture(t, root, "internal/application/assets/monitor/allowed_inside_block_test.go", src)
	r := &report.Report{}
	ScanMonitorInfraImport(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Fatalf("marker directly above an import spec should allow it, got %d violations: %+v", len(r.Violations), r.Violations)
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "ARCH-ALLOWLIST: monitor-infra-import") {
		t.Fatalf("marker site should produce one audit warning, got %+v", r.Warnings)
	}
}

// TestScanMonitorInfraImport_CommentOnly pins the descriptive-
// prose path: a line that mentions the infra path as a comment
// (e.g. documentation, sample code) does NOT surface as a
// violation. It DOES produce a warning (the comment-only hit
// is audit-pin residue per godlike/07 no-fake-availability).
func TestScanMonitorInfraImport_CommentOnly(t *testing.T) {
	root := t.TempDir()
	src := `package monitor

// See: github.com/Marcuss-ops/PipelineGen/internal/infrastructure/foo
// for the canonical Adapter concrete.
type ChannelMonitor struct{}
`
	writeFileFixture(t, root, "internal/application/assets/monitor/commented.go", src)
	r := &report.Report{}
	ScanMonitorInfraImport(root, &policy.Policy{}, r)
	if len(r.Violations) != 0 {
		t.Errorf("comment-only hit should produce 0 violations, got %d: %+v", len(r.Violations), r.Violations)
	}
	if len(r.Warnings) != 1 {
		t.Fatalf("comment-only hit should produce 1 warning, got %d: %+v", len(r.Warnings), r.Warnings)
	}
	if !strings.Contains(r.Warnings[0], "comment-only") {
		t.Errorf("warning should mention comment-only, got %q", r.Warnings[0])
	}
}

// TestScanMonitorInfraImport_TestFileIncluded pins the spec's
// _test.go INCLUSION RATIONALE: unlike Checks 0/1/3/5/8/9/11/23
// which exclude *_test.go, Check 54 does NOT. A forbidden
// import in a _test.go file is still reported as a violation
// (the test layer in monitor/ asserts the canonical Pattern-0
// surface via compile-time pins to the infra-side Adapter
// concrete, but the canonical form is the composition-root
// adapter, not a raw infra import in a test file).
func TestScanMonitorInfraImport_TestFileIncluded(t *testing.T) {
	root := t.TempDir()
	src := `package monitor

import (
	sqlassets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets"
)

type ChannelMonitor struct {
	_ sqlassets.SomeType
}
`
	writeFileFixture(t, root, "internal/application/assets/monitor/test_imports_test.go", src)
	r := &report.Report{}
	ScanMonitorInfraImport(root, &policy.Policy{}, r)
	// The _test.go file's import is still reported (1 violation).
	// This matches the spec: the test pin is allowed via the
	// composition-root adapter, NOT via a raw infra import.
	if len(r.Violations) != 1 {
		t.Fatalf("_test.go infra import should be reported (1 violation), got %d: %+v", len(r.Violations), r.Violations)
	}
	if !strings.Contains(r.Violations[0].File, "_test.go") {
		t.Errorf("violation should be on the _test.go file, got %q", r.Violations[0].File)
	}
}

// TestScanMonitorInfraImport_MixedFiles pins the per-file
// classification contract: one tree with a clean file, a
// hard-fail file, and a marker-allowed file produces 1
// violation + 1 warning (marker site) + 0 comment warnings.
func TestScanMonitorInfraImport_MixedFiles(t *testing.T) {
	root := t.TempDir()

	// Clean file: no infra import.
	writeFileFixture(t, root, "internal/application/assets/monitor/clean.go", `package monitor

type ChannelMonitor struct{}
`)

	// Hard-fail file: production import without marker.
	badSrc := `package monitor

import sqlassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/foo"

type Bad struct {
	_ sqlassets.SomeType
}
`
	writeFileFixture(t, root, "internal/application/assets/monitor/bad.go", badSrc)

	// Marker-allowed file: marker+1 single-line.
	allowedSrc := `package monitor

// ARCH-ALLOWLIST: monitor-infra-import
import sqlassets "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/foo"

type Allowed struct {
	_ sqlassets.SomeType
}
`
	writeFileFixture(t, root, "internal/application/assets/monitor/allowed.go", allowedSrc)

	r := &report.Report{}
	ScanMonitorInfraImport(root, &policy.Policy{}, r)
	if len(r.Violations) != 1 {
		t.Errorf("mixed tree should produce 1 violation (bad.go), got %d: %+v", len(r.Violations), r.Violations)
	}
	if len(r.Warnings) != 1 {
		t.Errorf("mixed tree should produce 1 warning (allowed.go marker), got %d: %+v", len(r.Warnings), r.Warnings)
	}
}

// TestIsMarkerLine pins the marker-recognition contract:
// leading whitespace + `//` + `ARCH-ALLOWLIST: monitor-infra-import`.
// Typos in the magic word = lint failure (corruption-safe by
// design per godlike/07).
func TestIsMarkerLine(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"// ARCH-ALLOWLIST: monitor-infra-import", true},
		{"  // ARCH-ALLOWLIST: monitor-infra-import", true},
		{"\t// ARCH-ALLOWLIST: monitor-infra-import", true},
		{"//ARCH-ALLOWLIST: monitor-infra-import", true},   // no space
		{"// ARCH-ALLOWLIST:monitor-infra-import", true},   // no space after colon
		{"// ARCH-ALLOWLIST:  monitor-infra-import", true}, // double space
		{"// ARCH-ALLOWLIST: monitor-infra-impor", false},  // typo
		{"// ARCH-ALLOWLIST admin-infra-import", false},    // missing colon
		{"// just a comment", false},
		{"import \"github.com/Marcuss-ops/PipelineGen/internal/infrastructure\"", false},
		{"", false},
	}
	for _, tc := range cases {
		got := isMarkerLine(tc.line)
		if got != tc.want {
			t.Errorf("isMarkerLine(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

// TestIsMarkerAllowedForImportLine pins the supported marker placements:
// immediately above a single-line import, immediately above the concrete
// spec inside import (...), or immediately above the enclosing import (.
func TestIsMarkerAllowedForImportLine(t *testing.T) {
	cases := []struct {
		name        string
		markerLines []int
		lines       []string // 0-indexed slice
		currentLine int      // 1-indexed
		want        bool
	}{
		{
			name:        "single-line: marker on currentLine-1",
			markerLines: []int{3},
			lines: []string{
				"package x",
				"",
				"// ARCH-ALLOWLIST: monitor-infra-import",
				`import "path"`,
			},
			currentLine: 4,
			want:        true,
		},
		{
			name:        "multi-line: marker on import( - 1",
			markerLines: []int{3},
			lines: []string{
				"package x",
				"",
				"// ARCH-ALLOWLIST: monitor-infra-import",
				"import (",
				"\t\"path\"",
			},
			currentLine: 5,
			want:        true,
		},
		{
			name:        "multi-line deep: marker on import( - 1, import on a later line",
			markerLines: []int{3},
			lines: []string{
				"package x",
				"",
				"// ARCH-ALLOWLIST: monitor-infra-import",
				"import (",
				"\t\"y\"",
				"",
				"\t\"path\"",
			},
			currentLine: 7,
			want:        true,
		},
		{
			name:        "multi-line: marker directly above concrete import spec",
			markerLines: []int{6},
			lines: []string{
				"package x",
				"",
				"import (",
				"\t\"context\"",
				"",
				"\t// ARCH-ALLOWLIST: monitor-infra-import",
				"\t\"path\"",
				")",
			},
			currentLine: 7,
			want:        true,
		},
		{
			name:        "marker too far (single-line): marker on currentLine-2",
			markerLines: []int{2},
			lines: []string{
				"package x",
				"// ARCH-ALLOWLIST: monitor-infra-import",
				"",
				`import "path"`,
			},
			currentLine: 4,
			want:        false,
		},
		{
			name:        "marker too far (multi-line): marker on import( - 2",
			markerLines: []int{2},
			lines: []string{
				"package x",
				"// ARCH-ALLOWLIST: monitor-infra-import",
				"",
				"import (",
				"\t\"path\"",
			},
			currentLine: 5,
			want:        false,
		},
		{
			name:        "no marker",
			markerLines: nil,
			lines: []string{
				"package x",
				`import "path"`,
			},
			currentLine: 2,
			want:        false,
		},
		{
			name:        "no enclosing import (var declaration)",
			markerLines: []int{2},
			lines: []string{
				"package x",
				"// ARCH-ALLOWLIST: monitor-infra-import",
				"var Y = 1",
			},
			currentLine: 3,
			want:        false,
		},
		{
			name:        "closed import block cannot authorize later literal",
			markerLines: []int{2},
			lines: []string{
				"package x",
				"// ARCH-ALLOWLIST: monitor-infra-import",
				"import (",
				"\t\"context\"",
				")",
				`var Y = "path"`,
			},
			currentLine: 6,
			want:        false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isMarkerAllowedForImportLine(tc.markerLines, tc.lines, tc.currentLine)
			if got != tc.want {
				t.Errorf("isMarkerAllowedForImportLine(...) = %v, want %v", got, tc.want)
			}
		})
	}
}
