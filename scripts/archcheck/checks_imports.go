// Package main — archcheck import-graph rules.
//
// checks_imports.go owns the 3 import-graph Wave 14-19 checks:
//
//   - checkAPIInfrastructureImports: API → infrastructure edge allowlist
//     validator. The pre-PR1 baseline was static; this rule is now
//     allowlist-driven via
//     docs/migrations/api-infrastructure-imports-allowlist.txt and
//     emits BOTH stale-allowlist entries (allowlist entries that no
//     longer match any API infrastructure import) AND new violation
//     entries (API imports not in the allowlist). The pre-PR2
//     approach was a single static allowlist baked into CI scripts.
//
//   - checkApplicationToInfrastructure: hard-gated temporary ratchet for
//     `internal/application/<x>/` → `internal/infrastructure/<y>/` imports.
//     Exact production import edges are checked against a decreasing
//     migration allowlist; new and stale entries both fail.
//
//   - checkCrossCapabilityImport: Wave 19 (June 2026) edge counter
//     for internal/application/<capA>/ → internal/application/<capB>/
//     cross-capability imports. Pair-deduplicated via the
//     "srcCap->importCap" pair-key so the counter reports each
//     capability-pair once (not per-edge).
//
// Helpers (all godlike/06 SSOT co-located with their sole consumers):
//
//   - applicationCapabilities: filesystem-derived capability set,
//     replaces pre-PR2 hardcoded 22-entry map per Wave 19 PR2-3.
//   - capabilityOfFile: best-effort file-path → capability classifier
//     used by checkCrossCapabilityImport.
//   - capabilityOfImport: best-effort import-string → capability
//     classifier used by checkCrossCapabilityImport.
//   - loadAllowlist: file-loader helper for the allowlist mechanism
//     used ONLY by checkAPIInfrastructureImports (godlike/06 SSOT
//     one-canonical-owner-per-fact).
package main

import (
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	bl "github.com/Marcuss-ops/PipelineGen/scripts/archcheck/baseline"
)

// checkAPIInfrastructureImports validates that every
// `internal/api/<pkg>/` import of `internal/infrastructure/<sub>/`
// is present in the canonical allowlist file
// (docs/migrations/api-infrastructure-imports-allowlist.txt), and
// that the allowlist itself does not contain stale entries.
//
// Returns a stats map with keys "actual" (int, distinct paths in
// production code), "allowed" (int, distinct allowlist paths),
// "stale" (int, allowlist entries with no matching production
// import), and "violations" (int, `actual - allowed` + `stale`).
//
// The rg invocation in this function is the canonical "infra-edge"
// pattern (reused with different directory roots in checkApplicationToInfrastructure
// + phase0_checks.go::checkHandlerToDB). All three rely on
// execErrIsNoMatch (in checks.go) to classify the "no matches" branch
// from the genuine "rg failed" branch.
//
// Pre-PR2 this rule lived in a single static allowlist baked into the
// CI script. PR1+ moved the allowlist into a checked-in file so the
// ratchet can detect both new violations AND stale entries (allowlist
// entries that no longer correspond to any production import).
func checkAPIInfrastructureImports() (map[string]int, []string) {
	stats := map[string]int{
		"actual":     0,
		"allowed":    0,
		"stale":      0,
		"violations": 0,
	}
	out, err := exec.Command("rg", "-l",
		`github\.com/Marcuss-ops/PipelineGen/internal/infrastructure/`,
		"internal/api",
		"--glob", "*.go",
		"--glob", "!*_test.go",
	).Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
			actual := []string{}
			allowlist, allowErr := loadAllowlist("docs/migrations/api-infrastructure-imports-allowlist.txt")
			if allowErr != nil {
				return stats, []string{fmt.Sprintf("checkAPIInfrastructureImports: load allowlist: %v", allowErr)}
			}
			staleAllowlist := bl.SubtractSet(allowlist, actual)
			stats["allowed"] = len(allowlist)
			stats["stale"] = len(staleAllowlist)
			stats["violations"] = len(staleAllowlist)
			var violations []string
			for _, stale := range staleAllowlist {
				violations = append(violations, "stale allowlist entry with no matching API infrastructure import: "+stale)
			}
			return stats, violations
		}
		stats["violations"] = -1
		return stats, []string{fmt.Sprintf("checkAPIInfrastructureImports: rg failed: %v", err)}
	}
	actual := bl.NormalizePaths(splitNonEmpty(string(out)))

	allowlist, err := loadAllowlist("docs/migrations/api-infrastructure-imports-allowlist.txt")
	if err != nil {
		stats["violations"] = -1
		return stats, []string{fmt.Sprintf("checkAPIInfrastructureImports: load allowlist: %v", err)}
	}

	staleAllowlist := bl.SubtractSet(allowlist, actual)
	violations := bl.SubtractSet(actual, allowlist)
	for _, stale := range staleAllowlist {
		violations = append(violations, "stale allowlist entry with no matching API infrastructure import: "+stale)
	}
	sort.Strings(violations)
	stats["actual"] = len(actual)
	stats["allowed"] = len(allowlist)
	stats["stale"] = len(staleAllowlist)
	stats["violations"] = len(violations)
	return stats, violations
}

