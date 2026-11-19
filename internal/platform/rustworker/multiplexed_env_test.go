package rustworker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeEnvEchoWorker(t *testing.T) string {
	t.Helper()
	script := filepath.Join(t.TempDir(), "worker.sh")
	body := "#!/bin/sh\nIFS= read -r line\nprintf '{\"dir\":\"%s\"}\\n' \"$SEMANTIC_CACHE_DIR\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write worker: %v", err)
	}
	return script
}

func TestMultiplexedPersistentRunnerWithEnvPassesTheOverride(t *testing.T) {
	script := writeEnvEchoWorker(t)
	runner := NewMultiplexedPersistentRunnerWithEnv(1, []string{"SEMANTIC_CACHE_DIR=/tmp/semantic-cache"})
	defer runner.Reset()

	out, _, err := runner.Run(context.Background(), script, []byte("{}\n"), 4096)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(string(out), "/tmp/semantic-cache") {
		t.Fatalf("worker did not receive the override: %s", out)
	}
}

func TestMultiplexedPersistentRunnerWithoutEnvKeepsInheriting(t *testing.T) {
	t.Setenv("SEMANTIC_CACHE_DIR", "/tmp/inherited-cache")
	script := writeEnvEchoWorker(t)
	runner := NewMultiplexedPersistentRunner(1)
	defer runner.Reset()

	out, _, err := runner.Run(context.Background(), script, []byte("{}\n"), 4096)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(string(out), "/tmp/inherited-cache") {
		t.Fatalf("the default runner must keep the inherited environment: %s", out)
	}
}
