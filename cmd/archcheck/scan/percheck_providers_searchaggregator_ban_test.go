// Package scan — companion test for percheck_providers_searchaggregator_ban.go.
//
// Pins:
//   (a) "legacy literal trips" — a probe file that references
//       `providers.SearchAggregator` in production code trips
//       a violation.
//   (b) "comment-only is residue-accounted" — the SAME literal
//       in a `//` doc-comment line emits a WARN (not a violation).
//   (c) "scan-root self-package is exempt" — a probe file inside
//       `cmd/archcheck/scan/` referencing the literal does NOT
//       trip (false-positive exemption for the scanner's own
//       package).
//   (d) "test files are exempt" — a `_test.go` probe file
//       referencing the literal does NOT trip (regression-guard
//       allowlist per godlike/06).
package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// TestScanProvidersSearchAggregatorBan_LegacyLiteralTrips verifies
// that a production Go file referencing `providers.SearchAggregator`
// emits a violation (not a WARN).
func TestScanProvidersSearchAggregatorBan_LegacyLiteralTrips(t *testing.T) {
	tmp := t.TempDir()
	probeFile := filepath.Join(tmp, "internal/app/legacy_aggregator_probe.go")
	probeBody := `package app

import (
	providers "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
)

// LegacyAggregatorProbe is a probe struct that deliberately references
// the legacy providers.SearchAggregator god-service literal so the
// forward-prevention gate surfaces a violation.
type LegacyAggregatorProbe struct{}

func (LegacyAggregatorProbe) Use(reg *providers.Registry) *providers.SearchAggregator {
	return nil
}
`
	if err := os.WriteFile(probeFile, []byte(probeBody), 0o644); err != nil {
		t.Fatalf("write probe file: %v", err)
	}

	r := &report.Report{}
	ScanProvidersSearchAggregatorBan(tmp, &policy.Policy{}, r)

	if len(r.Violations) == 0 {
		t.Fatalf("expected at least one violation for legacy literal; got 0")
	}
	found := false
	for _, v := range r.Violations {
		if v.Rule == providersSearchAggregatorRule &&
			v.File == "internal/app/legacy_aggregator_probe.go" &&
			v.MatchedRule == "forbidden_legacy_search_aggregator_reference" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected violation for legacy literal probe; got=%+v", r.Violations)
	}
}

// TestScanProvidersSearchAggregatorBan_CommentOnlyIsResidueAccounted
// verifies that the same literal inside a `//` doc-comment line
// emits a single WARN per file (not a violation).
func TestScanProvidersSearchAggregatorBan_CommentOnlyIsResidueAccounted(t *testing.T) {
	tmp := t.TempDir()
	probeFile := filepath.Join(tmp, "internal/app/legacy_comment_probe.go")
	probeBody := `package app

// LegacyCommentProbe is a probe struct whose docstring mentions
// the banned literal providers.SearchAggregator so the residue-
// accounting WARN surfaces (NOT a violation, per godlike/07).
type LegacyCommentProbe struct{}
`
	if err := os.WriteFile(probeFile, []byte(probeBody), 0o644); err != nil {
		t.Fatalf("write probe file: %v", err)
	}

	r := &report.Report{}
	ScanProvidersSearchAggregatorBan(tmp, &policy.Policy{}, r)

	for _, v := range r.Violations {
		if v.File == "internal/app/legacy_comment_probe.go" {
			t.Errorf("comment-only line should not emit a violation; got=%+v", v)
		}
	}
	foundWarn := false
	for _, w := range r.Warnings {
		if w == providersSearchAggregatorRule+
			" banned-literal: comment-only reference(s) to providers.SearchAggregator in internal/app/legacy_comment_probe.go (descriptive prose; non-fatal per godlike/07 no-fake-availability; replace or remove before next sweep)" {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Errorf("expected residue-accounting WARN; got warnings=%+v", r.Warnings)
	}
}

// TestScanProvidersSearchAggregatorBan_ScanRootSelfPackageIsExempt
// verifies that a probe file inside `cmd/archcheck/scan/` does
// NOT emit a violation for the banned literal (false-positive
// exemption).
func TestScanProvidersSearchAggregatorBan_ScanRootSelfPackageIsExempt(t *testing.T) {
	tmp := t.TempDir()
	probeFile := filepath.Join(tmp, "cmd/archcheck/scan/self_probe.go")
	if err := os.MkdirAll(filepath.Dir(probeFile), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	probeBody := `package scan

const selfProbeLiteral = "providers.SearchAggregator"
`
	if err := os.WriteFile(probeFile, []byte(probeBody), 0o644); err != nil {
		t.Fatalf("write probe file: %v", err)
	}

	r := &report.Report{}
	ScanProvidersSearchAggregatorBan(tmp, &policy.Policy{}, r)

	for _, v := range r.Violations {
		if v.File == "cmd/archcheck/scan/self_probe.go" {
			t.Errorf("scan-root self-package should be exempt; got violation=%+v", v)
		}
	}
}

// TestScanProvidersSearchAggregatorBan_TestFilesAreExempt verifies
// that a `_test.go` probe file does NOT emit a violation (regression-
// guard allowlist per godlike/06).
func TestScanProvidersSearchAggregatorBan_TestFilesAreExempt(t *testing.T) {
	tmp := t.TempDir()
	probeFile := filepath.Join(tmp, "internal/app/probe_test_target_test.go")
	probeBody := `package app

import _ "context"

// This test file references providers.SearchAggregator in a
// doc-comment. Regression-guard allowlist must skip _test.go.
type _ struct{}
`
	if err := os.WriteFile(probeFile, []byte(probeBody), 0o644); err != nil {
		t.Fatalf("write probe file: %v", err)
	}

	r := &report.Report{}
	ScanProvidersSearchAggregatorBan(tmp, &policy.Policy{}, r)

	for _, v := range r.Violations {
		if v.File == "internal/app/probe_test_target_test.go" {
			t.Errorf("_test.go files should be exempt; got violation=%+v", v)
		}
	}
	for _, w := range r.Warnings {
		if w == providersSearchAggregatorRule+
			" banned-literal: comment-only reference(s) to providers.SearchAggregator in internal/app/probe_test_target_test.go (descriptive prose; non-fatal per godlike/07 no-fake-availability; replace or remove before next sweep)" {
			t.Errorf("_test.go files should be exempt from WARN too; got=%q", w)
		}
	}
}
