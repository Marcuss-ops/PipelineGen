// Package scan — percheck_digest_sha256_ban.go
//
// Digest SSOT gate (August 2026): `crypto/sha256` is the APPLICATION
// hashing primitive, and its ONE canonical owner is
// `internal/kernel/digest` (godlike/06 SSOT — one algorithm, one owner).
// Every other package MUST delegate to `internal/kernel/digest` instead
// of importing `crypto/sha256` directly; the byte-identical migration
// pins (internal/kernel/digest + the golden old==new tests) guarantee
// that delegating produces the same hex output.
//
// The gate bans the import `"crypto/sha256"` from every production .go
// file outside:
//
//  1. `internal/kernel/digest/` — the SSOT owner (the only package
//     authorized to import crypto/sha256 for application hashing).
//  2. TLS / protocol crypto internals (permanent exempt set, see
//     digestSHA256PermanentExemptPrefixes): packages whose sha256 use is
//     HMAC-SHA256 signing or TLS cert fingerprinting — NOT application
//     content identity. These are `pkg/hmacsign` (webhook signing),
//     `pkg/tlsload` (TLS cert fingerprint) and
//     `internal/platform/delivery` (signed delivery URLs).
//  3. The transitional allowlist
//     `docs/migrations/digest-sha256-imports-allowlist.txt` — every file
//     that still imports crypto/sha256 while the digest migration is in
//     flight. Each entry carries owner + deadline and is removed in the
//     SAME change that migrates the file to internal/kernel/digest.
//
// Symmetric gate (godlike/07 zero-baseline rule): a stale allowlist
// entry whose file no longer imports crypto/sha256 trips the gate too,
// so the allowlist can only shrink toward zero. A missing allowlist file
// is a fail-closed SeverityError under rule
// `percheck_digest_sha256_ban_allowlist_missing`.
//
// Scope: production `.go` files under internal/, pkg/, cmd/ and root
// scripts. `_test.go` files are exempt (test fixtures may construct
// crypto/sha256 directly to pin golden vectors). Comment-only references
// to the import path are residue-accounted as WARN, never violations.
//
// Matched rule_id: percheck_digest_sha256_ban
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

// digestSHA256BanRule is the rule-family id the scanner emits.
const digestSHA256BanRule = "percheck_digest_sha256_ban"

// digestSHA256AllowlistMissingRule fires when the transitional allowlist
// file is missing or unreadable (fail-closed, godlike/07 NO-FAKE-
// AVAILABILITY: without the file the operator cannot audit grandfathered
// imports).
const digestSHA256AllowlistMissingRule = "percheck_digest_sha256_ban_allowlist_missing"

// digestSHA256AllowlistStaleRule fires when an allowlist entry no longer
// imports crypto/sha256 (zero-baseline ratchet — the allowlist must
// shrink toward empty as files migrate to internal/kernel/digest).
const digestSHA256AllowlistStaleRule = "percheck_digest_sha256_ban_allowlist_stale"

// digestSHA256ImportPath is the canonical banned import statement
// (standard library, always spelled exactly "crypto/sha256").
const digestSHA256ImportPath = `"crypto/sha256"`

// digestSHA256SSOTRoot is the ONLY package authorized to import
// crypto/sha256 for application hashing (godlike/06 SSOT).
const digestSHA256SSOTRoot = "internal/kernel/digest/"

// The pure primitive is exposed from pkg/digest so leaf packages do not need
// to import internal/. internal/kernel/digest remains the semantic contract
// and compatibility facade for the target tree.
const digestSHA256LeafSSOTRoot = "pkg/digest/"

// digestSHA256AllowlistFile is the on-disk SSOT for grandfathered
// imports still in migration. Path is repo-relative (resolved against
// the scan root). Each non-comment, non-blank line is ONE repo-relative
// Go file path whose banned import is exceptionally permitted while the
// digest migration is in flight.
const digestSHA256AllowlistFile = "docs/migrations/digest-sha256-imports-allowlist.txt"

