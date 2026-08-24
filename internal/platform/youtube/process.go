package youtube

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/process"
)

// ProcessRunnerAdapter is the canonical os/exec wrapper for the youtube
// domain. It bridges the application-layer ProcessRunnerPort shape
// (stdout, stderr, error) onto internal/platform/process.Run.
type ProcessRunnerAdapter struct{}

// NewProcessRunnerAdapter returns an adapter with no per-call state.
func NewProcessRunnerAdapter() *ProcessRunnerAdapter {
	return &ProcessRunnerAdapter{}
}

// Run executes a subprocess and returns its stdout/stderr verbatim.
// CombinedOutput=FALSE is the canonical behaviour: yt-dlp prints JSON
// to stdout and diagnostics to stderr and the two MUST NOT mix.
func (a *ProcessRunnerAdapter) Run(ctx context.Context, name string, args []string) (string, string, error) {
	opts := process.Options{
		Timeout:        process.DefaultTimeout,
		CombinedOutput: false,
		MaxOutputBytes: 4 * 1024 * 1024, // 4MB cap to prevent OOM on runaway yt-dlp
	}
	res, err := process.Run(ctx, name, args, opts)
	if res == nil {
		return "", "", err
	}
	return res.Stdout, res.Stderr, err
}
