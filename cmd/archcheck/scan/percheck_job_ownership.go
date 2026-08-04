// Package scan — percheck_job_ownership.go validates the declared background-job
// ownership surface against runtime wiring evidence.
//
// The ownership YAML intentionally uses operator-facing names for a few jobs
// (for example, media.stock_pipeline) while Go uses canonical wire types. This
// file is the single alias map between those two views. The check is static and
// side-effect free: it does not construct the composition root or touch a
// database, but it requires both a JobPolicy registry entry and a production
// RegisterHandler call for every declared ownership job.
package scan

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

const (
	jobOwnershipRule       = "percheck_job_ownership"
	jobOwnershipSourcePath = "architecture/ownership/jobs.yaml"
)

type ownershipJob struct {
	name string
	line int
}

type jobRuntimeEvidence struct {
	RegistryExpressions []string
	HandlerExpressions  []string
}

// ownershipJobRuntimeAliases is the only translation table between the
// operator-facing ownership inventory and canonical runtime job types. Alias
// rows are allowed to point at the same runtime type when they describe the
// same consumable job from two operational views (artlist.run/media.artlist).
var ownershipJobRuntimeAliases = map[string]string{
	"script.generate_batch":    "script.generate",
	"artlist.run":              "media.artlist",
	"media.artlist":            "media.artlist",
	"media.youtube_clip":       "youtube_clip.extract",
	"media.stock_pipeline":     "media.stock",
	"maintenance.deep_cleanup": "system.cleanup",
	"catalog.sync":             "catalog.sync",
}

// runtimeJobEvidenceByType lists the canonical identifier expressions accepted at
// the registry and handler boundaries. The scanner compares AST expressions,
// not source substrings, so comments and string literals cannot satisfy it.
var runtimeJobEvidenceByType = map[string]jobRuntimeEvidence{
	"script.generate": {
		RegistryExpressions: []string{"TypeScriptGenerate"},
		HandlerExpressions:  []string{"jobscript.TypeGenerate", "script.TypeGenerate"},
	},
	"media.artlist": {
		RegistryExpressions: []string{"TypeArtlistRun"},
		HandlerExpressions:  []string{"media.TypeArtlistRun", "appjobs.TypeArtlistRun"},
	},
	"youtube_clip.extract": {
		RegistryExpressions: []string{"TypeYouTubeClipExtract"},
		HandlerExpressions:  []string{"jobyoutube.TypeClipExtract", "appjobs.TypeYouTubeClipExtract"},
	},
	"media.stock": {
		RegistryExpressions: []string{"TypeMediaStock"},
		HandlerExpressions:  []string{"appjobs.TypeMediaStock"},
	},
	"system.cleanup": {
		RegistryExpressions: []string{"TypeSystemCleanup"},
		HandlerExpressions:  []string{"appjobs.TypeSystemCleanup"},
	},
	"catalog.sync": {
		RegistryExpressions: []string{"TypeCatalogSync"},
		HandlerExpressions:  []string{"appjobs.TypeCatalogSync"},
	},
}

// ScanJobOwnership reports declared jobs that cannot be traced to both a
// canonical runtime registry entry and a production handler registration.
func ScanJobOwnership(root string, _ *policy.Policy, r *report.Report) {
	jobs, err := loadOwnershipJobs(root)
	if err != nil {
		r.Violations = append(r.Violations, report.Violation{
			File:        jobOwnershipSourcePath,
			Rule:        jobOwnershipRule,
			Severity:    string(report.SeverityError),
			MatchedRule: "ownership_source_missing",
			Note:        fmt.Sprintf("cannot read declared job ownership: %v", err),
		})
		return
	}

	registryExpressions, handlerExpressions := collectRuntimeJobEvidence(root)
	for _, declared := range jobs {
		runtimeType, ok := ownershipJobRuntimeAliases[declared.name]
		if !ok {
			emitJobOwnershipViolation(r, declared, "unmapped_runtime_type", "no canonical runtime job type is declared for this ownership alias")
			continue
		}
		evidence, ok := runtimeJobEvidenceByType[runtimeType]
		if !ok {
			emitJobOwnershipViolation(r, declared, "runtime_evidence_missing", "canonical runtime job type has no evidence contract")
			continue
		}
		if !containsExpression(registryExpressions, evidence.RegistryExpressions...) {
			emitJobOwnershipViolation(r, declared, "runtime_registry_missing", fmt.Sprintf("runtime type %q has no AST JobPolicy registry entry", runtimeType))
		}
		if !containsExpression(handlerExpressions, evidence.HandlerExpressions...) {
			emitJobOwnershipViolation(r, declared, "runtime_handler_missing", fmt.Sprintf("runtime type %q has no production AST RegisterHandler binding", runtimeType))
		}
	}
}

