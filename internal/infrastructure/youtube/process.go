package youtube

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"go.uber.org/zap"
)

// ProcessRunner executes external processes (yt-dlp, ffmpeg, python3, etc.)
// using the canonical os/exec pattern. Returns stdout, stderr, and error.
type ProcessRunner struct {
	log *zap.Logger
}

// NewProcessRunner constructs the adapter.
func NewProcessRunner(log *zap.Logger) *ProcessRunner {
	return &ProcessRunner{log: log}
}

// Run executes the named command with args and returns stdout, stderr, err.
func (r *ProcessRunner) Run(ctx context.Context, name string, args []string) (string, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), stderr.String(), fmt.Errorf("exec %s: %w", name, err)
	}
	return stdout.String(), stderr.String(), nil
}
