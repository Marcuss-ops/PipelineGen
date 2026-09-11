// Package scan — ssot.go: the generic, data-driven forward-prevention engine
// for godlike/06 "one owner per fact" gates.
//
// Every such gate shares ONE skeleton:
//
//	prepare (optional, may fail closed and short-circuit)
//	walk the tree
//	  → skip the standard skip-dirs and the scanner's own package
//	  → take only in-scope, non-test .go files outside the canonical owners
//	  → per line: skip comments, then ask the rule's detector
//	  → OR hand the whole file to a stateful scanner
//	  → emit report.Violation{Rule, MatchedRule, Note, File, Line, Package}
//
// Ten per-fact scanners (embedding constants, duration probe, observability
// operation, speech timing, project derivation, evidence precedence, stopword
// maps, metadata-key registry, indexed-state writer, asset-committer event)
// each used to re-implement that skeleton with their own walk, comment policy,
// snippet truncation, warning bucket and package extraction — well over a
// thousand lines of near-identical code, plus a per-fact `pkgFromXxxRel` +
// `truncateXxx` pair that existed only to serve the copy. The copies could
// drift (and the exhaustiveness of each had to be re-reviewed by hand), and
// every new SSOT fact cost another ~130-line file.
//
// The skeleton now lives here ONCE. A fact is DATA: a row in ssotRules
// (ssot_registry.go) carrying its scope, skip set, owner set, comment policy,
// severity and either a per-line Detect or a stateful ScanFile. Adding a fact
// is a registry row, not a scanner.
//
// Two hooks cover the non-trivial facts without duplicating the walk:
//
//   - Prepare runs once before the walk. It may emit fail-closed configuration
//     violations and return continueWalk=false to skip the tree (the
//     metadata-key registry gate needs this: a missing canonical registry file
//     is a gate failure, and flooding the report with config-drift noise on
//     top of it would hide the real cause).
//   - ScanFile replaces the per-line loop for rules that need cross-line state
//     (stopword-map brace tracking, the indexed-state SET-clause tracker) or a
//     per-file residue-warning bucket.
//
// The rule ids and the exported ScanXxx entry points are unchanged, so
// checks.go, the per-gate tests and every report consumer see the same
// surface: the consolidation removes duplicated code, not coverage.
package governance

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// ssotCommentPrefixes is the standard comment policy: a line whose trimmed
// form starts with one of these is descriptive prose, never a violation.
var ssotCommentPrefixes = []string{"//", "/*", "*"}

// ssotStandardSkipDirs is the standard skip-dir set for the family.
var ssotStandardSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
}

// ssotScannerSourcePrefix is the scanner's own package. Its declarations name
// the forbidden literals for documentation, so it can never be a violation.
// It is always skipped, in addition to a rule's own SkipPathPrefixes.
const ssotScannerSourcePrefix = "cmd/archcheck/scan"

// ssotSnippetMaxLen bounds the snippet surface so report JSON stays stable.
const ssotSnippetMaxLen = 120

// ssotSnippetMarker is appended when a snippet is truncated.
const ssotSnippetMarker = " <<<"

// ssotNoDirSkips disables directory skipping for a rule that must walk the
// whole tree, including .git/vendor (an empty non-nil map, vs nil = the
// standard skip set).
var ssotNoDirSkips = map[string]bool{}

// ssotRule is one forward-prevention fact.
type ssotRule struct {
	// Name is the rule-family id written to report.Violation.Rule.
	Name string
	// MatchedRule is the machine-readable reason written to MatchedRule.
	MatchedRule string
	// Severity is the emitted severity. Empty means SeverityError.
	Severity string
	// Scope are the path prefixes the rule applies to. Empty = the whole tree.
	Scope []string
	// SkipDirs replaces ssotStandardSkipDirs when non-nil. An EMPTY non-nil
	// map disables directory skipping entirely (the observability rule
	// deliberately walks the whole tree).
	SkipDirs map[string]bool
	// SkipPathPrefixes are extra path prefixes skipped at directory and file
	// level, on top of ssotScannerSourcePrefix.
	SkipPathPrefixes []string
	// Owners are the canonical owner packages, exempt at directory and file
	// level.
	Owners []string
	// CommentPrefixes replaces ssotCommentPrefixes when non-nil.
	CommentPrefixes []string
	// Prepare runs once before the walk and returns opaque state handed to
	// Detect/ScanFile. continueWalk=false short-circuits the walk (fail-closed
	// configuration gates).
	Prepare func(root string, r *report.Report, rule *ssotRule) (state any, continueWalk bool)
	// Detect returns one violation note per detected violation on the line.
	// An empty (or nil) result means the line is clean. The raw, untrimmed
	// line is passed so anchored patterns (`^\s*(?:const|var)?…`) behave
	// exactly as they did in the per-fact scanners; relPath is relative to
	// the scan root with forward slashes, for file-scoped conditions.
	// Mutually exclusive with ScanFile.
	Detect func(state any, relPath, line string) []string
	// ScanFile replaces the default per-line loop for rules that need
	// cross-line state or a per-file residue bucket. Mutually exclusive with
	// Detect.
	ScanFile func(state any, path, relPath string, r *report.Report, rule *ssotRule)
	// SuppressResidueWarnings silences the rule's per-file residue-warning
	// bucket (the asset-committer gate's operator-facing productionOnly mode).
	SuppressResidueWarnings bool
}

