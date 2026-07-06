package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ── YAML AST helpers ──────────────────────────────────────────────────

// scannedScalar holds one extracted YAML leaf scalar for token
// extraction and reporting. Line is 1-indexed for direct use in
// Finding.Line.
type scannedScalar struct {
	line  int
	value string
}

// keyFrame is one (indent, key) entry in the indentation-tracking
// stack used by scanYAMLScopedLeafScalars. Indent is the column
// count of the leading whitespace on the line that introduced the
// frame; key is the bare key name (no colons). The stack is
// maintained such that deeper-or-equal indent frames are popped
// before pushing a new frame so the top always points to the
// most-recent shallower key — that is, the YAML "nearest shallower
// key" ancestor rule applied to a line-by-line walker.
type keyFrame struct {
	indent int
	key    string
}

// leafScanCompile is the regex set compiled once for the YAML leaf
// scalar extractor. Lazy-compiled via package-init so callers don't
// pay the compile cost on every leaf.
var (
	leafKeyValueRE = regexp.MustCompile(`^\s*[\w][\w_-]*:\s+(.+?)\s*$`)
	blockAnchorRE  = regexp.MustCompile(`^\s*[\w][\w_-]*:\s*[|>][-+]?\s*$`)
	listItemRE     = regexp.MustCompile(`^\s*-\s+(.+?)\s*$`)
)

// collectYAMLFiles for slice 4/4 of action P0-5 returns ONLY the two
// specific yamlDir files named in the user spec:
//
//	yamlDir/current.yaml   — active migration + wave tracker
//	yamlDir/issues.yaml    — open technical-issues tracker
//
// Other yaml surfaces (policy.yaml, deprecations.yaml,
// ownership.generated.yaml, ownership/<split>.yaml, migrations/*.yaml,
// archive/**/*.yaml) are deliberately EXCLUDED: each is owned by
// different tooling (cmd/archcheck policy rules, cmd/architecture-
// aggregate generator, migration inventory composer, snapshot audit)
// and their stale-reference drift is a different bug class that
// symbol_refs.go should not chase.
//
// Each candidate path is os.Stat-checked so the gate stays GREEN
// when one is briefly absent (e.g. on a fresh clone whose bootstrap
// step has not yet written issues.yaml), instead of erroring out.
func collectYAMLFiles(yamlDir string) ([]string, error) {
	scoped := []string{
		filepath.Join(yamlDir, "current.yaml"),
		filepath.Join(yamlDir, "issues.yaml"),
	}
	var files []string
	for _, p := range scoped {
		if _, err := os.Stat(p); err == nil {
			files = append(files, p)
		}
	}
	sort.Strings(files)
	return files, nil
}

// scanYAMLLeafScalars opens path and extracts every leaf string
// scalar (line + value) it can find, skipping full-line comments
// (per AGENTS.md zero-legacy rule) and block-mapping keys. Block
// scalars (`|-`, `>-`, ...) are joined and treated as a single leaf.
func scanYAMLLeafScalars(path string) ([]scannedScalar, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(raw), "\n")

	var out []scannedScalar
	inBlock := false
	blockBody := []string{}
	blockTopLine := 0
	blockMinIndent := 0

	for i, rawLine := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(rawLine)

		// Skip full-line comments (`#`-prefixed).
		if strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Block-scalar body handling: collect continuation
		// lines until indentation falls back to indent of the
		// anchor or below. Body lines that are themselves
		// comments are dropped (per spec); body lines that
		// are blank are kept (they are part of the literal).
		if inBlock {
			indent := len(rawLine) - len(strings.TrimLeft(rawLine, " "))
			stillInBlock := indent > blockMinIndent || rawLine == ""
			commentLine := strings.HasPrefix(strings.TrimSpace(rawLine), "#")
			switch {
			case stillInBlock && !commentLine && rawLine != "":
				blockBody = append(blockBody, rawLine)
				continue
			case stillInBlock:
				continue
			default:
				out = append(out, scannedScalar{
					line:  blockTopLine,
					value: strings.Join(blockBody, "\n"),
				})
				inBlock = false
				blockBody = nil
			}
		}

		// Block-scalar anchor: `key: |`, `key: >`, `key: |-`,
		// `key: >-` (literal/folded block-scalar markers).
		if blockAnchorRE.MatchString(rawLine) {
			inBlock = true
			blockTopLine = lineNum
			// Body indent must be strictly greater than the
			// anchor's own indent column. YAML's spec (§8.1.1)
			// requires the block-scalar's content to be MORE
			// indented than the block's parent — so a body
			// line at indent +1 (relative to the anchor) is
			// already inside the block. Using `+ 1` matches
			// `internal/exit_gate: |-` style fixtures where the
			// body sits at 2 spaces under a column-0 anchor.
			// (A previous `+ 2` variant rejected those body
			// lines as out-of-block, see symbol_refs_test.go
			// regression history.)
			blockMinIndent = len(rawLine) - len(strings.TrimLeft(rawLine, " ")) + 1
			continue
		}

		// Leaf scalar: `key: value` plain-flow lines.
		if m := leafKeyValueRE.FindStringSubmatch(rawLine); m != nil {
			value := strings.Trim(m[1], `"'`)
			out = append(out, scannedScalar{line: lineNum, value: value})
			continue
		}

		// List item: `- value` (the most common in YAML for
		// `linked_issues: [...]` and similar sequences).
		if m := listItemRE.FindStringSubmatch(rawLine); m != nil {
			value := strings.Trim(m[1], `"'`)
			out = append(out, scannedScalar{line: lineNum, value: value})
			continue
		}
	}

	// Flush a trailing open block scalar (defensive — file
	// ended on a still-open block).
	if inBlock {
		out = append(out, scannedScalar{
			line:  blockTopLine,
			value: strings.Join(blockBody, "\n"),
		})
	}

	return out, nil
}

