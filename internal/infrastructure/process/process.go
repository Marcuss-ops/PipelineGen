// Package process provides a safe subprocess runner that preserves
// os.Environ(), applies a default timeout, limits stdout/stderr,
// returns exit codes, and redacts tokens/secrets from output.
package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"syscall"
	"time"
)

// Options configures how a command is run.
type Options struct {
	// Timeout is the maximum duration. Zero or negative means DefaultTimeout (10 min).
	Timeout time.Duration

	// WorkDir sets the working directory. Empty means current.
	WorkDir string

	// Env adds extra environment variables. When set, these are appended
	// to os.Environ() — the provided entries take precedence on duplicates.
	Env []string

	// CombinedOutput returns both stdout and stderr together in Output.
	CombinedOutput bool

	// MaxOutputBytes caps stdout+stderr combined. 0 means no limit.
	MaxOutputBytes int64
}

// Result holds the output from a command execution.
type Result struct {
	Stdout   string
	Stderr   string
	Output   string // Combined output if CombinedOutput is true
	ExitCode int
	Duration time.Duration
	TimedOut bool
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

// mergeEnv appends opts.Env to os.Environ(). Duplicate keys (matching on
// KEY= prefix) from opts.Env replace the os.Environ() entries, while
// preserving all other os.Environ() entries.
func mergeEnv(extra []string) []string {
	if len(extra) == 0 {
		return os.Environ()
	}
	base := os.Environ()
	dedup := make(map[string]int)
	for i, e := range base {
		for j := 0; j < len(e); j++ {
			if e[j] == '=' {
				dedup[e[:j+1]] = i
				break
			}
		}
	}
	for _, e := range extra {
		prefix := ""
		for j := 0; j < len(e); j++ {
			if e[j] == '=' {
				prefix = e[:j+1]
				break
			}
		}
		if prefix == "" {
			base = append(base, e)
			continue
		}
		if idx, ok := dedup[prefix]; ok {
			base[idx] = e
		} else {
			base = append(base, e)
		}
	}
	return base
}

// Run executes a command with the given options. Uses exec.CommandContext
// to prevent injection attacks (no shell).
func Run(ctx context.Context, name string, args []string, opts Options) (*Result, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)

	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}
	cmd.Env = mergeEnv(opts.Env)

	var stdout, stderr bytes.Buffer
	result := &Result{}
	start := time.Now()

	if opts.CombinedOutput {
		out, err := cmd.CombinedOutput()
		result.Duration = time.Since(start)
		result.Output = truncateSafe(string(out), opts.MaxOutputBytes)
		result.ExitCode = exitCode(err)
		result.TimedOut = isTimeout(err)
		if err != nil {
			return result, fmt.Errorf("command %s failed: %w (output: %s)", name, err, redactSecrets(result.Output))
		}
	} else {
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		result.Duration = time.Since(start)
		result.Stdout = truncateSafe(stdout.String(), opts.MaxOutputBytes)
		result.Stderr = truncateSafe(stderr.String(), opts.MaxOutputBytes)
		result.ExitCode = exitCode(err)
		result.TimedOut = isTimeout(err)
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

// ── Exit code & timeout helpers ────────────────────────────────────────

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok {
			return ws.ExitStatus()
		}
		return 1
	}
	return -1
}

func isTimeout(err error) bool {
	return errors.Is(err, context.DeadlineExceeded)
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
