// Package scan — percheck_media_txn_boundary is the forward-prevention gate
// that makes the cross-database media-mutation defect unrepresentable.
//
// MEDIA-SSOT (September 2026): the media domain is owned by exactly one
// engine (PostgreSQL + pgvector), while the transactional outbox that used to
// own the media write lives on SQLite. The former canonical write boundary
// exposed `UpsertClipTx(ctx, tx *sql.Tx, …)` and
// `SetIndexStateTx(ctx, tx *sql.Tx, …)`: a `*sql.Tx` is engine-agnostic by
// type, so the SQLite dispatcher could hand its own transaction to the
// PostgreSQL writer. That compiles, then fails at the first statement
// (`unrecognized token: ":"`), which is the worst possible failure mode —
// a type-correct program that is architecturally impossible.
//
// `percheck_media_assets_writer_canonical` bans direct SQL writes to
// media_assets. It does NOT ban a tx-bound method on the write boundary, so a
// future contributor could re-introduce the exact seam this gate exists to
// prevent. This rule closes that gap.
//
// Two facts are enforced, both structurally:
//
//	(a) No exported `UpsertClipTx` / `SetIndexStateTx` method may exist
//	    outside the canonical PostgreSQL media package. The tx-bound
//	    helpers there are package-private by contract.
//	(b) The canonical write boundary interface
//	    (`persistence.CanonicalAssetWriter`) must not name `*sql.Tx`
//	    anywhere in its method set — the boundary exposes self-owned
//	    entry points only.
//
// Zero production allowlist: only migrations, `_test.go` files and the
// archcheck scanner package itself are excluded, exactly like the sibling
// media gates.
//
// matched rule_id: `percheck_media_txn_boundary`.
package boundaries

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// mediaTxnBoundaryRule is the rule-family id the scanner emits.
const mediaTxnBoundaryRule = "percheck_media_txn_boundary"

// mediaTxnBoundaryScanRoots are the directory roots the gate walks. cmd/ is
// included because the documented ban is repo-wide (the admin CLI is a
// historical media-write surface).
var mediaTxnBoundaryScanRoots = []string{
	"internal",
	"cmd",
}

// mediaTxnBoundaryTxBoundNames are the DEMOLISHED transaction-bound media
// mutation method names. Any occurrence outside the canonical PostgreSQL
// media package is a violation.
var mediaTxnBoundaryTxBoundNames = []string{
	"UpsertClipTx",
	"SetIndexStateTx",
}

// mediaTxnBoundaryNameRe matches one of the demolished identifiers.
var mediaTxnBoundaryNameRe = regexp.MustCompile(`\b(UpsertClipTx|SetIndexStateTx)\b`)

// canonicalWriteBoundaryFile is the interface file that declares the
// production write boundary. Its CanonicalAssetWriter method set must stay
// free of `*sql.Tx`.
const canonicalWriteBoundaryFile = "internal/capabilities/assets/persistence/mutator.go"

// canonicalWriteBoundaryInterface names the interface that MUST remain
// tx-free.
const canonicalWriteBoundaryInterface = "CanonicalAssetWriter"

