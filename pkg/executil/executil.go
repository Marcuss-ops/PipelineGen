package executil

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

// Options configures how a command is run.
type Options struct {
	Timeout      time.Duration
	WorkDir      string
	Env          []string
	CombinedOutput bool
}

// Result holds the output from a command execution.
type Result struct {
	Stdout string
	Stderr string
	Output string // Combined output if CombinedOutput is true
}

// DefaultOptions returns sensible defaults.
func DefaultOptions() Options {
	return Options{
		Timeout:        10 * time.Minute,
		CombinedOutput: true,
	}
}

// Run executes a command with the given options.
// Uses exec.CommandContext to prevent injection attacks (no shell).
func Run(ctx context.Context, name string, args []string, opts Options) (*Result, error) {
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, name, args...)

	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}
	if len(opts.Env) > 0 {
		cmd.Env = opts.Env
	}

	result := &Result{}

	if opts.CombinedOutput {
		out, err := cmd.CombinedOutput()
		result.Output = string(out)
		if err != nil {
			return result, fmt.Errorf("command %s failed: %w (output: %s)", name, err, result.Output)
		}
	} else {
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		result.Stdout = stdout.String()
		result.Stderr = stderr.String()
		if err != nil {
			return result, fmt.Errorf("command %s failed: %w (stdout: %s, stderr: %s)", name, err, result.Stdout, result.Stderr)
		}
	}

	return result, nil
}

// RunSimple is a convenience wrapper around Run with default options.
func RunSimple(ctx context.Context, name string, args ...string) (*Result, error) {
	return Run(ctx, name, args, DefaultOptions())
}

// LookPath checks if a command exists in PATH.
func LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

// CommandExists checks if a command exists.
func CommandExists(name string) bool {
	_, err := LookPath(name)
	return err == nil
}
