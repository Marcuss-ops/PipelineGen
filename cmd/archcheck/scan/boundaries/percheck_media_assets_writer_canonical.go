// Package scan — percheck_media_assets_writer_canonical is the
// forward-prevention gate that bans direct SQL writes to media_assets
// from ANY package outside the canonical AssetCommitter
// (data-layer unification, August 2026).
//
// godlike/06 SSOT: the canonical owner of media_assets writes is
// persistence.AssetCommitter (implemented by PostgresMediaCommitter at
// internal/platform/postgres/media/media_committer.go). Every code path
// that durably creates, updates, or deletes a media_assets row MUST route
// through this port — YouTube, Artlist, local clips, voiceover, images,
// recovery.
//
// MEDIA DEMOLITION (September 2026, POSTGRES-MEDIA-CUTOVER): the SQLite
// media writer family (SQLiteAssetCommitter / SQLiteMediaCommitter) has
// been REMOVED — PostgreSQL + pgvector is the ONLY production writer of
// media_assets. The SQLite engine retains only the non-media mutation
// primitives (lifecycle/enrich/index-state machines, admin console,
// maintenance) which are still exempted below as canonical owners of the
// narrow UPDATE surfaces they own. The mutation-primitive files are
// exempted so the gate keeps flagging any NEW SQLite writer that would
// resurrect the demolished media commit path.
//
// This gate is the codebase-wide fence (a generalisation of
// percheck_finalizer_no_direct_sql which scopes only the finalizer
// package). It walks the ENTIRE internal/ tree (excluding test files,
// the canonical owner, the archcheck scanner, and SQL migration files)
// and emits a violation for any non-canonical SQL write to media_assets.
//
// Exemptions:
//   - exactly the canonical writer files in mediaAssetsWriterCanonicalOwners
//     below: the PostgreSQL + pgvector PostgresMediaCommitter family (the
//     ONLY production media writer after the September 2026 demolition) and
//     the surviving SQLite non-media mutation primitives (narrow UPDATE
//     surfaces, no commit, no outbox emission).
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
// MEDIA DEMOLITION (September 2026): ONE engine adapter family remains —
// PostgreSQL + pgvector (PostgresMediaCommitter, proven by the parity
// suite internal/platform/postgres/media/parity_test.go and the cutover
// TestCutover_ suite). The SQLite entries below are the surviving
// non-media mutation primitives ONLY (no commit, no outbox emission);
// the demolished SQLite media writer files are deliberately NOT listed,
// so their reappearance is a violation, not an exemption.
var mediaAssetsWriterCanonicalOwners = map[string]bool{
	// SQLite non-media mutation primitives (narrow UPDATE surfaces —
	// lifecycle/enrich/index-state machines, admin console, maintenance).
	// These do NOT commit assets and do NOT emit outbox events.
	"internal/platform/sqlite/assets/imagesregistry/media_asset_mutations.go": true,
	// PostgreSQL + pgvector canonical family (the ONLY media writer).
	"internal/platform/postgres/media/committer.go":       true,
	"internal/platform/postgres/media/media_committer.go": true,
	"internal/platform/postgres/media/mutations.go":       true,
	"internal/platform/postgres/media/writer.go":          true,
	"internal/platform/postgres/media/renditions.go":      true,
	"internal/platform/postgres/media/index_event.go":     true,
	"internal/platform/postgres/media/registry.go":        true,
	// Media cutover (September 2026): the PG outbox consumption family —
	// claim/complete/fail lifecycle + canonical index worker (the ONLY
	// consumer of asset.index.requested in media-SSOT mode) + the pgvector
	// derived-surface writer.
	"internal/platform/postgres/media/outbox_worker.go":      true,
	"internal/platform/postgres/media/vector_surfaces.go":    true,
	"internal/platform/postgres/media/backfill.go":           true,
	"internal/platform/postgres/media/backfill_committer.go": true,
	// Enrichment pipeline (FASE-4, September 2026): hard-feature analyzer
	// + visual embedding pipeline + backfill engine write the DERIVED
	// surfaces (media_asset_features, media_embeddings) and the enrichment
	// state columns of media_assets.
	"internal/platform/postgres/media/feature_analyzer.go":    true,
	"internal/platform/postgres/media/visual_embedding.go":    true,
	"internal/platform/postgres/media/enrichment_backfill.go": true,
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

// mediaAssetsWriterSkipPathPrefixes are exemptions for scanner files,
// SQL migration files (both engines), and the TEST-ONLY testsupport
// package (the hermetic SQLite AssetCommitter test double — clearly
// marked test-only, never imported by production code; the certify
// script's SQLITE_MEDIA_WRITERS=0 gate likewise excludes it).
var mediaAssetsWriterSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
	"migrations/sqlite",
	"migrations/postgres",
	"internal/platform/sqlite/assets/imagesregistry/testsupport",
	"internal/platform/sqlite/assets/imagesregistry/testsupport/",
}

// mediaAssetsWriterRule is the rule-family id the scanner emits.
const mediaAssetsWriterRule = "percheck_media_assets_writer_canonical"

// mediaAssetsWriterNote is the violation Note string.
const mediaAssetsWriterNote = "forbidden direct SQL write to media_assets outside the canonical AssetCommitter (data-layer unification, August 2026; media cutover + SQLite media demolition, September 2026): the canonical owner is persistence.AssetCommitter — PostgresMediaCommitter at internal/platform/postgres/media/media_committer.go is the ONLY production writer of media_assets (the SQLite media writer family is REMOVED). Every asset commit (YouTube, Artlist, local, voiceover, images, recovery) MUST route through AssetCommitter.CommitAndIndex or CommitTx."

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
				" | route through persistence.AssetCommitter (canonical SSOT: internal/platform/postgres/media/media_committer.go — the SQLite media writer family is demolished)",
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