// mediaTxnBoundaryInterfaceRe captures the body of an interface declaration.
func mediaTxnBoundaryInterfaceRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?s)\b` + regexp.QuoteMeta(name) + `\s+interface\s*\{`)
}

// ScanMediaTxBoundary walks the repo and flags any leaked transaction-bound
// media mutation surface.
func ScanMediaTxBoundary(root string, _ *policy.Policy, r *report.Report) {
	for _, scanRoot := range mediaTxnBoundaryScanRoots {
		absRoot := filepath.Join(root, scanRoot)
		filepath.Walk(absRoot, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() {
				if policy.StandardSkipDirs[filepath.Base(path)] {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			inspectMediaTxnBoundaryFile(root, path, r)
			return nil
		})
	}
	inspectCanonicalWriteBoundary(root, r)
}

// inspectMediaTxnBoundaryFile flags the demolished identifiers outside the
// canonical PostgreSQL media package.
func inspectMediaTxnBoundaryFile(root, absPath string, r *report.Report) {
	relPath, err := filepath.Rel(root, absPath)
	if err != nil {
		relPath = absPath
	}
	relPath = filepath.ToSlash(relPath)

	// The canonical PostgreSQL media package owns the (package-private)
	// tx-bound helpers; its own files are the only legitimate home.
	if policy.IsCanonicalMediaWriter(relPath) {
		return
	}
	if strings.HasPrefix(relPath, policy.ScannerSourcePrefix) {
		return
	}
	if hasAnyPathPrefix(relPath, policy.SQLMigrationPrefixes) || policy.IsTestOnlySupportFile(relPath) {
		return
	}

	source, err := os.ReadFile(absPath)
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{
			File:        relPath,
			Line:        0,
			Rule:        mediaTxnBoundaryRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "file_unreadable",
			Note:        mediaTxnBoundaryRule + ": cannot open file: " + err.Error(),
		})
		return
	}
	masked := source
	fileSet := token.NewFileSet()
	if parsed, parseErr := parser.ParseFile(fileSet, absPath, source, parser.ParseComments); parseErr == nil && parsed != nil {
		masked = maskGoComments(source, fileSet, parsed)
	}
	maskedStr := string(masked)
	for _, match := range mediaTxnBoundaryNameRe.FindAllStringSubmatchIndex(maskedStr, -1) {
		name := maskedStr[match[2]:match[3]]
		lineNo := 1 + strings.Count(maskedStr[:match[0]], "\n")
		r.Violations = append(r.Violations, report.Violation{
			File:        relPath,
			Line:        lineNo,
			Rule:        mediaTxnBoundaryRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "tx_bound_media_method",
			Note: mediaTxnBoundaryRule + ": " + name + " is a transaction-bound media mutation and must not exist outside internal/platform/postgres/media/ " +
				"(MEDIA-SSOT, September 2026). A `*sql.Tx` is engine-agnostic by type; the boundary let the SQLite outbox dispatcher hand its own transaction to the PostgreSQL writer. " +
				"Use the self-owned commits (CommitAndIndex / CommitAsset / CommitDiscoveredAssetAndIndex) instead.",
		})
	}
}

// inspectCanonicalWriteBoundary asserts the canonical write boundary interface
// keeps a tx-free method set.
func inspectCanonicalWriteBoundary(root string, r *report.Report) {
	absPath := filepath.Join(root, filepath.FromSlash(canonicalWriteBoundaryFile))
	source, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			// Sub-tree scans (unit fixtures) legitimately do not contain the
			// boundary file. Absence is not assertable from a partial tree;
			// the repo-root strict run always has it, and deleting it breaks
			// the build because every production committer asserts the
			// interface. Skip rather than emit a false positive.
			return
		}
		r.Violations = append(r.Violations, report.Violation{
			File:        canonicalWriteBoundaryFile,
			Line:        0,
			Rule:        mediaTxnBoundaryRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "canonical_boundary_missing",
			Note:        mediaTxnBoundaryRule + ": canonical write boundary file is unreadable: " + err.Error(),
		})
		return
	}
	masked := source
	fileSet := token.NewFileSet()
	if parsed, parseErr := parser.ParseFile(fileSet, absPath, source, parser.ParseComments); parseErr == nil && parsed != nil {
		masked = maskGoComments(source, fileSet, parsed)
	}
	body, start := interfaceBody(string(masked), canonicalWriteBoundaryInterface)
	if body == "" {
		r.Violations = append(r.Violations, report.Violation{
			File:        canonicalWriteBoundaryFile,
			Line:        0,
			Rule:        mediaTxnBoundaryRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "canonical_boundary_missing",
			Note:        mediaTxnBoundaryRule + ": " + canonicalWriteBoundaryInterface + " interface not found in " + canonicalWriteBoundaryFile,
		})
		return
	}
	if strings.Contains(body, "*sql.Tx") || strings.Contains(body, "sql.Tx") {
		lineNo := 1 + strings.Count(string(masked)[:start], "\n")
		r.Violations = append(r.Violations, report.Violation{
			File:        canonicalWriteBoundaryFile,
			Line:        lineNo,
			Rule:        mediaTxnBoundaryRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "tx_bound_canonical_boundary",
			Note: mediaTxnBoundaryRule + ": " + canonicalWriteBoundaryInterface + " must expose a tx-FREE method set. " +
				"Naming `*sql.Tx` here re-opens the cross-database media seam (MEDIA-SSOT, September 2026).",
		})
	}
}

// interfaceBody returns the braced body of the named interface and the byte
// offset of its declaration. It handles one level of nested braces (interface
// bodies do not nest in Go, but embedded generic constraints can carry them).
func interfaceBody(source, name string) (string, int) {
	loc := mediaTxnBoundaryInterfaceRe(name).FindStringIndex(source)
	if loc == nil {
		return "", 0
	}
	open := strings.Index(source[loc[0]:loc[1]], "{")
	if open < 0 {
		return "", 0
	}
	start := loc[0] + open
	depth := 0
	for i := start; i < len(source); i++ {
		switch source[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return source[start+1 : i], start
			}
		}
	}
	return "", 0
}
