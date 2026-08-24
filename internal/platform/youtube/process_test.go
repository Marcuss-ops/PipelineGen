package youtube

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessRunnerAdapter_EchoIsolatesStdoutAndStderr(t *testing.T) {
	sh, err := exec.LookPath("/bin/sh")
	if err != nil {
		t.Skipf("/bin/sh not available: %v", err)
	}
	a := NewProcessRunnerAdapter()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stdout, stderr, err := a.Run(ctx, sh, []string{"-c", "echo test-out; echo test-err >&2"})
	require.NoError(t, err)
	assert.Equal(t, "test-out\n", stdout, "stdout must capture only the echoed line")
	assert.Equal(t, "test-err\n", stderr, "stderr must capture only the redirected line")
}

func TestProcessRunnerAdapter_NonexistentBinaryFails(t *testing.T) {
	a := NewProcessRunnerAdapter()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, err := a.Run(ctx, "/nonexistent/yt-fake-binary-12345", []string{"--version"})
	require.Error(t, err)
	lower := strings.ToLower(err.Error())
	assert.True(t,
		strings.Contains(lower, "failed") ||
			strings.Contains(lower, "not found") ||
			strings.Contains(lower, "no such file"),
		"expected wrapped error mentioning the binary or 'failed', got %q", err.Error())
}

func TestProcessRunnerAdapter_ContextCancellationSurfacesAsError(t *testing.T) {
	sh, err := exec.LookPath("/bin/sh")
	if err != nil {
		t.Skipf("/bin/sh not available: %v", err)
	}
	a := NewProcessRunnerAdapter()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	_, _, err = a.Run(ctx, sh, []string{"-c", "sleep 60; echo done"})
	require.Error(t, err, "context cancellation must surface as an error")
}

func TestProcessRunnerAdapter_NilSafeConstruction(t *testing.T) {
	a := NewProcessRunnerAdapter()
	assert.NotNil(t, a)
}