// digestSHA256PermanentExemptPrefixes is the permanent exempt set:
// TLS / protocol crypto internals whose sha256 use is HMAC-SHA256
// signing or TLS fingerprinting — NOT application content identity.
// These packages are NOT migration candidates and never appear in the
// allowlist. Per the digest SSOT rule, exceptions must be motivated;
// the motivation here is "TLS/crypto protocol internals".
var digestSHA256PermanentExemptPrefixes = []string{
	// HMAC-SHA256 webhook signing (X-Velox-Signature: sha256=<hex>).
	"pkg/hmacsign/",
	// TLS certificate fingerprint (FingerprintSHA256 of the DER).
	"pkg/tlsload/",
	// HMAC-SHA256 signed asset-delivery URLs (sig=<hex>).
	"internal/platform/delivery/",
}

// digestSHA256BanSkipDirs mirrors the standard sibling scanning policy
// (percheck_qdrant_index_import_ban.go et al.).
var digestSHA256BanSkipDirs = map[string]bool{
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

// digestSHA256BanSkipPathPrefixes is the scanner-package-exemption set:
// cmd/archcheck/scan references the literal import path in notes and
// prose, so it is excluded from the gate (defense-in-depth; the scan
// package itself never imports crypto/sha256).
var digestSHA256BanSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// digestSHA256ImportRe matches the quoted import statement. The pattern
// is an isolated string-literal test: only lines carrying the exact
// `"crypto/sha256"` import under double quotes trip the gate. The
// terminator `(/|\")` is unnecessary for a stdlib leaf package (no
// subpaths exist), so the pattern is the quoted literal itself.
var digestSHA256ImportRe = regexp.MustCompile(regexp.QuoteMeta(digestSHA256ImportPath))

// digestSHA256BanNote is the violation Note for any non-exempt import.
const digestSHA256BanNote = "forbidden import of \"crypto/sha256\" outside internal/kernel/digest. The digest SSOT rule (godlike/06) centralizes the SHA-256 algorithm in internal/kernel/digest (SHA256Bytes/SHA256String/SHA256Reader/Fingerprint/ValidateSHA256); every other package MUST delegate to it. Delegation is byte-identical (verified by the golden old==new tests), so migrating does not change persisted digests. Exempt: internal/kernel/digest (owner), TLS/protocol internals (pkg/hmacsign, pkg/tlsload, internal/platform/delivery), and the transitional allowlist docs/migrations/digest-sha256-imports-allowlist.txt (each entry removed in the same change as its migration)."

// digestSHA256WarnBucket is the centralized residue-emitter: comment-only
// references to the banned import path are non-fatal per godlike/07.
func digestSHA256WarnBucket(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, digestSHA256BanRule+" "+label+" "+msg)
}

