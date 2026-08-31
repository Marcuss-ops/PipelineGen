package scriptgeneration

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestScriptGenerationMasterDoesNotConcatOrPublishFinalVideo(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(filename)

	paths := []string{
		filepath.Join(root, "runner.go"),
		filepath.Join(root, "runner_deps.go"),
		filepath.Join(root, "runner_execution.go"),
		filepath.Join(root, "runner_lifecycle.go"),
		filepath.Join(root, "ports.go"),
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(data)
		for _, forbidden := range []string{
			"AssembleFinalVideo",
			"assembleFinalVideo",
			"FinalVideoAssembler",
			"PublishFinalVideo",
			"FinalVideoPublisher",
			"SetFinalVideoAssembler",
			"SetFinalVideoPublisher",
			"FinalVideoRequired",
			"FinalVideoReference",
			"assemble_final",
		} {
			if strings.Contains(text, forbidden) {
				t.Errorf("%s contains forbidden final-video master path %q", path, forbidden)
			}
		}
	}
}
