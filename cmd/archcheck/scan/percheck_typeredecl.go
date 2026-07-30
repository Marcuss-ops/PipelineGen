// Package scan — per-check ripgrep-equivalent scanners (Phase 1).
//
// scan/percheck_typeredecl.go owns the Go migration of
// scripts/ci-architectural-checks.sh::Check 5 (same-package
// duplicate-type-declarations lint). Phase 1 of
// PR-ARCHCHECK-GO-MIGRATION-PHASE-1 (deadline 2026-08-15) ships
// this scanner alongside the original shell check, which is RETAINED
// as a transitional baseline per godlike/08 §"Zero-baseline rule"
// (the live tree may carry transitional redeclarations while the
// EXPAND→BACKFILL→CUTOVER→CONTRACT migration sequence runs).
//
// Cross-references:
//   - architecture/current.yaml#PR-ARCHCHECK-GO-MIGRATION-PHASE-1: the
//     wave-tracker entry that registers this closure.
//   - architecture/current.yaml#id-20: the original type-redecl
//     tracker (QDRANT-RECOVERY-001 follow-up).
//   - docs/migrations/duplicate-types-allowlist.txt: the per-package
//     allowlist the scanner reads (same file the shell check reads —
//     single source of truth). Post-P1-7 the allowlist has zero
//     internal/domain/job cells; the canonical kernel/job surface is
//     the sole owner of all job-related types.
//   - docs/architecture/godlike/07_ZERO_LEGACY_POLICY.md §"moved-to-shared-types-package":
//     the canonical resolution order (pick one file as canonical OR
//     add an allowlist row with owner + deadline).
package scan

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// duplicateTypesAllowlistRelPath is the repo-relative path to the
// canonical per-package type-redeclaration allowlist. The shell
// check reads the same file (single source of truth per godlike/06
// SSOT). The path is relative to the project root (not the
// archcheck binary's CWD) so the scanner is CWD-stable.
const duplicateTypesAllowlistRelPath = "docs/migrations/duplicate-types-allowlist.txt"

