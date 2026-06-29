// Package main — CheckSymbolReferences.
//
// symbol_refs.go is the artifact-resolution half of the archcheck gate
// added by action P0-5 slice 4/4 of the cleanup plan (June 2026). The
// rest of scripts/archcheck/ governs CODEBASE-shape regressions
// (database/sql in api/application/domain, interface{} growth,
// dependency setters, ...). This file governs a different axis:
// TOKEN-LEVEL STALE REFERENCES in the architecture documentation
// files (architecture/*.yaml, architecture/archive/**/*.yaml, plus the
// issues file added in slice 2/4).
//
// How it works:
//
//  1. Walk every architecture/**/*.yaml file under the supplied
//     yamlDir (top-level *.yaml files PLUS yamlDir/archive/**/*.yaml,
//     recursively). The function deliberately scopes to those two
//     surfaces and NOT to ownership/*.yaml or migrations/*.yaml —
//     the latter are aggregate-data sub-trees whose contents are
//     owned by other tools (cmd/architecture-aggregate, the migration
//     inventory composer) and whose stale references would surface
//     as a different class of bug that this check should not chase.
//  2. Extract leaf-string-scalar values (the right-hand side of
//     `key: value` lines and `- item` list entries; literal/folded
//     block-scalar bodies (`|-`, `>-`) are joined and treated as one
//     leaf). Block-mapping keys are NOT extracted.
//  3. Skip full-line comments (lines starting with `#` after
//     trim). AGENTS.md zero-legacy rule.
//  4. Find sub-tokens whose FIRST 3+ bytes match one of the
//     canonical Go-path prefixes (`internal/`, `pkg/`, `cmd/`),
//     extending greedily through path chars (a-z, 0-9, _, -, .,
//     /), then trim trailing punctuation. Tokens are deduped per
//     leaf scalar.
//  5. Route each token to one of four validators and emit one
//     Finding per missing reference.
//
// Routing (mirrors the AGENTS.md allowed zones and the existing
// scripts/archcheck/main.go focus gate):
//
//	internal/<...>/*.go   os.Stat the path
//	internal/<pkg>      go list github.com/Marcuss-ops/PipelineGen/<token>
//	pkg/<x>            go list ./<token> (+ os.Stat for *.go tail)
//	cmd/<binary>       os.Stat cmd/<binary>/main.go (+ os.Stat for *.go tail)
//
// False-positive guards:
//   - Full-line comments (`#` lines) are skipped.
//   - Tokens that don't start with `internal/`/`pkg/`/`cmd/` AND
//     don't end with `.go` are NOT considered paths; left alone.
//
// Wired into scripts/ci-architectural-checks.sh as Check N
// (post-Check-1, pre-final-summary). Run via:
//
//	go run ./scripts/archcheck --symbol-refs
//
// Output is written to stderr (one structured line per finding, plus
// a trailing `symbol_refs: N unresolved references` summary). Exit
// code is 1 when any finding is present (CI gate convention).
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// modulePath is the PipelineGen Go module name. Hardcoded rather
// than read from go.mod because the archcheck package is already
// ~10 files and adding a go.mod parser would push the stdlib-only
// dependency story over budget. If anyone renames the module, they
// will need to flip this constant and re-run --symbol-refs locally
// to confirm zero findings before pushing.
const modulePath = "github.com/Marcuss-ops/PipelineGen"

// Finding is one unresolved architecture-reference. File is
// repo-relative. Line is 1-indexed. Token is the raw path token as
// extracted from the YAML scalar (no normalisation — what the
// authored YAML looks like, byte-for-byte). Kind is one of the
// four canonical validators (`internal_go_file`, `internal_pkg`,
// `pkg_go_file`, `pkg_pkg`, `cmd_go_file`, `cmd_binary`). Reason is
// a short operator-readable explanation of WHY the token didn't
// resolve (missing file / go list error / missing main.go).
type Finding struct {
	File   string
	Line   int
	Token  string
	Kind   string
	Reason string
}

// Findings is a sortable slice of Finding. Sorted for deterministic
// CI output (CI dashboards alphabetise by (file, line) so the same
// tree yields the same diff across CI runs).
type Findings []Finding

