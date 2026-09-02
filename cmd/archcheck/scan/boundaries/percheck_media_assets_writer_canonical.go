// Package scan — percheck_media_assets_writer_canonical is the
// forward-prevention gate that bans direct SQL writes to media_assets
// from ANY package outside the canonical AssetCommitter
// (data-layer unification, August 2026).
//
// godlike/06 SSOT: the canonical owner of media_assets writes is
// persistence.AssetCommitter (implemented by SQLiteAssetCommitter at
// internal/platform/sqlite/assets/imagesregistry/asset_committer.go).
// Every code path that durably creates, updates, or deletes a
// media_assets row MUST route through this port — YouTube, Artlist,
// local clips, voiceover, images, recovery.
//
// This gate is the codebase-wide fence (a generalisation of
// percheck_finalizer_no_direct_sql which scopes only the finalizer
// package). It walks the ENTIRE internal/ tree (excluding test files,
// the canonical owner, the archcheck scanner, and SQL migration files)
// and emits a violation for any non-canonical SQL write to media_assets.
//
// Exemptions:
//   - exactly the canonical writer files in mediaAssetsWriterCanonicalOwners
//     below: the SQLiteAssetCommitter / SQLiteMediaCommitter family (current
//     canonical owner during the staged migration) and the PostgresMediaCommitter
//     family (the staged PostgreSQL + pgvector successor implementing the same
//     persistence.AssetCommitter port). Together they are the only production
//     owners of media_assets SQL.
//   - *_test.go files (regression-guard surface).
//   - migrations/sqlite/*.sql and migrations/postgres/*.sql (canonical schema
//     migration files).
//   - cmd/archcheck/scan/** (this scanner references the forbidden literals).
//
// Comment-only references to media_assets in prose are WARNed
// (residue accounting, godlike/07), not violated.
//
// matched rule_id: `percheck_media_assets_writer_canonical`.
package boundaries

import (
	"bufio"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// mediaAssetsWriterCanonicalOwners lists the files that are the
// canonical SOLE owners of SQL that writes media_assets. The gate does
// NOT inspect these files — they ARE the SSOT.
//
// Two engine adapters of ONE canonical family (media-domain cutover,
// September 2026): the SQLite family remains the active canonical owner
// until FASE 7 cutover; the PostgreSQL family implements the identical
// persistence.AssetCommitter port and is proven behavior-identical by the
// parity suite (internal/platform/postgres/media/parity_test.go).
var mediaAssetsWriterCanonicalOwners = map[string]bool{
	// SQLite canonical family (current owner during staged migration).
	"internal/platform/sqlite/assets/imagesregistry/asset_committer.go":                      true,
	"internal/platform/sqlite/assets/imagesregistry/asset_committer_mutations.go":            true,
	"internal/platform/sqlite/assets/imagesregistry/asset_committer_projection_mutations.go": true,
	"internal/platform/sqlite/assets/imagesregistry/canonical_clip_mutations.go":             true,
	"internal/platform/sqlite/assets/imagesregistry/media_committer.go":                      true,
	// PostgreSQL + pgvector canonical family (staged successor, same port).
	"internal/platform/postgres/media/committer.go":       true,
	"internal/platform/postgres/media/media_committer.go": true,
	"internal/platform/postgres/media/mutations.go":       true,
	"internal/platform/postgres/media/writer.go":          true,
	"internal/platform/postgres/media/renditions.go":      true,
	"internal/platform/postgres/media/index_event.go":     true,
	"internal/platform/postgres/media/registry.go":        true,
}

// mediaAssetsWriterScanRoots are the directory roots the gate walks.
// Every Go file under these roots (except tests + canonical owners +
// archcheck) is inspected for forbidden SQL patterns.
var mediaAssetsWriterScanRoots = []string{
	"internal",
}

// mediaAssetsWriterForbiddenRe matches a Go line that contains a
// direct SQL write to media_assets (INSERT, UPDATE, DELETE, REPLACE).
// Case-insensitive, substring match for forward-prevention.
var mediaAssetsWriterForbiddenRe = regexp.MustCompile(
	`(?is)\b(INSERT\s+(?:OR\s+\w+\s+)?INTO|UPDATE|DELETE\s+FROM|REPLACE\s+INTO)\s+media_assets\b\s*(?:SET|\(|WHERE)`,
)

// mediaAssetsWriterReferenceRe remains broad because it is used only for
// comment-residue accounting; it must not classify error strings as writes.
var mediaAssetsWriterReferenceRe = regexp.MustCompile(
	`(?i)\b(INSERT\s+(?:OR\s+\w+\s+)?INTO|UPDATE|DELETE\s+FROM|REPLACE\s+INTO)\s+media_assets\b`,
)

// mediaAssetsWriterSkipDirs mirrors the standard skip-dir set.
var mediaAssetsWriterSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
	"testdata":     true,
	"test":         true,
}

