package structure

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

func inputImmutabilityTestRoot(t *testing.T, relPath, body string) string {
	t.Helper()
	root := t.TempDir()
	full := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return root
}

func scanInputViolations(t *testing.T, body string) []report.Violation {
	t.Helper()
	root := inputImmutabilityTestRoot(t, "internal/application/example/example.go", body)
	r := &report.Report{}
	ScanInputImmutability(root, nil, r)
	return r.Violations
}

func TestScanInputImmutability_LocalRequestVariableIsNotAParameter(t *testing.T) {
	got := scanInputViolations(t, `package example

type GenerateRequest struct { Source string }

func Handle() {
	var req GenerateRequest
	req.Source = "default"
}
`)
	if len(got) != 0 {
		t.Fatalf("local request variable must not be reported: %+v", got)
	}
}

func TestScanInputImmutability_ValueInputNormalizationDoesNotMutateCaller(t *testing.T) {
	got := scanInputViolations(t, `package example

type CreateInput struct { ID string }

func Create(input CreateInput) {
	input.ID = "generated"
}
`)
	if len(got) != 0 {
		t.Fatalf("value parameter normalization must not be reported: %+v", got)
	}
}

func TestScanInputImmutability_PointerRequestFieldMutationIsReported(t *testing.T) {
	got := scanInputViolations(t, `package example

type GenerateRequest struct { Source string }

func Generate(req *GenerateRequest) {
	req.Source = "default"
}
`)
	if len(got) != 1 {
		t.Fatalf("pointer request mutation: got %d violations, want 1: %+v", len(got), got)
	}
	if got[0].Line != 6 || got[0].MatchedRule != "input_struct_mutation" {
		t.Fatalf("unexpected violation: %+v", got[0])
	}
}

func TestScanInputImmutability_WholePointerReassignmentIsReported(t *testing.T) {
	got := scanInputViolations(t, `package example

type BuildInput struct { Name string }

func Build(input *BuildInput) {
	*input = BuildInput{Name: "normalized"}
}
`)
	if len(got) != 1 {
		t.Fatalf("whole pointer reassignment: got %d violations, want 1: %+v", len(got), got)
	}
}

func TestScanInputImmutability_NestedFieldAndIndexMutationsAreReported(t *testing.T) {
	got := scanInputViolations(t, `package example

type Options struct { Name string }
type BuildParams struct { Options Options; Tags []string }

func Build(params *BuildParams) {
	params.Options.Name = "x"
	params.Tags[0] = "y"
}
`)
	if len(got) != 2 {
		t.Fatalf("nested mutations: got %d violations, want 2: %+v", len(got), got)
	}
}

func TestScanInputImmutability_HTTPRequestIsExcluded(t *testing.T) {
	got := scanInputViolations(t, `package example

import "net/http"

func Middleware(request *http.Request) {
	request.Method = "POST"
}
`)
	if len(got) != 0 {
		t.Fatalf("net/http request must be excluded: %+v", got)
	}
}

func TestScanInputImmutability_ShadowedLocalDoesNotMatchParameterObject(t *testing.T) {
	got := scanInputViolations(t, `package example

type GenerateRequest struct { Source string }

func Generate(req *GenerateRequest) {
	{
		req := GenerateRequest{}
		req.Source = "local"
	}
}
`)
	if len(got) != 0 {
		t.Fatalf("shadowed local must not be reported: %+v", got)
	}
}

func TestScanInputImmutability_NestedClosureCaptureIsReported(t *testing.T) {
	got := scanInputViolations(t, `package example

type GenerateCommand struct { Source string }

func Generate(req *GenerateCommand) {
	fn := func() { req.Source = "captured" }
	fn()
}
`)
	if len(got) != 1 {
		t.Fatalf("closure capture mutation: got %d violations, want 1: %+v", len(got), got)
	}
}

func TestScanInputImmutability_TestFilesAreSkipped(t *testing.T) {
	root := inputImmutabilityTestRoot(t, "internal/application/example/example_test.go", `package example

type GenerateRequest struct { Source string }
func Generate(req *GenerateRequest) { req.Source = "test" }
`)
	r := &report.Report{}
	ScanInputImmutability(root, nil, r)
	if len(r.Violations) != 0 {
		t.Fatalf("test files must be skipped: %+v", r.Violations)
	}
}

func TestScanInputImmutability_CanonicalTreeHasNoCallerVisibleMutations(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", "..", ".."))
	r := &report.Report{}
	ScanInputImmutability(root, nil, r)
	if len(r.Violations) != 0 {
		for _, violation := range r.Violations {
			t.Logf("%s:%d: %s", violation.File, violation.Line, violation.Note)
		}
		t.Fatalf("canonical tree has %d caller-visible input mutations", len(r.Violations))
	}
}
