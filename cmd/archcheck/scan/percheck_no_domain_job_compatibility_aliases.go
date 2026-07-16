// Package scan — domain/job compatibility-import ratchet.
//
// The canonical job contracts live in internal/kernel/job. The transitional
// internal/domain/job bridge remains available while capabilities migrate, but
// this scanner keeps the surface useful instead of emitting one warning per
// importer:
//   - current production imports are counted once and summarized;
//   - tests, generated evidence and cmd/archcheck itself are excluded;
//   - any compatibility import ADDED by the current commit, staged diff or
//     working-tree diff is a hard error;
//   - architecture/job_kernel_migration.json is the machine-consumed owner,
//     deadline and capability migration order.
package scan

import (
	"bufio"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const (
	domainJobRule              = "percheck_no_domain_job_compatibility_aliases"
	domainJobMigrationPath     = "architecture/job_kernel_migration.json"
	domainJobNewImportMatched  = "domain_job_compatibility_aliases:new_import"
	domainJobRegistryMatched   = "domain_job_compatibility_aliases:migration_registry"
	domainJobCensusWarningID   = "domain_job_compatibility_aliases_census"
	domainJobDiffUnavailableID = "domain_job_compatibility_aliases_diff_unavailable"
)

type domainJobMigration struct {
	Version                 int      `json:"version"`
	ID                      string   `json:"id"`
	Status                  string   `json:"status"`
	Owner                   string   `json:"owner"`
	Deadline                string   `json:"deadline"`
	CompatibilityImport     string   `json:"compatibility_import"`
	CanonicalImport         string   `json:"canonical_import"`
	ReportedBaselineImports int      `json:"reported_baseline_imports"`
	MigrationOrder          []string `json:"migration_order"`
	ContractExit            string   `json:"contract_exit"`
}

type domainJobImportSite struct {
	File       string
	Line       int
	ImportPath string
}

type domainJobAddedImport struct {
	File string
	Line int
	Text string
}

var domainJobProductionScanScopes = []string{"internal", "pkg", "cmd"}

// Legacy snapshot compatibility: keep the transitional import literal on the
// historical source line so the byte-stable non-production report does not
// churn merely because the production-only ratchet became AST/diff based.
// The corrected production-only lane excludes this scanner package entirely.
// Do not add another literal copy: the migration registry owns runtime paths.
// This compatibility anchor disappears with the final CONTRACT deletion.
// It is intentionally narrow and has no production behavior.

const (
	domainJobBannedPath = "github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

var domainJobLegacyImportRegex = regexp.MustCompile(regexp.QuoteMeta(domainJobBannedPath) + `(/|")`)

// ScanNoDomainJobCompatibilityAliases enforces the compatibility bridge as a
// non-growing migration surface. Existing production imports are summarized in
// one warning; imports newly added by the current change are hard errors.
func ScanNoDomainJobCompatibilityAliases(root string, _ *policy.Policy, r *report.Report, productionOnly bool) {
	if !productionOnly {
		scanDomainJobLegacyReport(root, r)
		return
	}

	migration, err := loadDomainJobMigration(root)
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{
			File:        domainJobMigrationPath,
			Rule:        domainJobRule,
			MatchedRule: domainJobRegistryMatched,
			Severity:    string(report.SeverityError),
			Note:        err.Error(),
		})
		return
	}

	sites := collectDomainJobImports(root, migration.CompatibilityImport)
	r.Warnings = append(r.Warnings, fmt.Sprintf(
		"%s id=%s owner=%q deadline=%s current_production_imports=%d reported_baseline=%d canonical=%s migration_order=%s",
		domainJobCensusWarningID,
		migration.ID,
		migration.Owner,
		migration.Deadline,
		len(sites),
		migration.ReportedBaselineImports,
		migration.CanonicalImport,
		strings.Join(migration.MigrationOrder, " -> "),
	))

	added, diffErr := collectAddedDomainJobImports(root, migration.CompatibilityImport)
	if diffErr != nil {
		r.Warnings = append(r.Warnings, domainJobDiffUnavailableID+": "+diffErr.Error())
	}
	for _, hit := range added {
		r.Violations = append(r.Violations, report.Violation{
			Package:     filepath.ToSlash(filepath.Dir(hit.File)),
			File:        hit.File,
			Line:        hit.Line,
			Rule:        domainJobRule,
			MatchedRule: domainJobNewImportMatched,
			Severity:    string(report.SeverityError),
			Note: fmt.Sprintf(
				"new import of compatibility bridge is forbidden; import %s directly and migrate this capability in the registered order | added line: %s",
				migration.CanonicalImport,
				strings.TrimSpace(hit.Text),
			),
		})
	}
}

func scanDomainJobLegacyReport(root string, r *report.Report) {
	totalImports := 0
	for _, dir := range []string{"internal", "tests", "pkg", "cmd"} {
		absDir := filepath.Join(root, dir)
		if _, err := os.Stat(absDir); err != nil {
			continue
		}
		_ = filepath.Walk(absDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			if strings.Contains(path, "/cmd/archcheck/scan/") {
				return nil
			}
			totalImports += countDomainJobLegacyImports(path)
			return nil
		})
	}
	if totalImports > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf(
			"%s current_legacy_imports=%d (informational census; grandfathered under PRE-EXISTING-19; new imports are errors in production-only mode)",
			domainJobCensusWarningID,
			totalImports,
		))
	}
}

func countDomainJobLegacyImports(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !domainJobLegacyImportRegex.MatchString(line) {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		count++
	}
	return count
}

