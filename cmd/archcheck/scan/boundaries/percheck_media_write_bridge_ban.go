// Package scan — percheck_media_write_bridge_ban is the forward-prevention
// gate for the media-write split-brain that the SQL-level writer gate cannot
// see.
//
// THE HOLE THIS CLOSES. percheck_media_assets_writer_canonical bans direct SQL
// writes to media_assets. It inspects STATEMENTS, so it is blind to a write
// that goes through an interface: `someRepo.Upsert(ctx, a)` contains no SQL at
// the call site at all. That is exactly how the YouTube enrichment defect
// landed — `youtube/usecase` held a `detail.Repository`, called `Upsert`, and
// in PostgreSQL mode the composition root had satisfied that interface with the
// SQLite AssetStoreSQLite facade, so enrichment wrote media_assets on the WRONG
// ENGINE while every gate stayed green.
//
// The generic seam `detail.Repository` / `detail.Service` is the construct
// that makes that possible: the type carries no information about which
// database owns the write, so a caller cannot be told apart from a correct one
// by the compiler. Narrow typed ports (AssetDetailsReader, AssetCommitter,
// MediaAssetReader, …) do not have that property, because each names the
// engine it belongs to.
//
// WHAT IT ENFORCES. In production Go code (non-test, outside the canonical
// zones below), a call to a media-MUTATING method on a value declared as the
// generic seam is a violation. Detected structurally on the AST:
//
//	(a) `x.Upsert(ctx, a)` / `SoftDelete` / `Restore` / `HardDelete` where `x`
//	    is declared in the same file as `detail.Repository` (field, parameter,
//	    result, or `var`), and
//	(b) `x.Save(ctx, d)` / `Delete(ctx, id)` for `detail.Service`, plus the
//	    chained `x.Repository().Upsert(...)` form, which is how ClipsRegistry
//	    and the ingest lifecycle reach the repository seam.
//
// Reads are deliberately NOT this gate's business:
// percheck_sqlite_media_reader_ban owns the SQLite read counter and already
// carries a register for the documented media-disabled readers. Banning the
// seam's read methods here would double-report the same fact.
//
// ZERO PRODUCTION ALLOWLIST (achieved 2026-09-13). The gate landed with three
// registered exceptions; all three have since been migrated and their entries
// deleted in the same change:
//
//   - artifacts/clips_adapter.go DeleteMedia — now resolves
//     persistence.CanonicalAssetSoftDeleter from the canonical committer and
//     fails closed otherwise (no SQLite fallback);
//   - ingest/adapter_clip.go DeleteAssetRecord — same port, and the generic
//     `repo detail.Repository` field was deleted outright;
//   - app/wiring/canonical_media_committer.go — the SQLite admin store that
//     held the seam was itself deleted.
//
// The two registers below are therefore EMPTY and the gate is a pure
// forward-prevention rule, pinned by
// TestMediaWriteBridgeRegistersAreEmpty. The register mechanism is kept — an
// entry whose write is later migrated still FAILS
// TestMediaWriteBridgeRegisterHasNoStaleEntries — so a future documented
// exception must be added deliberately and ratchets out again on migration.
//
// godlike/06 SSOT: the exemption vocabulary (skip dirs, scanner-source prefix,
// SQL-migration prefixes, canonical media writer identity) comes from
// cmd/archcheck/policy/exempt.go — this file declares no second copy.
//
// matched rule_id: `percheck_media_write_bridge_ban`.
package boundaries

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// mediaWriteBridgeRule is the rule-family id the scanner emits.
const mediaWriteBridgeRule = "percheck_media_write_bridge_ban"

// mediaWriteBridgeScanRoots are the roots the gate walks. cmd/ is included for
// the same reason the sibling media gates widen to it: the documented claim is
// repo-wide, and the admin CLI is the historical media-write surface.
var mediaWriteBridgeScanRoots = []string{
	"internal",
	"cmd",
}

