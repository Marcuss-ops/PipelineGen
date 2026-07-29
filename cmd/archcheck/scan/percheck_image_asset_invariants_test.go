// Package scan — percheck_image_asset_invariants_test.go pins the
// forward-prevention contract for the ImageAsset literal ban
// (Rule A) + Gemma DTO leak prevention (Rule B).
//
// Each test builds an in-memory temp tree (no fs side-effects),
// drives ScanImageAssetInvariants, and asserts that the reported
// Violations / Warnings exactly match the expected surface.
//
// godlike/06 SSOT discipline: tests pin the canonical Rule A
// + Rule B contracts. Forward-pointer drift that changes either
// rule's expected surface must update this file in lockstep.
package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// imageAssetTestPolicy returns the canonical-empty Policy
// surface used by every test in this file. The ImageAsset
// gate does NOT use pol fields at v1 (forward-pointer note in
// the scanner's package-level doc), but the uniform signature
// requires a non-nil pointer.
func imageAssetTestPolicy() *policy.Policy {
	return &policy.Policy{}
}

// imageAssetTestReport returns the canonical-empty Report
// surface. The scanner mutates r.Violations + r.Warnings; tests
// inspect the cumulative state at the end.
func imageAssetTestReport() *report.Report {
	return &report.Report{
		Summary: report.Summary{
			ByReason:   map[string]int{},
			BySeverity: map[string]int{},
		},
	}
}

// imageAssetWriteTree writes a small on-disk file tree under
// dir and returns the path. The tree is the canonical staging
// surface for the scanners (no chdir required; WalkDir rooted
// at root scopes every test).
func imageAssetWriteTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for relPath, contents := range files {
		fullPath := filepath.Join(dir, relPath)
		// Best-effort mkdir -p; t.TempDir leaves a fresh dir
		// so the only mkdir needed is for nested parents.
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %q: %v", filepath.Dir(fullPath), err)
		}
		if err := os.WriteFile(fullPath, []byte(contents), 0o644); err != nil {
			t.Fatalf("write %q: %v", fullPath, err)
		}
	}
}

// imageAssetViolations filters r.Violations to only those whose
// Rule equals `rule`. Used to scope test assertions to the
// specific rule under test (Rule A or Rule B) so the test does
// not over-assert (the OTHER side-effect of the scanner comes
// from the SAME scan call).
func imageAssetViolations(r *report.Report, rule string) []report.Violation {
	out := []report.Violation{}
	for _, v := range r.Violations {
		if v.Rule == "percheck_image_asset_invariants" && v.MatchedRule == rule {
			out = append(out, v)
		}
	}
	return out
}

// ─── Rule A tests ──────────────────────────────────────────────────────────

// TestRuleA_LiteralBanFailsProduction verifies that a .go file
// OUTSIDE the canonical owner + canonical builder declares
// `&asset.ImageAsset{...}` → Rule A violation (SeverityError).
func TestRuleA_LiteralBanFailsProduction(t *testing.T) {
	dir := t.TempDir()
	imageAssetWriteTree(t, dir, map[string]string{
		"internal/application/images/random_factory.go": `package factory
import "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
// production code: ImageAsset literal in a non-canonical place
func build() *asset.ImageAsset {
	return &asset.ImageAsset{Origin: "x", Provider: "y", ContentHash: "z", Width: 1, Height: 1}
}
`,
	})
	r := imageAssetTestReport()
	ScanImageAssetInvariants(dir, imageAssetTestPolicy(), r)
	violations := imageAssetViolations(r, "image_asset_literal_ban")
	if len(violations) != 1 {
		t.Fatalf("want 1 Rule A violation, got %d (all=%+v)", len(violations), r.Violations)
	}
	if violations[0].Severity != string(report.SeverityError) {
		t.Fatalf("want SeverityError, got %s", violations[0].Severity)
	}
	if !strings.Contains(violations[0].Note, "PR-CANONICAL-IMAGE-ASSET-INVARIANTS") {
		t.Fatalf("Note missing forward-pointer anchor: %q", violations[0].Note)
	}
}

// TestRuleA_DomainAliasLiteralFailsProduction ensures the
// `&domainasset.ImageAsset{` variant is also banned under
// Rule A (the alias points to the same canonical type).
func TestRuleA_DomainAliasLiteralFailsProduction(t *testing.T) {
	dir := t.TempDir()
	imageAssetWriteTree(t, dir, map[string]string{
		"internal/application/images/random_alias_factory.go": `package factory
import domainasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
// domain-alias literal is also banned
func build() *domainasset.ImageAsset {
	return &domainasset.ImageAsset{Origin: "x"}
}
`,
	})
	r := imageAssetTestReport()
	ScanImageAssetInvariants(dir, imageAssetTestPolicy(), r)
	violations := imageAssetViolations(r, "image_asset_literal_ban")
	if len(violations) != 1 {
		t.Fatalf("want 1 Rule A violation, got %d (all=%+v)", len(violations), r.Violations)
	}
}