// ScanDigestSHA256Ban walks every production .go file under the repo
// (internal/, pkg/, cmd/, scripts/) and emits a violation for any file
// that imports "crypto/sha256" outside the exempt set (SSOT root,
// permanent TLS/protocol exemptions, or the transitional allowlist).
//
// Symmetric gate: stale allowlist entries (file no longer imports
// crypto/sha256) trip `percheck_digest_sha256_ban_allowlist_stale` so
// the allowlist ratchets to zero. A missing allowlist file trips
// `percheck_digest_sha256_ban_allowlist_missing` (fail-closed).
func ScanDigestSHA256Ban(root string, pol *policy.Policy, r *report.Report) {
	_ = pol // reserved for future SeverityOverride plumbing.

	allowlistPath := filepath.Join(root, filepath.FromSlash(digestSHA256AllowlistFile))
	allowlist := map[string]bool{}
	allowlistOK := false
	if _, err := os.Stat(allowlistPath); err != nil {
		// Fail-closed: missing allowlist is a godlike/07 NO-FAKE-
		// AVAILABILITY regression — operators cannot audit
		// grandfathered imports without the canonical file in tree.
		r.Violations = append(r.Violations, report.Violation{
			File:        digestSHA256AllowlistFile,
			MatchedRule: "digest_sha256_ban_allowlist_missing",
			Rule:        digestSHA256AllowlistMissingRule,
			Severity:    string(report.SeverityError),
			Note:        "fail-closed: digest-sha256-imports allowlist is missing or unreadable — godlike/07 NO-FAKE-AVAILABILITY: operators cannot audit grandfathered crypto/sha256 imports without the canonical file in tree; commit an empty file or restore the canonical surfaces and rerun the percheck",
		})
	} else {
		allowlist = loadDigestSHA256Allowlist(allowlistPath)
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
			if digestSHA256BanSkipDirs[base] {
				return filepath.SkipDir
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				relSlash := filepath.ToSlash(rel)
				if hasAnyPathPrefix(relSlash, digestSHA256BanSkipPathPrefixes) {
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
		if !digestSHA256InScope(relSlash) {
			return nil
		}
		// SSOT root + permanent TLS/protocol exemptions never need
		// allowlisting and are never flagged.
		if digestSHA256Exempt(relSlash) {
			return nil
		}
		hits := scanDigestSHA256ImportFile(path, relSlash, r)
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
				Package:     pkgFromDigestSHA256Rel(rel),
				File:        rel,
				Line:        line,
				Rule:        digestSHA256BanRule,
				Severity:    string(report.SeverityError),
				MatchedRule: "digest_sha256_import_outside_ssot",
				Note:        digestSHA256BanNote,
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
				File:        digestSHA256AllowlistFile,
				MatchedRule: "digest_sha256_ban_allowlist_stale",
				Rule:        digestSHA256AllowlistStaleRule,
				Severity:    string(report.SeverityWarn),
				Note:        "stale allowlist entry: file " + rel + " no longer imports \"crypto/sha256\" — remove it from " + digestSHA256AllowlistFile + " in the same change that migrated it to internal/kernel/digest (godlike/08 zero-baseline rule: allowlist entries must exactly mirror the codebase)",
			})
		}
	}
}

// digestSHA256InScope reports whether a repo-relative path is part of
// the scanned production surface (internal/, pkg/, cmd/ and root-level
// Go scripts). Everything else (docs, examples, tests/fixtures, web,
// node-scraper) is out of scope.
func digestSHA256InScope(relSlash string) bool {
	return strings.HasPrefix(relSlash, "internal/") ||
		strings.HasPrefix(relSlash, "pkg/") ||
		strings.HasPrefix(relSlash, "cmd/")
}

// digestSHA256Exempt reports whether a repo-relative path is inside the
// SSOT root (internal/kernel/digest/) or a permanent TLS/protocol
// exemption.
func digestSHA256Exempt(relSlash string) bool {
	if strings.HasPrefix(relSlash, digestSHA256SSOTRoot) || strings.HasPrefix(relSlash, digestSHA256LeafSSOTRoot) {
		return true
	}
	return hasAnyPathPrefix(relSlash, digestSHA256PermanentExemptPrefixes)
}

// scanDigestSHA256ImportFile opens a single .go file and returns the
// line numbers of every line that carries the quoted "crypto/sha256"
// import. Comment-only references are residue-accounted as WARN
// (godlike/07 discipline), never violations.
func scanDigestSHA256ImportFile(path, relPath string, r *report.Report) []int {
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
			strings.Contains(line, digestSHA256ImportPath) {
			commentOnly++
			continue
		}
		if !digestSHA256ImportRe.MatchString(line) {
			continue
		}
		hits = append(hits, lineNo)
	}
	if commentOnly > 0 {
		digestSHA256WarnBucket(r, "digest-sha256-import-comments:",
			strconv.Itoa(commentOnly)+" comment-only reference(s) in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
	return hits
}

// loadDigestSHA256Allowlist reads the allowlist file into a set of
// repo-relative Go file paths. Lines starting with `#` (after trim) and
// blank lines are skipped; trailing inline `# owner=...` annotations are
// stripped (the loader keeps the first whitespace-delimited token).
func loadDigestSHA256Allowlist(path string) map[string]bool {
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

// pkgFromDigestSHA256Rel extracts the package identifier from a
// repo-relative file path. Mirrors pkgFromQdrantImportBanRel.
func pkgFromDigestSHA256Rel(rel string) string {
	dir := filepath.Dir(rel)
	if dir == "." || dir == "" {
		return "."
	}
	return filepath.ToSlash(dir)
}
