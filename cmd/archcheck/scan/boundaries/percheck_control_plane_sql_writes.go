// Package scan contains the control-plane write-path architecture gate.
//
// The control plane is SQLite-backed, but business code must not acquire a
// second write path by embedding INSERT/UPDATE/DELETE statements in an
// application service. This scanner is deliberately source-based: it catches
// the regression before the code can be wired into a running process.
package boundaries

import (
	"bufio"
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const controlPlaneSQLWritesRule = "percheck_control_plane_sql_writes"

// These are the canonical repository/write-owner surfaces currently present
// during the legacy-root cutover. New writers belong under platform/sqlite;
// legacy infrastructure exceptions are concrete files, not broad package
// exemptions, so adding a new service cannot silently acquire write authority.
var controlPlaneSQLCanonicalPrefixes = []string{
	"internal/platform/sqlite/",
	"internal/platform/qdrant/indexing/clipindexer/indexing_api_persistence.go",
	"internal/platform/qdrant/indexing/clipindexer/indexing_state.go",
	"internal/capabilities/assets/providers/stock/enrichment/handler_repository.go",
	"internal/application/assets/lifecycle/service_voiceover.go",
	"internal/application/assets/finalizer/asset_finalizer_renditions.go",
	"internal/application/assets/finalizer/asset_finalizer_versions.go",
	"internal/capabilities/jobs/policy/job_completion_writer.go",
}

var controlPlaneSQLTables = map[string]bool{
	"media_assets":             true,
	"asset_text_tracks":        true,
	"jobs":                     true,
	"job_steps":                true,
	"registry_events":          true,
	"registry_runs":            true,
	"projection_registry":      true,
	"backup_registry":          true,
	"outbox_events":            true,
	"content_objects":          true,
	"media_asset_sources":      true,
	"source_identity_registry": true,
	"canonical_mutations":      true,
	"asset_locations":          true,
	"asset_versions":           true,
	"asset_renditions":         true,
	"job_events":               true,
}

var controlPlaneSQLMutationRE = regexp.MustCompile(`(?is)\b(insert\s+(?:or\s+\w+\s+)?into|update|delete\s+from|replace\s+into)\s+(?:if\s+not\s+exists\s+)?([a-z_][a-z0-9_]*)\b`)
var controlPlaneSQLExecRE = regexp.MustCompile(`(?i)\b(?:exec(?:context)?|queryrow(?:context)?)\s*\(`)

// ScanControlPlaneSQLWrites rejects production SQL mutations against
// canonical control-plane tables outside the explicit repository allowlist.
// The scanner is inherently production-only: test files and comments are
// excluded, so the productionOnly argument is retained for CheckSpec
// compatibility but has no alternate behavior.
func ScanControlPlaneSQLWrites(root string, pol *policy.Policy, r *report.Report, _ bool) {
	_ = pol

	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			base := entry.Name()
			if base == ".git" || base == "vendor" || base == "node_modules" || base == "data" || base == "out" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !isControlPlaneSQLScanScope(rel) {
			return nil
		}
		if isCanonicalControlPlaneSQLPath(rel) {
			return nil
		}
		return scanControlPlaneSQLFile(path, rel, r)
	})
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{
			Rule:        controlPlaneSQLWritesRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "scan_root_unreadable",
			File:        "internal",
			Note:        "control-plane SQL write scan failed closed: " + err.Error(),
		})
	}
}

func scanControlPlaneSQLFile(path, rel string, r *report.Report) error {
	source, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	// Parse first so a malformed file cannot hide a write from the source
	// gate. The AST is used only to locate comments; SQL remains text because
	// it is normally embedded in Go string literals rather than Go syntax.
	fileSet := token.NewFileSet()
	parsed, parseErr := parser.ParseFile(fileSet, path, source, parser.ParseComments)
	if parseErr != nil {
		r.Violations = append(r.Violations, report.Violation{
			Package:     filepath.ToSlash(filepath.Dir(rel)),
			File:        rel,
			Rule:        controlPlaneSQLWritesRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "unparseable_production_go",
			Note:        "control-plane SQL write scan failed closed because the production Go file is not parseable: " + parseErr.Error(),
		})
		return nil
	}
	masked := maskGoComments(source, fileSet, parsed)

	scanner := bufio.NewScanner(bytes.NewReader(masked))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var window []string
	lastReported := make(map[string]int)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		code := scanner.Text()
		if strings.TrimSpace(code) == "" {
			continue
		}
		window = append(window, code)
		if len(window) > 12 {
			window = window[len(window)-12:]
		}
		joined := strings.Join(window, " ")
		// A table name in an error message or explanatory string is not
		// a write. Require a nearby database execution call as well; this
		// keeps the source scan strict without flagging prose literals.
		if !controlPlaneSQLExecRE.MatchString(joined) {
			continue
		}
		matches := controlPlaneSQLMutationRE.FindAllStringSubmatch(joined, -1)
		for _, match := range matches {
			table := strings.ToLower(match[2])
			if !controlPlaneSQLTables[table] {
				continue
			}
			key := strings.ToLower(match[1]) + ":" + table
			if previous, ok := lastReported[key]; ok && lineNo-previous <= 2 {
				continue
			}
			lastReported[key] = lineNo
			r.Violations = append(r.Violations, report.Violation{
				Package:     filepath.ToSlash(filepath.Dir(rel)),
				File:        rel,
				Line:        lineNo,
				Rule:        controlPlaneSQLWritesRule,
				Severity:    string(report.SeverityError),
				MatchedRule: "non_canonical_" + key,
				Note:        "direct control-plane SQL write is outside the canonical repository/write-owner allowlist; route the mutation through the typed canonical repository",
			})
		}
	}
	return scanner.Err()
}

func isControlPlaneSQLScanScope(rel string) bool {
	if hasAnyPathPrefix(rel, []string{"cmd/admin/", "cmd/archcheck/scan/"}) {
		return false
	}
	return hasAnyPathPrefix(rel, []string{"internal/", "api/", "cmd/"})
}

func isCanonicalControlPlaneSQLPath(rel string) bool {
	for _, prefix := range controlPlaneSQLCanonicalPrefixes {
		if strings.HasSuffix(prefix, "/") {
			if strings.HasPrefix(rel, prefix) {
				return true
			}
			continue
		}
		if rel == prefix {
			return true
		}
	}
	return false
}

// maskGoComments blanks Go comments while preserving newlines and all string
// literals. In particular, SQL raw strings containing `/* ... */` or `//`
// remain intact and are still scanned; only comments in actual Go syntax are
// removed. AST comment positions make this safe without a second hand-rolled
// lexer.
func maskGoComments(source []byte, fileSet *token.FileSet, file *ast.File) []byte {
	masked := append([]byte(nil), source...)
	positionFile := fileSet.File(file.Pos())
	if positionFile == nil {
		return masked
	}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			start := positionFile.Offset(comment.Pos())
			end := positionFile.Offset(comment.End())
			if start < 0 {
				start = 0
			}
			if end > len(masked) {
				end = len(masked)
			}
			for i := start; i < end; i++ {
				if masked[i] != '\n' {
					masked[i] = ' '
				}
			}
		}
	}
	return masked
}
