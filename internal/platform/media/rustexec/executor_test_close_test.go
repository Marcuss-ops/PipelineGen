package rustexec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestExecutorCloseTerminatesPersistentWorker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell process required")
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	workerPath := filepath.Join(dir, "persistent.sh")
	worker := fmt.Sprintf("#!/bin/sh\nprintf '%%s' \"$$\" > %q\nwhile IFS= read -r line; do printf '{\"ok\":true}\\n'; done\n", pidFile)
	if err := os.WriteFile(workerPath, []byte(worker), 0o755); err != nil {
		t.Fatal(err)
	}

	executor := NewExecutorWithLimit(workerPath, "ffmpeg", 1, nil)
	if _, _, err := executor.Run(context.Background(), []byte(`{"request":true}`)); err != nil {
		t.Fatalf("executor Run: %v", err)
	}
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("read worker pid: %v", err)
	}
	var pid int
	if _, err := fmt.Sscanf(string(pidBytes), "%d", &pid); err != nil || pid <= 0 {
		t.Fatalf("worker pid = %q, parse error = %v", pidBytes, err)
	}

	executor.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("persistent worker %d survived Executor.Close", pid)
}
