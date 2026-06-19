// Package process provides a safe process runner that preserves
// os.Environ(), applies a default timeout, limits stdout/stderr,
// and redacts tokens and secrets from output.
//
// Deprecated: use internal/infrastructure/process instead.
// This file delegates to the canonical implementation.
package platform

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
)

// ExecOptions configures how a command is run.
type ExecOptions = process.Options

// Result holds the output from a command execution.
type Result = process.Result

// Deprecated: use process.DefaultOptions.
func DefaultExecOptions() ExecOptions { return process.DefaultOptions() }

// Deprecated: use process.Run.
func Run(ctx context.Context, name string, args []string, opts ExecOptions) (*Result, error) {
	return process.Run(ctx, name, args, opts)
}

// Deprecated: use process.RunSimple.
func RunSimple(ctx context.Context, name string, args ...string) (*Result, error) {
	return process.RunSimple(ctx, name, args...)
}

// Deprecated: use process.LookPath.
func LookPath(name string) (string, error) { return process.LookPath(name) }

// Deprecated: use process.CommandExists.
func CommandExists(name string) bool { return process.CommandExists(name) }
