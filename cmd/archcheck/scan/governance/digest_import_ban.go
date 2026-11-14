// Package scan — digest_import_ban.go
//
// Shared engine for the digest import gates (crypto/md5 and crypto/sha256).
// Both gates walk the production Go surface, flag a banned stdlib import
// outside its SSOT owner, honor a transitional allowlist, and ratchet stale
// allowlist entries to zero. The mechanics live here once so the two gates
// cannot drift apart: only the spec (rule ids, import path, exempt roots,
// allowlist, notes, scope) differs per gate.
package governance

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// digestImportBanSkipDirs is the shared directory-skip policy.
var digestImportBanSkipDirs = map[string]bool{
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

// digestImportBanSkipPathPrefixes excludes the scanner package itself: it
// references the literal import paths in notes and prose.
var digestImportBanSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// digestImportBanSpec is the per-gate configuration consumed by
// scanDigestImportBan.
type digestImportBanSpec struct {
	RuleID                      string
	ImportMatchedRule           string
	ImportPath                  string
	SSOTRoots                   []string
	PermanentExemptPrefixes     []string
	AllowlistFile               string
	AllowlistMissingRule        string
	AllowlistMissingMatchedRule string
	AllowlistStaleRule          string
	AllowlistStaleMatchedRule   string
	BanNote                     string
	MissingNote                 string
	StaleNote                   func(rel string) string
	InScope                     func(rel string) bool
	Warn                        func(*report.Report, string, string)
	WarnLabel                   string
}

// scanDigestImportBan walks the repo, emits a violation for every
// non-exempt import of spec.ImportPath, and enforces the allowlist both
// directions (missing file fails closed, stale entry warns).
func scanDigestImportBan(root string, spec digestImportBanSpec, r *report.Report) {
	allowlistPath := filepath.Join(root, filepath.FromSlash(spec.AllowlistFile))
	allowlist := map[string]bool{}
	allowlistOK := false
	if _, err := os.Stat(allowlistPath); err != nil {
		r.Violations = append(r.Violations, report.Violation{
			File:        spec.AllowlistFile,
			MatchedRule: spec.AllowlistMissingMatchedRule,
			Rule:        spec.AllowlistMissingRule,
			Severity:    string(report.SeverityError),
			Note:        spec.MissingNote,
		})
	} else {
		allowlist = loadDigestImportAllowlist(allowlistPath)
		allowlistOK = true
	}

	importRe := regexp.MustCompile(regexp.QuoteMeta(spec.ImportPath))
	found := map[string][]int{}

	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if digestImportBanSkipDirs[filepath.Base(path)] {
				return filepath.SkipDir
			}
			if rel, relErr := filepath.Rel(root, path); relErr == nil &&
				hasAnyPathPrefix(filepath.ToSlash(rel), digestImportBanSkipPathPrefixes) {
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
		if !spec.InScope(relSlash) || digestImportExempt(relSlash, spec) {
			return nil
		}
		if hits := scanDigestImportFile(path, relSlash, spec, importRe, r); len(hits) > 0 {
			found[relSlash] = hits
		}
		return nil
	})

	for rel, lines := range found {
		if allowlist[rel] {
			continue
		}
		for _, line := range lines {
			r.Violations = append(r.Violations, report.Violation{
				Package:     digestImportPackage(rel),
				File:        rel,
				Line:        line,
				Rule:        spec.RuleID,
				Severity:    string(report.SeverityError),
				MatchedRule: spec.ImportMatchedRule,
				Note:        spec.BanNote,
			})
		}
	}

	if allowlistOK {
		for rel := range allowlist {
			if _, ok := found[rel]; ok {
				continue
			}
			r.Violations = append(r.Violations, report.Violation{
				File:        spec.AllowlistFile,
				MatchedRule: spec.AllowlistStaleMatchedRule,
				Rule:        spec.AllowlistStaleRule,
				Severity:    string(report.SeverityWarn),
				Note:        spec.StaleNote(rel),
			})
		}
	}
}

// digestImportExempt reports whether the path belongs to an SSOT root or a
// permanent protocol exemption.
func digestImportExempt(rel string, spec digestImportBanSpec) bool {
	for _, root := range spec.SSOTRoots {
		if strings.HasPrefix(rel, root) {
			return true
		}
	}
	return hasAnyPathPrefix(rel, spec.PermanentExemptPrefixes)
}

// scanDigestImportFile returns the line numbers carrying the banned import.
// Comment-only references are residue-accounted as WARN, never violations.
func scanDigestImportFile(path, relPath string, spec digestImportBanSpec, importRe *regexp.Regexp, r *report.Report) []int {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var hits []int
	lineNo := 0
	commentOnly := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimLeft(line, " \t")
		if (strings.HasPrefix(trimmed, "//") ||
			strings.HasPrefix(trimmed, "/*") ||
			strings.HasPrefix(trimmed, "*")) &&
			strings.Contains(line, spec.ImportPath) {
			commentOnly++
			continue
		}
		if importRe.MatchString(line) {
			hits = append(hits, lineNo)
		}
	}
	if commentOnly > 0 {
		spec.Warn(r, spec.WarnLabel,
			strconv.Itoa(commentOnly)+" comment-only reference(s) in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
	return hits
}

// loadDigestImportAllowlist reads the allowlist into a set of repo-relative
// Go paths. Blank lines and `#` comments are skipped; inline `# owner=...`
// annotations are stripped.
func loadDigestImportAllowlist(path string) map[string]bool {
	out := map[string]bool{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, "#"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" {
			continue
		}
		out[filepath.ToSlash(line)] = true
	}
	return out
}

// digestImportPackage extracts the package identifier from a repo-relative
// file path.
func digestImportPackage(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}
