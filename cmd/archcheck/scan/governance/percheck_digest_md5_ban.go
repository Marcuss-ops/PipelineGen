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
// Scope: production `.go` files under internal/, pkg/, cmd/. `_test.go`
// files are exempt (test fixtures may construct crypto/md5 directly to pin
// golden vectors). Comment-only references are WARN residue.
//
// Matched rule_id: percheck_digest_md5_ban
package governance

import (
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// digestMD5BanRule is the rule-family id the scanner emits.
const digestMD5BanRule = "percheck_digest_md5_ban"

// digestMD5AllowlistMissingRule fires when the transitional allowlist file
// is missing or unreadable (fail-closed, godlike/07 NO-FAKE-AVAILABILITY).
const digestMD5AllowlistMissingRule = "percheck_digest_md5_ban_allowlist_missing"

// digestMD5AllowlistStaleRule fires when an allowlist entry no longer
// imports crypto/md5 (zero-baseline ratchet).
const digestMD5AllowlistStaleRule = "percheck_digest_md5_ban_allowlist_stale"

// digestMD5ImportPath is the canonical banned import statement.
const digestMD5ImportPath = `"crypto/md5"`

// digestMD5SSOTRoot is the ONLY package authorized to import crypto/md5.
const digestMD5SSOTRoot = "internal/platform/checksum/"

// digestMD5AllowlistFile is the on-disk SSOT for grandfathered imports.
const digestMD5AllowlistFile = "docs/migrations/digest-md5-imports-allowlist.txt"

// digestMD5BanNote is the violation Note for any non-exempt import.
const digestMD5BanNote = "forbidden import of \"crypto/md5\" outside internal/platform/checksum. MD5 is compatibility-only (godlike/06): legacy DB columns, legacy import paths, pre-existing local fingerprints (LegacyMD5*), and the provider-supplied Drive md5Checksum (ProviderMD5Checksum). It is NEVER an identity or dedup signal — content identity is SHA-256 owned by internal/kernel/digest. Every other package MUST delegate to internal/platform/checksum (byte-identical digests, pinned by golden old==new tests). Exempt: internal/platform/checksum (owner) and the transitional allowlist docs/migrations/digest-md5-imports-allowlist.txt (each entry removed in the same change as its migration)."

// digestMD5WarnBucket is the centralized residue-emitter for comment-only
// references to the banned import path.
func digestMD5WarnBucket(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, digestMD5BanRule+" "+label+" "+msg)
}

// ScanDigestMD5Ban walks every production .go file and emits a violation for
// any file that imports "crypto/md5" outside the exempt set.
func ScanDigestMD5Ban(root string, _ *policy.Policy, r *report.Report) {
	scanDigestImportBan(root, digestImportBanSpec{
		RuleID:                      digestMD5BanRule,
		ImportMatchedRule:           "digest_md5_import_outside_ssot",
		ImportPath:                  digestMD5ImportPath,
		SSOTRoots:                   []string{digestMD5SSOTRoot},
		AllowlistFile:               digestMD5AllowlistFile,
		AllowlistMissingRule:        digestMD5AllowlistMissingRule,
		AllowlistMissingMatchedRule: "digest_md5_ban_allowlist_missing",
		AllowlistStaleRule:          digestMD5AllowlistStaleRule,
		AllowlistStaleMatchedRule:   "digest_md5_ban_allowlist_stale",
		BanNote:                     digestMD5BanNote,
		MissingNote:                 "fail-closed: digest-md5-imports allowlist is missing or unreadable — godlike/07 NO-FAKE-AVAILABILITY: operators cannot audit grandfathered crypto/md5 imports without the canonical file in tree; commit an empty file or restore the canonical surfaces and rerun the percheck",
		StaleNote: func(rel string) string {
			return "stale allowlist entry: file " + rel + " no longer imports \"crypto/md5\" — remove it from " + digestMD5AllowlistFile + " in the same change that migrated it to internal/platform/checksum (godlike/08 zero-baseline rule: allowlist entries must exactly mirror the codebase)"
		},
		InScope:   digestMD5InScope,
		Warn:      digestMD5WarnBucket,
		WarnLabel: "digest-md5-import-comments:",
	}, r)
}

// digestMD5InScope reports whether a repo-relative path is part of the
// scanned production surface (internal/, pkg/, cmd/).
func digestMD5InScope(relSlash string) bool {
	return strings.HasPrefix(relSlash, "internal/") ||
		strings.HasPrefix(relSlash, "pkg/") ||
		strings.HasPrefix(relSlash, "cmd/")
}
