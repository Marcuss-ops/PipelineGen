// Package scan — Check 73: SceneAssetBinder SSOT (Wave 5, July 2026).
//
// scan/percheck_assetbinder.go owns the forward-prevention gate for
// the canonical per-scene asset binder. After the Phase 2 postprocessor
// unification, the ONLY place that assigns clip/stock bindings to
// scenes is internal/capabilities/scripts/scene/binder.go. Any other
// production code that directly mutates scene.Bindings.Clip or
// scene.Bindings.Stock, or constructs scriptpkg.ClipBinding /
// scriptpkg.StockBinding literals, bypasses the canonical binder and
// risks P0 #2 / P1 #10 invariant regressions.
//
// Allowlist:
//   - internal/capabilities/scripts/scene/binder.go : the canonical binder.
//   - *_test.go                                    : tests may construct fixtures directly.
//   - internal/kernel/script types                 : domain types own ClipBinding/StockBinding definitions.
//
// Pattern anchors:
//
//	Bindings\.Clip\s*=                             — direct clip binding assignment
//	Bindings\.Stock\s*=                            — direct stock binding assignment
//	ClipBinding\{                                  — direct clip binding literal (outside domain)
//	StockBinding\{                                 — direct stock binding literal (outside domain)
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// assetBinderRelPath is the canonical binder file.
const assetBinderRelPath = "internal/capabilities/scripts/scene/binder.go"

// assetBinderForbiddenPatterns pairs a regex substring with a human-readable
// description. The scanner looks for these substrings in production code
// outside the allowlist.
var assetBinderForbiddenPatterns = []struct {
	pattern string
	desc    string
}{
	{"Bindings.Clip =", "direct scene.Bindings.Clip assignment"},
	{"Bindings.Stock =", "direct scene.Bindings.Stock assignment"},
	{"ClipBinding{", "direct scriptpkg.ClipBinding literal"},
	{"StockBinding{", "direct scriptpkg.StockBinding literal"},
}

// ScanAssetBinderSSOT walks <root>/internal/application/** and
// <root>/internal/api/** for non-test .go files, scanning each line
// for direct scene-binding mutations outside the canonical binder.
// Full-line comments lines (leading `//` after trim) are excluded.
func ScanAssetBinderSSOT(root string, pol *policy.Policy, r *report.Report) {
	skipDirs := map[string]bool{
		".git": true, "vendor": true, "node_modules": true,
		"node-scraper": true, "examples": true, "scripts": true,
	}

	binderPrefix := filepath.ToSlash(assetBinderRelPath)

	for _, subdir := range []string{"internal/application", "internal/api"} {
		dir := filepath.Join(root, subdir)
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if skipDirs[filepath.Base(path)] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			relSlash := filepath.ToSlash(rel)
			if relSlash == binderPrefix {
				return nil
			}
			scanAssetBinderFile(root, path, relSlash, r)
			return nil
		})
	}
}

func scanAssetBinderFile(root, path, relPath string, r *report.Report) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	lineNum := 0
	for sc.Scan() {
		lineNum++
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		for _, p := range assetBinderForbiddenPatterns {
			if !strings.Contains(line, p.pattern) {
				continue
			}
			r.Violations = append(r.Violations, report.Violation{
				File:        relPath,
				Line:        lineNum,
				Rule:        "percheck_assetbinder_ssot",
				Severity:    string(report.SeverityError),
				MatchedRule: "scene_asset_binder_ssot",
				Note:        "direct " + p.desc + " outside canonical SceneAssetBinder (internal/capabilities/scripts/scene/binder.go); route binding through SceneAssetBinder.BindClips / BindStock",
			})
		}
	}
}
