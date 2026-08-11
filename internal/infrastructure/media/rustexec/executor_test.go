package rustexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

type failingProcessRunner struct{}

func (failingProcessRunner) Run(context.Context, string, []byte, int64) ([]byte, []byte, error) {
	return nil, []byte("runner failed"), errors.New("runner failed")
}

type responseProcessRunner struct {
	stdout []byte
}

func (r responseProcessRunner) Run(context.Context, string, []byte, int64) ([]byte, []byte, error) {
	return r.stdout, nil, nil
}

func TestExecutorCleansPartFilesAfterFailedRun(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "clip.mp4")
	part := partPathForCleanup(output)
	if err := os.WriteFile(part, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(request{OutputPath: output})
	if err != nil {
		t.Fatal(err)
	}

	executor := NewExecutorWithLimit("unused", "ffmpeg", 1, nil)
	executor.runner = failingProcessRunner{}
	if _, _, err := executor.Run(context.Background(), payload); err == nil {
		t.Fatal("expected executor failure")
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatalf("part file still exists after failed run: %v", err)
	}
}

func TestClientCleansPartFilesAfterFailedRustResponse(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "clip.mp4")
	part := partPathForCleanup(output)
	if err := os.WriteFile(part, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}

	executor := NewExecutorWithLimit("unused", "ffmpeg", 1, nil)
	executor.runner = responseProcessRunner{stdout: []byte(`{"ok":false,"operation":"normalize","error":"failed"}`)}
	client := NewClientWithExecutor(executor, nil)
	_, err := client.call(context.Background(), request{Operation: "normalize", OutputPath: output})
	if err == nil {
		t.Fatal("expected failed Rust response")
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatalf("part file still exists after failed response: %v", err)
	}
}

func TestBoundedBufferLimitsStderrAndMarksTruncation(t *testing.T) {
	buffer := &boundedBuffer{limit: 32}
	if _, err := buffer.Write([]byte(strings.Repeat("x", 4096))); err != nil {
		t.Fatal(err)
	}
	got := buffer.Bytes()
	if len(got) > 32 || !strings.HasSuffix(string(got), "[output truncated]") {
		t.Fatalf("bounded stderr length/marker mismatch: len=%d output=%q", len(got), got)
	}
}

func TestExecutorTimeoutKillsRustProcessTreeAndCleansPart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process groups required")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "sleep.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "clip.mp4")
	part := partPathForCleanup(output)
	if err := os.WriteFile(part, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(request{OutputPath: output})
	if err != nil {
		t.Fatal(err)
	}
	executor := NewExecutorWithLimit(script, "ffmpeg", 1, nil)
	executor.timeout = 100 * time.Millisecond
	started := time.Now()
	if _, _, err := executor.Run(context.Background(), payload); err == nil {
		t.Fatal("expected configured executor timeout")
	}
	if time.Since(started) >= 2*time.Second {
		t.Fatal("executor timeout did not return promptly")
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatalf("part file still exists after timeout: %v", err)
	}
}

func TestRustProcessRunnerKillsDescendantsOnCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process groups required")
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	script := filepath.Join(dir, "spawn.sh")
	body := fmt.Sprintf("#!/bin/sh\nsleep 30 &\nchild=$!\nprintf '%%s' \"$child\" > %q\nwait \"$child\"\n", pidFile)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, _, err := (rustProcessRunner{}).Run(ctx, script, nil, 1024)
	if err == nil {
		t.Fatal("expected cancellation error")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(pidFile)
		if readErr == nil {
			var pid int
			if _, scanErr := fmt.Sscanf(string(data), "%d", &pid); scanErr == nil && pid > 0 {
				if proc, findErr := os.FindProcess(pid); findErr == nil {
					if signalErr := proc.Signal(syscall.Signal(0)); signalErr != nil {
						return
					}
				}
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("descendant survived cancellation; pid file=%q", string(dataOrEmpty(t, pidFile)))
}

func TestPersistentRustProcessRunnerReusesDispatcherProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell process required")
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	script := filepath.Join(dir, "persistent.sh")
	body := fmt.Sprintf("#!/bin/sh\nprintf '%%s' \"$$\" > %q\nwhile IFS= read -r line; do printf '{\"ok\":true}\\n'; done\n", pidFile)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := newPersistentRustProcessRunner().(*persistentRustProcessRunner)
	defer runner.reset()
	for i := 0; i < 2; i++ {
		stdout, _, err := runner.Run(context.Background(), script, []byte(`{"request":true}`), 1024)
		if err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
		if string(stdout) != "{\"ok\":true}\n" {
			t.Fatalf("unexpected response %q", stdout)
		}
	}
	if _, err := os.Stat(pidFile); err != nil {
		t.Fatalf("persistent process did not start: %v", err)
	}
}

func dataOrEmpty(t *testing.T, path string) []byte {
	t.Helper()
	data, _ := os.ReadFile(path)
	return data
}

func TestResourceLimiterWaitIsCancellationAware(t *testing.T) {
	limiter := NewResourceLimiter(1)
	release, err := limiter.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := limiter.Acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline, got %v", err)
	}
}