// TestRuleA_ExemptsCanonicalOwnerAndBuilder verifies the two
// allow-listed files (canonical_metadata.go +
// storage_ingest_direct.go) are exempt from Rule A even if
// they declared `&asset.ImageAsset{`.
func TestRuleA_ExemptsCanonicalOwnerAndBuilder(t *testing.T) {
	dir := t.TempDir()
	imageAssetWriteTree(t, dir, map[string]string{
		"internal/kernel/asset/canonical_metadata.go": `package asset
// canonical owner — literal MAY appear here as the structural source-of-truth
type ImageAsset struct {
	Origin   string
	Provider string
	Hash     string
	Width    int
	Height   int
	MetadataJSON []byte
}
func (a *ImageAsset) ResetCanonical() *ImageAsset { return &ImageAsset{Origin: "x"} }
`,
		"internal/application/images/storage_ingest_direct.go": `package images
import asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
// canonical builder — literal MAY appear here if the typed route promotes to a literal in the future
func Build() *asset.ImageAsset { return &asset.ImageAsset{Origin: "x"} }
`,
	})
	r := imageAssetTestReport()
	ScanImageAssetInvariants(dir, imageAssetTestPolicy(), r)
	violations := imageAssetViolations(r, "image_asset_literal_ban")
	if len(violations) != 0 {
		t.Fatalf("want 0 Rule A violations inside canonical owner+builder, got %d (all=%+v)",
			len(violations), r.Violations)
	}
}

// TestRuleA_ExemptsTestFiles verifies `*_test.go` files are
// exempt — test stubs legitimately need ImageAsset fixtures.
func TestRuleA_ExemptsTestFiles(t *testing.T) {
	dir := t.TempDir()
	imageAssetWriteTree(t, dir, map[string]string{
		"internal/application/images/some_factory_test.go": `package factory_test
import asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
func Stub() *asset.ImageAsset { return &asset.ImageAsset{AssetID: "stub-id"} }
`,
	})
	r := imageAssetTestReport()
	ScanImageAssetInvariants(dir, imageAssetTestPolicy(), r)
	violations := imageAssetViolations(r, "image_asset_literal_ban")
	if len(violations) != 0 {
		t.Fatalf("want 0 Rule A violations inside test files, got %d", len(violations))
	}
}

// TestRuleA_CommentOnlyEmitsWarn confirms a comment-only line
// referencing the literal → SeverityWarn (r.Warnings), NOT
// r.Violations.
func TestRuleA_CommentOnlyEmitsWarn(t *testing.T) {
	dir := t.TempDir()
	imageAssetWriteTree(t, dir, map[string]string{
		"internal/application/images/notes.go": `package images
// historically ` + "`&asset.ImageAsset{...}`" + ` was the direct literal path; now we route through canonical metadata.
func Note() {}
`,
	})
	r := imageAssetTestReport()
	ScanImageAssetInvariants(dir, imageAssetTestPolicy(), r)
	if got := len(imageAssetViolations(r, "image_asset_literal_ban")); got != 0 {
		t.Fatalf("want 0 Rule A violations for comment-only, got %d", got)
	}
	if len(r.Warnings) != 1 {
		t.Fatalf("want 1 warning (comment-only Rule A residue), got %d: %v", len(r.Warnings), r.Warnings)
	}
	if !strings.Contains(r.Warnings[0], "Rule A") {
		t.Fatalf("warning missing Rule A label: %q", r.Warnings[0])
	}
}

// ─── Rule B tests ──────────────────────────────────────────────────────────

// TestRuleB_FieldInGemmaScopeFailsProduction verifies a struct
// field with a JSON tag naming a forbidden field, in the
// Gemma-prompt-construction scope, trips Rule B.
func TestRuleB_FieldInGemmaScopeFailsProduction(t *testing.T) {
	dir := t.TempDir()
	imageAssetWriteTree(t, dir, map[string]string{
		"internal/application/scripts/usecase/dto.go": `package usecase
type Envelope struct {
	Ref string ` + "`json:\"ref\"`" + `
	// FORBIDDEN — must not appear in gemmma-prompt scope
	AssetID string ` + "`json:\"asset_id\"`" + `
}
func Build() Envelope { return Envelope{Ref: "r", AssetID: "x"} }
`,
	})
	r := imageAssetTestReport()
	ScanImageAssetInvariants(dir, imageAssetTestPolicy(), r)
	violations := imageAssetViolations(r, "gemma_dto_leak_ban")
	if len(violations) != 1 {
		t.Fatalf("want 1 Rule B violation, got %d (all=%+v)", len(violations), r.Violations)
	}
	if !strings.Contains(violations[0].Note, "forbidden-field: asset_id") {
		t.Fatalf("Note must reference the offending field name; got %q", violations[0].Note)
	}
}

