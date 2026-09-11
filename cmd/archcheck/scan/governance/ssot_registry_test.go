// Package scan — ssot_registry_test.go
//
// Pins the invariants of the generic SSOT engine (ssot.go) and its registry
// (ssot_registry.go):
//
//   - rule ids are unique and the registry has not silently shrunk;
//   - every rule carries a detector and a matched-rule reason;
//   - the engine honours scope, owners, skip prefixes, test-file and comment
//     policy generically (so a new registry row inherits them for free);
//   - every exported ScanXxx entry point resolves to a registered rule.
//
// These are the guarantees that let a new forward-prevention fact be a
// registry row instead of a new ~130-line scanner.
package governance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// ssotRegistryWantRules is the pinned registry. Changing it is a deliberate
// act: the test exists so a fact cannot be dropped by accident.
var ssotRegistryWantRules = []string{
	embeddingConstantsRule,
	durationProbeSSOTRule,
	observabilityOperationSSOTRule,
	speechTimingSSOTRule,
	projectDerivationSSOTRule,
	evidencePrecedenceSSOTRule,
	stopwordMapRule,
	metadataKeyScannerRule,
	indexedStateWriterSSOTRule,
	assetCommitterEventSSOTRule,
}

func writeSSOTFixture(t *testing.T, root, rel, body string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", filepath.Dir(full), err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write %q: %v", full, err)
	}
}

func newSSOTReport() *report.Report {
	return &report.Report{Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}}}
}

func ssotViolationsForRule(r *report.Report, rule string) []report.Violation {
	var out []report.Violation
	for _, v := range r.Violations {
		if v.Rule == rule {
			out = append(out, v)
		}
	}
	return out
}

// TestSSOTRegistryMatchesPinnedSet verifies the registry still contains
// exactly the expected rules, in order and with unique ids.
func TestSSOTRegistryMatchesPinnedSet(t *testing.T) {
	got := ssotRuleNames()
	if len(got) != len(ssotRegistryWantRules) {
		t.Fatalf("registry has %d rules, want %d: %v", len(got), len(ssotRegistryWantRules), got)
	}
	for i, want := range ssotRegistryWantRules {
		if got[i] != want {
			t.Errorf("rule[%d] = %q, want %q", i, got[i], want)
		}
	}
	seen := map[string]bool{}
	for _, name := range got {
		if seen[name] {
			t.Errorf("duplicate rule id %q", name)
		}
		seen[name] = true
	}
}

// TestSSOTRegistryRulesAreComplete verifies each row is usable by the engine.
func TestSSOTRegistryRulesAreComplete(t *testing.T) {
	for _, rule := range ssotRules {
		if rule.Name == "" {
			t.Errorf("rule has empty Name: %+v", rule)
		}
		if rule.MatchedRule == "" {
			t.Errorf("rule %q has empty MatchedRule", rule.Name)
		}
		if rule.Detect == nil && rule.ScanFile == nil {
			t.Errorf("rule %q has neither a Detect nor a ScanFile func (would scan nothing and always pass)", rule.Name)
		}
	}
}

// TestSSOTRuleByNamePanicsOnUnknown pins the fail-closed lookup: an
// unregistered id must panic rather than silently scanning nothing.
func TestSSOTRuleByNamePanicsOnUnknown(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("ssotRuleByName must panic on an unregistered rule id")
		}
	}()
	ssotRuleByName("percheck_this_rule_does_not_exist")
}

// TestSSOTEntryPointsAreWired verifies every exported entry point resolves to
// a registered rule and runs clean on an empty tree.
func TestSSOTEntryPointsAreWired(t *testing.T) {
	dir := t.TempDir()
	r := newSSOTReport()
	ScanEmbeddingConstantsSSOT(dir, nil, r, true)
	ScanDurationProbeSSOT(dir, &policy.Policy{}, r)
	ScanObservabilityOperationSSOT(dir, &policy.Policy{}, r)
	ScanSpeechTimingSSOT(dir, &policy.Policy{}, r)
	ScanProjectDerivationSSOT(dir, &policy.Policy{}, r)
	ScanEvidencePrecedenceSSOT(dir, &policy.Policy{}, r)
	ScanStopwordMapsInApp(dir, &policy.Policy{}, r)
	ScanMetadataKeys(dir, &policy.Policy{}, r)
	ScanIndexedStateWriterSSOT(dir, &policy.Policy{}, r)
	ScanAssetCommitterEventSSOT(dir, &policy.Policy{}, r, true)
	for _, v := range r.Violations {
		// The metadata-key rule fail-closes on a missing canonical registry,
		// so on an empty tree it legitimately emits that typed config
		// violation; every other rule must stay silent.
		if v.MatchedRule == "registry_canonical_missing" {
			continue
		}
		t.Fatalf("empty tree must be clean, got %+v", v)
	}
}

