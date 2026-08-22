// Package scan — percheck_digest_md5_ban.go
//
// MD5 compatibility gate (August 2026): `crypto/md5` is compatibility-only
// and its ONE canonical owner is `internal/platform/checksum` (godlike/06
// SSOT — one algorithm, one owner). MD5 must never be used for content
// identity, dedup, or idempotency (that is internal/kernel/digest's SHA-256);
// it exists solely for legacy DB columns, legacy import paths, pre-existing
// local fingerprints (LegacyMD5*) and the provider-supplied Google Drive
// md5Checksum (ProviderMD5Checksum). Every other package MUST delegate to
// `internal/platform/checksum` instead of importing `crypto/md5` directly.
//
// The gate bans the import `"crypto/md5"` from every production .go file
// outside:
//
//  1. `internal/platform/checksum/` — the SSOT owner (the only package
//     authorized to import crypto/md5).
//  2. The transitional allowlist
//     `docs/migrations/digest-md5-imports-allowlist.txt` — every file that
//     still imports crypto/md5 while the checksum migration is in flight.
//     Each entry is removed in the SAME change that migrates the file to
//     internal/platform/checksum.
//
// Symmetric gate (godlike/07 zero-baseline rule): a stale allowlist entry
// whose file no longer imports crypto/md5 trips the gate too, so the
// allowlist can only shrink toward zero. A missing allowlist file is a
// fail-closed SeverityError under rule `percheck_digest_md5_ban_allowlist_missing`.
//
// Scope: production `.go` files under internal/, pkg/, cmd/. `_test.go`
// files are exempt (test fixtures may construct crypto/md5 directly to pin
// golden vectors). Comment-only references to the import path are
// residue-accounted as WARN, never violations.
//
// Matched rule_id: percheck_digest_md5_ban
package scan

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

// digestMD5BanRule is the rule-family id the scanner emits.
const digestMD5BanRule = "percheck_digest_md5_ban"

// digestMD5AllowlistMissingRule fires when the transitional allowlist file
// is missing or unreadable (fail-closed, godlike/07 NO-FAKE-AVAILABILITY:
// without the file the operator cannot audit grandfathered imports).
const digestMD5AllowlistMissingRule = "percheck_digest_md5_ban_allowlist_missing"

// digestMD5AllowlistStaleRule fires when an allowlist entry no longer
// imports crypto/md5 (zero-baseline ratchet — the allowlist must shrink
// toward empty as files migrate to internal/platform/checksum).
const digestMD5AllowlistStaleRule = "percheck_digest_md5_ban_allowlist_stale"

// digestMD5ImportPath is the canonical banned import statement (standard
// library, always spelled exactly "crypto/md5").
const digestMD5ImportPath = `"crypto/md5"`

// digestMD5SSOTRoot is the ONLY package authorized to import crypto/md5
// (godlike/06 SSOT — MD5 is compatibility-only).
const digestMD5SSOTRoot = "internal/platform/checksum/"

// digestMD5AllowlistFile is the on-disk SSOT for grandfathered imports
// still in migration. Path is repo-relative (resolved against the scan
// root). Each non-comment, non-blank line is ONE repo-relative Go file
// path whose banned import is exceptionally permitted while the checksum
// migration is in flight. The allowlist is currently EMPTY (all importers
// migrated in August 2026); it exists so a future partial migration can
// land without breaking CI.
const digestMD5AllowlistFile = "docs/migrations/digest-md5-imports-allowlist.txt"

// digestMD5BanSkipDirs mirrors the standard sibling scanning policy
// (percheck_digest_sha256_ban.go et al.).
var digestMD5BanSkipDirs = map[string]bool{
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

// digestMD5BanSkipPathPrefixes is the scanner-package-exemption set:
// cmd/archcheck/scan references the literal import path in notes and
// prose, so it is excluded from the gate (defense-in-depth; the scan
// package itself never imports crypto/md5).
var digestMD5BanSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// digestMD5ImportRe matches the quoted import statement. The pattern is an
// isolated string-literal test: only lines carrying the exact `"crypto/md5"`
// import under double quotes trip the gate.
var digestMD5ImportRe = regexp.MustCompile(regexp.QuoteMeta(digestMD5ImportPath))

// digestMD5BanNote is the violation Note for any non-exempt import.
const digestMD5BanNote = "forbidden import of \"crypto/md5\" outside internal/platform/checksum. MD5 is compatibility-only (godlike/06): legacy DB columns, legacy import paths, pre-existing local fingerprints (LegacyMD5*), and the provider-supplied Drive md5Checksum (ProviderMD5Checksum). It is NEVER an identity or dedup signal — content identity is SHA-256 owned by internal/kernel/digest. Every other package MUST delegate to internal/platform/checksum (byte-identical digests, pinned by golden old==new tests). Exempt: internal/platform/checksum (owner) and the transitional allowlist docs/migrations/digest-md5-imports-allowlist.txt (each entry removed in the same change as its migration)."

// digestMD5WarnBucket is the centralized residue-emitter: comment-only
// references to the banned import path are non-fatal per godlike/07.
func digestMD5WarnBucket(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, digestMD5BanRule+" "+label+" "+msg)
}

