// Package scan — struct-deps scanner.
//
// scan/structdeps.go owns the "struct_deps" rule family that counts
// fields in Dependencies / Deps / Options structs. Mega-structs that
// bundle >8 mandatory ports circumvent the constructor-parameter gate;
// this scanner catches them at the type level.
//
// P1.7 (June 2026): the constructor-parameter scan (constructors.go)
// counts ONLY func New<X>(...) parameters. Struct-based dependency
// injection (where a single Dependencies struct carries 11-20+ fields)
// is invisible to that scan. This scanner detects struct types whose
// name is "Dependencies", "Deps", "Options", or ends with those
// suffixes (e.g. "ServiceDeps", "UseCaseDeps") and counts their fields.
//
// Optional-field exclusion: well-known optional/nil-safe types
// (*zap.Logger, primitive config values) are excluded from the count
// so the gate focuses on mandatory operational ports. The exclusion
// list is intentionally narrow — add to it when the team agrees a
// type is always optional.
//
// Cross-references:
//   - cmd/archcheck/scan/constructors.go: the sibling constructor-param scanner
//   - architecture/policy.yaml: max_struct_deps threshold
//   - docs/architecture/godlike/08_ARCHITECTURE_CI_GATES.md §"Complexity budgets"
package scan

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// structDepsNames is the set of type-name suffixes that trigger the
// struct-field count. A type whose name equals one of these, or
// ends with one of these (e.g. "ServiceDeps" ends with "Deps"),
// is flagged.
var structDepsNames = []string{"Dependencies", "Deps", "Options"}

// optionalFieldTypes is the set of type patterns that are considered
// optional/nil-safe and excluded from the mandatory-port count.
// Matching is by the LAST word of the field type (after the last
// dot, space, or `*`).
//
//   - *zap.Logger — always nil-safe in constructors
//   - int, string, bool, float64 — configuration knobs, not ports
//   - time.Duration — configuration value
//
// This list is intentionally narrow. Expand when the team agrees a
// type is always optional (an allowlist, NOT a denylist).
var optionalFieldTypes = map[string]bool{
	"Logger":   true, // *zap.Logger — always nil-safe via zap.NewNop()
	"int":      true, // configuration knobs
	"string":   true,
	"bool":     true,
	"float64":  true,
	"Duration": true, // time.Duration
}

// typeDeclRe matches `type <Name> struct {` at the start of a line
// (with optional leading whitespace).
var typeDeclRe = regexp.MustCompile(`^\s*type\s+(\w+)\s+struct\s*\{`)

// ScanStructDeps walks non-test Go source files under <root>/internal/,
// finds `type <Name> struct {` declarations whose name matches the
// Dependencies/Deps/Options family, counts their mandatory-port fields,
// and emits a warn-severity violation when the field count exceeds the
// threshold.
//
// Optional fields (*zap.Logger, primitive config types) are excluded
// from the count per optionalFieldTypes. When pol.MaxStructDeps <= 0
// the scanner is a no-op (opt-out).
func ScanStructDeps(root string, pol *policy.Policy, r *report.Report) {
	if pol.MaxStructDeps <= 0 {
		return
	}
	threshold := pol.MaxStructDeps

	skipDirs := map[string]bool{
		".git": true, "vendor": true, "node_modules": true,
		"node-scraper": true, "examples": true, "scripts": true,
	}
	internalDir := filepath.Join(root, "internal")

	_ = filepath.WalkDir(internalDir, func(path string, d os.DirEntry, err error) error {
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
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		rel, _ := filepath.Rel(root, path)
		relPath := filepath.ToSlash(rel)

		sc := bufio.NewScanner(f)
		lineNum := 0
		for sc.Scan() {
			lineNum++
			line := sc.Text()
			m := typeDeclRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			typeName := m[1]
			if !isDepsStructName(typeName) {
				continue
			}

			// Collect the struct body from the opening '{'
			// onward until braces balance.
			openBrace := strings.Index(line, "{")
			body := line[openBrace:]
			depth := depthOf(body, '{', '}')
			startLine := lineNum
			for depth > 0 && sc.Scan() {
				lineNum++
				next := sc.Text()
				body += "\n" + next
				depth = depthOf(body, '{', '}')
			}

			// Count mandatory-port fields (exclude optional).
			fieldCount := countMandatoryFields(body)
			if fieldCount > threshold {
				r.Violations = append(r.Violations, report.Violation{
					File:         relPath,
					Line:         startLine,
					ActualCount:  fieldCount,
					AllowedCount: threshold,
					MatchedRule:  "max_struct_deps",
					Rule:         "struct_deps",
					Severity:     "warn",
					Note: fmt.Sprintf(
						"struct %s has %d mandatory-port fields (max %d); split into smaller port bundles (e.g. DeliveryPorts + MediaProcessingPorts). Optional fields (*zap.Logger, primitive config) are excluded.",
						typeName, fieldCount, threshold,
					),
				})
			}
		}
		return nil
	})
}

