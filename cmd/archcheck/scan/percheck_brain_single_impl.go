// Package scan — percheck_brain_single_impl.go
//
// Forward-prevention gate: every canonical brain component MUST have
// exactly one production implementation. The canonical components are:
//
//   - PhraseNormalizer         → internal/application/brain/normalizer
//   - VisualIntentResolver     → internal/application/brain/intent
//   - CandidateRanker (port)   → internal/application/brain/ranker
//     CandidateRanker (impl)   → internal/app/wiring (composition-root
//     wiring; the MediaMemory-backed adapter lives there to avoid the
//     brain <-> mediamemory architectural import cycle)
//   - SceneVisualPlanner       → internal/application/brain/planner
//   - SearchFanOut             → internal/application/search
//
// The MediaMemoryResolutionPort lives in internal/application/brain
// but is implemented by the MediaMemory cascade in
// internal/application/mediamemory; it is therefore not listed as a
// brain-package component here.
//
// The gate counts production constructors in the canonical home
// package (non-test .go files outside any adapters/ subdirectory).
// More than one constructor for the same component is a hard gate
// violation: it means two brains exist. Zero constructors are allowed
// while a component is being wired and are reported as a warning only.
package scan

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const brainSingleImplRule = "percheck_brain_single_impl"

// canonicalBrainComponent pins the canonical home and the
// production-constructor surface for one brain component.
type canonicalBrainComponent struct {
	Name         string
	PkgPath      string
	Constructors []string
}

// brainComponents is the canonical registry. The constructor names
// are the SOLE allowed production entrypoints; any extra constructor
// implementing the same component trips the gate.
var brainComponents = []canonicalBrainComponent{
	{Name: "PhraseNormalizer", PkgPath: "internal/application/brain/normalizer", Constructors: []string{"NewDefaultNormalizer"}},
	{Name: "VisualIntentResolver", PkgPath: "internal/application/brain/intent", Constructors: []string{"NewDefaultResolver"}},
	{Name: "CandidateRanker", PkgPath: "internal/app/wiring", Constructors: []string{"NewMediaMemoryRankerAdapter"}},
	{Name: "SceneVisualPlanner", PkgPath: "internal/application/brain/planner", Constructors: []string{"NewDefaultPlanner"}},
	{Name: "SearchFanOut", PkgPath: "internal/application/search", Constructors: []string{"NewSearchFanOut"}},
	// EmbeddingChannelRegistry is canonical but its production constructor
	// has not been extracted to internal/application/search yet. Include
	// it as soon as a canonical NewEmbeddingChannelRegistry exists.
	// {Name: "EmbeddingChannelRegistry", PkgPath: "internal/application/search", Constructors: []string{"NewEmbeddingChannelRegistry"}},
}

// ScanBrainSingleImpl walks the canonical home packages of every
// registered brain component and reports duplicate production
// constructors.
func ScanBrainSingleImpl(root string, pol *policy.Policy, r *report.Report) {
	_ = pol // reserved for future severity override
	for _, comp := range brainComponents {
		pkgDir := filepath.Join(root, filepath.FromSlash(comp.PkgPath))
		count := countProductionConstructors(pkgDir, comp.Constructors)
		if count == 0 {
			r.Warnings = append(r.Warnings, brainSingleImplRule+": canonical brain component "+comp.Name+" has no production implementation in "+comp.PkgPath+" (constructors: "+strings.Join(comp.Constructors, ", ")+")")
			continue
		}
		if count > 1 {
			r.Violations = append(r.Violations, report.Violation{
				Package:      comp.PkgPath,
				Rule:         brainSingleImplRule,
				Severity:     string(report.SeverityError),
				MatchedRule:  "brain_component_multiple_impls",
				ActualCount:  count,
				AllowedCount: 1,
				Note:         "canonical brain component " + comp.Name + " has more than one production implementation in " + comp.PkgPath + " (constructors: " + strings.Join(comp.Constructors, ", ") + ")",
			})
		}
	}
}

// countProductionConstructors returns the number of non-test .go
// files outside adapters/ that contain any of the named constructors.
func countProductionConstructors(pkgDir string, constructors []string) int {
	re := constructorRegex(constructors)
	if re == nil {
		return 0
	}

	count := 0
	seen := map[string]bool{}

	_ = filepath.WalkDir(pkgDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == "adapters" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(pkgDir, path)
		if seen[rel] {
			return nil
		}
		if containsProductionConstructor(path, re) {
			seen[rel] = true
			count++
		}
		return nil
	})

	return count
}

func constructorRegex(constructors []string) *regexp.Regexp {
	escaped := make([]string, 0, len(constructors))
	for _, c := range constructors {
		escaped = append(escaped, regexp.QuoteMeta(c))
	}
	// Match a line that starts (after whitespace) with `func <Name>(`.
	pattern := `(?m)^\s*func\s+(?:` + strings.Join(escaped, "|") + `)\s*\(`
	return regexp.MustCompile(pattern)
}

func containsProductionConstructor(path string, re *regexp.Regexp) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") {
			continue
		}
		if re.MatchString(line) {
			return true
		}
	}
	return false
}
