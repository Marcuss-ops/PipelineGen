// Package process provides a safe subprocess runner that preserves
// os.Environ(), applies a default timeout, limits stdout/stderr,
// returns exit codes, and redacts tokens/secrets from output.
package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
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

	// AllowShellFallback enables a last-resort /bin/sh -lc execution
	// path when direct exec fails because the target binary is missing.
	// Default false to preserve the no-shell contract.
	AllowShellFallback bool
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

	cmd := commandContext(ctx, name, args...)

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
		result.TimedOut = isTimeout(ctx, err)
		if err != nil {
			if shouldTryPythonFallback(name, err) {
				if pyResult, pyErr := runViaPythonScript(ctx, name, args, opts, start); pyErr == nil {
					return pyResult, nil
				}
			}
			if isNotFoundExecError(err) && opts.AllowShellFallback {
				return runViaShell(ctx, name, args, opts, start)
			}
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
		result.TimedOut = isTimeout(ctx, err)
		if err != nil {
			if shouldTryPythonFallback(name, err) {
				if pyResult, pyErr := runViaPythonScript(ctx, name, args, opts, start); pyErr == nil {
					return pyResult, nil
				}
			}
			if isNotFoundExecError(err) && opts.AllowShellFallback {
				return runViaShell(ctx, name, args, opts, start)
			}
			return result, fmt.Errorf("command %s failed: %w (stdout: %s, stderr: %s)",
				name, err, redactSecrets(result.Stdout), redactSecrets(result.Stderr))
		}
	}

	return result, nil
}

func isNotFoundExecError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, exec.ErrNotFound) {
		return true
	}
	return strings.Contains(err.Error(), "no such file or directory")
}

func runViaShell(ctx context.Context, name string, args []string, opts Options, start time.Time) (*Result, error) {
	shellCmd := shellJoin(name, args)
	sh := commandContext(ctx, "/bin/sh", "-lc", shellCmd)
	if opts.WorkDir != "" {
		sh.Dir = opts.WorkDir
	}
	sh.Env = mergeEnv(opts.Env)

	var stdout, stderr bytes.Buffer
	result := &Result{}
	if opts.CombinedOutput {
		out, err := sh.CombinedOutput()
		result.Duration = time.Since(start)
		result.Output = truncateSafe(string(out), opts.MaxOutputBytes)
		result.ExitCode = exitCode(err)
		result.TimedOut = isTimeout(ctx, err)
		if err != nil {
			return result, fmt.Errorf("command %s failed via shell fallback: %w (output: %s)", name, err, redactSecrets(result.Output))
		}
		return result, nil
	}

	sh.Stdout = &stdout
	sh.Stderr = &stderr
	err := sh.Run()
	result.Duration = time.Since(start)
	result.Stdout = truncateSafe(stdout.String(), opts.MaxOutputBytes)
	result.Stderr = truncateSafe(stderr.String(), opts.MaxOutputBytes)
	result.ExitCode = exitCode(err)
	result.TimedOut = isTimeout(ctx, err)
	if err != nil {
		return result, fmt.Errorf("command %s failed via shell fallback: %w (stdout: %s, stderr: %s)",
			name, err, redactSecrets(result.Stdout), redactSecrets(result.Stderr))
	}
	return result, nil
}

