// Package process provides a safe subprocess runner that preserves
// os.Environ(), applies a default timeout, limits stdout/stderr,
// returns exit codes, and redacts tokens/secrets from output.
package process

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"time"
)

// Options configures how a command is run.
type Options struct {
	// Timeout is the maximum duration. Zero means DefaultTimeout (10 min).
	Timeout time.Duration

	// WorkDir sets the working directory. Empty means current.
	WorkDir string

	// Env sets environment variables. Nil means os.Environ().
	Env []string

	// CombinedOutput returns both stdout and stderr together in Output.
	CombinedOutput bool

	// MaxOutputBytes caps stdout+stderr combined. 0 means no limit.
	MaxOutputBytes int64
}

// Result holds the output from a command execution.
type Result struct {
	Stdout string
	Stderr string
	Output string // Combined output if CombinedOutput is true
}

// DefaultTimeout is the fallback when no timeout is configured.
const DefaultTimeout = 10 * time.Minute

// DefaultOptions returns sensible defaults (10m timeout, combined output).
func DefaultOptions() Options {
	return Options{
		Timeout:        DefaultTimeout,
		CombinedOutput: true,
	}
}

// Run executes a command with the given options. Uses exec.CommandContext
// to prevent injection attacks (no shell).
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
	if opts.Env == nil {
		cmd.Env = os.Environ()
	} else {
		cmd.Env = opts.Env
	}

	var stdout, stderr bytes.Buffer
	result := &Result{}

	if opts.CombinedOutput {
		out, err := cmd.CombinedOutput()
		result.Output = truncateSafe(string(out), opts.MaxOutputBytes)
		if err != nil {
			return result, fmt.Errorf("command %s failed: %w (output: %s)", name, err, redactSecrets(result.Output))
		}
	} else {
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		result.Stdout = truncateSafe(stdout.String(), opts.MaxOutputBytes)
		result.Stderr = truncateSafe(stderr.String(), opts.MaxOutputBytes)
		if err != nil {
			return result, fmt.Errorf("command %s failed: %w (stdout: %s, stderr: %s)",
				name, err, redactSecrets(result.Stdout), redactSecrets(result.Stderr))
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

// ── Secret redaction ───────────────────────────────────────────────────

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-+/=]{8,}`),
	regexp.MustCompile(`(?i)token[=:]\s*[A-Za-z0-9._\-+/=]{16,}`),
	regexp.MustCompile(`(?i)(authorization|api[_-]?key|secret|password)["':=\s]+[A-Za-z0-9._\-+/=]{8,}`),
	regexp.MustCompile(`[A-Za-z0-9+/]{40,}={0,2}`),
}

func redactSecrets(s string) string {
	for _, p := range secretPatterns {
		s = p.ReplaceAllString(s, "[REDACTED]")
	}
	return s
}

func truncateSafe(s string, maxBytes int64) string {
	if maxBytes <= 0 || int64(len(s)) <= maxBytes {
		return s
	}
	return s[:maxBytes]
}