// ScanDigestMD5Ban walks every production .go file under the repo
// (internal/, pkg/, cmd/) and emits a violation for any file that imports
// "crypto/md5" outside the exempt set (SSOT root or the transitional
// allowlist).
//
// Symmetric gate: stale allowlist entries (file no longer imports
// crypto/md5) trip `percheck_digest_md5_ban_allowlist_stale` so the
// allowlist ratchets to zero. A missing allowlist file trips
// `percheck_digest_md5_ban_allowlist_missing` (fail-closed).
func ScanDigestMD5Ban(root string, pol *policy.Policy, r *report.Report) {
	_ = pol // reserved for future SeverityOverride plumbing.

	allowlistPath := filepath.Join(root, filepath.FromSlash(digestMD5AllowlistFile))
	allowlist := map[string]bool{}
	allowlistOK := false
	if _, err := os.Stat(allowlistPath); err != nil {
		// Fail-closed: missing allowlist is a godlike/07 NO-FAKE-
		// AVAILABILITY regression — operators cannot audit grandfathered
		// imports without the canonical file in tree.
		r.Violations = append(r.Violations, report.Violation{
			File:        digestMD5AllowlistFile,
			MatchedRule: "digest_md5_ban_allowlist_missing",
			Rule:        digestMD5AllowlistMissingRule,
			Severity:    string(report.SeverityError),
			Note:        "fail-closed: digest-md5-imports allowlist is missing or unreadable — godlike/07 NO-FAKE-AVAILABILITY: operators cannot audit grandfathered crypto/md5 imports without the canonical file in tree; commit an empty file or restore the canonical surfaces and rerun the percheck",
		})
	} else {
		allowlist = loadDigestMD5Allowlist(allowlistPath)
		allowlistOK = true
	}

	// found maps repo-relative file paths → set of line numbers with a
	// real import. Only files OUTSIDE the exempt roots are scanned.
	found := map[string][]int{}

	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if digestMD5BanSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				relSlash := filepath.ToSlash(rel)
				if hasAnyPathPrefix(relSlash, digestMD5BanSkipPathPrefixes) {
					return filepath.SkipDir
				}
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
		// Out-of-scope roots (docs, examples, vendor handled above).
		if !digestMD5InScope(relSlash) {
			return nil
		}
		// SSOT root never needs allowlisting and is never flagged.
		if strings.HasPrefix(relSlash, digestMD5SSOTRoot) {
			return nil
		}
		hits := scanDigestMD5ImportFile(path, relSlash, r)
		if len(hits) > 0 {
			found[relSlash] = hits
		}
		return nil
	})

	// Non-allowlisted import → violation.
	for rel, lines := range found {
		if allowlist[rel] {
			continue
		}
		for _, line := range lines {
			r.Violations = append(r.Violations, report.Violation{
				Package:     pkgFromDigestMD5Rel(rel),
				File:        rel,
				Line:        line,
				Rule:        digestMD5BanRule,
				Severity:    string(report.SeverityError),
				MatchedRule: "digest_md5_import_outside_ssot",
				Note:        digestMD5BanNote,
			})
		}
	}

	// Stale allowlist entries → violation (zero-baseline ratchet).
	if allowlistOK {
		for rel := range allowlist {
			if _, ok := found[rel]; ok {
				continue
			}
			r.Violations = append(r.Violations, report.Violation{
				File:        digestMD5AllowlistFile,
				MatchedRule: "digest_md5_ban_allowlist_stale",
				Rule:        digestMD5AllowlistStaleRule,
				Severity:    string(report.SeverityWarn),
				Note:        "stale allowlist entry: file " + rel + " no longer imports \"crypto/md5\" — remove it from " + digestMD5AllowlistFile + " in the same change that migrated it to internal/platform/checksum (godlike/08 zero-baseline rule: allowlist entries must exactly mirror the codebase)",
			})
		}
	}
}

// digestMD5InScope reports whether a repo-relative path is part of the
// scanned production surface (internal/, pkg/, cmd/). Everything else
// (docs, examples, tests/fixtures, web, node-scraper) is out of scope.
func digestMD5InScope(relSlash string) bool {
	return strings.HasPrefix(relSlash, "internal/") ||
		strings.HasPrefix(relSlash, "pkg/") ||
		strings.HasPrefix(relSlash, "cmd/")
}

// scanDigestMD5ImportFile opens a single .go file and returns the line
// numbers of every line that carries the quoted "crypto/md5" import.
// Comment-only references are residue-accounted as WARN (godlike/07
// discipline), never violations.
func scanDigestMD5ImportFile(path, relPath string, r *report.Report) []int {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var hits []int
	lineNo := 0
	commentOnly := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		trimmed := strings.TrimLeft(line, " \t")
		// Comment-only references to the banned import path are
		// descriptive prose, not real imports. WARN, do NOT violate.
		if (strings.HasPrefix(trimmed, "//") ||
			strings.HasPrefix(trimmed, "/*") ||
			strings.HasPrefix(trimmed, "*")) &&
			strings.Contains(line, digestMD5ImportPath) {
			commentOnly++
			continue
		}
		if !digestMD5ImportRe.MatchString(line) {
			continue
		}
		hits = append(hits, lineNo)
	}
	if commentOnly > 0 {
		digestMD5WarnBucket(r, "digest-md5-import-comments:",
			strconv.Itoa(commentOnly)+" comment-only reference(s) in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
	return hits
}

// loadDigestMD5Allowlist reads the allowlist file into a set of
// repo-relative Go file paths. Lines starting with `#` (after trim) and
// blank lines are skipped; trailing inline `# owner=...` annotations are
// stripped (the loader keeps the first whitespace-delimited token).
func loadDigestMD5Allowlist(path string) map[string]bool {
	out := map[string]bool{}
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

// pkgFromDigestMD5Rel extracts the package identifier from a repo-relative
// file path. Mirrors pkgFromDigestSHA256Rel.
func pkgFromDigestMD5Rel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}
