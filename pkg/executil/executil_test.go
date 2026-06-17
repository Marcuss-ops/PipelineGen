package executil

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestDefaultOptions(t *testing.T) {
	opts := DefaultOptions()
	if opts.Timeout != 10*time.Minute {
		t.Errorf("expected Timeout=10m, got %v", opts.Timeout)
	}
	if !opts.CombinedOutput {
		t.Error("expected CombinedOutput=true")
	}
}

func TestRun_Echo(t *testing.T) {
	result, err := Run(context.Background(), "echo", []string{"hello world"}, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "hello world\n" {
		t.Errorf("expected 'hello world\\n', got %q", result.Output)
	}
}

func TestRun_WorkDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "executil-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	result, err := Run(context.Background(), "pwd", nil, Options{
		WorkDir:        tmpDir,
		Timeout:        5 * time.Second,
		CombinedOutput: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != tmpDir+"/\n" && result.Output != tmpDir+"\n" {
		t.Errorf("expected %q or %q, got %q", tmpDir+"/\n", tmpDir+"\n", result.Output)
	}
}

func TestRun_CombinedOutput(t *testing.T) {
	result, err := Run(context.Background(), "echo", []string{"test"}, Options{
		CombinedOutput: false,
		Timeout:        5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "test\n" {
		t.Errorf("expected 'test\\n', got %q", result.Stdout)
	}
	if result.Output != "" {
		t.Errorf("expected empty Output, got %q", result.Output)
	}
}

func TestRun_Env(t *testing.T) {
	result, err := Run(context.Background(), "sh", []string{"-c", "echo $TEST_VAR"}, Options{
		Env:            append(os.Environ(), "TEST_VAR=hello"),
		Timeout:        5 * time.Second,
		CombinedOutput: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "hello\n" {
		t.Errorf("expected 'hello\\n', got %q", result.Output)
	}
}

func TestRun_Timeout(t *testing.T) {
	_, err := Run(context.Background(), "sleep", []string{"10"}, Options{
		Timeout: 100 * time.Millisecond,
	})
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestRun_CommandNotFound(t *testing.T) {
	_, err := Run(context.Background(), "nonexistent-command-xyz", nil, DefaultOptions())
	if err == nil {
		t.Error("expected error for nonexistent command")
	}
}

func TestRunSimple(t *testing.T) {
	result, err := RunSimple(context.Background(), "echo", "hello")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "hello\n" {
		t.Errorf("expected 'hello\\n', got %q", result.Output)
	}
}

func TestLookPath(t *testing.T) {
	path, err := LookPath("echo")
	if err != nil {
		t.Fatal(err)
	}
	if path == "" {
		t.Error("expected non-empty path")
	}
	_, err = LookPath("nonexistent-command-xyz")
	if err == nil {
		t.Error("expected error for nonexistent command")
	}
}

func TestCommandExists(t *testing.T) {
	if !CommandExists("echo") {
		t.Error("expected 'echo' to exist")
	}
	if CommandExists("nonexistent-command-xyz") {
		t.Error("expected nonexistent command to not exist")
	}
}

func TestRun_Cancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Run(ctx, "echo", []string{"hello"}, DefaultOptions())
	if err == nil {
		t.Error("expected error for cancelled context")
	}
}