// detailSeamImportPath is the one package whose types this gate reasons about.
// Matching the import path (not the `detail` spelling) means a file that aliases
// the package is still analysed, and a file that merely has a local identifier
// named `detail` is not.
const detailSeamImportPath = "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"

// mediaWriteBridgeRepoWriteMethods are the media-mutating methods of
// detail.Repository. FindByExternalRef/Get/List/Count are reads and belong to
// the reader gate.
var mediaWriteBridgeRepoWriteMethods = map[string]bool{
	"Upsert":     true,
	"SoftDelete": true,
	"Restore":    true,
	"HardDelete": true,
}

// mediaWriteBridgeServiceWriteMethods are the media-mutating methods of
// detail.Service. Get/List/Count are reads (the reader gate's business).
var mediaWriteBridgeServiceWriteMethods = map[string]bool{
	"Save":   true,
	"Delete": true,
}

// mediaWriteBridgeRepositoryAccessor is the detail.Service method that returns
// the detail.Repository seam. `svc.Repository().Upsert(...)` is the call shape
// ClipsRegistry and the ingest lifecycle use to reach the repository write
// boundary, so the chain has to be followed or the gate would miss its most
// common production instance.
const mediaWriteBridgeRepositoryAccessor = "Repository"

// mediaWriteBridgeZones are the path prefixes that are the seam's own
// implementation plane and are therefore exempt by construction:
//
//   - internal/kernel/asset/detail/ — the interface definition itself;
//   - internal/platform/sqlite/ — the operational store that implements the
//     seam (its media_assets SQL writes are fenced by
//     percheck_media_assets_writer_canonical, so this gate adds nothing there);
//   - internal/platform/postgres/media/ — the canonical PostgreSQL owner.
//
// Deliberately prefixes, not an exact-file list: these three packages ARE the
// seam, and their internal layout is expected to shrink. A NEW production
// capability calling the seam still lives outside every zone and fails.
var mediaWriteBridgeZones = []string{
	"internal/kernel/asset/detail/",
	"internal/platform/sqlite/",
	"internal/platform/postgres/media/",
}

// mediaWriteBridgeGrandfatheredFiles is the explicit DEBT REGISTER for
// production files that still perform a media write through the generic seam.
// Each entry would be a real SQLite/PostgreSQL split-brain write, and removal
// of the consumer means deleting its entry here in the same change.
//
// ENTRY RETIRED 2026-09-13: this register is now EMPTY, and that is the
// acceptance criterion of the write-bridge work rather than an oversight. Its
// two entries were the last production seam writes:
//
//   - internal/capabilities/assets/artifacts/clips_adapter.go — DeleteMedia
//     soft-deleted through `r.assets detail.Repository`, which the composition
//     root satisfied with the operational SQLite facade even when PostgreSQL
//     was the media SSOT. The field is gone; DeleteMedia resolves
//     persistence.CanonicalAssetSoftDeleter from the canonical committer and
//     fails closed without it.
//   - internal/capabilities/assets/ingest/adapter_clip.go — DeleteAssetRecord
//     was the ingest-lifecycle twin of the same defect, with the same fix, and
//     its `repo detail.Repository` field was deleted outright.
//
// An empty map is the correct terminal state: the staleness pin fails if either
// entry is ever re-added without a matching seam, and a future entry here must
// be a NEW, reviewed admission that a media write still has no single owner.
//
// This is an exact-file list, not a directory prefix, so a new file dropped
// into one of these packages is NOT auto-exempted.
var mediaWriteBridgeGrandfatheredFiles = map[string]bool{}

