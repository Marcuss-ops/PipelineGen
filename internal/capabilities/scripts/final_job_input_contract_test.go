package scriptgeneration

// final_job_input_contract_test.go pins the read contract of the final-job
// assembly. `finalJobInputFor` builds a projection carrying only the certified
// result surfaces the payload builder may consume; the point of BON-01 is that
// the assembly cannot start reading unrelated run state by accident.
//
// The projection is a value-level contract: it is a *GenerateResult with the
// other fields left zero, so a stray read returns a zero value instead of
// failing to compile. This test supplies the missing static half. It reads the
// allowed field set straight out of `finalJobInputFor` (so adding a field to
// the projection updates the contract in one place) and fails if any other
// final-job file reads a GenerateResult field outside it.
//
// Two files are allowed to read the whole result, because the runner hands
// them the durable run result and never the projection: the checkpoint repair
// and the remote-submit/audit lane. Everything else must stay inside the
// projection — a new exception needs a reason here, not a silent read.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// finalJobResultFullReaderFiles maps a final-job file to the reason it may read
// the durable result instead of the projection.
var finalJobResultFullReaderFiles = map[string]string{
	"final_job_overlay_repair.go": "repairOverlayCheckpointOnResume receives the durable run result from runner_execution (it repairs the checkpoint, it does not assemble the payload)",
	"final_job_source.go":         "submitFinalJob and auditFinalJobArtifactReuse receive the durable run result from runner_execution (they own the remote receipt history)",
}

// finalJobProjectionCarrierNames are the identifiers that hold either the
// projection or the durable result inside the final-job package. A read on any
// other identifier is not part of the contract.
var finalJobProjectionCarrierNames = map[string]bool{"result": true, "input": true}

func TestFinalJobAssemblyReadsOnlyTheProjection(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(filename)

	resultFields := structFieldNames(t, filepath.Join(root, "contract", "model_result.go"), "GenerateResult")
	if len(resultFields) == 0 {
		t.Fatal("could not read the GenerateResult field list")
	}
	allowed := finalJobProjectionFields(t, filepath.Join(root, "finaljob", "final_job_payload.go"))
	if len(allowed) == 0 {
		t.Fatal("could not read the finalJobInputFor projection")
	}

	entries, err := os.ReadDir(filepath.Join(root, "finaljob"))
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var offenders []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if !strings.HasPrefix(name, "final_job") {
			continue
		}
		if _, exempt := finalJobResultFullReaderFiles[name]; exempt {
			continue
		}
		read := finalJobResultReads(t, filepath.Join(root, "finaljob", name))
		for _, field := range read {
			if !resultFields[field] || allowed[field] {
				continue
			}
			offenders = append(offenders, name+": reads GenerateResult."+field+" outside the projection")
		}
	}
	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Fatalf("final-job files must read only the %d projection fields exposed by finalJobInputFor:\n  - %s\n\nEither read the value from the projection, or add the file to finalJobResultFullReaderFiles with the reason it runs on the durable result.",
			len(allowed), strings.Join(offenders, "\n  - "))
	}
}

// finalJobProjectionFields returns the GenerateResult fields copied into the
// projection by finalJobInputFor.
func finalJobProjectionFields(t *testing.T, path string) map[string]bool {
	t.Helper()
	file := parseGoFile(t, path)
	var fields map[string]bool
	ast.Inspect(file, func(n ast.Node) bool {
		decl, ok := n.(*ast.FuncDecl)
		if !ok || decl.Name.Name != "finalJobInputFor" || decl.Body == nil {
			return true
		}
		fields = make(map[string]bool)
		ast.Inspect(decl.Body, func(inner ast.Node) bool {
			selector, ok := inner.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if ident, ok := selector.X.(*ast.Ident); ok && ident.Name == "result" {
				fields[selector.Sel.Name] = true
			}
			return true
		})
		return false
	})
	return fields
}

// finalJobResultReads returns every field name read through a projection
// carrier in the file.
func finalJobResultReads(t *testing.T, path string) []string {
	t.Helper()
	file := parseGoFile(t, path)
	seen := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		selector, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := selector.X.(*ast.Ident)
		if !ok || !finalJobProjectionCarrierNames[ident.Name] {
			return true
		}
		seen[selector.Sel.Name] = true
		return true
	})
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// structFieldNames returns the declared field names of a named struct type.
func structFieldNames(t *testing.T, path, typeName string) map[string]bool {
	t.Helper()
	file := parseGoFile(t, path)
	fields := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		decl, ok := n.(*ast.GenDecl)
		if !ok || decl.Tok != token.TYPE {
			return true
		}
		for _, spec := range decl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != typeName {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok || structType.Fields == nil {
				continue
			}
			for _, field := range structType.Fields.List {
				for _, name := range field.Names {
					fields[name.Name] = true
				}
			}
		}
		return true
	})
	return fields
}

func parseGoFile(t *testing.T, path string) *ast.File {
	t.Helper()
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return parsed
}
