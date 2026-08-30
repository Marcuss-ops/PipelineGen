package wiring

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestScriptGenerationMasterDoesNotOwnFinalVideoRendering is an architectural
// guard for the Master/worker boundary. PipelineGen may render localized clips,
// sub-videos and overlays, but it must never assemble or publish the complete
// final video from script.generate. assemble_final remains a transport intent
// for an external render worker.
func TestScriptGenerationMasterDoesNotOwnFinalVideoRendering(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	wiringDir := filepath.Dir(thisFile)

	checks := []struct {
		path      string
		forbidden []string
	}{
		{
			path: filepath.Join(wiringDir, "script_generation_runtime.go"),
			forbidden: []string{
				"SetFinalVideoAssembler(",
				"SetFinalVideoPublisher(",
			},
		},
		{
			path: filepath.Clean(filepath.Join(wiringDir, "..", "..", "capabilities", "scripts", "runner_execution.go")),
			forbidden: []string{
				"assembleFinalVideo(",
				"PublishFinalVideo(",
			},
		},
	}

	for _, check := range checks {
		data, err := os.ReadFile(check.path)
		if err != nil {
			t.Fatalf("read %s: %v", check.path, err)
		}
		text := string(data)
		for _, token := range check.forbidden {
			if strings.Contains(text, token) {
				t.Fatalf("master final-video boundary violated: %s contains %q", check.path, token)
			}
		}
	}
}
