package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func writeProviderPolicyFixture(t *testing.T, root, rel, content string) string {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	return filepath.ToSlash(rel)
}

func providerPolicyViolations(r *report.Report) []report.Violation {
	var out []report.Violation
	for _, v := range r.Violations {
		if v.Rule == providerPolicyOwnerRule {
			out = append(out, v)
		}
	}
	return out
}

// TestScanProviderPolicySingleOwner_SecondDeclarationViolates pins the
// structural half: a policy literal outside the canonical owner fails closed,
// because no test cross-checks a second table against real adapter output.
func TestScanProviderPolicySingleOwner_SecondDeclarationViolates(t *testing.T) {
	root := t.TempDir()
	writeProviderPolicyFixture(t, root, "internal/app/other/catalog.go",
		"package other\n\nvar policies = []providerassets.ProviderPolicy{\n\t{Name: \"pexels\", Enabled: true, MediaType: \"image\", Priority: 20},\n}\n")

	r := &report.Report{}
	ScanProviderPolicySingleOwner(root, &policy.Policy{}, r)

	got := providerPolicyViolations(r)
	if len(got) != 1 {
		t.Fatalf("got %d single-owner violations, want 1: %#v", len(got), r.Violations)
	}
	if got[0].File != "internal/app/other/catalog.go" || got[0].Line == 0 || got[0].Severity != string(report.SeverityError) {
		t.Fatalf("unexpected violation: %#v", got[0])
	}
}

// TestScanProviderPolicySingleOwner_OwnerIsExempt pins that the one canonical
// declaration is allowed — otherwise the gate could never be satisfied.
func TestScanProviderPolicySingleOwner_OwnerIsExempt(t *testing.T) {
	root := t.TempDir()
	writeProviderPolicyFixture(t, root, providerPolicyOwnerFile,
		"package wiring\n\nfunc providerCatalogPolicies() []providerassets.ProviderPolicy {\n\treturn []providerassets.ProviderPolicy{\n\t\t{Name: \"pexels\", Enabled: true, MediaType: \"video\", Priority: 20},\n\t}\n}\n")

	r := &report.Report{}
	ScanProviderPolicySingleOwner(root, &policy.Policy{}, r)
	if got := providerPolicyViolations(r); len(got) != 0 {
		t.Fatalf("owner declaration must be exempt, got %#v", got)
	}
}

// TestScanProviderPolicySingleOwner_NonDeclarationsAreNotFlagged pins the
// false-positive guards: a zero-value literal (lookup miss), a signature type,
// the differently-named MediaProviderPolicy, and _test.go fixtures.
func TestScanProviderPolicySingleOwner_NonDeclarationsAreNotFlagged(t *testing.T) {
	root := t.TempDir()
	writeProviderPolicyFixture(t, root, "internal/capabilities/assets/providerassets/catalog_builder.go",
		"package providerassets\n\nfunc (r *ProviderPolicyRegistry) Get(name string) (ProviderPolicy, bool) {\n\treturn ProviderPolicy{}, false\n}\n")
	writeProviderPolicyFixture(t, root, "internal/app/other/signature.go",
		"package other\n\nfunc NewProviderPolicyRegistry(policies []providerassets.ProviderPolicy) error { return nil }\n")
	writeProviderPolicyFixture(t, root, "internal/app/other/media_policy.go",
		"package other\n\nvar m = MediaPlanSpec{ProviderPolicy: mediadomain.MediaProviderPolicy{Artlist: mediadomain.MediaToggleEnabled}}\n")
	writeProviderPolicyFixture(t, root, "internal/app/other/catalog_test.go",
		"package other\n\nvar fixture = []ProviderPolicy{{Name: \"pexels\", MediaType: \"image\"}}\n")
	writeProviderPolicyFixture(t, root, "cmd/admin/internal/whatever/notes.go",
		"package whatever\n\n// ProviderPolicy{...} declarations live in providerCatalogPolicies().\n")

	r := &report.Report{}
	ScanProviderPolicySingleOwner(root, &policy.Policy{}, r)
	if got := providerPolicyViolations(r); len(got) != 0 {
		t.Fatalf("non-declarations must not be flagged, got %#v", got)
	}
	warned := false
	for _, w := range r.Warnings {
		if strings.HasPrefix(w, providerPolicyOwnerRule+" ") {
			warned = true
		}
	}
	if !warned {
		t.Fatal("comment-only references must be residue-accounted as a warning")
	}
}

// TestScanProviderPolicySingleOwner_RepoTreeIsClean runs the gate against the
// real tree: the repo must declare provider policies in exactly one place.
func TestScanProviderPolicySingleOwner_RepoTreeIsClean(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "architecture", "policy.yaml")); statErr != nil {
		t.Skipf("repo root not resolvable from the test working directory: %v", statErr)
	}

	r := &report.Report{}
	ScanProviderPolicySingleOwner(root, &policy.Policy{}, r)
	if got := providerPolicyViolations(r); len(got) != 0 {
		t.Fatalf("repo tree declares provider policies outside the single owner: %#v", got)
	}
}