// scanSSOTRule runs one registry rule over the tree rooted at root.
func scanSSOTRule(root string, r *report.Report, rule ssotRule) {
	if rule.Severity == "" {
		rule.Severity = string(report.SeverityError)
	}

	var state any
	if rule.Prepare != nil {
		var continueWalk bool
		state, continueWalk = rule.Prepare(root, r, &rule)
		if !continueWalk {
			return
		}
	}

	skipDirs := rule.SkipDirs
	if skipDirs == nil {
		skipDirs = ssotStandardSkipDirs
	}
	commentPrefixes := rule.CommentPrefixes
	if commentPrefixes == nil {
		commentPrefixes = ssotCommentPrefixes
	}
	skipPrefixes := append([]string{ssotScannerSourcePrefix}, rule.SkipPathPrefixes...)

	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		relSlash := filepath.ToSlash(rel)

		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			if hasAnyPathPrefix(relSlash, skipPrefixes) || hasAnyPathPrefix(relSlash, rule.Owners) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(relSlash, "_test.go") {
			return nil
		}
		if len(rule.Scope) > 0 && !hasAnyPathPrefix(relSlash, rule.Scope) {
			return nil
		}
		if hasAnyPathPrefix(relSlash, skipPrefixes) || hasAnyPathPrefix(relSlash, rule.Owners) {
			return nil
		}
		if rule.ScanFile != nil {
			rule.ScanFile(state, path, relSlash, r, &rule)
			return nil
		}
		scanSSOTFile(state, path, relSlash, r, &rule, commentPrefixes)
		return nil
	})
}

// scanSSOTFile emits one violation per detected line in a single .go file.
func scanSSOTFile(state any, path, relSlash string, r *report.Report, rule *ssotRule, commentPrefixes []string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		if ssotIsComment(line, commentPrefixes) {
			continue
		}
		notes := rule.Detect(state, relSlash, line)
		if len(notes) == 0 {
			continue
		}
		for _, note := range notes {
			ssotEmit(r, rule, relSlash, lineNo, rule.MatchedRule, note)
		}
	}
}

// ssotEmit appends one violation for rule, using the rule's configured
// severity and default matched-rule reason. Stateful scanners pass their own
// matchedRule/reason when a fact emits more than one.
func ssotEmit(r *report.Report, rule *ssotRule, relPath string, line int, matchedRule, note string) {
	severity := rule.Severity
	if severity == "" {
		severity = string(report.SeverityError)
	}
	r.Violations = append(r.Violations, report.Violation{
		Package:     pkgFromRelPath(relPath),
		File:        relPath,
		Line:        line,
		Rule:        rule.Name,
		Severity:    severity,
		MatchedRule: matchedRule,
		Note:        note,
	})
}

// ssotWarn appends one residue-accounting warning in the family format
// `<rule> <label> <msg>`, unless the rule suppresses its residue bucket.
func ssotWarn(r *report.Report, rule *ssotRule, label, msg string) {
	if rule.SuppressResidueWarnings {
		return
	}
	r.Warnings = append(r.Warnings, rule.Name+" "+label+" "+msg)
}

// ssotIsComment reports whether a line is descriptive prose under prefixes.
func ssotIsComment(line string, prefixes []string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	for _, prefix := range prefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

// ssotMatchLine builds the common single-predicate Detect: when pred matches
// the raw line, the violation note is base + " | snippet: <line>".
//
// It replaces the per-fact `<name>Match` / regex + note-composition pairs the
// scanners each carried.
func ssotMatchLine(base string, pred func(line string) bool) func(state any, relPath, line string) []string {
	return func(_ any, _ string, line string) []string {
		if !pred(line) {
			return nil
		}
		return []string{base + " | snippet: " + truncateSSOTSnippet(line)}
	}
}

// truncateSSOTSnippet bounds the snippet surface at ssotSnippetMaxLen.
func truncateSSOTSnippet(s string) string {
	if len(s) > ssotSnippetMaxLen {
		return s[:ssotSnippetMaxLen] + ssotSnippetMarker
	}
	return s
}

// pkgFromRelPath extracts the package identifier from a repo-relative path.
func pkgFromRelPath(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}

// ssotRuleNames returns the registered rule ids in registry order. It exists
// for the registry-integrity test and for operator introspection.
func ssotRuleNames() []string {
	out := make([]string, 0, len(ssotRules))
	for _, rule := range ssotRules {
		out = append(out, rule.Name)
	}
	return out
}

// ssotRuleByName resolves a registered rule by id. A missing id is a
// programming error in the registry, so it panics loudly at first use rather
// than silently scanning nothing (godlike/07: a gate that cannot fail open).
func ssotRuleByName(name string) ssotRule {
	for _, rule := range ssotRules {
		if rule.Name == name {
			return rule
		}
	}
	panic("governance: unregistered ssot rule referenced: " + name)
}
