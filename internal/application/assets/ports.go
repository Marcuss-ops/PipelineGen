package assets

import (
	"context"
	"time"
)

// ProcessResult is the output of a subprocess execution.
type ProcessResult struct {
	Stdout string
	Stderr string
	Output string
}

// ProcessRunner is the port interface for running external processes.
// The composition root injects an infrastructure implementation.
type ProcessRunner interface {
	Run(ctx context.Context, name string, args []string, opts ProcessOptions) (*ProcessResult, error)
	RunSimple(ctx context.Context, name string, args ...string) (*ProcessResult, error)
}

// DefaultProcessOptions returns sensible defaults (10m timeout, combined output).
func DefaultProcessOptions() ProcessOptions {
	return ProcessOptions{Timeout: 10 * time.Minute, CombinedOutput: true}
}

// ProcessOptions configures how a process is executed.
type ProcessOptions struct {
	WorkDir        string
	CombinedOutput bool
	Timeout        time.Duration
}

// ToolChecker is the port interface for checking if external binaries exist.
type ToolChecker interface {
	CommandExists(name string) bool
	LookPath(name string) (string, error)
}

// DBHealthCheckResult is the result of a database health check.
type DBHealthCheckResult struct {
	OK    bool
	Error string
}

// DBHealthChecker is the port interface for checking database health.
// The composition root injects an infrastructure implementation.
type DBHealthChecker interface {
	GetAllDBs() []string
	GetDBPath(dataDir, relPath string) string
	Ping(ctx context.Context, dbPath string) DBHealthCheckResult
}
