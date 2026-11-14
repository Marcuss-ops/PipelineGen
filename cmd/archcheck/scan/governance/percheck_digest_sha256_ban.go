// Package scan — percheck_digest_sha256_ban.go
//
// Digest SSOT gate (August 2026): `crypto/sha256` is the APPLICATION
// hashing primitive, and its ONE canonical owner is `internal/kernel/digest`
// (godlike/06 SSOT — one algorithm, one owner). Every other package MUST
// delegate to `internal/kernel/digest` instead of importing `crypto/sha256`
// directly; the byte-identical migration pins (internal/kernel/digest + the
// golden old==new tests) guarantee that delegating produces the same hex
// output.
//
// Permanent exemptions are TLS/protocol internals whose sha256 use is
// HMAC-SHA256 signing or TLS cert fingerprinting — NOT application content
// identity (pkg/hmacsign, pkg/tlsload, internal/platform/delivery).
//
// Scope: production `.go` files under internal/, pkg/ and cmd/. `_test.go`
// files are exempt. Comment-only references are WARN residue.
//
// Matched rule_id: percheck_digest_sha256_ban
package governance

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// digestSHA256BanRule is the rule-family id the scanner emits.
const digestSHA256BanRule = "percheck_digest_sha256_ban"

// digestSHA256AllowlistMissingRule fires when the transitional allowlist
// file is missing or unreadable (fail-closed).
const digestSHA256AllowlistMissingRule = "percheck_digest_sha256_ban_allowlist_missing"

// digestSHA256AllowlistStaleRule fires when an allowlist entry no longer
// imports crypto/sha256 (zero-baseline ratchet).
const digestSHA256AllowlistStaleRule = "percheck_digest_sha256_ban_allowlist_stale"

// digestSHA256ImportPath is the canonical banned import statement.
const digestSHA256ImportPath = `"crypto/sha256"`

// digestSHA256SSOTRoot is the ONLY package authorized to import
// crypto/sha256 for application hashing.
const digestSHA256SSOTRoot = "internal/kernel/digest/"

// digestSHA256AllowlistFile is the on-disk SSOT for grandfathered imports.
const digestSHA256AllowlistFile = "docs/migrations/digest-sha256-imports-allowlist.txt"

// digestSHA256PermanentExemptPrefixes is the permanent TLS/protocol exempt
// set. These packages are NOT migration candidates.
var digestSHA256PermanentExemptPrefixes = []string{
	// HMAC-SHA256 webhook signing (X-Velox-Signature: sha256=<hex>).
	"pkg/hmacsign/",
	// TLS certificate fingerprint (FingerprintSHA256 of the DER).
	"pkg/tlsload/",
	// HMAC-SHA256 signed asset-delivery URLs (sig=<hex>).
	"internal/platform/delivery/",
}

// digestSHA256BanNote is the violation Note for any non-exempt import.
const digestSHA256BanNote = "forbidden import of \"crypto/sha256\" outside internal/kernel/digest. The digest SSOT rule (godlike/06) centralizes the SHA-256 algorithm in internal/kernel/digest (SHA256Bytes/SHA256String/SHA256Reader/Fingerprint/ValidateSHA256); every other package MUST delegate to it. Delegation is byte-identical (verified by the golden old==new tests), so migrating does not change persisted digests. Exempt: internal/kernel/digest (owner), TLS/protocol internals (pkg/hmacsign, pkg/tlsload, internal/platform/delivery), and the transitional allowlist docs/migrations/digest-sha256-imports-allowlist.txt (each entry removed in the same change as its migration)."

// digestSHA256WarnBucket is the centralized residue-emitter for comment-only
// references to the banned import path.
func digestSHA256WarnBucket(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, digestSHA256BanRule+" "+label+" "+msg)
}

// ScanDigestSHA256Ban walks every production .go file and emits a violation
// for any file that imports "crypto/sha256" outside the exempt set.
func ScanDigestSHA256Ban(root string, _ *policy.Policy, r *report.Report) {
	scanDigestImportBan(root, digestImportBanSpec{
		RuleID:                      digestSHA256BanRule,
		ImportMatchedRule:           "digest_sha256_import_outside_ssot",
		ImportPath:                  digestSHA256ImportPath,
		SSOTRoots:                   []string{digestSHA256SSOTRoot},
		PermanentExemptPrefixes:     digestSHA256PermanentExemptPrefixes,
		AllowlistFile:               digestSHA256AllowlistFile,
		AllowlistMissingRule:        digestSHA256AllowlistMissingRule,
		AllowlistMissingMatchedRule: "digest_sha256_ban_allowlist_missing",
		AllowlistStaleRule:          digestSHA256AllowlistStaleRule,
		AllowlistStaleMatchedRule:   "digest_sha256_ban_allowlist_stale",
		BanNote:                     digestSHA256BanNote,
		MissingNote:                 "fail-closed: digest-sha256-imports allowlist is missing or unreadable — godlike/07 NO-FAKE-AVAILABILITY: operators cannot audit grandfathered crypto/sha256 imports without the canonical file in tree; commit an empty file or restore the canonical surfaces and rerun the percheck",
		StaleNote: func(rel string) string {
			return "stale allowlist entry: file " + rel + " no longer imports \"crypto/sha256\" — remove it from " + digestSHA256AllowlistFile + " in the same change that migrated it to internal/kernel/digest (godlike/08 zero-baseline rule: allowlist entries must exactly mirror the codebase)"
		},
		InScope:   digestSHA256InScope,
		Warn:      digestSHA256WarnBucket,
		WarnLabel: "digest-sha256-import-comments:",
	}, r)
}

// digestSHA256InScope reports whether a repo-relative path is part of the
// scanned production surface (internal/, pkg/, cmd/).
func digestSHA256InScope(relSlash string) bool {
	return strings.HasPrefix(relSlash, "internal/") ||
		strings.HasPrefix(relSlash, "pkg/") ||
		strings.HasPrefix(relSlash, "cmd/")
}
