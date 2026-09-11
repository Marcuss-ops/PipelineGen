// Package policy — exempt.go is the SINGLE SOURCE OF TRUTH for the
// path/dir exemptions shared by the asset-commit scanner family:
//
//   - percheck_media_assets_writer_canonical
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

// TestOnlySupportPrefixes exempts the hermetic, test-only SQLite
// AssetCommitter double. It is never imported by production code, and the
// certify-media-cutover SQLITE_MEDIA_WRITERS=0 gate likewise excludes it.
var TestOnlySupportPrefixes = []string{
	"internal/platform/sqlite/assets/imagesregistry/testsupport",
	"internal/platform/sqlite/assets/imagesregistry/testsupport/",
}

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