func loadOwnershipJobs(root string) ([]ownershipJob, error) {
	path := filepath.Join(root, filepath.FromSlash(jobOwnershipSourcePath))
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var jobs []ownershipJob
	scanner := bufio.NewScanner(f)
	line := 0
	for scanner.Scan() {
		line++
		text := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(text, "- job_type:") {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(text, "- job_type:"))
		if name != "" {
			jobs = append(jobs, ownershipJob{name: name, line: line})
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return jobs, nil
}

// collectRuntimeJobEvidence parses production Go files and records only:
//   - JobPolicy composite literals with a Type field; and
//   - calls whose selector name is exactly RegisterHandler and whose first
//     argument is the job-type expression.
//
// AST parsing deliberately ignores comments, string literals, and *_test.go
// fixtures. Parse errors are ignored here because the repository's normal Go
// build/test gates report them separately; a missing evidence node fails this
// check closed rather than being inferred from raw text.
func collectRuntimeJobEvidence(root string) (registryExpressions, handlerExpressions []string) {
	fset := token.NewFileSet()
	var files []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			rel, relErr := filepath.Rel(root, path)
			if relErr == nil {
				parts := strings.Split(filepath.ToSlash(rel), "/")
				if len(parts) == 1 && isRuntimeEvidenceSkipRoot(parts[0]) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)

	for _, path := range files {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.CompositeLit:
				if !isJobPolicyType(n.Type) {
					return true
				}
				for _, element := range n.Elts {
					field, ok := element.(*ast.KeyValueExpr)
					if !ok || expressionName(field.Key) != "Type" {
						continue
					}
					if name := expressionName(field.Value); name != "" {
						registryExpressions = append(registryExpressions, name)
					}
				}
			case *ast.CallExpr:
				selector, ok := n.Fun.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "RegisterHandler" || len(n.Args) < 2 {
					return true
				}
				// A binding is evidence only when the handler argument is
				// an actual function/method expression or a conversion to
				// the handler type. `nil` is deliberately rejected.
				if !isNonNilHandlerExpression(n.Args[1]) {
					return true
				}
				if name := expressionName(n.Args[0]); name != "" {
					handlerExpressions = append(handlerExpressions, name)
				}
			}
			return true
		})
	}
	return registryExpressions, handlerExpressions
}

func isRuntimeEvidenceSkipRoot(name string) bool {
	switch name {
	case ".git", "vendor", "node_modules", "examples", "docs", "testdata":
		return true
	default:
		return false
	}
}

func isJobPolicyType(expr ast.Expr) bool {
	return expressionName(expr) == "JobPolicy"
}

func isNonNilHandlerExpression(expr ast.Expr) bool {
	switch expr.(type) {
	case *ast.Ident:
		// Production bindings in this repository use a concrete method

		// value or an explicit HandlerFunc conversion. A bare identifier
		// is not enough evidence: it could be an arbitrary value accepted
		// by the any-shaped registration API.
		return false
	case *ast.SelectorExpr, *ast.FuncLit, *ast.CallExpr, *ast.ParenExpr:
		return true
	default:
		return false
	}
}

func expressionName(expr ast.Expr) string {
	switch n := expr.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.SelectorExpr:
		prefix := expressionName(n.X)
		if prefix == "" {
			return n.Sel.Name
		}
		return prefix + "." + n.Sel.Name
	case *ast.ParenExpr:
		return expressionName(n.X)
	}
	return ""
}

func containsExpression(expressions []string, expected ...string) bool {
	for _, expression := range expressions {
		for _, candidate := range expected {
			if expression == candidate {
				return true
			}
		}
	}
	return false
}

func emitJobOwnershipViolation(r *report.Report, declared ownershipJob, matched, note string) {
	r.Violations = append(r.Violations, report.Violation{
		File:        jobOwnershipSourcePath,
		Line:        declared.line,
		Rule:        jobOwnershipRule,
		Severity:    string(report.SeverityError),
		MatchedRule: matched,
		Note:        fmt.Sprintf("declared job %q: %s", declared.name, note),
	})
}