// mediaWriteBridgeDegradeOnlyFiles is the second, deliberately separate
// register: files whose seam write is only ever selected when the PostgreSQL
// media plane is CLOSED. Each entry names its selector — a documented graceful
// degrade is not the same kind of thing as the production split-brain debt
// above, and conflating them would make the debt list read as larger (and less
// actionable) than it is.
// ENTRY RETIRED 2026-09-13: this register is now EMPTY, and that is load-bearing
// rather than an oversight. Its single entry,
// internal/app/wiring/canonical_media_committer.go, was pardoning
// sqliteMediaAssetStore.Save — a media write through the generic detail.Service
// seam, selected only when the media plane was closed. Removing the SQLite
// store (MEDIA-SSOT P2-9) removed the seam, so the file has no
// detail.Repository/detail.Service media write left to exempt. An empty map is
// the correct terminal state: a future entry here must be a NEW, reviewed
// degrade path, and the staleness pin fails if this one is ever re-added
// without a matching seam.
var mediaWriteBridgeDegradeOnlyFiles = map[string]bool{}

// mediaWriteBridgeNote is the violation Note string.
const mediaWriteBridgeNote = "forbidden production media WRITE through the generic detail.Repository/detail.Service seam (MEDIA-SSOT write-bridge gate, September 2026): the seam type carries no information about which database owns the write, so a caller wired to the operational SQLite facade silently writes media_assets on the wrong engine while PostgreSQL is the SSOT. Route the mutation through the canonical write boundary (persistence.AssetCommitter.CommitAndIndex / CommitAsset, or the mutations dispatcher for index state), or through a narrow port that names its engine. If this is a documented media-disabled path, add the file to mediaWriteBridgeDegradeOnlyFiles with its selector in the same reviewed change."

// mediaWriteBridgeIsZoneExempt reports whether a repo-relative path is inside
// the seam's own implementation plane.
func mediaWriteBridgeIsZoneExempt(relPath string) bool {
	return hasAnyPathPrefix(relPath, mediaWriteBridgeZones)
}

// mediaWriteBridgeIsRegistered reports whether a repo-relative path is exempt
// by an explicit register entry.
func mediaWriteBridgeIsRegistered(relPath string) bool {
	return mediaWriteBridgeGrandfatheredFiles[relPath] || mediaWriteBridgeDegradeOnlyFiles[relPath]
}

// mediaWriteBridgeExemptFromScan applies the shared exemptions that come before
// any source read. It is shared by the scanner and the register liveness pin so
// the two can never disagree about which files are in scope.
func mediaWriteBridgeExemptFromScan(relPath string) bool {
	if strings.HasPrefix(relPath, policy.ScannerSourcePrefix) {
		return true
	}
	if hasAnyPathPrefix(relPath, policy.SQLMigrationPrefixes) || policy.IsTestOnlySupportFile(relPath) {
		return true
	}
	return mediaWriteBridgeIsZoneExempt(relPath) || mediaWriteBridgeIsRegistered(relPath)
}

// mediaWriteBridgeViolation is one detected seam write: the receiver's
// declaration kind plus the mutating method name.
type mediaWriteBridgeViolation struct {
	line int
	kind string
	name string
}

// seamKind classifies a type expression as the generic media seam. It resolves
// only through the detail package's import alias(es) so a same-named type in an
// unrelated package cannot be mistaken for the seam.
func mediaWriteBridgeSeamKind(expr ast.Expr, aliases map[string]bool) string {
	if expr == nil {
		return ""
	}
	switch e := expr.(type) {
	case *ast.StarExpr:
		return mediaWriteBridgeSeamKind(e.X, aliases)
	case *ast.SelectorExpr:
		ident, ok := e.X.(*ast.Ident)
		if !ok || !aliases[ident.Name] {
			return ""
		}
		switch e.Sel.Name {
		case "Repository":
			return "Repository"
		case "Service":
			return "Service"
		}
	}
	return ""
}

