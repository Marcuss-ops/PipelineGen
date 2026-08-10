package process

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultToolsChecker_ReportsMissingTools(t *testing.T) {
	missing := (&DefaultToolsChecker{RequiredTools: []string{"pipelinegen-tool-that-does-not-exist"}}).CheckTools(context.Background())
	if len(missing) != 1 || missing[0] != "pipelinegen-tool-that-does-not-exist" {
		t.Fatalf("CheckTools() = %v, want the configured missing tool", missing)
	}
}

func TestDefaultToolsChecker_UsesPATHForAvailableTool(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "pipelinegen-tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write test tool: %v", err)
	}
	t.Setenv("PATH", dir)
	missing := (&DefaultToolsChecker{RequiredTools: []string{"pipelinegen-tool"}}).CheckTools(context.Background())
	if len(missing) != 0 {
		t.Fatalf("CheckTools() = %v, want no missing tools for the controlled executable", missing)
	}
}

func TestNewToolsChecker_ReturnsHealthPort(t *testing.T) {
	if got := NewToolsChecker(); got == nil {
		t.Fatal("NewToolsChecker() returned nil")
	}
}

func TestCommandTTSChecker_InvalidPythonReturnsError(t *testing.T) {
	checker := &CommandTTSChecker{PythonBin: "pipelinegen-python-that-does-not-exist"}
	if err := checker.CheckTTS(context.Background()); err == nil {
		t.Fatal("CheckTTS() returned nil for an unavailable Python binary")
	}
}
