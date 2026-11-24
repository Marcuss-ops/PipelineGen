package boundaries

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const capabilitySQLBanRule = "percheck_capability_sql_ban"

// capabilitySQLInventoryStaleRule is the ratchet half of the gate: a file that
// no longer imports database/sql or a driver must delete its inventory entry in
// the same change, otherwise the register silently keeps authorising debt that
// no longer exists.
const capabilitySQLInventoryStaleRule = "percheck_capability_sql_inventory_stale"

// capabilitySQLDDLRe bans schema ownership inside a capability. The statements
// are raw string literals, so masking comments (not strings) is exactly the
// right filter: prose about CREATE TABLE is not schema, a raw DDL literal is.
var capabilitySQLDDLRe = regexp.MustCompile(`(?i)\b(CREATE|ALTER)\s+TABLE\b`)

// capabilitySQLForbiddenImports are the engine imports a capability must not
// own. database/sql covers the type-only debt (`sql.NullString`, `sql.Tx`)
// recorded in the inventory; the drivers make a capability unbuildable without
// a database.
var capabilitySQLForbiddenImports = []string{
	"database/sql",
	"github.com/jackc/pgx",
	"github.com/lib/pq",
	"modernc.org/sqlite",
	"github.com/mattn/go-sqlite3",
	"github.com/jmoiron/sqlx",
}

// capabilitySQLInventory is the exact-file debt register for capability files
// that still import database/sql on 2026-11-21. Every entry has an owner and a
// removal trigger. The register ratchets DOWN only: migrating a file to a
// platform port deletes its entry, and the stale half fails the build if it
// does not.
//
// RA-13 removed the three SQL/DDL owners (cliprender cache, localization reuse,
// artifacts repository) from the capability tree. What remains is I/O binder
// debt inside capabilities that use sql.Tx/sql.DB directly; moving each one to
// a typed port is the follow-up recorded by its trigger.
var capabilitySQLInventory = map[string]string{
	"internal/capabilities/ai/autotag/autotag.go":                                   "owner=ai; removal_trigger=tag persistence moves behind a kernel port",
	"internal/capabilities/assets/finalizer/tx_adapter.go":                          "owner=assets-finalizer; removal_trigger=tx adapter moves to platform/sqlite",
	"internal/capabilities/assets/ingest/adapter_clip.go":                           "owner=assets-ingest; removal_trigger=clip ingest reads move to pgmedia ports",
	"internal/capabilities/assets/lifecycle/service_voiceover.go":                   "owner=assets-lifecycle; removal_trigger=voiceover lifecycle store moves to platform",
	"internal/capabilities/assets/persistence/committer.go":                         "owner=assets-persistence; removal_trigger=committer port types replace *sql.Tx/sql.DB",
	"internal/capabilities/assets/providers/stock/enrichment/handler_repository.go": "owner=stock-enrichment; removal_trigger=handler repository moves to platform",
	"internal/capabilities/assets/providers/stock/enrichment/outbox_emitter.go":     "owner=stock-enrichment; removal_trigger=outbox emitter uses a domain port",
	"internal/capabilities/assets/texttracks/materializer.go":                       "owner=texttracks; removal_trigger=materializer store moves to platform/sqlite",
	"internal/capabilities/jobs/handlers/delivery.go":                               "owner=jobs; removal_trigger=delivery persistence moves to platform",
	"internal/capabilities/jobs/finalize/artifact_writer.go":                        "owner=jobs-finalize; removal_trigger=artifact writer moves to platform/sqlite",
	"internal/capabilities/jobs/finalize/job_completion_writer.go":                  "owner=jobs-finalize; removal_trigger=completion writer moves to platform/sqlite",
	"internal/capabilities/jobs/finalize/job_finalizer.go":                          "owner=jobs-finalize; removal_trigger=finalizer tx ownership moves to platform",
	"internal/capabilities/jobs/finalize/lease_fence.go":                            "owner=jobs-finalize; removal_trigger=lease fence moves to platform/sqlite",
	"internal/capabilities/jobs/outbox_registry.go":                                 "owner=jobs; removal_trigger=outbox registry moves to platform",
	"internal/capabilities/operations/ports.go":                                     "owner=operations; removal_trigger=tx-scoped repository port replaces *sql.Tx",
	"internal/capabilities/voiceover/service/finalizer.go":                          "owner=voiceover; removal_trigger=finalizer persistence moves to platform",
	"internal/capabilities/voiceover/service/finalizer_cleanup_outbox.go":           "owner=voiceover; removal_trigger=cleanup outbox moves to platform",
	"internal/capabilities/voiceover/service/finalizer_execute.go":                  "owner=voiceover; removal_trigger=finalizer execute moves to platform",
	"internal/capabilities/voiceover/service/persistence/repository.go":             "owner=voiceover; removal_trigger=persistence repository moves to platform/sqlite",
	"internal/capabilities/voiceover/service/ports_finalization.go":                 "owner=voiceover; removal_trigger=finalization port replaces *sql.Tx",
	"internal/capabilities/voiceover/service/upload_intent.go":                      "owner=voiceover; removal_trigger=upload intent persistence moves to platform",
}