// mediaWriteBridgeSeamNames collects every identifier in the file declared with
// the seam type, mapped to the seam kind it denotes. Struct fields,
// parameters/results and package-level or local `var` declarations all count;
// they are the receivers the AST walk below matches calls against.
func mediaWriteBridgeSeamNames(file *ast.File, aliases map[string]bool) map[string]string {
	names := map[string]string{}
	record := func(name *ast.Ident, typ ast.Expr) {
		if name == nil || name.Name == "_" {
			return
		}
		if kind := mediaWriteBridgeSeamKind(typ, aliases); kind != "" {
			names[name.Name] = kind
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.Field:
			// Struct fields and func params/results are both *ast.Field; a
			// field with several names (`a, b detail.Repository`) declares the
			// seam for each of them.
			for _, name := range node.Names {
				record(name, node.Type)
			}
		case *ast.ValueSpec:
			for i, name := range node.Names {
				if len(node.Values) == i && node.Type == nil {
					continue
				}
				record(name, node.Type)
			}
		}
		return true
	})
	return names
}

// mediaWriteBridgeReceiverKind resolves an expression to the seam kind it
// denotes, or "" when it is not a seam receiver.
//
// The receiver is routinely a selector rather than a bare identifier —
// `s.repo.Upsert(...)`, `bundle.Assets.Save(...)`, `o.svc.repo.Upsert(...)` —
// because the seam is normally a STRUCT FIELD. The selector's Sel name is
// therefore the declared field name, which is exactly what seamNames holds. A
// bare identifier is resolved directly.
func mediaWriteBridgeReceiverKind(expr ast.Expr, seamNames map[string]string) string {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return mediaWriteBridgeReceiverKind(e.X, seamNames)
	case *ast.StarExpr:
		return mediaWriteBridgeReceiverKind(e.X, seamNames)
	case *ast.Ident:
		return seamNames[e.Name]
	case *ast.SelectorExpr:
		// `s.repo` / `bundle.Assets`: the field's name is the selector, so an
		// unrelated receiver expression cannot produce a false positive and a
		// field spelled differently would not be recognised either way.
		return seamNames[e.Sel.Name]
	}
	return ""
}

// mediaWriteBridgeChainIsRepositorySeam reports whether an expression is a call
// to the detail.Service seam's Repository() accessor on a seam receiver.
func mediaWriteBridgeChainIsRepositorySeam(expr ast.Expr, seamNames map[string]string) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != mediaWriteBridgeRepositoryAccessor {
		return false
	}
	return mediaWriteBridgeReceiverKind(sel.X, seamNames) == "Service"
}

// mediaWriteBridgeFindViolations returns every generic-seam media write in a
// parsed file.
//
// It is deliberately receiver-name based rather than go/types based: the
// receivers that matter are declared in the same file (a struct field or a
// constructor parameter), which is the shape the composition root produces, and
// the gate stays a pure source scanner like every sibling media gate. The
// fail-closed path for unparseable files is handled by the caller.
func mediaWriteBridgeFindViolations(source []byte, fileSet *token.FileSet, file *ast.File) []mediaWriteBridgeViolation {
	aliases := map[string]bool{}
	for _, imp := range file.Imports {
		if strings.Trim(imp.Path.Value, `"`) != detailSeamImportPath {
			continue
		}
		if imp.Name != nil {
			aliases[imp.Name.Name] = true
		} else {
			aliases["detail"] = true
		}
	}
	if len(aliases) == 0 {
		return nil
	}
	seamNames := mediaWriteBridgeSeamNames(file, aliases)
	if len(seamNames) == 0 {
		return nil
	}

	var out []mediaWriteBridgeViolation
	record := func(call *ast.CallExpr, kind, method string) {
		line := 0
		if positionFile := fileSet.File(call.Pos()); positionFile != nil {
			line = positionFile.Line(call.Pos())
		}
		out = append(out, mediaWriteBridgeViolation{line: line, kind: kind, name: method})
	}

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		// Direct form: `seamVar.WriteMethod(...)` / `s.seamField.WriteMethod(...)`.
		if kind := mediaWriteBridgeReceiverKind(sel.X, seamNames); kind != "" {
			switch kind {
			case "Repository":
				if mediaWriteBridgeRepoWriteMethods[sel.Sel.Name] {
					record(call, kind, sel.Sel.Name)
				}
			case "Service":
				if mediaWriteBridgeServiceWriteMethods[sel.Sel.Name] {
					record(call, kind, sel.Sel.Name)
				}
			}
			return true
		}
		// Chained form: `seamService.Repository().WriteMethod(...)`.
		if mediaWriteBridgeChainIsRepositorySeam(sel.X, seamNames) && mediaWriteBridgeRepoWriteMethods[sel.Sel.Name] {
			record(call, "Repository", sel.Sel.Name)
		}
		return true
	})
	return out
}

