// Package policy — exempt.go is the SINGLE SOURCE OF TRUTH for the
// path/dir exemptions shared by the asset-commit scanner family:
//
//   - percheck_media_assets_writer_canonical
//   - percheck_clip_folders_writer_canonical
//   - percheck_asset_committer_event_ssot
//   - percheck_indexed_state_writer_ssot
//
// godlike/06 (one canonical owner per fact): each scanner used to declare
// its own copy of the standard skip-dir set, the scanner-source prefix, the
// test-only-support prefixes, and the canonical media-writer identity. Those
// copies could drift apart, so the shared entries live here and every
// scanner composes them instead of re-declaring them.
//
// Scanner-SPECIFIC exemptions (e.g. the event-ssot exempt zones, the
// canonical INDEXED-writer paths) stay with their scanner — only the shared
// vocabulary is consolidated here.
package policy

import "strings"

// StandardSkipDirs is the canonical directory-basename skip set shared by
// every asset-commit scanner.
var StandardSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
}

// SkipDirs returns a copy of StandardSkipDirs plus any scanner-specific
// extra directory basenames. Returning a copy keeps the shared set immutable
// so one scanner cannot widen another scanner's exemption surface.
func SkipDirs(extra ...string) map[string]bool {
	out := make(map[string]bool, len(StandardSkipDirs)+len(extra))
	for dir := range StandardSkipDirs {
		out[dir] = true
	}
	for _, dir := range extra {
		out[dir] = true
	}
	return out
}

// Prefixes concatenates exemption prefix groups in argument order into a
// fresh slice. The result is a new backing array, so callers can never alias
// and mutate a shared group.
func Prefixes(groups ...[]string) []string {
	total := 0
	for _, group := range groups {
		total += len(group)
	}
	out := make([]string, 0, total)
	for _, group := range groups {
		out = append(out, group...)
	}
	return out
}

// ScannerSourcePrefix exempts the archcheck scanner package itself: the
// scanner files declare the forbidden literals as regexes and documentation,
// so they must never be flagged as violations.
const ScannerSourcePrefix = "cmd/archcheck/scan"

// TestOnlySupportFiles is the CLOSED, exact set of test-only support files:
// the hermetic SQLite AssetCommitter double family. It is never imported by
// production code (only _test.go files reference it).
//
// This is an exact-file list and NOT a directory prefix on purpose. A prefix
// exemption auto-exempts any file dropped into the directory, so a re-created
// SQLite media writer could hide behind it without a single gate firing.
// Adding a file here is an explicit, reviewable act.
//
// The historical `certify-media-cutover` driver that used to cosign this
// exemption no longer exists, so the exemption is justified ONLY by the
// test-only import discipline — no counter asserts it.
var TestOnlySupportFiles = map[string]bool{
	"internal/platform/sqlite/assets/imagesregistry/testsupport/asset_commit_fields.go":               true,
	"internal/platform/sqlite/assets/imagesregistry/testsupport/asset_committer_renditions.go":        true,
	"internal/platform/sqlite/assets/imagesregistry/testsupport/clip_writer_helpers.go":               true,
	"internal/platform/sqlite/assets/imagesregistry/testsupport/index_request_committer.go":           true,
	"internal/platform/sqlite/assets/imagesregistry/testsupport/sqlite_asset_committer_testdouble.go": true,
	// sqlite_asset_committer_surface.go was split OUT of
	// sqlite_asset_committer_testdouble.go on 2026-09-18 to bring that file
	// back under the 600-LOC strict cap: the commit-normalisation helpers and
	// the media-asset mutation surface moved verbatim. Same package, same
	// test-only import discipline (verified: only _test.go files import this
	// package). It is a reviewable addition to the closed set, NOT a directory
	// exemption — a re-created production SQLite writer still trips the gate.
	"internal/platform/sqlite/assets/imagesregistry/testsupport/sqlite_asset_committer_surface.go": true,
}

// IsTestOnlySupportFile reports whether a repo-relative path is exactly one of
// the closed test-only support files. It is the single owner of that decision.
func IsTestOnlySupportFile(relPath string) bool { return TestOnlySupportFiles[relPath] }

