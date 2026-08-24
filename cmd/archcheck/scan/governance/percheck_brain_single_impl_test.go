// Package scan — percheck_brain_single_impl_test.go
//
// Pins the canonical brain single-implementation gate.
package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func makeBrainSingleImplFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestScanBrainSingleImpl_OneImplPasses(t *testing.T) {
	root := t.TempDir()
	makeBrainSingleImplFile(t, root, "internal/application/brain/normalizer/normalizer.go",
		`package normalizer

func NewDefaultNormalizer() PhraseNormalizer { return nil }
`)

	r := &report.Report{}
	ScanBrainSingleImpl(root, &policy.Policy{}, r)

	for _, v := range r.Violations {
		if v.Rule == brainSingleImplRule {
			t.Fatalf("expected no single-impl violations, got %+v", v)
		}
	}
}

func TestScanBrainSingleImpl_DuplicateConstructorsViolate(t *testing.T) {
	root := t.TempDir()
	makeBrainSingleImplFile(t, root, "internal/application/brain/normalizer/a.go",
		`package normalizer

func NewDefaultNormalizer() PhraseNormalizer { return nil }
`)
	makeBrainSingleImplFile(t, root, "internal/application/brain/normalizer/b.go",
		`package normalizer

func NewDefaultNormalizer() PhraseNormalizer { return nil }
`)

	r := &report.Report{}
	ScanBrainSingleImpl(root, &policy.Policy{}, r)

	if len(r.Violations) != 1 {
		t.Fatalf("expected exactly 1 violation, got %d", len(r.Violations))
	}
	if r.Violations[0].Rule != brainSingleImplRule {
		t.Errorf("rule = %q, want %q", r.Violations[0].Rule, brainSingleImplRule)
	}
	if r.Violations[0].MatchedRule != "brain_component_multiple_impls" {
		t.Errorf("matchedRule = %q, want brain_component_multiple_impls", r.Violations[0].MatchedRule)
	}
}

func TestScanBrainSingleImpl_AdaptersSubdirIgnored(t *testing.T) {
	root := t.TempDir()
	makeBrainSingleImplFile(t, root, "internal/application/brain/normalizer/normalizer.go",
		`package normalizer

func NewDefaultNormalizer() PhraseNormalizer { return nil }
`)
	makeBrainSingleImplFile(t, root, "internal/application/brain/normalizer/adapters/second.go",
		`package adapters

func NewDefaultNormalizer() PhraseNormalizer { return nil }
`)

	r := &report.Report{}
	ScanBrainSingleImpl(root, &policy.Policy{}, r)

	for _, v := range r.Violations {
		if v.Rule == brainSingleImplRule {
			t.Fatalf("adapters/ subdir must not count, got %+v", v)
		}
	}
}

func TestScanBrainSingleImpl_TestFilesIgnored(t *testing.T) {
	root := t.TempDir()
	makeBrainSingleImplFile(t, root, "internal/application/brain/normalizer/normalizer.go",
		`package normalizer

func NewDefaultNormalizer() PhraseNormalizer { return nil }
`)
	makeBrainSingleImplFile(t, root, "internal/application/brain/normalizer/normalizer_test.go",
		`package normalizer

func NewDefaultNormalizer() PhraseNormalizer { return nil }
`)

	r := &report.Report{}
	ScanBrainSingleImpl(root, &policy.Policy{}, r)

	for _, v := range r.Violations {
		if v.Rule == brainSingleImplRule {
			t.Fatalf("_test.go must not count, got %+v", v)
		}
	}
}

func TestScanBrainSingleImpl_ZeroImplWarns(t *testing.T) {
	root := t.TempDir()
	// No files in the canonical package.
	if err := os.MkdirAll(filepath.Join(root, "internal/application/brain/normalizer"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	r := &report.Report{}
	ScanBrainSingleImpl(root, &policy.Policy{}, r)

	for _, v := range r.Violations {
		if v.Rule == brainSingleImplRule {
			t.Fatalf("zero implementations must not produce a violation, got %+v", v)
		}
	}
	found := false
	for _, w := range r.Warnings {
		if strings.Contains(w, brainSingleImplRule) && strings.Contains(w, "PhraseNormalizer") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected zero-impl warning for PhraseNormalizer, got %v", r.Warnings)
	}
}

func TestScanBrainSingleImpl_SearchFanOutOnlyCountsItself(t *testing.T) {
	root := t.TempDir()
	makeBrainSingleImplFile(t, root, "internal/capabilities/assets/search/registry.go",
		`package search

func NewBackendRegistry() *BackendRegistry { return nil }
`)
	makeBrainSingleImplFile(t, root, "internal/capabilities/assets/search/telemetry.go",
		`package search

func NewSearchFanOut(inner *Aggregator) SearchFanOut { return nil }
`)

	r := &report.Report{}
	ScanBrainSingleImpl(root, &policy.Policy{}, r)

	for _, v := range r.Violations {
		if v.Rule == brainSingleImplRule && v.Package == "internal/capabilities/assets/search" {
			t.Fatalf("other New* constructors in search must not count as SearchFanOut implementations, got %+v", v)
		}
	}
}