// mediaAssetsWriterSkipPathPrefixes are exemptions for scanner files +
// SQL migration files (both engines).
var mediaAssetsWriterSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
	"migrations/sqlite",
	"migrations/postgres",
}

// mediaAssetsWriterRule is the rule-family id the scanner emits.
const mediaAssetsWriterRule = "percheck_media_assets_writer_canonical"

// mediaAssetsWriterNote is the violation Note string.
const mediaAssetsWriterNote = "forbidden direct SQL write to media_assets outside the canonical AssetCommitter (data-layer unification, August 2026; media-domain cutover, September 2026): the canonical owner is persistence.AssetCommitter — SQLiteAssetCommitter at internal/platform/sqlite/assets/imagesregistry/asset_committer.go, with PostgresMediaCommitter at internal/platform/postgres/media/committer.go as the staged successor implementing the same port. Every asset commit (YouTube, Artlist, local, voiceover, images, recovery) MUST route through AssetCommitter.CommitAndIndex or CommitTx. The Dispatcher.EnqueueAndIndex fail-closed gate blocks the legacy UpsertClipTx fallback in production."

// mediaAssetsWriterWarn is the WARN-bucket emitter for residue accounting.
func mediaAssetsWriterWarn(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, mediaAssetsWriterRule+" "+label+" "+msg)
}

// ScanMediaAssetsWriterCanonical walks the internal/ tree and inspects
// every non-test Go file for direct SQL writes to media_assets. Each
// forbidden write emits a violation; comment-only references are
// residue-accounted (WARNed) per godlike/07.
//
// The canonical AssetCommitter + its sibling repository files are
// exempt — they ARE the SSOT. SQL migration files (.sql under
// migrations/sqlite/) are exempt (canonical schema evolution).
func ScanMediaAssetsWriterCanonical(root string, _ *policy.Policy, r *report.Report) {
	for _, scanRoot := range mediaAssetsWriterScanRoots {
		absRoot := filepath.Join(root, scanRoot)
		filepath.Walk(absRoot, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() {
				base := filepath.Base(path)
				if mediaAssetsWriterSkipDirs[base] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			if strings.HasSuffix(path, "_test.go") {
				return nil
			}
			inspectMediaAssetsWriterFile(root, path, r)
			return nil
		})
	}
}

// inspectMediaAssetsWriterFile opens a single Go file and scans each
// line for forbidden SQL patterns. Canonical owner files are exempt.
func inspectMediaAssetsWriterFile(root, absPath string, r *report.Report) {
	relPath, err := filepath.Rel(root, absPath)
	if err != nil {
		relPath = absPath
	}
	relPath = filepath.ToSlash(relPath)

	if mediaAssetsWriterCanonicalOwners[relPath] {
		return
	}
	for _, prefix := range mediaAssetsWriterSkipPathPrefixes {
		if strings.HasPrefix(relPath, prefix) {
			return
		}
	}

	f, err := os.Open(absPath)
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{
			File:        relPath,
			Line:        0,
			Rule:        mediaAssetsWriterRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "file_unreadable",
			Note:        mediaAssetsWriterNote + " | cannot open file: " + err.Error(),
		})
		return
	}
	defer f.Close()

	source, err := os.ReadFile(absPath)
	if err != nil {
		return
	}
	fileSet := token.NewFileSet()
	parsed, parseErr := parser.ParseFile(fileSet, absPath, source, parser.ParseComments)
	masked := source
	if parseErr == nil && parsed != nil {
		masked = maskGoComments(source, fileSet, parsed)
	}

	// Scan the complete comment-masked source so SQL split across lines is
	// still detected. String literals remain intact; comments are blanked.
	matches := mediaAssetsWriterForbiddenRe.FindAllStringSubmatchIndex(string(masked), -1)
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		start := match[0]
		verb := strings.ToLower(strings.TrimSpace(string(masked[match[2]:match[3]])))
		lineNo := 1 + strings.Count(string(masked[:start]), "\n")
		r.Violations = append(r.Violations, report.Violation{
			File:        relPath,
			Line:        lineNo,
			Rule:        mediaAssetsWriterRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "forbidden_sql_" + verb,
			Note: mediaAssetsWriterNote +
				" | file: " + relPath +
				" | matched verb: " + verb +
				" | route through persistence.AssetCommitter (canonical SSOT: internal/platform/sqlite/assets/imagesregistry/asset_committer.go)",
		})
	}

	// Keep the residue warning for actual comment-only references, but use
	// the broad matcher only on comment-prefixed lines. Error strings and
	// other descriptive runtime text are therefore not reported as writes.
	sc := bufio.NewScanner(strings.NewReader(string(source)))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	commentOnly := 0
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if (strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*")) && mediaAssetsWriterReferenceRe.MatchString(line) {
			commentOnly++
		}
	}
	if commentOnly > 0 {
		mediaAssetsWriterWarn(r, "forbidden-sql:",
			"comment-only reference(s) to direct SQL writes to media_assets in "+relPath+
				" (descriptive prose; non-fatal per godlike/07 no-fake-availability)")
	}
}