// isDepsStructName returns true when name equals or ends with one of
// the known mega-struct suffixes (Dependencies, Deps, Options).
func isDepsStructName(name string) bool {
	for _, suffix := range structDepsNames {
		if name == suffix || strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// depthOf returns the net depth of open minus close runes in s.
// Shared between constructors.go (parens) and structdeps.go (braces).
func depthOf(s string, open, close byte) int {
	d := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case open:
			d++
		case close:
			d--
		}
	}
	return d
}

// countMandatoryFields counts struct fields that are NOT excluded as
// optional. It uses a line-based heuristic:
//
//   - Tracks brace depth (inside the struct body, depth 1 means
//     top-level fields).
//   - Tracks raw-string literals (backtick-delimited) so multi-line
//     struct tags don't confuse depth tracking.
//   - Excludes comment-only lines.
//   - For each line at depth 1 that contains a field declaration,
//     checks if the field type is in the optional-exclusion set.
//
// Fields on the same line as the opening `{` (e.g.
// `type X struct { Repo Repository`) are handled: the `{` prefix
// is stripped before counting.
func countMandatoryFields(body string) int {
	lines := strings.Split(body, "\n")
	depth := 0
	count := 0
	inRawString := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track raw string literals (backtick-delimited struct tags
		// that span multiple lines).
		for i := 0; i < len(line); i++ {
			if line[i] == '`' && !inRawString {
				inRawString = true
			} else if line[i] == '`' && inRawString {
				inRawString = false
			}
		}

		// Track brace depth — process braces on this line.
		for _, c := range line {
			switch c {
			case '{':
				depth++
			case '}':
				depth--
			}
		}

		if inRawString {
			continue
		}

		// Only count lines at depth 1 (top-level struct fields).
		if depth != 1 {
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		// Exclude bare braces — opening `{` (possibly with a field
		// on the same line, handled below) and closing `}` (nested
		// struct edge, depth brought back to 1 by inner close).
		if trimmed == "{" || trimmed == "}" {
			continue
		}

		// Strip leading '{' if present (field on same line as
		// opening brace: `{ Repo Repository`).
		fieldLine := trimmed
		if strings.HasPrefix(fieldLine, "{") {
			fieldLine = strings.TrimSpace(strings.TrimPrefix(fieldLine, "{"))
		}
		if fieldLine == "" {
			continue
		}

		// Extract the field type and check if it's optional.
		if isOptionalField(fieldLine) {
			continue
		}

		count++
	}
	return count
}

// isOptionalField checks whether a field declaration line's type is
// in the optional-exclusion set. It looks at the LAST word of the
// type (after stripping the field name, pointer `*`, and package
// prefix).
//
// Examples:
//
//	"Logger *zap.Logger"       → last word "Logger" → optional
//	"MaxParallel int"          → last word "int"     → optional
//	"Repo Repository"          → last word "Repository" → NOT optional
//	"DB *sql.DB"               → last word "DB"      → NOT optional
//	"*sql.DB"                  → last word "DB"      → NOT optional (embedded)
func isOptionalField(line string) bool {
	// Remove the field name: split on whitespace, the first token
	// is the field name (or it's an embedded type with no name).
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return true // empty — skip
	}

	// The last word is the type (possibly after package prefix).
	typeWord := parts[len(parts)-1]
	// Strip pointer `*` if present.
	typeWord = strings.TrimPrefix(typeWord, "*")
	// Strip package prefix (e.g. `zap.Logger` → `Logger`).
	if idx := strings.LastIndex(typeWord, "."); idx >= 0 {
		typeWord = typeWord[idx+1:]
	}

	return optionalFieldTypes[typeWord]
}
