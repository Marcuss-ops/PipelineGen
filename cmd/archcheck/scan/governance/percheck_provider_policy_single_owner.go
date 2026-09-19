// Package scan — percheck_provider_policy_single_owner.go
//
// External-provider policy single-owner gate (September 2026).
//
// WHY THIS GATE EXISTS. The provider catalog's `ProviderPolicy` table used to
// be an inline slice inside `buildProviderAssetCatalog`, and its entries
// declared `MediaType: "image"` for providers whose wired adapters hit the
// Pexels/Pixabay **video** endpoints and returned clip-typed assets. Nothing
// caught it: `ProviderPolicy.MediaType` is read by no consumer, so the
// declaration was inert AND wrong — a config that lies is worse than a missing
// config, because the next reader trusts it.
//
// The fix has two halves, and they are deliberately different tools:
//
//  1. STRUCTURAL (this gate, statically decidable): the policy table has
//     exactly ONE owner — `providerCatalogPolicies()` in
//     `internal/app/wiring/build_provider_catalog.go`. A `ProviderPolicy`
//     composite literal anywhere else is a second declaration that the
//     semantic test below would not cover, so it fails closed here.
//
//  2. SEMANTIC (a test, not a gate): whether a declared `MediaType` matches
//     the surface its adapter actually serves is NOT statically decidable —
//     it depends on which endpoint the adapter calls and what media family it
//     types its results with. That half is pinned in
//     `internal/app/wiring/build_provider_catalog_test.go`
//     (`TestProviderCatalogPoliciesMatchTheShippedAdapterSurface`), which
//     drives the shipped searcher and derives the expected vocabulary from the
//     returned `ProviderAsset.MediaType`.
//
// Scope: production `.go` files under internal/ and cmd/. `_test.go` files are
// exempt (fixtures build policies directly). Empty literals (`ProviderPolicy{}`
// as a zero value for a lookup miss) are not declarations and are not flagged.
// Comment-only references are residue-accounted as WARN, never violations.
//
// Matched rule_id: percheck_provider_policy_single_owner
package governance

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// providerPolicyOwnerRule is the rule-family id the scanner emits.
const providerPolicyOwnerRule = "percheck_provider_policy_single_owner"

// providerPolicyOwnerFile is the ONLY production file authorized to declare
// `providerassets.ProviderPolicy` entries.
const providerPolicyOwnerFile = "internal/app/wiring/build_provider_catalog.go"

// providerPolicyOwnerNote is the violation Note.
const providerPolicyOwnerNote = "ProviderPolicy composite literal outside its single owner. The external-provider policy table is owned by providerCatalogPolicies() in internal/app/wiring/build_provider_catalog.go: it is the one place whose declared MediaType is cross-checked against real adapter output by TestProviderCatalogPoliciesMatchTheShippedAdapterSurface. A second declaration escapes that check, and a declaration nothing verifies is exactly how the pexels/pixabay entries claimed \"image\" over video adapters (MUDA 2026-09-19). Move the entry into providerCatalogPolicies() instead of declaring a second table."

// providerPolicyLiteralRe matches a NON-EMPTY composite literal:
//   - `ProviderPolicy{<something>` -> declaration (flagged);
//   - `ProviderPolicy{` at end of line -> multi-line declaration (flagged; the
//     scanner sees one line at a time, so the `$` alternative covers it);
//   - `ProviderPolicy{}`          -> zero value for a lookup miss (NOT flagged);
//   - `ProviderPolicy {`          -> a type in a signature (NOT flagged);
//   - `MediaProviderPolicy{...}`  -> a DIFFERENT type, excluded by the
//     leading `[^A-Za-z0-9_]` boundary (the preceding char is a letter).
var providerPolicyLiteralRe = regexp.MustCompile(`(^|[^A-Za-z0-9_])ProviderPolicy\{($|[^}])`)

// providerPolicySkipDirs mirrors the sibling scanning policy.
var providerPolicySkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
	"testdata":     true,
}

// providerPolicySkipPathPrefixes excludes the scanner package itself, which
// necessarily references the literal in this file's prose and regex.
var providerPolicySkipPathPrefixes = []string{"cmd/archcheck/scan"}

// ScanProviderPolicySingleOwner walks every production .go file under
// internal/ and cmd/ and emits a violation for any non-empty
// `ProviderPolicy{...}` literal outside the canonical owner file.
func ScanProviderPolicySingleOwner(root string, _ *policy.Policy, r *report.Report) {
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if providerPolicySkipDirs[filepath.Base(path)] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil && hasAnyProviderPolicyPrefix(filepath.ToSlash(rel)) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)
		if !providerPolicyInScope(relSlash) || relSlash == providerPolicyOwnerFile {
			return nil
		}
		scanProviderPolicyFile(path, relSlash, r)
		return nil
	})
}

// providerPolicyInScope reports whether a repo-relative path is part of the
// scanned production surface (internal/, cmd/).
func providerPolicyInScope(relSlash string) bool {
	return strings.HasPrefix(relSlash, "internal/") || strings.HasPrefix(relSlash, "cmd/")
}

func hasAnyProviderPolicyPrefix(relSlash string) bool {
	for _, prefix := range providerPolicySkipPathPrefixes {
		if strings.HasPrefix(relSlash, prefix) {
			return true
		}
	}
	return false
}

// scanProviderPolicyFile opens one .go file and flags every line carrying a
// non-empty ProviderPolicy literal. Comment-only references are residue,
// not debt.
func scanProviderPolicyFile(path, relPath string, r *report.Report) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	commentOnly := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimLeft(line, " \t")
		isComment := strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*")
		if !providerPolicyLiteralRe.MatchString(line) {
			continue
		}
		if isComment {
			commentOnly++
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     providerPolicyPkg(relPath),
			File:        relPath,
			Line:        lineNo,
			Rule:        providerPolicyOwnerRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "provider_policy_literal_outside_single_owner",
			Note:        providerPolicyOwnerNote,
		})
	}
	if commentOnly > 0 {
		r.Warnings = append(r.Warnings, providerPolicyOwnerRule+" policy-literal-comments: "+
			strconv.Itoa(commentOnly)+" comment-only reference(s) in "+relPath+
			" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}

// providerPolicyPkg extracts the package directory from a repo-relative path.
func providerPolicyPkg(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}
