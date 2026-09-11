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
// narrow UPDATE surfaces they own.
//
// CANONICAL OWNERSHIP IS BY PACKAGE, NOT BY FILENAME. The previous
// hand-maintained filename list drifted the moment a new canonical file
// landed inside the media package (delete_saga.go, MEDIA-SSOT P0-2) and the
// gate then reported its own SSOT owner as a violation. Ownership is now
// resolved through policy.IsCanonicalMediaWriter: a canonical package prefix
// (internal/platform/postgres/media/) plus the few non-package files that
// are legitimate narrow writers. A re-created demolished SQLite writer lives
// outside every canonical prefix, so it stays a violation by construction.
//
// This gate is ALSO the single owner of the finalizer-package SQL fence that
// percheck_finalizer_no_direct_sql used to enforce on its own: the
// finalizer scope additionally bans direct writes to asset_locations and
// outbox_events (see mediaAssetsWriterScopedRules). Two scanners encoding
// the same fact were merged into one rule family.
//
// The shared exemption vocabulary (skip dirs, scanner-source prefix,
// test-only-support prefixes, SQL migration prefixes) comes from
// cmd/archcheck/policy/exempt.go — this file declares no second copy.
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
	`(?is)\b(INSERT\s+(?:OR\s+\w+\s+)?INTO|UPDATE|DELETE\s+FROM|REPLACE\s+INTO)\s+media_assets\b\s*(?:SET|\()`,
)

// mediaAssetsWriterReferenceRe remains broad because it is used only for
// comment-residue accounting; it must not classify error strings as writes.
var mediaAssetsWriterReferenceRe = regexp.MustCompile(
	`(?i)\b(INSERT\s+(?:OR\s+\w+\s+)?INTO|UPDATE|DELETE\s+FROM|REPLACE\s+INTO)\s+media_assets\b`,
)

// mediaAssetsWriterScopedRule declares an extra table fence that applies
// ONLY inside one package scope. It exists so the gate that used to live in
// percheck_finalizer_no_direct_sql is folded here instead of duplicated:
// asset_locations and outbox_events are canonical AssetCommitter-owned
// tables, and the finalizer package must never write them directly.
type mediaAssetsWriterScopedRule struct {
	scopePrefix string
	tables      *regexp.Regexp
}

// mediaAssetsWriterScopedRules is the single owner of the scoped table
// fences.
var mediaAssetsWriterScopedRules = []mediaAssetsWriterScopedRule{
	{
		scopePrefix: "internal/capabilities/assets/finalizer",
		tables: regexp.MustCompile(
			`(?is)\b(INSERT\s+(?:OR\s+\w+\s+)?INTO|UPDATE|DELETE\s+FROM|REPLACE\s+INTO)\s+(asset_locations|outbox_events)\b`,
		),
	},
}

// mediaAssetsWriterRule is the rule-family id the scanner emits.
const mediaAssetsWriterRule = "percheck_media_assets_writer_canonical"

// mediaAssetsWriterNote is the violation Note string.
const mediaAssetsWriterNote = "forbidden direct SQL write to media_assets outside the canonical AssetCommitter (data-layer unification, August 2026; media cutover + SQLite media demolition, September 2026): the canonical owner is persistence.AssetCommitter — the whole internal/platform/postgres/media/ package is the ONLY production writer of media_assets (the SQLite media writer family is REMOVED). Every asset commit (YouTube, Artlist, local, voiceover, images, recovery) MUST route through AssetCommitter.CommitAndIndex or CommitTx."

// mediaAssetsWriterWarn is the WARN-bucket emitter for residue accounting.
func mediaAssetsWriterWarn(r *report.Report, label, msg string) {
	r.Warnings = append(r.Warnings, mediaAssetsWriterRule+" "+label+" "+msg)
}

// ScanMediaAssetsWriterCanonical walks the internal/ tree and inspects
// every non-test Go file for direct SQL writes to media_assets (plus the
// scoped asset_locations/outbox_events fence). Each forbidden write emits a
// violation; comment-only references are residue-accounted (WARNed) per
// godlike/07.
//
// The canonical media-writer identity is resolved through
// policy.IsCanonicalMediaWriter — package ownership, not a filename list.
func ScanMediaAssetsWriterCanonical(root string, _ *policy.Policy, r *report.Report) {
	for _, scanRoot := range mediaAssetsWriterScanRoots {
		absRoot := filepath.Join(root, scanRoot)
		filepath.Walk(absRoot, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() {
				base := filepath.Base(path)
				if policy.StandardSkipDirs[base] {
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

	// Canonical ownership is package-based, resolved by the shared SSOT.
	if policy.IsCanonicalMediaWriter(relPath) {
		return
	}
	if strings.HasPrefix(relPath, policy.ScannerSourcePrefix) {
		return
	}
	if hasAnyPathPrefix(relPath, policy.SQLMigrationPrefixes) || hasAnyPathPrefix(relPath, policy.TestOnlySupportPrefixes) {
		return
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
	maskedStr := string(masked)

	// media_assets fence: repo-wide.
	matches := mediaAssetsWriterForbiddenRe.FindAllStringSubmatchIndex(maskedStr, -1)
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		reportMediaAssetsWriterViolation(r, relPath, maskedStr, match[0], maskedStr[match[2]:match[3]],
			"media_assets")
	}

	// Scoped fences: only inside the declared package scope.
	for _, scoped := range mediaAssetsWriterScopedRules {
		if !strings.HasPrefix(relPath, scoped.scopePrefix) {
			continue
		}
		for _, match := range scoped.tables.FindAllStringSubmatchIndex(maskedStr, -1) {
			if len(match) < 6 {
				continue
			}
			table := maskedStr[match[4]:match[5]]
			reportMediaAssetsWriterViolation(r, relPath, maskedStr, match[0], maskedStr[match[2]:match[3]],
				table)
		}
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

// reportMediaAssetsWriterViolation appends one violation for a matched SQL
// write, keeping the MatchedRule vocabulary stable (forbidden_sql_<verb>).
func reportMediaAssetsWriterViolation(r *report.Report, relPath, maskedStr string, start int, verbSlice string, table string) {
	verb := strings.ToLower(strings.TrimSpace(verbSlice))
	lineNo := 1 + strings.Count(maskedStr[:start], "\n")
	r.Violations = append(r.Violations, report.Violation{
		File:        relPath,
		Line:        lineNo,
		Rule:        mediaAssetsWriterRule,
		Severity:    string(report.SeverityError),
		MatchedRule: "forbidden_sql_" + verb,
		Note: mediaAssetsWriterNote +
			" | file: " + relPath +
			" | matched verb: " + verb +
			" | table: " + table +
			" | route through persistence.AssetCommitter (canonical SSOT: internal/platform/postgres/media/ — the SQLite media writer family is demolished)",
	})
}