// ScanCapabilitySQLBan bans SQL engine ownership in internal/capabilities.
//
// The rule has three halves:
//  1. no capability file may import database/sql or a database driver unless
//     its exact path is in the inventory;
//  2. no capability file may own table DDL (CREATE/ALTER TABLE) — DDL belongs
//     to migrations/ and the platform adapter that bootstraps it;
//  3. every inventory entry must still be a real import site (ratchet down).
func ScanCapabilitySQLBan(root string, _ *policy.Policy, r *report.Report) {
	seen := map[string]bool{}
	capRoot := filepath.Join(root, "internal", "capabilities")
	_ = filepath.WalkDir(capRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() {
			if policy.StandardSkipDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			return nil
		}
		inspectCapabilitySQLFile(root, path, seen, r)
		return nil
	})
	reportStaleCapabilitySQLInventory(seen, r)
}

func inspectCapabilitySQLFile(root, absPath string, seen map[string]bool, r *report.Report) {
	relPath, err := filepath.Rel(root, absPath)
	if err != nil {
		relPath = absPath
	}
	relPath = filepath.ToSlash(relPath)
	if strings.HasPrefix(relPath, policy.ScannerSourcePrefix) {
		return
	}
	source, err := os.ReadFile(absPath)
	if err != nil {
		r.Warnings = append(r.Warnings, "capability-sql-ban read "+relPath+": "+err.Error())
		return
	}
	fileSet := token.NewFileSet()
	parsed, parseErr := parser.ParseFile(fileSet, absPath, source, parser.ParseComments)
	if parseErr != nil {
		r.Warnings = append(r.Warnings, "capability-sql-ban parse "+relPath+": "+parseErr.Error())
		return
	}
	masked := maskGoComments(source, fileSet, parsed)
	maskedStr := string(masked)
	if loc := capabilitySQLDDLRe.FindStringIndex(maskedStr); loc != nil {
		r.Violations = append(r.Violations, report.Violation{
			File: relPath, Line: 1 + strings.Count(maskedStr[:loc[0]], "\n"),
			Rule: capabilitySQLBanRule, MatchedRule: "capability_schema_ownership",
			Severity: string(report.SeverityError),
			Note: "table DDL belongs to migrations/ and the platform adapter that bootstraps it; " +
				"a capability that runs CREATE/ALTER TABLE owns a schema outside the migration ledger (see RA-13).",
		})
	}
	for _, imp := range parsed.Imports {
		importPath := strings.Trim(strings.TrimSpace(imp.Path.Value), `"`)
		if !forbiddenCapabilitySQLImport(importPath) {
			continue
		}
		seen[relPath] = true
		if _, inventoried := capabilitySQLInventory[relPath]; inventoried {
			continue
		}
		r.Violations = append(r.Violations, report.Violation{
			File: relPath, Line: fileSet.Position(imp.Pos()).Line,
			Rule: capabilitySQLBanRule, MatchedRule: "capability_sql_import",
			Severity: string(report.SeverityError),
			Note: "internal/capabilities must consume a client-owned port, not " + importPath +
				"; move the statements to internal/platform and inject the port. If this is type-only debt, add the exact file to capabilitySQLInventory with an owner and a removal trigger.",
		})
	}
}

func forbiddenCapabilitySQLImport(importPath string) bool {
	for _, banned := range capabilitySQLForbiddenImports {
		if importPath == banned || strings.HasPrefix(importPath, banned+"/") {
			return true
		}
	}
	return false
}

func reportStaleCapabilitySQLInventory(seen map[string]bool, r *report.Report) {
	stale := make([]string, 0, len(capabilitySQLInventory))
	for rel := range capabilitySQLInventory {
		if !seen[rel] {
			stale = append(stale, rel)
		}
	}
	sort.Strings(stale)
	for _, rel := range stale {
		r.Violations = append(r.Violations, report.Violation{
			File: rel, Rule: capabilitySQLInventoryStaleRule, MatchedRule: "capability_sql_inventory_stale",
			Severity: string(report.SeverityError),
			Note: "inventory entry is stale: this capability file no longer imports database/sql or a driver. " +
				"Delete the entry in the same change that migrated it — " + capabilitySQLInventory[rel],
		})
	}
}