// goPathPrefixes are the lexical prefixes that mark a YAML scalar
// token as a Go-path reference. They mirror AGENTS.md's allowed
// zones. Anything else is left alone (no false-positive "this
// looks like a path" matches). Order is signficant: tokens are
// scanned for each prefix in turn; the first hit wins.
var goPathPrefixes = []string{"internal/", "pkg/", "cmd/"}

// CheckSymbolReferences walks yamlDir for *.yaml files
// (top-level + yamlDir/archive/ recursively) and returns one
// Finding per Go-like token that does not resolve on the file
// system or via `go list`. The function is the canonical
// entry-point used by scripts/ci-architectural-checks.sh Check N.
//
// Returns nil Findings + nil error when the YAML tree is clean.
// Returns Findings != nil + nil error when some refs resolve and
// some don't (the caller should treat the non-empty Findings as
// the failure signal). Returns Findings = nil + non-nil error when
// one or more files could not be scanned at all (read error); the
// error message names the file path for the operator's
// convenience.
//
// The returned Findings is sorted by (File, Line, Token).
func CheckSymbolReferences(yamlDir string) (Findings, error) {
	files, err := collectYAMLFiles(yamlDir)
	if err != nil {
		return nil, err
	}

	out := Findings{}
	for _, f := range files {
		leaves, scanErr := scanYAMLLeafScalars(f)
		if scanErr != nil {
			return out, fmt.Errorf("scan %s: %w", f, scanErr)
		}
		for _, leaf := range leaves {
			tokens := extractGoPathTokens(leaf.value)
			for _, tok := range tokens {
				if reason := validateToken(tok); reason != "" {
					out = append(out, Finding{
						File:   f,
						Line:   leaf.line,
						Token:  tok,
						Kind:   classifyToken(tok),
						Reason: reason,
					})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Token < out[j].Token
	})
	return out, nil
}

// collectYAMLFiles enumerates the two YAML surfaces that this check
// owns: yamlDir/*.yaml (top-level) and yamlDir/archive/**/*.yaml
// (recursive). Both are in-scope per slice 4/4: current.yaml +
// issues.yaml + ownership.generated.yaml (although generated) live
// at the top level; the historical/audit snapshot
// (`architecture/archive/2026-06-29/current-snapshot-*.yaml`) lives
// under yamlDir/archive. Recursion is intentionally LIMITED to
// archive/ — the ownership/, migrations/ sub-trees are owned by
// different tooling and their stale refs would surface as a
// different bug class.
func collectYAMLFiles(yamlDir string) ([]string, error) {
	var files []string

	topEntries, err := os.ReadDir(yamlDir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", yamlDir, err)
	}
	for _, e := range topEntries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			files = append(files, filepath.Join(yamlDir, e.Name()))
		}
	}

	archiveDir := filepath.Join(yamlDir, "archive")
	if info, err := os.Stat(archiveDir); err == nil && info.IsDir() {
		err := filepath.Walk(archiveDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() && strings.HasSuffix(info.Name(), ".yaml") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk %s: %w", archiveDir, err)
		}
	}

	sort.Strings(files)
	return files, nil
}

// scannedScalar holds one extracted YAML leaf scalar for token
// extraction and reporting. Line is 1-indexed for direct use in
// Finding.Line.
type scannedScalar struct {
	line  int
	value string
}

// leafScanCompile is the regex set compiled once for the YAML leaf
// scalar extractor. Lazy-compiled via package-init so callers don't
// pay the compile cost on every leaf.
var (
	leafKeyValueRE = regexp.MustCompile(`^\s*[\w][\w_-]*:\s+(.+?)\s*$`)
	blockAnchorRE  = regexp.MustCompile(`^\s*[\w][\w_-]*:\s*[|>][-+]?\s*$`)
	listItemRE     = regexp.MustCompile(`^\s*-\s+(.+?)\s*$`)
)

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