// checkApplicationToInfrastructure enforces the application boundary. The
// reverse direction (infrastructure -> application) remains allowed because
// concrete adapters implement application-owned ports. The temporary ledger
// is exact-edge based and must decrease as migrations remove imports.
const (
	applicationInfrastructureImportAllowlistFile = "docs/migrations/application-infrastructure-imports-allowlist.txt"
	applicationInfrastructureImportBaselineFile  = "docs/migrations/application-infrastructure-imports-baseline.txt"
)

func checkApplicationToInfrastructure() (map[string]int, []string) {
	return checkApplicationToInfrastructureAt(".", applicationInfrastructureImportAllowlistFile, applicationInfrastructureImportBaselineFile)
}

func checkApplicationToInfrastructureAt(root, allowlistPath, bootstrapBaselinePath string) (map[string]int, []string) {
	stats := map[string]int{"actual": 0, "allowed": 0, "baseline": 0, "stale": 0, "violations": 0}
	actual, err := scanApplicationInfrastructureImports(root)
	if err != nil {
		stats["violations"] = -1
		return stats, []string{fmt.Sprintf("checkApplicationToInfrastructure: scan: %v", err)}
	}
	allowlist, err := loadAllowlist(allowlistPath)
	if err != nil {
		stats["violations"] = -1
		return stats, []string{fmt.Sprintf("checkApplicationToInfrastructure: load allowlist: %v", err)}
	}
	baseline, err := loadCommittedAllowlist(root, allowlistPath)
	if err != nil {
		// Bootstrap is allowed only before the active ledger has a
		// parent revision. After that, the previous commit is the
		// immutable ratchet baseline and local edits to a parallel
		// baseline file cannot authorize new exceptions.
		baseline, err = loadAllowlist(bootstrapBaselinePath)
	}
	if err != nil {
		stats["violations"] = -1
		return stats, []string{fmt.Sprintf("checkApplicationToInfrastructure: load baseline: %v", err)}
	}
	newEdges := bl.SubtractSet(actual, allowlist)
	staleEdges := bl.SubtractSet(allowlist, actual)
	growthEdges := bl.SubtractSet(allowlist, baseline)
	violations := make([]string, 0, len(newEdges)+len(staleEdges)+len(growthEdges))
	for _, edge := range newEdges {
		violations = append(violations, "unallowlisted application→infrastructure import: "+edge)
	}
	for _, edge := range staleEdges {
		violations = append(violations, "stale application→infrastructure allowlist entry: "+edge)
	}
	for _, edge := range growthEdges {
		violations = append(violations, "application→infrastructure allowlist grew beyond baseline: "+edge)
	}
	sort.Strings(violations)
	stats["actual"] = len(actual)
	stats["allowed"] = len(allowlist)
	stats["baseline"] = len(baseline)
	stats["stale"] = len(staleEdges)
	stats["violations"] = len(violations)
	return stats, violations
}