// isPathChar reports whether c is permitted inside an extracted
// path token. Validated byte-by-byte (NOT Unicode-aware) because
// all valid Go import paths / file paths are ASCII.
func isPathChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z':
		return true
	case c >= 'A' && c <= 'Z':
		return true
	case c >= '0' && c <= '9':
		return true
	case c == '_', c == '.', c == '-', c == '/':
		return true
	}
	return false
}

// trimTrailingPunctuation drops trailing noise (`,`, `)`, `]`, `;`,
// trailing `.`, trailing `/`) from a candidate token. The function
// is conservative — it never SHORTENS a token that could still be
// a valid Go-path reference. It does NOT touch internal
// punctuation; only the very last 1–3 bytes that are clearly
// non-path.
func trimTrailingPunctuation(s string) string {
	for len(s) > 0 {
		c := s[len(s)-1]
		if c == '.' || c == '/' || c == ',' || c == ')' || c == ']' || c == ';' {
			s = s[:len(s)-1]
			continue
		}
		break
	}
	return s
}

// scanYAMLScopedLeafScalars replaces scanYAMLLeafScalars for the
// slice 4/4 production path. It walks yamlPath line by line,
// tracks an indent-aware stack of parent-key frames, and emits a
// leaf scalar ONLY when its ancestor chain includes one of the
// scopedParents keys. scanYAMLLeafScalars is retained unchanged for
// the existing TestScanYAMLLeafScalars_SkipsComments unit-test
// surface so the leaf-scalar extractor remains testable in
// isolation.
//
// Indent-tracking rules:
//
//	`key: value`     at indent I => pop stack[].indent >= I, push (I, key).
//	`- key: value`   at indent I => the dash sits at column I,
//	                   the `key:` starts at I+2; push (I+2, key).
//	`- value`        (bare list item, no sub-key) at indent I =>
//	                   no stack mutation; ancestor chain preserved.
//	`key: |` / `key: >`  (block-scalar anchor) at indent I =>
//	                   push (I, key); body lines inherit.
//
// Comments (full-line `#`-prefixed) are skipped per AGENTS.md
// zero-legacy rule. Bodies within `|-` / `>-` block scalars are
// collected and joined as a single leaf so prose like
// `internal/application/voiceover` written across multiple lines
// is detected as one token. The body inherits the anchor's parent
// context, so a block scalar under `linked_issues:` is in scope
// while a block scalar under `exit_gate:` (which contains
// narrative prose) is intentionally out.
func scanYAMLScopedLeafScalars(yamlPath string, scopedParents map[string]bool) ([]scannedScalar, error) {
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(raw), "\n")

	// Lazy-compiled per-scan regexes (one per file scan; cheap
	// relative to file I/O but local to the function so the test
	// fixture path stays compact).
	keyOnlyRE := regexp.MustCompile(`^\s*([\w][\w_-]*):`)
	// keyBareRE matches a key-only line (e.g. `linked_issues:` with
	// no value after the colon). These open a new parent context
	// whose descendants must be in-scope under the linked_issues /
	// blocker filter — without this match, the parent-key stack would
	// never see `linked_issues:` and every nested leaf would be
	// silently dropped (parent-context bug surfaced by
	// TestScanYAMLScopedLeafScalars_FiltersToParentKeys in
	// symbol_refs_test.go).
	keyBareRE := regexp.MustCompile(`^\s*([\w][\w_-]*):\s*$`)
	listItemKeyValueRE := regexp.MustCompile(`^\s*-\s+([\w][\w_-]*):\s+(.+?)\s*$`)

	var stack []keyFrame
	var out []scannedScalar
	inBlock := false
	blockBody := []string{}
	blockTopLine := 0
	blockMinIndent := 0

	pushKey := func(indent int, key string) {
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, keyFrame{indent: indent, key: key})
	}
	isInScope := func() bool {
		for _, f := range stack {
			if scopedParents[f.key] {
				return true
			}
		}
		return false
	}

	for i, rawLine := range lines {
		lineNum := i + 1
		trimmed := strings.TrimSpace(rawLine)

		// Skip full-line comments.
		if strings.HasPrefix(trimmed, "#") {
			continue
		}

		indent := len(rawLine) - len(strings.TrimLeft(rawLine, " "))

		// Block-scalar body handling: append body lines until
		// indentation drops back to the anchor's column. Body
		// lines inherit the anchor's parent context.
		if inBlock {
			stillInBlock := indent > blockMinIndent || rawLine == ""
			commentLine := strings.HasPrefix(strings.TrimSpace(rawLine), "#")
			switch {
			case stillInBlock && !commentLine && rawLine != "":
				blockBody = append(blockBody, rawLine)
				continue
			case stillInBlock:
				continue
			default:
				if isInScope() {
					out = append(out, scannedScalar{
						line:  blockTopLine,
						value: strings.Join(blockBody, "\n"),
					})
				}
				inBlock = false
				blockBody = nil
			}
		}

		// Block-scalar anchor: `key: |`, `key: >`, `key: |-`,
		// `key: >-`. Push the anchor's key onto the stack.
		if blockAnchorRE.MatchString(rawLine) {
			if m := keyOnlyRE.FindStringSubmatch(rawLine); m != nil {
				pushKey(indent, m[1])
			}
			inBlock = true
			blockTopLine = lineNum
			blockMinIndent = len(rawLine) - len(strings.TrimLeft(rawLine, " ")) + 1
			continue
		}

		// - key: value (list-item entry — the per-ticket fields
		// under `linked_issues:` arrive in this shape).
		if m := listItemKeyValueRE.FindStringSubmatch(rawLine); m != nil {
			key, val := m[1], strings.Trim(m[2], `"'`)
			pushKey(indent+2, key)
			if isInScope() {
				out = append(out, scannedScalar{line: lineNum, value: val})
			}
			continue
		}

		// key:   (bare key-only line — opens a new parent context.
		// Must be matched BEFORE the `key: value` plain-flow case
		// so the bare shape is consumed by pushKey (not by the value
		// extractor, which would fail to match `\s+(.+?)\s*$` and
		// silently drop the parent context). Examples: `linked_issues:`,
		// `blocker:`, `description:`, `owner:`.
		if m := keyBareRE.FindStringSubmatch(rawLine); m != nil {
			pushKey(indent, m[1])
			continue
		}

		// key: value (plain-flow).
		if m := leafKeyValueRE.FindStringSubmatch(rawLine); m != nil {
			if km := keyOnlyRE.FindStringSubmatch(rawLine); km != nil {
				pushKey(indent, km[1])
			}
			val := strings.Trim(m[1], `"'`)
			if isInScope() {
				out = append(out, scannedScalar{line: lineNum, value: val})
			}
			continue
		}

		// - value (bare list item, e.g. `blocker: ["14"]`).
		if m := listItemRE.FindStringSubmatch(rawLine); m != nil {
			val := strings.Trim(m[1], `"'`)
			if isInScope() {
				out = append(out, scannedScalar{line: lineNum, value: val})
			}
			continue
		}
	}

	// Defensive flush of a trailing open block scalar (file ended
	// on a still-open block).
	if inBlock {
		if isInScope() {
			out = append(out, scannedScalar{
				line:  blockTopLine,
				value: strings.Join(blockBody, "\n"),
			})
		}
	}
	return out, nil
}
