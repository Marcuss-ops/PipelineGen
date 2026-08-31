package scriptgeneration

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestPayloadsScriptsDoNotReferenceFinalVideoAssembly is the guardrail half
// of the producer-only boundary: PipelineGen must never concat or publish
// the final video (that is the Master's responsibility). This test asserts
// the legacy final-assembly flags are ABSENT from operational payloads,
// fixtures, and CLI scripts (.sh/.py/.yml/.yaml/.json). Removing these
// references is the migration goal; this test makes that absence
// permanently enforceable.
//
// Guardrail exemption: the negative verifications themselves
// (runner_final_video_boundary_test.go, handler_unknown_fields_test.go,
// handler_generate_request.go) and the pre-removal gate
// (scripts/ci/pre-removal-verify.sh) necessarily name these tokens as
// forbidden remote-contract keys. They are exempted here by path.
func TestPayloadsScriptsDoNotReferenceFinalVideoAssembly(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filename))))

	forbidden := []string{
		"assemble_final",
		"assembleFinalVideo",
		"FinalVideoAssembler",
		"PublishFinalVideo",
		"final_video_path",
	}

	exempt := map[string]bool{
		"scripts/ci/pre-removal-verify.sh":                       true,
		"internal/capabilities/scripts/runner_final_video_boundary_test.go": true,
		"internal/capabilities/script/handler_unknown_fields_test.go":       true,
		"internal/capabilities/scripts/payloads_no_final_video_flag_test.go": true,
	}

	// handler_generate_request.go is Go source, not payload/fixture/script,
	// but exempt it anyway for the fail-closed contract rejection it encodes.
	extensions := map[string]bool{
		".sh": true, ".py": true, ".yml": true, ".yaml": true, ".json": true,
	}

	roots := []string{
		filepath.Join(root, "scripts"),
		filepath.Join(root, "tests"),
		filepath.Join(root, "testdata"),
		filepath.Join(root, "config"),
		filepath.Join(root, "docs", "examples"),
	}

	hits := 0
	for _, base := range roots {
		_ = filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if info.IsDir() {
				// skip vendored/derived trees that are not authored payloads
				if strings.Contains(path, "node_modules") || strings.Contains(path, ".git") {
					return filepath.SkipDir
				}
				return nil
			}
			ext := filepath.Ext(info.Name())
			if !extensions[ext] {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return nil
			}
			if exempt[filepath.ToSlash(rel)] {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			text := string(data)
			for _, flag := range forbidden {
				if strings.Contains(text, flag) {
					hits++
					t.Errorf("%s contains forbidden final-video assembly flag %q", rel, flag)
				}
			}
			return nil
		})
	}

	if hits == 0 {
		fmt.Println("OK: no payload/fixture/script references final-video assembly flags")
	}
}