func scanApplicationInfrastructureImports(root string) ([]string, error) {
	appDir := filepath.Join(root, "internal", "application")
	var edges []string
	err := filepath.WalkDir(appDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		for _, spec := range file.Imports {
			importPath, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return fmt.Errorf("unquote import in %s: %w", rel, err)
			}
			if strings.HasPrefix(importPath, "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/") {
				edges = append(edges, rel+" -> "+importPath)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(edges)
	return uniqueStrings(edges), nil
}

func uniqueStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

// checkCrossCapabilityImport is the Wave 19 (June 2026) edge counter
// for `internal/application/<capA>/` -> `internal/application/<capB>/`.
//
// Per the operator-pasted Dependency Rules spec, Section "Cross-capability
// communication": "Capability A must not import Capability B's
// transport, repository implementation, or internal service concrete."
// The preferred order is typed port > typed event > owned read model;
// direct cross-capability import is reserved for composition adapters.
//
// Target after Wave 19 hardening: 0 — except for a documented per-edge
// allowlist. PR1 reports COUNTS in the `Checks` map; no violations
// are emitted.
//
// Distinguish same-package from cross-package imports: a file at
// `internal/application/<capA>/x.go` importing
// `internal/application/<capA>/y` is a SAME-package reference (legal
// under any layout) and DOES NOT count. Only when the source file's
// capability differs from the imported capability does the edge
// contribute to the counter.
func checkCrossCapabilityImport() (map[string]int, []string) {
	stats := map[string]int{
		"actual":     0,
		"violations": 0,
	}
	out, err := exec.Command("rg", "-n",
		`github\.com/Marcuss-ops/PipelineGen/internal/application/`,
		"internal/application",
		"--type", "go",
		"--glob", "!*_test.go",
	).Output()
	if err != nil && !execErrIsNoMatch(err) {
		stats["violations"] = -1
		return stats, []string{fmt.Sprintf("checkCrossCapabilityImport: rg failed: %v", err)}
	}

	capabilities := applicationCapabilities()
	pairCount := 0
	seenPairs := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if line == "" {
			continue
		}
		// rg -n emits `path:linenum:matched_text`. SplitN with limit 3
		// gives us [path, linenum, content] — robust against path
		// strings that could in theory contain additional colons
		// (and a safer shape than strings.IndexByte for any future
		// rg output-format change). The "content" slice contains the
		// matched substring (the import string itself for our pattern).
		parts := strings.SplitN(line, ":", 3)
		if len(parts) < 3 {
			continue
		}
		src := parts[0]
		content := parts[2]
		srcCap := capabilityOfFile(src, capabilities)
		importCap := capabilityOfImport(content, capabilities)
		if srcCap == "" || importCap == "" {
			continue
		}
		if srcCap == importCap {
			continue
		}
		pairKey := srcCap + "->" + importCap
		if seenPairs[pairKey] {
			continue
		}
		seenPairs[pairKey] = true
		pairCount++
	}
	stats["actual"] = pairCount
	// PR1 observation-only (no violations). Operators can read the
	// pair count as a summary statistic; the full edge list (file-
	// level) is intentionally deferred to PR2 alongside the ratchet
	// baseline — see Wave 19 exit_gate in architecture/current.yaml.
	return stats, nil
}

// applicationCapabilities returns the set of capability sub-package
// names under internal/application/, derived from the filesystem at
// archcheck startup. Adds/removes of capability directories are picked
// up automatically without list edits (Wave 19 PR2-3 acceptance).
//
// Both focused and ratchet modes call this function and observe the
// same set (no drift between modes) because the underlying fs read is
// deterministic for a given checkout.
//
// Non-capability directories are filtered out: hidden directories
// (leading "."), well-known non-cap sub-paths (testdata, mocks,
// helpers). The filter is intentionally narrow — when in doubt, treat
// a directory as a capability so PR2's ratchet gate catches unintended
// drift at the (srcCap == importCap) filter layer.
//
// Edge cases:
//   - os.ReadDir error: a stderr warning is emitted and an empty set
//     returned. This surfaces the symptom (zero counts) immediately to
//     operators instead of silently degrading to a stale hardcoded map.
//     The ratchet gate stays GREEN because 0 < baseline is the natural
//     state. The next operator step is to fix the filesystem path.
//   - No matches at all (empty internal/application): same as above —
//     operators see a zero count instead of a poisoned 22-entry map.
//
// PR2-3 (June 2026) replaces the pre-PR2 hardcoded 22-entry map. The
// prior hook comment ("do not forget when promoting this rule to hard
// gate") is RESOLVED — the divergence risk (silent under-detect when
// new capability dirs are added) no longer exists because
// applicationCapabilities() re-reads the filesystem on every invocation.
func applicationCapabilities() map[string]bool {
	const appPath = "internal/application"
	out := map[string]bool{}
	entries, err := os.ReadDir(appPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "archcheck: applicationCapabilities: read %s: %v (returning empty set; check filesystem mount and operator access)\n", appPath, err)
		return out
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// Filter: hidden directories (e.g. .git) are never capabilities.
		if strings.HasPrefix(name, ".") {
			continue
		}
		// Filter: well-known non-capability sub-paths.
		switch name {
		case "testdata", "mocks", "helpers":
			continue
		}
		out[name] = true
	}
	return out
}