// TestOnlySupportDirPrefix is the DIRECTORY prefix form, used only by scanners
// whose skip parameter is prefix-based (ssot_registry). It is deliberately
// separate from the exact-file exemption so the forward-prevention media-writer
// gate cannot be widened by materialising a new file in that directory.
const TestOnlySupportDirPrefix = "internal/platform/sqlite/assets/imagesregistry/testsupport"

// SQLMigrationPrefixes exempts the canonical schema migration files of both
// engines — migration DDL/DML legitimately contains the forbidden literals.
var SQLMigrationPrefixes = []string{
	"migrations/sqlite",
	"migrations/postgres",
}

// CanonicalMediaWriterPrimary is the single primary canonical owner of the
// media_assets write path after the September 2026 SQLite media demolition:
// the PostgreSQL + pgvector committer. Scanners that need exactly one
// canonical file path use this constant.
const CanonicalMediaWriterPrimary = "internal/platform/postgres/media/media_committer.go"

// CanonicalMediaWriterPackagePrefixes lists the packages that ARE the
// media_assets write SSOT. Ownership is by PACKAGE, not by filename: a new
// file added inside the canonical PostgreSQL media package (e.g.
// delete_saga.go, added by MEDIA-SSOT P0-2) is a canonical writer by
// construction, while a re-created demolished SQLite writer stays a
// violation because it lives outside every canonical package.
//
// The previous filename list drifted the moment a new canonical file landed
// and the gate then reported the SSOT owner as a violation. Package
// ownership cannot drift that way.
var CanonicalMediaWriterPackagePrefixes = []string{
	"internal/platform/postgres/media/",
}

// CanonicalMediaWriterFiles is the canonical set of NON-package files that
// ARE part of the media_assets write SSOT: the surviving SQLite non-media
// mutation primitives (narrow UPDATE surfaces — no commit, no outbox
// emission). The PostgreSQL + pgvector family is expressed by
// CanonicalMediaWriterPackagePrefixes instead, so it does not need a second
// hand-maintained file list here.
//
// MEDIA DEMOLITION (September 2026): the demolished SQLite media writer
// files are deliberately NOT listed, so their reappearance is a violation,
// not an exemption.
var CanonicalMediaWriterFiles = map[string]bool{
	"internal/platform/sqlite/assets/imagesregistry/media_asset_mutations.go": true,
}

// IsCanonicalMediaWriter reports whether a repo-relative path is a canonical
// media writer: inside a canonical package prefix, or one of the explicitly
// listed non-package files. It is the SINGLE owner of that decision — every
// media-writer scanner composes it instead of re-deriving the identity.
func IsCanonicalMediaWriter(relPath string) bool {
	if CanonicalMediaWriterFiles[relPath] {
		return true
	}
	for _, prefix := range CanonicalMediaWriterPackagePrefixes {
		if strings.HasPrefix(relPath, prefix) {
			return true
		}
	}
	return false
}

// CanonicalClipFolderWriterPackagePrefixes lists the packages that ARE the
// clip_folders write SSOT (MEDIA-SSOT folder projection, September 2026).
//
// There are exactly two:
//
//   - internal/platform/postgres/media/ — the PostgreSQL projection owner
//     (FolderRepository), the read source for catalog sync;
//   - internal/platform/sqlite/assets/imagesregistry/ — the operational
//     writer, which owns the local row and mirrors every mutation into the
//     projection through the FolderProjection port.
//
// Ownership is by PACKAGE, not by filename, for the same reason as the
// media_assets fence: a new file inside either package is a canonical writer
// by construction, while a re-introduced raw `clip_folders` write anywhere
// else (cmd/admin operator tools, ad-hoc repositories) is a violation.
var CanonicalClipFolderWriterPackagePrefixes = []string{
	"internal/platform/postgres/media/",
	"internal/platform/sqlite/assets/imagesregistry/",
}

// IsCanonicalClipFolderWriter reports whether a repo-relative path is a
// canonical clip_folders writer. SINGLE owner of that decision; the
// percheck_clip_folders_writer_canonical scanner composes it.
func IsCanonicalClipFolderWriter(relPath string) bool {
	for _, prefix := range CanonicalClipFolderWriterPackagePrefixes {
		if strings.HasPrefix(relPath, prefix) {
			return true
		}
	}
	return false
}
