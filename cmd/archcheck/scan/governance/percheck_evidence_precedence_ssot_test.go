// Package scan — percheck_evidence_precedence_ssot_test.go pins the
// forward-prevention contract for the canonical evidence-precedence ban.
package governance

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func evidencePrecedenceTestReport() *report.Report {
	return &report.Report{Summary: report.Summary{ByReason: map[string]int{}, BySeverity: map[string]int{}}}
}

func evidencePrecedenceWriteTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, contents := range files {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", filepath.Dir(full), err)
		}
		if err := os.WriteFile(full, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %q: %v", full, err)
		}
	}
}

func evidencePrecedenceViolations(r *report.Report) []report.Violation {
	var out []report.Violation
	for _, v := range r.Violations {
		if v.Rule == evidencePrecedenceSSOTRule {
			out = append(out, v)
		}
	}
	return out
}

func TestEvidencePrecedenceSSOT_TranscriptFirstHelperViolates(t *testing.T) {
	dir := t.TempDir()
	evidencePrecedenceWriteTree(t, dir, map[string]string{
		"internal/foo/grounding.go": `package foo
func pick() string { return firstNonEmpty("transcript", "semantic_summary", "description") }
`,
	})
	r := evidencePrecedenceTestReport()
	ScanEvidencePrecedenceSSOT(dir, &policy.Policy{}, r)
	if got := len(evidencePrecedenceViolations(r)); got != 1 {
		t.Fatalf("want 1 violation, got %d: %+v", got, r.Violations)
	}
}

func TestEvidencePrecedenceSSOT_CanonicalResolverPasses(t *testing.T) {
	dir := t.TempDir()
	evidencePrecedenceWriteTree(t, dir, map[string]string{
		"internal/kernel/asset/evidence.go": `package asset
func ResolveEvidence(in EvidenceInput) EvidenceDocument { return EvidenceDocument{} }
`,
	})
	r := evidencePrecedenceTestReport()
	ScanEvidencePrecedenceSSOT(dir, &policy.Policy{}, r)
	if got := len(evidencePrecedenceViolations(r)); got != 0 {
		t.Fatalf("want 0 violations in the canonical owner, got %d: %+v", got, r.Violations)
	}
}

func TestEvidencePrecedenceSSOT_ProducerViaResolveEvidencePasses(t *testing.T) {
	dir := t.TempDir()
	evidencePrecedenceWriteTree(t, dir, map[string]string{
		"internal/foo/grounding.go": `package foo
import "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
func pick(clip *asset.Asset, transcript string) asset.EvidenceDocument {
	return asset.ResolveEvidence(asset.EvidenceInput{AssetID: clip.ID, Transcript: transcript, Description: clip.GetMetadataString("description")})
}
`,
	})
	r := evidencePrecedenceTestReport()
	ScanEvidencePrecedenceSSOT(dir, &policy.Policy{}, r)
	if got := len(evidencePrecedenceViolations(r)); got != 0 {
		t.Fatalf("want 0 violations for canonical ResolveEvidence consumers, got %d: %+v", got, r.Violations)
	}
}

func TestEvidencePrecedenceSSOT_DescriptionOnlyReconstructionPasses(t *testing.T) {
	// A description-first historical reconstruction with no transcript tier is
	// not a copy of the grounding precedence and must NOT be flagged.
	dir := t.TempDir()
	evidencePrecedenceWriteTree(t, dir, map[string]string{
		"internal/platform/qdrant/recovery/reader.go": `package recovery
func firstString(m map[string]any, keys ...string) string { return "" }
func load(p map[string]any) { _ = firstString(p, "description", "semantic_description", "visual_summary") }
`,
	})
	r := evidencePrecedenceTestReport()
	ScanEvidencePrecedenceSSOT(dir, &policy.Policy{}, r)
	if got := len(evidencePrecedenceViolations(r)); got != 0 {
		t.Fatalf("want 0 violations for description-only reconstruction, got %d: %+v", got, r.Violations)
	}
}

func TestEvidencePrecedenceSSOT_TestFilesExempt(t *testing.T) {
	dir := t.TempDir()
	evidencePrecedenceWriteTree(t, dir, map[string]string{
		"internal/foo/grounding_test.go": `package foo
var _ = firstNonEmpty("transcript", "summary")
`,
	})
	r := evidencePrecedenceTestReport()
	ScanEvidencePrecedenceSSOT(dir, &policy.Policy{}, r)
	if got := len(evidencePrecedenceViolations(r)); got != 0 {
		t.Fatalf("want 0 violations in test files, got %d", got)
	}
}