// TestSSOTEngineHonoursScopeOwnersAndExemptions exercises the generic engine
// through one rule: out-of-scope, owner and test files are skipped, an
// in-scope non-owner file with a comment decoy only violates on the real line.
func TestSSOTEngineHonoursScopeOwnersAndExemptions(t *testing.T) {
	dir := t.TempDir()
	// Out of scope: the embedding rule's Scope is internal/ only.
	writeSSOTFixture(t, dir, "cmd/tool/decl.go", "package tool\n\nconst m = \"multilingual-e5-base\"\n")
	// Owner package: exempt.
	writeSSOTFixture(t, dir, "internal/kernel/models/registry_x.go", "package models\n\nconst m = \"multilingual-e5-base\"\n")
	// Test file: exempt.
	writeSSOTFixture(t, dir, "internal/foo/decl_test.go", "package foo\n\nconst m = \"multilingual-e5-base\"\n")
	// In scope, non-owner, comment decoy + real declaration.
	writeSSOTFixture(t, dir, "internal/foo/decl.go",
		"package foo\n\n// const m = \"multilingual-e5-base\"\nconst m = \"multilingual-e5-base\"\n")

	r := newSSOTReport()
	ScanEmbeddingConstantsSSOT(dir, nil, r, true)

	got := ssotViolationsForRule(r, embeddingConstantsRule)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 violation (the in-scope declaration), got %d: %+v", len(got), got)
	}
	v := got[0]
	if v.File != "internal/foo/decl.go" {
		t.Errorf("File = %q, want internal/foo/decl.go", v.File)
	}
	if v.Line != 4 {
		t.Errorf("Line = %d, want 4 (the comment decoy on line 3 must not match)", v.Line)
	}
	if v.MatchedRule != "non_canonical_embedding_constant" {
		t.Errorf("MatchedRule = %q, want non_canonical_embedding_constant", v.MatchedRule)
	}
	if v.Package != "internal/foo" {
		t.Errorf("Package = %q, want internal/foo", v.Package)
	}
	if v.Severity != string(report.SeverityError) {
		t.Errorf("Severity = %q, want error", v.Severity)
	}
}

// TestSSOTEngineSkipsScannerSource verifies the always-on scanner-package
// exemption: every rule may name its forbidden literal in its own docs.
func TestSSOTEngineSkipsScannerSource(t *testing.T) {
	dir := t.TempDir()
	writeSSOTFixture(t, dir, ssotScannerSourcePrefix+"/doc.go", "package scan\n\nconst m = \"multilingual-e5-base\"\n")

	r := newSSOTReport()
	ScanEmbeddingConstantsSSOT(dir, nil, r, true)
	if got := ssotViolationsForRule(r, embeddingConstantsRule); len(got) != 0 {
		t.Errorf("scanner source must be exempt, got %+v", got)
	}
}

// TestSSOTEngineObservabilityMultiCondition pins the one rule that emits
// independent notes from two conditions on the same line, with the
// canonical-store carve-out driven by the file path.
func TestSSOTEngineObservabilityMultiCondition(t *testing.T) {
	dir := t.TempDir()
	// Non-canonical store: second writer violates.
	writeSSOTFixture(t, dir, "internal/foo/writer.go", "package foo\n\nvar q = `INSERT INTO performance_operations`\n")
	// Canonical store: the same write is allowed.
	writeSSOTFixture(t, dir, "internal/platform/sqlite/performance/store.go", "package performance\n\nvar q = `INSERT INTO performance_operations`\n")
	// Retired recorder violates anywhere.
	writeSSOTFixture(t, dir, "internal/bar/rec.go", "package bar\n\nvar _ MeasuredOperationRecorder\n")

	r := newSSOTReport()
	ScanObservabilityOperationSSOT(dir, &policy.Policy{}, r)

	got := ssotViolationsForRule(r, observabilityOperationSSOTRule)
	if len(got) != 2 {
		t.Fatalf("want 2 violations (second writer + retired recorder), got %d: %+v", len(got), got)
	}
	var sawWriter, sawRecorder bool
	for _, v := range got {
		switch v.File {
		case "internal/foo/writer.go":
			sawWriter = true
		case "internal/bar/rec.go":
			sawRecorder = true
		case "internal/platform/sqlite/performance/store.go":
			t.Errorf("canonical projection store must be exempt, got %+v", v)
		}
		if v.MatchedRule != "observability_operation_single_writer" {
			t.Errorf("MatchedRule = %q, want observability_operation_single_writer", v.MatchedRule)
		}
	}
	if !sawWriter || !sawRecorder {
		t.Errorf("missing expected violation (writer=%v recorder=%v)", sawWriter, sawRecorder)
	}
}