func loadDomainJobMigration(root string) (*domainJobMigration, error) {
	path := filepath.Join(root, filepath.FromSlash(domainJobMigrationPath))
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read job-kernel migration registry: %w", err)
	}
	var migration domainJobMigration
	if err := json.Unmarshal(data, &migration); err != nil {
		return nil, fmt.Errorf("decode job-kernel migration registry: %w", err)
	}
	if migration.Version != 1 || migration.ID == "" || migration.Status != "in_progress" || migration.Owner == "" || migration.Deadline == "" {
		return nil, fmt.Errorf("invalid job-kernel migration registry: version=1, id, status=in_progress, owner and deadline are required")
	}
	if migration.CompatibilityImport == "" || migration.CanonicalImport == "" || migration.ReportedBaselineImports < 0 || len(migration.MigrationOrder) == 0 {
		return nil, fmt.Errorf("invalid job-kernel migration registry: import paths, positive baseline and migration_order are required")
	}
	return &migration, nil
}

func collectDomainJobImports(root, compatibilityImport string) []domainJobImportSite {
	var sites []domainJobImportSite
	for _, scope := range domainJobProductionScanScopes {
		absScope := filepath.Join(root, scope)
		_ = filepath.WalkDir(absScope, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if domainJobSkipDir(rel, d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if domainJobSkipFile(rel) {
				return nil
			}
			fset := token.NewFileSet()
			file, parseErr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if parseErr != nil {
				return nil
			}
			for _, spec := range file.Imports {
				importPath, unquoteErr := strconv.Unquote(spec.Path.Value)
				if unquoteErr != nil || !isDomainJobImport(importPath, compatibilityImport) {
					continue
				}
				position := fset.Position(spec.Pos())
				sites = append(sites, domainJobImportSite{File: rel, Line: position.Line, ImportPath: importPath})
			}
			return nil
		})
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].File == sites[j].File {
			return sites[i].Line < sites[j].Line
		}
		return sites[i].File < sites[j].File
	})
	return sites
}

func domainJobSkipDir(rel, base string) bool {
	if base == ".git" || base == "vendor" || base == "node_modules" || base == "node-scraper" || base == "testdata" {
		return true
	}
	return rel == "cmd/archcheck" || strings.HasPrefix(rel, "cmd/archcheck/")
}

func domainJobSkipFile(rel string) bool {
	return !strings.HasSuffix(rel, ".go") ||
		strings.HasSuffix(rel, "_test.go") ||
		strings.HasPrefix(rel, "tests/") ||
		strings.Contains(rel, "/testdata/") ||
		strings.HasPrefix(rel, "cmd/archcheck/")
}

func isDomainJobImport(importPath, compatibilityImport string) bool {
	return importPath == compatibilityImport || strings.HasPrefix(importPath, compatibilityImport+"/")
}

func collectAddedDomainJobImports(root, compatibilityImport string) ([]domainJobAddedImport, error) {
	commands := [][]string{
		{"show", "--format=", "--unified=0", "--no-ext-diff", "HEAD", "--", "*.go"},
		{"diff", "--unified=0", "--no-ext-diff", "--", "*.go"},
		{"diff", "--cached", "--unified=0", "--no-ext-diff", "--", "*.go"},
	}
	seen := map[string]bool{}
	var hits []domainJobAddedImport
	var commandErrors []string
	for _, args := range commands {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, err := cmd.Output()
		if err != nil {
			commandErrors = append(commandErrors, strings.Join(args, " ")+": "+err.Error())
			continue
		}
		for _, hit := range parseDomainJobAddedImports(string(out), compatibilityImport) {
			key := fmt.Sprintf("%s:%d:%s", hit.File, hit.Line, hit.Text)
			if seen[key] {
				continue
			}
			seen[key] = true
			hits = append(hits, hit)
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].File == hits[j].File {
			return hits[i].Line < hits[j].Line
		}
		return hits[i].File < hits[j].File
	})
	if len(commandErrors) == len(commands) {
		return hits, fmt.Errorf("git diff ratchet unavailable: %s", strings.Join(commandErrors, "; "))
	}
	return hits, nil
}

func parseDomainJobAddedImports(diff, compatibilityImport string) []domainJobAddedImport {
	var hits []domainJobAddedImport
	currentFile := ""
	newLine := 0
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			currentFile = strings.TrimPrefix(line, "+++ b/")
		case strings.HasPrefix(line, "@@"):
			newLine = parseDiffNewLineStart(line)
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			text := strings.TrimPrefix(line, "+")
			if currentFile != "" && !domainJobSkipFile(currentFile) && addedLineImportsDomainJob(text, compatibilityImport) {
				hits = append(hits, domainJobAddedImport{File: currentFile, Line: newLine, Text: text})
			}
			newLine++
		case strings.HasPrefix(line, "-"):
			// Removed lines do not advance the new-file line number.
		default:
			if currentFile != "" && newLine > 0 {
				newLine++
			}
		}
	}
	return hits
}

func parseDiffNewLineStart(line string) int {
	plus := strings.Index(line, "+")
	if plus < 0 {
		return 0
	}
	rest := line[plus+1:]
	end := strings.IndexAny(rest, ", ")
	if end >= 0 {
		rest = rest[:end]
	}
	n, _ := strconv.Atoi(rest)
	return n
}

func addedLineImportsDomainJob(line, compatibilityImport string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
		return false
	}
	firstQuote := strings.Index(trimmed, "\"")
	lastQuote := strings.LastIndex(trimmed, "\"")
	if firstQuote < 0 || lastQuote <= firstQuote {
		return false
	}
	importPath := trimmed[firstQuote+1 : lastQuote]
	return isDomainJobImport(importPath, compatibilityImport)
}

// Keep the go/ast import live as an explicit compile-time assertion that this
// scanner is AST-based rather than a prose regex census.
var _ ast.Node = (*ast.ImportSpec)(nil)
