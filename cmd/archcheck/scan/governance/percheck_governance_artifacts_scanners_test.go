// Package scan — tests for the enforcement-surface checks of
// percheck_governance_artifacts (rule-id ownership + scan-root existence).
package governance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func writeGateSource(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func runGateScanners(t *testing.T, root string) []report.Violation {
	t.Helper()
	r := &report.Report{}
	scanRuleIDOwnership(root, r)
	scanGateScanRoots(root, r)
	return r.Violations
}

func matchedRules(violations []report.Violation) []string {
	out := make([]string, 0, len(violations))
	for _, v := range violations {
		out = append(out, v.MatchedRule)
	}
	return out
}

func hasMatchedRule(violations []report.Violation, want string) bool {
	for _, v := range violations {
		if v.MatchedRule == want {
			return true
		}
	}
	return false
}

// TestGateScanRoots_DeadRoot: a scanner walking a literal directory that does
// not exist is green by construction — WalkDir returns immediately — so it must
// be reported. This is the percheck_metadata_registry failure mode.
func TestGateScanRoots_DeadRoot(t *testing.T) {
	root := t.TempDir()
	writeGateSource(t, root, "cmd/archcheck/scan/x/probe.go", `package x

import (
	"os"
	"path/filepath"
)

func Scan(root string) {
	_ = filepath.WalkDir(filepath.Join(root, "internal/gone"), func(p string, d os.DirEntry, err error) error { return nil })
}
`)
	violations := runGateScanners(t, root)
	if !hasMatchedRule(violations, "scan_root_missing") {
		t.Fatalf("expected scan_root_missing, got %v", matchedRules(violations))
	}
	for _, v := range violations {
		if v.MatchedRule == "scan_root_missing" && !strings.Contains(v.Note, "internal/gone") {
			t.Fatalf("violation does not name the dead root: %q", v.Note)
		}
	}
}

// TestGateScanRoots_LiveRootIsFine: a scanner walking a directory that exists
// must not be reported (the percheck_sourcestager / driveaccess family).
func TestGateScanRoots_LiveRootIsFine(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "internal", "live"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeGateSource(t, root, "cmd/archcheck/scan/x/probe.go", `package x

import (
	"os"
	"path/filepath"
)

func Scan(root string) {
	_ = filepath.WalkDir(filepath.Join(root, "internal", "live"), func(p string, d os.DirEntry, err error) error { return nil })
}
`)
	if violations := runGateScanners(t, root); len(violations) != 0 {
		t.Fatalf("expected no violations, got %v", matchedRules(violations))
	}
}

// TestGateScanRoots_RootParameterShapes: the multi-root and constant-root shapes
// the registered scanners actually use must resolve, so a dead root behind them
// is still caught.
func TestGateScanRoots_RootParameterShapes(t *testing.T) {
	root := t.TempDir()
	writeGateSource(t, root, "cmd/archcheck/scan/x/multi.go", `package x

import (
	"os"
	"path/filepath"
)

const retiredRoot = "internal/retired"

func Scan(root string) {
	for _, subdir := range []string{"internal/alpha", "internal/beta"} {
		dir := filepath.Join(root, subdir)
		_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error { return nil })
	}
	_ = filepath.WalkDir(filepath.Join(root, retiredRoot), func(p string, d os.DirEntry, err error) error { return nil })
}
`)
	violations := runGateScanners(t, root)
	for _, want := range []string{"internal/alpha", "internal/beta", "internal/retired"} {
		found := false
		for _, v := range violations {
			if v.MatchedRule == "scan_root_missing" && strings.Contains(v.Note, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected a scan_root_missing naming %s, got %+v", want, violations)
		}
	}
}

// TestGateScanRoots_DeclaredAbsentRoot: a forward-prevention ban whose pass
// state IS the absence of the path is the one legitimate dead root — and the
// declaration is verified, so it fails closed once the path reappears.
func TestGateScanRoots_DeclaredAbsentRoot(t *testing.T) {
	root := t.TempDir()
	writeGateSource(t, root, "cmd/archcheck/scan/x/ban.go", `package x

import (
	"os"
	"path/filepath"
)

func Scan(root string) {
	_ = filepath.WalkDir(filepath.Join(root, "internal/platform/sqlite/assets/clips"), func(p string, d os.DirEntry, err error) error { return nil })
}
`)
	if violations := runGateScanners(t, root); len(violations) != 0 {
		t.Fatalf("declared-absent root must not be reported, got %v", matchedRules(violations))
	}

	// Recreate the path: the declaration is now stale and must fail closed.
	if err := os.MkdirAll(filepath.Join(root, "internal", "platform", "sqlite", "assets", "clips"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	violations := runGateScanners(t, root)
	if !hasMatchedRule(violations, "absent_root_declaration_stale") {
		t.Fatalf("expected absent_root_declaration_stale, got %v", matchedRules(violations))
	}
}

// TestRuleIDOwnership_TwoOwnersOfOneID: the percheck_metadata_registry failure
// mode — one file declares the id as a constant, another emits it as a literal.
func TestRuleIDOwnership_TwoOwnersOfOneID(t *testing.T) {
	root := t.TempDir()
	writeGateSource(t, root, "cmd/archcheck/scan/a/decl.go", `package a

const sharedRule = "percheck_shared_rule"
`)
	writeGateSource(t, root, "cmd/archcheck/scan/b/emit.go", `package b

import "github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"

func emit() report.Violation {
	return report.Violation{Rule: "percheck_shared_rule"}
}
`)
	violations := runGateScanners(t, root)
	if !hasMatchedRule(violations, "rule_id_not_single_owner") {
		t.Fatalf("expected rule_id_not_single_owner, got %v", matchedRules(violations))
	}
}

// TestRuleIDOwnership_OneOwnerIsFine: one declaration owner, plus a second
// scanner that REFUSES to copy the literal and references the owner's constant
// instead. That is the drift-free shape, and it must stay green.
func TestRuleIDOwnership_OneOwnerIsFine(t *testing.T) {
	root := t.TempDir()
	writeGateSource(t, root, "cmd/archcheck/scan/a/one.go", `package a

import "github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"

// OwnRule is the single declaration site for this fact.
const OwnRule = "percheck_own_rule"

func emit() report.Violation {
	return report.Violation{Rule: OwnRule}
}
`)
	writeGateSource(t, root, "cmd/archcheck/scan/b/reuse.go", `package b

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/scan/a"
)

func emit() report.Violation {
	return report.Violation{Rule: a.OwnRule, MatchedRule: "second_site"}
}
`)
	if violations := runGateScanners(t, root); len(violations) != 0 {
		t.Fatalf("expected no violations, got %v", matchedRules(violations))
	}
}

// TestRuleIDOwnership_TwoDeclarations: two constants for one id is the other
// ownership failure shape and must be reported.
func TestRuleIDOwnership_TwoDeclarations(t *testing.T) {
	root := t.TempDir()
	writeGateSource(t, root, "cmd/archcheck/scan/a/decl.go", `package a

const sharedRule = "percheck_shared_rule"
`)
	writeGateSource(t, root, "cmd/archcheck/scan/b/decl.go", `package b

const alsoSharedRule = "percheck_shared_rule"
`)
	violations := runGateScanners(t, root)
	if !hasMatchedRule(violations, "rule_id_not_single_owner") {
		t.Fatalf("expected rule_id_not_single_owner, got %v", matchedRules(violations))
	}
}

// TestGateScanners_RegisteredTreeIsClean is the regression pin: the real gate
// tree must not contain a dead scan root or a two-owner rule id.
func TestGateScanners_RegisteredTreeIsClean(t *testing.T) {
	repoRoot := filepath.Join("..", "..", "..", "..")
	repoRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	r := &report.Report{}
	scanRuleIDOwnership(repoRoot, r)
	scanGateScanRoots(repoRoot, r)
	if len(r.Violations) != 0 {
		t.Fatalf("gate tree is not clean: %+v", r.Violations)
	}
}

var _ = policy.StandardSkipDirs
