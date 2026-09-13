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
//
// cmd/ is included because the claim in godlike/06 + AGENTS.md is that NO
// code path outside the canonical AssetCommitter writes media_assets. The
// admin CLI (cmd/admin) is the historical exception surface: it used to hold
// one-shot SQLite media writers. Leaving cmd/ unscanned made the documented
// claim false, so the scan root was widened (POSTGRES-MEDIA-CUTOVER audit,
// September 2026).
var mediaAssetsWriterScanRoots = []string{
	"internal",
	"cmd",
}

// mediaAssetsWriterForbiddenRe matches a Go line that contains a
// direct SQL write to media_assets (INSERT, UPDATE, DELETE, REPLACE).
// Case-insensitive, substring match for forward-prevention.
//
// The INSERT/REPLACE/UPDATE branches require the `SET`/`(` trailer so a
// column-list or SET clause is present; the DELETE branch does NOT, because a
// legitimate `DELETE FROM media_assets` needs no trailer. Requiring the
// trailer on DELETE (the pre-September-2026 shape) silently exempted every
// row deletion outside the committer — a hole found by the media-cutover
// audit and closed here.
var mediaAssetsWriterForbiddenRe = regexp.MustCompile(
	`(?is)\b(INSERT\s+(?:OR\s+\w+\s+)?INTO|REPLACE\s+INTO|UPDATE)\s+media_assets\b\s*(?:SET|\()|\b(DELETE\s+FROM)\s+media_assets\b`,
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
	// Test-only support is an EXACT-file exemption (policy.IsTestOnlySupportFile),
	// not a directory prefix: a file newly materialised under
	// .../imagesregistry/testsupport/ is a violation until it is explicitly added
	// to the closed list. A prefix would auto-exempt a re-created media writer.
	if hasAnyPathPrefix(relPath, policy.SQLMigrationPrefixes) || policy.IsTestOnlySupportFile(relPath) {
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
	//
	// The regex has two top-level alternatives (trailer-carrying
	// INSERT/REPLACE/UPDATE, trailer-less DELETE), so the verb lives in a
	// DIFFERENT capture group depending on which branch matched. Take the
	// FIRST participating capture group instead of assuming group 1: Go
	// reports unmatched groups as -1 and slicing on that panics.
	matches := mediaAssetsWriterForbiddenRe.FindAllStringSubmatchIndex(maskedStr, -1)
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		verb := ""
		for group := 2; group+1 < len(match); group += 2 {
			if match[group] >= 0 {
				verb = maskedStr[match[group]:match[group+1]]
				break
			}
		}
		if verb == "" {
			continue
		}
		reportMediaAssetsWriterViolation(r, relPath, maskedStr, match[0], verb,
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
