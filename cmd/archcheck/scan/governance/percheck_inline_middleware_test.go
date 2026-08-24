// Package scan — TDD tests for percheck_inline_middleware.go
// (architecture/current.yaml#SCRIPT-FLOW-SPLIT.linked_issues[PR-CHECK-58-INLINE-MIDDLEWARE]).
//
// These tests construct isolated fixture directory trees under
// t.TempDir() that mirror the production internal/api/<feature>/
// shape, and verify the scanner's 3-bucketing contract:
//
//  1. UNDER-THRESHOLD + signature → no violation (size condition
//     alone exempts a file regardless of its signature content)
//  2. OVER-THRESHOLD + signature → violation emitted (the
//     compound gate fires; canonical extraction candidate)
//  3. OVER-THRESHOLD + no signature → no violation (size alone
//     does NOT trigger the gate — middleware-agnostic bloat is
//     out of scope for this forward-prevention rule)
//  4. middleware_auth.go (canonical leaf-name) → no violation even
//     when over-threshold + signature (the canonical home is
//     exempted by SCRIPT-FLOW-SPLIT precedent)
//  5. _test.go → no violation (test files exempted per AGENTS.md
//     Pattern 8 + the CANonICAL_PATTERN_0 contract)
//
// The tests NEVER touch the production tree (root = t.TempDir())
// so they cannot accidentally trigger false-positives on real
// code, and they parallel-safely because each test owns its own
// t.TempDir() copy.
//
// Per godlike/06 SSOT (one canonical owner per fact):
// percheck_inline_middleware.go is the canonical SOLE owner of
// the inline-middleware-for-feature-route detection contract;
// these tests are the canonical SOLE verification surface. Any
// future change to the violation note format or signature list
// MUST update both the production code AND these tests in the
// same PR (godlike/07 no-fake-availability).
package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// writeFileUnderTemp creates a file at <tmpRoot>/<relPath> with
// the given contents. Uses 0644 perms. Helper to keep the test
// bodies focused on the bucket-shape assertions rather than
// os.WriteFile boilerplate. Returns the absolute path of the
// created file.
func writeFileUnderTemp(t *testing.T, tmpRoot, relPath, contents string) string {
	t.Helper()
	abs := filepath.Join(tmpRoot, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(abs, []byte(contents), 0o644); err != nil {
		t.Fatalf("writeFile %s: %v", abs, err)
	}
	return abs
}

// TestScanInlineMiddleware_PassUnderThresholdWithSignature
// verifies the size-condition gate. A small file (50 LoC) with
// the inline-middleware signature EnableAuth + AdminTokenProvider
// present MUST NOT emit a Violation — the 300-LoC threshold is
// not exceeded, so the compound gate does not fire.
func TestScanInlineMiddleware_PassUnderThresholdWithSignature(t *testing.T) {
	tmp := t.TempDir()
	// Build internal/api/<feature>/ path mirroring the production layout.
	writeFileUnderTemp(t, tmp, "internal/api/script/small_with_sig.go", buildBodyWithSignedBlock(50, true))

	r := &report.Report{}
	ScanInlineMiddleware(tmp, &policy.Policy{}, r)

	if len(r.Violations) != 0 {
		t.Fatalf("expected 0 violations for small + signed file, got %d: %+v",
			len(r.Violations), r.Violations)
	}
}

// TestScanInlineMiddleware_FailOverThresholdWithSignature is
// the canonical trigger case: a 350-LoC file with the
// inline-middleware signature. The compound gate MUST fire and
// emit exactly 1 Violation with ActualLines=350, MaxLines=300,
// and the canonical violation note template (byte-stable per
// godlike/06 SSOT — shell + Go scanners produce equivalent output).
func TestScanInlineMiddleware_FailOverThresholdWithSignature(t *testing.T) {
	tmp := t.TempDir()
	writeFileUnderTemp(t, tmp, "internal/api/script/large_with_sig.go", buildBodyWithSignedBlock(350, true))

	r := &report.Report{}
	ScanInlineMiddleware(tmp, &policy.Policy{}, r)

	if len(r.Violations) != 1 {
		t.Fatalf("expected exactly 1 violation for large + signed file, got %d: %+v",
			len(r.Violations), r.Violations)
	}
	v := r.Violations[0]
	if v.ActualLines != 350 {
		t.Fatalf("expected ActualLines=350, got %d (violation: %+v)", v.ActualLines, v)
	}
	if v.MaxLines != 300 {
		t.Fatalf("expected MaxLines=300, got %d (violation: %+v)", v.MaxLines, v)
	}
	// Canonical note template byte-stable check (godlike/06 SSOT):
	// the violation note MUST contain the canonical extraction
	// directive + AGENTS.md Pattern 5 + SCRIPT-FLOW-SPLIT precedent
	// substring so shell + Go scanners produce equivalent
	// operator-facing remediation guidance.
	wantSubstr := "extract to internal/api/script/middleware_auth.go per AGENTS.md Pattern 5 + SCRIPT-FLOW-SPLIT precedent"
	if !strings.Contains(v.Note, wantSubstr) {
		t.Fatalf("expected Note to contain %q; got %q", wantSubstr, v.Note)
	}
}

// TestScanInlineMiddleware_PassOverThresholdNoSignature
// verifies the signature-condition gate. An oversized 350-LoC file
// WITHOUT any of the 4 signatures MUST NOT emit a Violation —
// middleware-agnostic bloat is out of scope for the
// forward-prevention rule. The compound gate fails when either
// condition is unmet.
func TestScanInlineMiddleware_PassOverThresholdNoSignature(t *testing.T) {
	tmp := t.TempDir()
	writeFileUnderTemp(t, tmp, "internal/api/script/large_no_sig.go", buildBodyWithSignedBlock(350, false))

	r := &report.Report{}
	ScanInlineMiddleware(tmp, &policy.Policy{}, r)

	if len(r.Violations) != 0 {
		t.Fatalf("expected 0 violations for large + unsigned file (middleware-agnostic bloat), got %d: %+v",
			len(r.Violations), r.Violations)
	}
}

// TestScanInlineMiddleware_PassMiddlewareAuthExempt verifies the
// canonical-leaf-name exemption. A 350-LoC file NAMED
// middleware_auth.go with the inline-middleware signature MUST
// NOT emit a Violation — by SCRIPT-FLOW-SPLIT precedent, the
// canonical home of the 4-element auth cluster is exempted
// regardless of LoC. This is the negative-test for the
// middlewareAuthLeafName exclusion in scanInlineMiddlewareFile.
func TestScanInlineMiddleware_PassMiddlewareAuthExempt(t *testing.T) {
	tmp := t.TempDir()
	writeFileUnderTemp(t, tmp, "internal/api/script/middleware_auth.go", buildBodyWithSignedBlock(350, true))

	r := &report.Report{}
	ScanInlineMiddleware(tmp, &policy.Policy{}, r)

	if len(r.Violations) != 0 {
		t.Fatalf("expected 0 violations for middleware_auth.go exemption, got %d: %+v",
			len(r.Violations), r.Violations)
	}
}

// TestScanInlineMiddleware_ExcludesTestFiles verifies the _test.go
// glob exclusion. A 350-LoC _test.go file with the signature MUST
// NOT emit a Violation — tests may freely reference the canonical
// port (mock-driving AdminTokenProvider) without affecting the
// production-contract gate. Mirrors Check 54's _test.go
// exclusion rationale.
func TestScanInlineMiddleware_ExcludesTestFiles(t *testing.T) {
	tmp := t.TempDir()
	writeFileUnderTemp(t, tmp, "internal/api/script/handler_test.go", buildBodyWithSignedBlock(350, true))

	r := &report.Report{}
	ScanInlineMiddleware(tmp, &policy.Policy{}, r)

	if len(r.Violations) != 0 {
		t.Fatalf("expected 0 violations for _test.go exclusion, got %d: %+v",
			len(r.Violations), r.Violations)
	}
}

// buildBodyWithSignedBlock synthesises a Go file body of
// `totalLines` lines. If `signed` is true, it embeds the
// canonical 4-element auth cluster (RequireAdminToken +
// extractHeaderToken + EnableAuth + AdminTokenProvider) as
// inline middleware. If `signed` is false, it produces a body with
// 0 of the 4 signatures (purely comment + boilerplate content).
//
// The helper produces a syntactically VALID Go file so the
// scanner's regex-based substring scan is the only condition
// exercised (no parser-level false negatives). The final byte is
// always `\n` so the line count produced by `wc -l` and the
// percheck scanner's bufio.Scanner agree exactly.
func buildBodyWithSignedBlock(totalLines int, signed bool) string {
	var b strings.Builder
	b.WriteString("package script\n\n")
	if signed {
		b.WriteString("import (\n\t\"context\"\n\t\"github.com/gin-gonic/gin\"\n)\n\n")
		// Inline-middleware signatures deliberately placed in a
		// non-middleware_auth.go file to trigger the gate when
		// totalLines > 300.
		b.WriteString("type AdminTokenProvider interface {\n")
		b.WriteString("\tEnableAuth() bool\n")
		b.WriteString("\tAdminToken() string\n")
		b.WriteString("}\n\n")
		b.WriteString("type featureHandler struct{}\n\n")
		b.WriteString("func (h *featureHandler) EnableAuth() bool { return false }\n")
		b.WriteString("func (h *featureHandler) AdminToken() string { return \"\" }\n\n")
		b.WriteString("func extractHeaderToken(c *gin.Context) string { return c.GetHeader(\"X-Token\") }\n\n")
		b.WriteString("func RequireAdminToken(_ AdminTokenProvider) gin.HandlerFunc {\n")
		b.WriteString("\treturn func(c *gin.Context) { _ = context.TODO(); c.Next() }\n")
		b.WriteString("}\n\n")
		b.WriteString("var _ AdminTokenProvider = (*featureHandler)(nil)\n\n")
	} else {
		b.WriteString("import \"context\"\n\n")
		b.WriteString("type featureHandler struct{}\n\n")
		b.WriteString("func (h *featureHandler) Handle(_ context.Context) error { return nil }\n\n")
	}
	// Pad to totalLines via comment lines (mirrors real-world
	// feature-routing files which carry extensive prose
	// documentation). Each comment line is one logical line per
	// wc -l semantics. The trailing `\n` on every WriteString
	// ensures the final byte is `\n` so scanner line-count ==
	// wc -l count.
	headerLines := countNewlines(b.String())
	for i := 0; i < totalLines-headerLines; i++ {
		b.WriteString("// padding line for size threshold test (no forbidden signatures)\n")
	}
	return b.String()
}

// countNewlines is a tiny helper that returns the number of
// newline characters in s (mirrors `wc -l <file>` line-count
// semantics for newline-terminated files).
func countNewlines(s string) int {
	return strings.Count(s, "\n")
}
