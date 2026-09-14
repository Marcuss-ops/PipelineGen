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

// TestDefaultToolsChecker_ConfiguredPathWinsOverPATH pins the fix for the
// false negative a healthy deployment produced when YTDLP_PATH pointed at
// scripts/yt-dlp-pipeline: the wrapper lives outside the service PATH, so the
// bare `yt-dlp` lookup failed even though downloads worked.
func TestDefaultToolsChecker_ConfiguredPathWinsOverPATH(t *testing.T) {
	dir := t.TempDir()
	wrapper := filepath.Join(dir, "yt-dlp-pipeline")
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write wrapper: %v", err)
	}
	// An EMPTY PATH proves the configured path, not PATH, satisfied the probe.
	t.Setenv("PATH", "")
	checker := &DefaultToolsChecker{
		RequiredTools: []string{"yt-dlp"},
		ToolPaths:     map[string]string{"yt-dlp": wrapper},
	}
	if missing := checker.CheckTools(context.Background()); len(missing) != 0 {
		t.Fatalf("CheckTools() = %v, want no missing tool for the configured wrapper", missing)
	}
}

// TestDefaultToolsChecker_ConfiguredPathMissingStillReported keeps the
// fail-closed side: an explicitly configured but unusable executable must
// still surface the tool as missing instead of silently passing.
func TestDefaultToolsChecker_ConfiguredPathMissingStillReported(t *testing.T) {
	checker := &DefaultToolsChecker{
		RequiredTools: []string{"yt-dlp"},
		ToolPaths:     map[string]string{"yt-dlp": filepath.Join(t.TempDir(), "absent-wrapper")},
	}
	missing := checker.CheckTools(context.Background())
	if len(missing) != 1 || missing[0] != "yt-dlp" {
		t.Fatalf("CheckTools() = %v, want yt-dlp reported missing", missing)
	}
}

// TestDefaultToolsChecker_EmptyConfiguredPathFallsBackToPATH pins that an
// unset/blank configured path keeps the historical PATH-backed behaviour.
func TestDefaultToolsChecker_EmptyConfiguredPathFallsBackToPATH(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "pipelinegen-path-tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write test tool: %v", err)
	}
	t.Setenv("PATH", dir)
	checker := &DefaultToolsChecker{
		RequiredTools: []string{"pipelinegen-path-tool"},
		ToolPaths:     map[string]string{"pipelinegen-path-tool": "   "},
	}
	if missing := checker.CheckTools(context.Background()); len(missing) != 0 {
		t.Fatalf("CheckTools() = %v, want PATH fallback to satisfy the probe", missing)
	}
}

func TestNewToolsCheckerWithPaths_ReturnsHealthPort(t *testing.T) {
	got := NewToolsCheckerWithPaths(map[string]string{"yt-dlp": "/usr/bin/true"})
	if got == nil {
		t.Fatal("NewToolsCheckerWithPaths() returned nil")
	}
}

func TestCommandTTSChecker_InvalidPythonReturnsError(t *testing.T) {
	checker := &CommandTTSChecker{PythonBin: "pipelinegen-python-that-does-not-exist"}
	if err := checker.CheckTTS(context.Background()); err == nil {
		t.Fatal("CheckTTS() returned nil for an unavailable Python binary")
	}
}