func runViaPythonScript(ctx context.Context, name string, args []string, opts Options, start time.Time) (*Result, error) {
	// PR-PROCESS-CWD-HIJACK guard (security review, July 2026):
	// CWD-relative paths could be hijacked by an attacker who drops a
	// file (e.g. `ls` with a #!python shebang) into the runner's
	// current working directory. The Python fallback is triggered on
	// exec.ErrNotFound (PATH lookup failed) but looksLikePythonScript
	// opens `name` via os.Open which resolves against CWD. Require
	// an absolute path so the only files we open are the ones the
	// kernel-level exec just rejected (i.e. the absolute path was
	// verified to NOT exist as a binary, but might still exist as a
	// script). godlike/07 fail-closed: if a caller wants the
	// fallback with a relative path, they must resolve it to an
	// absolute path themselves (e.g. via filepath.Abs or by
	// pre-computing the location of the script).
	if !filepath.IsAbs(name) {
		return nil, fmt.Errorf("python fallback requires absolute path (got %q; CWD-relative paths rejected to prevent local-file hijacking)", name)
	}
	if !looksLikePythonScript(name) {
		return nil, fmt.Errorf("no python fallback for %s", name)
	}
	pythonPath := "/usr/bin/python3"
	if _, err := os.Stat(pythonPath); err != nil {
		if found, lookErr := exec.LookPath("python3"); lookErr == nil {
			pythonPath = found
		} else {
			return nil, fmt.Errorf("python fallback unavailable: %w", err)
		}
	}
	pyArgs := append([]string{name}, args...)
	sh := commandContext(ctx, pythonPath, pyArgs...)
	if opts.WorkDir != "" {
		sh.Dir = opts.WorkDir
	}
	sh.Env = mergeEnv(opts.Env)

	var stdout, stderr bytes.Buffer
	result := &Result{}
	if opts.CombinedOutput {
		out, err := sh.CombinedOutput()
		result.Duration = time.Since(start)
		result.Output = truncateSafe(string(out), opts.MaxOutputBytes)
		result.ExitCode = exitCode(err)
		result.TimedOut = isTimeout(ctx, err)
		if err != nil {
			return result, fmt.Errorf("command %s failed via python fallback: %w (output: %s)", name, err, redactSecrets(result.Output))
		}
		return result, nil
	}

	sh.Stdout = &stdout
	sh.Stderr = &stderr
	err := sh.Run()
	result.Duration = time.Since(start)
	result.Stdout = truncateSafe(stdout.String(), opts.MaxOutputBytes)
	result.Stderr = truncateSafe(stderr.String(), opts.MaxOutputBytes)
	result.ExitCode = exitCode(err)
	result.TimedOut = isTimeout(ctx, err)
	if err != nil {
		return result, fmt.Errorf("command %s failed via python fallback: %w (stdout: %s, stderr: %s)",
			name, err, redactSecrets(result.Stdout), redactSecrets(result.Stderr))
	}
	return result, nil
}

// commandContext keeps a subprocess and descendants in one process group.
// CommandContext's default cancellation kills only the direct child; browser
// fallbacks such as node -> Chrome can otherwise leave headless descendants
// orphaned when the timeout fires.
func commandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
			return nil
		}
		return cmd.Process.Kill()
	}
	return cmd
}

func looksLikePythonScript(name string) bool {
	fi, err := os.Stat(name)
	if err != nil || fi.IsDir() {
		return false
	}
	f, err := os.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, 128)
	n, _ := io.ReadFull(f, buf)
	line := string(buf[:n])
	if !strings.HasPrefix(line, "#!") {
		return false
	}
	return strings.Contains(strings.ToLower(line), "python")
}

func shouldTryPythonFallback(name string, err error) bool {
	if !looksLikePythonScript(name) {
		return false
	}
	if isNotFoundExecError(err) {
		return true
	}
	return exitCode(err) == 127
}

func shellJoin(name string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuote(name))
	for _, arg := range args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
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

func isTimeout(ctx context.Context, err error) bool {
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded)
}

// ── Secret redaction ───────────────────────────────────────────────────

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-+/=]{8,}`),
	regexp.MustCompile(`(?i)token[=:]\s*[A-Za-z0-9._\-+/=]{16,}`),
	regexp.MustCompile(`(?i)(authorization|api[_-]?key|secret|password)["':=\s]+[A-Za-z0-9._\-+/=]{8,}`),
	// PR-PROCESS-SECRET-PATTERNS extension (security review, July 2026):
	// broadened the charset to also catch Base64URL (adds `.` for JWT
	// segment separators, `-` and `_` for the URL-safe alphabet) so
	// JWTs (header.payload.signature) and other base64url-encoded
	// tokens no longer leak through the redactor.
	regexp.MustCompile(`[A-Za-z0-9._\-+/]{40,}={0,2}`),
	// AWS access key ID canonical shape (AKIA + 16 uppercase alnum).
	// Pre-existing rules would only catch these when prefixed with an
	// "api_key" / "secret" keyword; the standalone shape is now caught.
	// Word-boundary anchor prevents matching inside larger identifiers
	// (e.g. "XAKIAIOSFODNN7EXAMPLE" would otherwise match).
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
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