// capabilityOfFile returns the capability name for a Go file under
// `internal/application/<cap>/...`. Returns "" for files OUTSIDE that
// layout (e.g. architecture metadata, scripts directory).
func capabilityOfFile(relPath string, caps map[string]bool) string {
	const prefix = "internal/application/"
	if !strings.HasPrefix(relPath, prefix) {
		return ""
	}
	rest := relPath[len(prefix):]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return "" // file directly in internal/application/ root
	}
	candidate := rest[:slash]
	if !caps[candidate] {
		return "" // unknown sub-package — classification is best-effort
	}
	return candidate
}

// capabilityOfImport returns the capability name for an import line
// shaped like `github.com/Marcuss-ops/PipelineGen/internal/application/<cap>/...`.
// Returns "" for non-matching imports.
func capabilityOfImport(importLine string, caps map[string]bool) string {
	const marker = "github.com/Marcuss-ops/PipelineGen/internal/application/"
	idx := strings.Index(importLine, marker)
	if idx < 0 {
		return ""
	}
	rest := importLine[idx+len(marker):]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return "" // import of internal/application/ root (no Go file there, but defensive)
	}
	candidate := rest[:slash]
	if !caps[candidate] {
		return ""
	}
	return candidate
}

// loadAllowlist reads an allowlist file (one edge or path per line, "#"-prefixed
// comments and blank lines skipped) and returns the sorted, normalized result.
// It is shared by the API import ratchet and the application-boundary ratchet.
//
// The error message is operator-actionable: "allowlist file is required;
// see docs/migrations/api-infrastructure-imports-allowlist.txt" so an
// operator running ratchet and seeing this error knows exactly which
// file is missing and where to find documentation.
//
// bl.NormalizePaths canonicalizes for cross-platform compatibility
// (Windows backslashes -> forward slashes, etc.) so comparison against
// rg output (always slash-separated) is byte-stable.
func loadAllowlist(path string) ([]string, error) {
	text, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w (allowlist file is required; see docs/migrations/application-infrastructure-imports-allowlist.txt)", path, err)
	}
	return parseAllowlist(text), nil
}

func parseAllowlist(text []byte) []string {
	var out []string
	for _, line := range strings.Split(string(text), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return uniqueStrings(bl.NormalizePaths(out))
}

func loadCommittedAllowlist(root, path string) ([]string, error) {
	cmd := exec.Command("git", "-C", root, "show", "HEAD^:"+path)
	text, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("read committed %s: %w", path, err)
	}
	return parseAllowlist(text), nil
}
