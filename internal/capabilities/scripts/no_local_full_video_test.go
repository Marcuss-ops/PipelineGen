package scriptgeneration

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestNoLocalFullVideoExecutionPath is the permanent guardrail for the
// producer-only boundary: PipelineGen may render fragments, overlays, clips
// and stock pieces, but it must never execute a complete film locally. The
// remote assembler is the only owner of the final video. These symbols were
// the public full-film execution paths; none of them may come back.
func TestNoLocalFullVideoExecutionPath(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(filename))))

	forbidden := []string{
		"AssembleCopy",
		"MuxFinalAudioCopy",
		"RenderCanonicalPlan",
		"RenderShort",
		"OperationAssembleCopy",
		"OperationAdminRender",
		"admin_render",
		"assemble_copy",
	}

	base := filepath.Join(root, "internal")
	err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if info.Name() == "testdata" || strings.Contains(path, string(filepath.Separator)+".git") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".go") || strings.HasSuffix(info.Name(), "_test.go") {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		text := string(data)
		for _, symbol := range forbidden {
			if strings.Contains(text, symbol) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s reintroduces local full-video execution symbol %q", rel, symbol)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal: %v", err)
	}
}