// TestRuleB_FieldOutsideGemmaScopeIsIgnored verifies the
// path-scope filter: a forbidden-field JSON tag outside
// internal/application/scripts/usecase/ does NOT trip.
func TestRuleB_FieldOutsideGemmaScopeIsIgnored(t *testing.T) {
	dir := t.TempDir()
	imageAssetWriteTree(t, dir, map[string]string{
		// This file lives in internal/application/images/ —
		// outside the gemmma-prompt scope. Rule B is silently
		// skipped because the compile-time gate is upstream of
		// any non-model-facing surface.
		"internal/application/images/dto.go": `package images
type Envelope struct {
	Ref string ` + "`json:\"ref\"`" + `
	AssetID string ` + "`json:\"asset_id\"`" + `
}
`,
	})
	r := imageAssetTestReport()
	ScanImageAssetInvariants(dir, imageAssetTestPolicy(), r)
	if got := len(imageAssetViolations(r, "gemma_dto_leak_ban")); got != 0 {
		t.Fatalf("want 0 Rule B violations outside gemmma scope, got %d", got)
	}
}

// TestRuleB_ExemptsClipviewOwner confirms the canonical
// SOLE model-facing owner (clipview/**) is exempt from
// Rule B — its deny-list is the SSOT seal.
func TestRuleB_ExemptsClipviewOwner(t *testing.T) {
	dir := t.TempDir()
	imageAssetWriteTree(t, dir, map[string]string{
		// clipview/types.go carries the canonical deny-list
		// ITSELF. The compile-time guard is upstream of that
		// reflect-loop contract; we explicitly omit clipview/**
		// from Rule B scan.
		"internal/application/clipview/types.go": `package clipview
var ForbiddenCandidateViewJSONFields = []string{
	"asset_id", "drive_link",
}
type CandidateView struct {
	Ref string ` + "`json:\"ref\"`" + `
}
`,
	})
	r := imageAssetTestReport()
	ScanImageAssetInvariants(dir, imageAssetTestPolicy(), r)
	if got := len(imageAssetViolations(r, "gemma_dto_leak_ban")); got != 0 {
		t.Fatalf("want 0 Rule B violations inside clipview/**, got %d", got)
	}
}

// TestRuleB_OmitEmptyOptionStillTrips confirms the
// `json:"NAME,omitempty"` style also trips Rule B. The
// normalize step strips the option suffix before matching.
func TestRuleB_OmitEmptyOptionStillTrips(t *testing.T) {
	dir := t.TempDir()
	imageAssetWriteTree(t, dir, map[string]string{
		"internal/application/scripts/usecase/dto2.go": `package usecase
type Envelope struct {
	DriveLink string ` + "`json:\"drive_link,omitempty\"`" + `
}
`,
	})
	r := imageAssetTestReport()
	ScanImageAssetInvariants(dir, imageAssetTestPolicy(), r)
	violations := imageAssetViolations(r, "gemma_dto_leak_ban")
	if len(violations) != 1 {
		t.Fatalf("want 1 Rule B violation for omittedJSON tag, got %d", len(violations))
	}
	if !strings.Contains(violations[0].Note, "forbidden-field: drive_link") {
		t.Fatalf("Note must reference drive_link; got %q", violations[0].Note)
	}
}

// ─── Cross-rule test ────────────────────────────────────────────────────────

// TestBoth_RulesCoexistOnSameFile ensures both rules can flag
// the same file when both invariants are violated (Rule A
// literal + Rule B tag).
func TestBoth_RulesCoexistOnSameFile(t *testing.T) {
	dir := t.TempDir()
	imageAssetWriteTree(t, dir, map[string]string{
		"internal/application/scripts/usecase/leaky.go": `package usecase
import asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
type Envelope struct {
	Ref string ` + "`json:\"ref\"`" + `
	AssetID string ` + "`json:\"asset_id\"`" + `
}
func Build() *asset.ImageAsset {
	return &asset.ImageAsset{Origin: "x", Provider: "y", Hash: "z"}
}
`,
	})
	r := imageAssetTestReport()
	ScanImageAssetInvariants(dir, imageAssetTestPolicy(), r)

	if got := len(imageAssetViolations(r, "image_asset_literal_ban")); got != 1 {
		t.Fatalf("want 1 Rule A violation, got %d", got)
	}
	if got := len(imageAssetViolations(r, "gemma_dto_leak_ban")); got != 1 {
		t.Fatalf("want 1 Rule B violation, got %d", got)
	}
}