// ScanTypeRedeclarations walks every non-test .go file under
// <root>/internal/, extracts each package's exported type
// declarations via go/parser AST, and emits an `error`-severity
// violation for every (package, TypeName) pair declared in >=2
// files of the same Go package that is NOT in the canonical
// allowlist.
//
// Severity is `error` because the same-package redeclaration is a
// build error in Go (the shell check exits 1 on hits; the Go
// scanner must produce the same fail-closed behaviour under
// --strict mode). For non-strict mode, the runner still prints
// the report; the exit code remains 0 unless --strict is on.
//
// Skipped directories (mirrors ScanPackages skipDirs): .git,
// vendor, node_modules, node-scraper, examples, scripts.
// *_test.go files are excluded per the original shell check
// semantics (the pre-PR-check-5 false-positive class was
// cross-package-NAME not cross-file-in-same-package; test
// fixtures may freely redeclare test-local types without
// surfacing as violations).
//
// Type aliases (`type X = Y`) are captured under the alias name
// (X), matching the shell check's awk regex `^type[[:space:]]+[A-Z]`
// semantics. Generic types (`type X[T any]`) are captured under
// the type name (X), with the type parameter list ignored — the
// shell awk regex matches the same way.
//
// Allowlist file format (matches shell, with dirpath-aware superset):
//
//	# comments are ignored
//	<pkg>:<TypeName>                 # legacy — suffix-match against new key
//	<dirpath>\x00<pkg>\x00<TypeName>  # precise — exact key match
//
// Only the first whitespace-separated token is consumed. A
// missing allowlist file is treated as zero entries (no
// exceptions), matching the shell `if [ -f ... ]` guard.
func ScanTypeRedeclarations(root string, pol *policy.Policy, r *report.Report) {
	allowlist := loadDuplicateTypesAllowlist(root)

	skipDirs := map[string]bool{
		".git": true, "vendor": true, "node_modules": true,
		"node-scraper": true, "examples": true, "scripts": true,
	}

	// typeSites maps a NUL-separated (dirpath,pkg,TypeName)
	// composite key to the list of (file, line) sites where the
	// type is declared. We use a composite key (rather than a
	// nested map) so the dedup-and-sort pass is a single linear walk.
	//
	// The dirpath component distinguishes distinct Go packages that
	// happen to share the same package NAME (eliminating cross-directory
	// same-package-NAME false positives while still detecting TRUE
	// same-package (same-dir) redeclarations). Prior to P1-7 this
	// example was `package job` in internal/domain/job vs
	// internal/kernel/job; post-P1-7 only internal/kernel/job owns
	// `package job` and the cross-directory mirroring is gone.
	type site struct{ file, line string }
	typeSites := map[string][]site{}

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

		fset := token.NewFileSet()
		f, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil || f == nil {
			// Skip files that fail to parse (defensive: the
			// pre-PR-check-5 false-positive class included
			// parse-failure false-positives; the shell awk
			// also silently skips non-parseable files).
			return nil
		}
		pkg := f.Name.Name
		if pkg == "" {
			return nil
		}

		// Walk the AST for exported TypeSpec declarations.
		// The single full-file parse exposes BOTH the package
		// name (f.Name.Name) AND the type-spec list
		// (f.Decls). No two-pass pattern needed.
		relPath, _ := filepath.Rel(root, path)
		relPath = filepath.ToSlash(relPath)
		pkgPath := filepath.Dir(relPath)

		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				// Exported types only (lowercase skipped),
				// matching the shell awk regex
				// `^type[[:space:]]+[A-Z]`.
				if !ast.IsExported(ts.Name.Name) {
					continue
				}
				pos := fset.Position(ts.Pos())
				key := pkgPath + "\x00" + pkg + "\x00" + ts.Name.Name
				typeSites[key] = append(typeSites[key], site{
					file: relPath,
					line: pos.String(), // "file:line:col"
				})
			}
		}
		return nil
	})

	// Emit violations for keys with >= 2 sites not in allowlist.
	// Sort the violation output by (package, TypeName) for stable
	// snapshot-test output.
	type violationKey struct {
		pkg, name string
		sites     []site
	}
	var offenders []violationKey
	for key, sites := range typeSites {
		if len(sites) < 2 {
			continue
		}
		// Allowlist matching accepts BOTH the dirpath-aware
		// composite key (new format) AND the legacy
		// `<pkg>:<TypeName>` form (existing entries). Legacy
		// entries are matched as a suffix of the dirpath-aware
		// key: we convert their `:` separator to `\x00` and
		// check whether the converted entry suffix-matches the
		// trailing portion of the key.
		allowed := false
		if _, ok := allowlist[key]; ok {
			allowed = true
		}
		if !allowed {
			for entry := range allowlist {
				converted := strings.Replace(entry, ":", "\x00", 1)
				if converted != entry && strings.HasSuffix(key, converted) {
					allowed = true
					break
				}
			}
		}
		if allowed {
			continue
		}
		// Split the dirpath-aware key back into its 3 parts.
		parts := strings.Split(key, "\x00")
		if len(parts) != 3 {
			continue
		}
		offenders = append(offenders, violationKey{
			pkg:   parts[1],
			name:  parts[2],
			sites: sites,
		})
	}
	sort.Slice(offenders, func(i, j int) bool {
		if offenders[i].pkg != offenders[j].pkg {
			return offenders[i].pkg < offenders[j].pkg
		}
		return offenders[i].name < offenders[j].name
	})

	for _, off := range offenders {
		// Build a stable site list (sort by file then line) for the
		// Note field, mirroring the shell `sites: a.go:1, b.go:2`
		// output shape.
		sortedSites := append([]site(nil), off.sites...)
		sort.Slice(sortedSites, func(i, j int) bool {
			if sortedSites[i].file != sortedSites[j].file {
				return sortedSites[i].file < sortedSites[j].file
			}
			return sortedSites[i].line < sortedSites[j].line
		})
		siteStrs := make([]string, 0, len(sortedSites))
		for _, s := range sortedSites {
			siteStrs = append(siteStrs, s.file+":"+shortLine(s.line))
		}
		r.Violations = append(r.Violations, report.Violation{
			Package:     "internal/" + off.pkg,
			Rule:        "percheck_type_redecl",
			Severity:    string(report.SeverityError),
			MatchedRule: "same_package_duplicate_type_declarations",
			Note:        off.pkg + "." + off.name + " (count=" + strconv.Itoa(len(sortedSites)) + " in same package); sites: " + strings.Join(siteStrs, ", "),
		})
	}
}

// loadDuplicateTypesAllowlist reads the canonical per-package
// type-redecl allowlist from the repo-relative path. Returns an
// empty set when the file is missing (matches shell `if [ -f ...
// ]` guard). Lines starting with `#` or whitespace-only are
// skipped; only the first whitespace-separated token
// (`<pkg>:<TypeName>`) is consumed.
func loadDuplicateTypesAllowlist(root string) map[string]bool {
	out := map[string]bool{}
	path := filepath.Join(root, duplicateTypesAllowlistRelPath)
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// First whitespace-separated token.
		if idx := strings.IndexAny(line, " \t"); idx > 0 {
			line = line[:idx]
		}
		if line == "" {
			continue
		}
		out[line] = true
	}
	return out
}

// shortLine extracts the trailing :line:col from a token.Position
// .String() output ("file:line:col"). Returns the full string if
// the format is unexpected.
func shortLine(posStr string) string {
	// posStr is "file:line:col" — keep just the ":line" suffix
	// to match the shell check's "<file>:<line>" diagnostic shape.
	idx := strings.LastIndex(posStr, ":")
	if idx < 0 {
		return posStr
	}
	prev := strings.LastIndex(posStr[:idx], ":")
	if prev < 0 {
		return posStr
	}
	return posStr[prev+1:]
}