// extractGoPathTokens scans a scalar value for substrings that
// START with one of the canonical goPathPrefixes, extending
// greedily through path chars. Tokens are de-duplicated (a single
// scalar can mention the same path twice — we report it once).
func extractGoPathTokens(s string) []string {
	var tokens []string
	seen := map[string]bool{}
	add := func(t string) {
		if t == "" || seen[t] {
			return
		}
		seen[t] = true
		tokens = append(tokens, t)
	}

	for _, pref := range goPathPrefixes {
		from := 0
		for from < len(s) {
			idx := strings.Index(s[from:], pref)
			if idx < 0 {
				break
			}
			absStart := from + idx
			end := absStart + len(pref)
			for end < len(s) && isPathChar(s[end]) {
				end++
			}
			tok := trimTrailingPunctuation(s[absStart:end])
			// Drop single-char noise (e.g. `cmd/.` retrimmed
			// to `cmd/`) — a token must add at least one char
			// beyond the prefix.
			if len(tok) > len(pref) {
				add(tok)
			}
			if end > absStart+1 {
				from = end - 1
			} else {
				from = end
			}
		}
	}

	return tokens
}

// classifyToken picks one of the canonical Kind strings. The
// classifier is purely lexical (no IO) so callers can use it even
// when validateToken would otherwise side-effect (e.g. while
// previewing the kind of a token before running the validator).
func classifyToken(tok string) string {
	switch {
	case strings.HasPrefix(tok, "internal/") && strings.HasSuffix(tok, ".go"):
		return "internal_go_file"
	case strings.HasPrefix(tok, "internal/"):
		return "internal_pkg"
	case strings.HasPrefix(tok, "pkg/") && strings.HasSuffix(tok, ".go"):
		return "pkg_go_file"
	case strings.HasPrefix(tok, "pkg/"):
		return "pkg_pkg"
	case strings.HasPrefix(tok, "cmd/") && strings.HasSuffix(tok, ".go"):
		return "cmd_go_file"
	case strings.HasPrefix(tok, "cmd/"):
		return "cmd_binary"
	}
	return "go_file"
}

// validateToken returns "" when the token resolves cleanly, or a
// non-empty failure reason otherwise. The reason embeds the
// kind-specific IO failure mode for the operator (which file is
// missing, which go list module-path failed). The function is the
// single routing choke-point.
func validateToken(tok string) string {
	if strings.HasSuffix(tok, ".go") {
		if _, err := os.Stat(tok); err != nil {
			return fmt.Sprintf("missing .go file: %s", tok)
		}
		return ""
	}

	switch {
	case strings.HasPrefix(tok, "internal/"):
		if err := runGoList(modulePath + "/" + tok); err != nil {
			return fmt.Sprintf("go list failed: %s", err)
		}
		return ""
	case strings.HasPrefix(tok, "pkg/"):
		if err := runGoList("./" + tok); err != nil {
			return fmt.Sprintf("go list failed: %s", err)
		}
		return ""
	case strings.HasPrefix(tok, "cmd/"):
		main := filepath.Join(tok, "main.go")
		if _, err := os.Stat(main); err != nil {
			return fmt.Sprintf("missing cmd main: %s", main)
		}
		return ""
	}
	return "not a Go-like path"
}

// runGoList shells out to the Go toolchain to validate the package
// path. We use CombinedOutput so the caller sees both stderr and
// stdout as a single error-detail block (useful when a stale
// import path triggers the standard `go list` error chain with
// module-resolution hints).
func runGoList(pkg string) error {
	cmd := exec.Command("go", "list", pkg)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", pkg, strings.TrimSpace(string(out)))
	}
	return nil
}

// runSymbolRefsChecks is the archcheck dispatcher entry-point
// invoked by main.go when cfg.Mode == ModeSymbolRefs. It runs
// CheckSymbolReferences against the canonical yamlDir
// (`architecture/`, in cwd-relative terms — CI runs from
// REPO_ROOT), prints each finding as one structured line to
// stderr, then prints the trailing summary line. Exits 1 when
// findings is non-empty (CI gate convention). Exits 0 otherwise.
//
// Called from scripts/ci-architectural-checks.sh Check N as:
//
//	go run ./scripts/archcheck --symbol-refs
func runSymbolRefsChecks() int {
	yamlDir := "architecture"
	findings, err := CheckSymbolReferences(yamlDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "symbol_refs: scan error: %v\n", err)
		return 2
	}
	if len(findings) == 0 {
		return 0
	}
	for _, f := range findings {
		fmt.Fprintf(os.Stderr,
			"symbol_refs: %s:%d  kind=%s token=%s  %s\n",
			f.File, f.Line, f.Kind, f.Token, f.Reason)
	}
	fmt.Fprintf(os.Stderr, "symbol_refs: %d unresolved references\n", len(findings))
	return 1
}