// ScanMediaWriteBridgeBan walks internal/ and cmd/ and reports every
// non-test Go file that performs a media write through the generic
// detail.Repository/detail.Service seam.
func ScanMediaWriteBridgeBan(root string, _ *policy.Policy, r *report.Report) {
	for _, scanRoot := range mediaWriteBridgeScanRoots {
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
			inspectMediaWriteBridgeFile(root, path, r)
			return nil
		})
	}
}

// inspectMediaWriteBridgeFile scans one production Go file for a generic-seam
// media write and reports it unless exempt.
func inspectMediaWriteBridgeFile(root, absPath string, r *report.Report) {
	relPath, err := filepath.Rel(root, absPath)
	if err != nil {
		relPath = absPath
	}
	relPath = filepath.ToSlash(relPath)
	if mediaWriteBridgeExemptFromScan(relPath) {
		return
	}

	source, err := os.ReadFile(absPath)
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{
			File:        relPath,
			Line:        0,
			Rule:        mediaWriteBridgeRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "file_unreadable",
			Note:        mediaWriteBridgeNote + " | cannot open file: " + err.Error(),
		})
		return
	}

	fileSet := token.NewFileSet()
	parsed, parseErr := parser.ParseFile(fileSet, absPath, source, parser.ParseComments)
	if parseErr != nil || parsed == nil {
		// Fail-closed: a file that cannot be parsed must not become an
		// exemption. Report it as unparsable rather than silently passing.
		r.Violations = append(r.Violations, report.Violation{
			File:        relPath,
			Line:        0,
			Rule:        mediaWriteBridgeRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "file_unparseable",
			Note:        mediaWriteBridgeNote + " | cannot parse file (fail-closed): " + parseErr.Error(),
		})
		return
	}

	for _, found := range mediaWriteBridgeFindViolations(source, fileSet, parsed) {
		r.Violations = append(r.Violations, report.Violation{
			File:        relPath,
			Line:        found.line,
			Rule:        mediaWriteBridgeRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "forbidden_seam_media_write",
			Note: mediaWriteBridgeNote +
				" | file: " + relPath +
				" | seam: detail." + found.kind +
				" | method: " + found.name,
		})
	}
}

// mediaWriteBridgeRegisterEntryIsLive reports whether a debt-register entry
// still has a generic-seam media write to pardon.
//
// WHY THIS EXISTS. The register is a ratchet: an entry that survives after its
// consumer migrated turns the register into a permanent allowlist people stop
// reading. An entry is live only when the file still contains at least one
// detected seam write — the same predicate inspectMediaWriteBridgeFile uses to
// report a violation. Anything else must be deleted in the same change.
func mediaWriteBridgeRegisterEntryIsLive(root, relPath string) (bool, error) {
	absPath := filepath.Join(root, filepath.FromSlash(relPath))
	source, err := os.ReadFile(absPath)
	if err != nil {
		return false, err
	}
	fileSet := token.NewFileSet()
	parsed, parseErr := parser.ParseFile(fileSet, absPath, source, parser.ParseComments)
	if parseErr != nil || parsed == nil {
		// Fail-closed in the same direction as the scanner: an unparseable
		// registered file is treated as still live so it cannot silently
		// become a ghost entry.
		return true, nil
	}
	return len(mediaWriteBridgeFindViolations(source, fileSet, parsed)) > 0, nil
}
