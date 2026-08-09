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

func TestBoundedBufferLimitsStderrAndMarksTruncation(t *testing.T) {
	buffer := &boundedBuffer{limit: 32}
	if _, err := buffer.Write([]byte(strings.Repeat("x", 4096))); err != nil {
		t.Fatal(err)
	}
	got := buffer.Bytes()
	if len(got) > 32+len("\n[output truncated]") || !strings.HasSuffix(string(got), "[output truncated]") {
		t.Fatalf("bounded stderr length/marker mismatch: len=%d output=%q", len(got), got)
	}
}

func TestExecProcessRunnerKillsDescendantsOnCancellation(t *testing.T) {
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
	_, _, err := (execProcessRunner{}).Run(ctx, script, nil, 1024)
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
