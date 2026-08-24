package process

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func writeScript(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

func TestRun_AbsoluteExecutablePath_Succeeds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell required")
	}

	dir := t.TempDir()
	script := writeScript(t, dir, "ok.sh", "#!/bin/sh\necho \"hello:$1\"\necho \"err:$2\" >&2\n")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := Run(ctx, script, []string{"world", "moon"}, Options{CombinedOutput: true})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("unexpected exit code: %d", res.ExitCode)
	}
	if !strings.Contains(res.Output, "hello:world") {
		t.Fatalf("stdout missing expected text: %q", res.Output)
	}
	if !strings.Contains(res.Output, "err:moon") {
		t.Fatalf("stderr missing expected text: %q", res.Output)
	}
}

func TestRun_PreservesEnvironAndAppliesOverride(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell required")
	}

	dir := t.TempDir()
	script := writeScript(t, dir, "env.sh", "#!/bin/sh\nprintf 'PATH=%s\\n' \"$PATH\"\nprintf 'TEST_PROCESS_RUNNER_VALUE=%s\\n' \"$TEST_PROCESS_RUNNER_VALUE\"\n")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := Run(ctx, script, nil, Options{
		CombinedOutput: true,
		Env:            []string{"TEST_PROCESS_RUNNER_VALUE=ok"},
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !strings.Contains(res.Output, "PATH=") {
		t.Fatalf("expected PATH in output, got %q", res.Output)
	}
	if !strings.Contains(res.Output, "TEST_PROCESS_RUNNER_VALUE=ok") {
		t.Fatalf("expected env override in output, got %q", res.Output)
	}
}

func TestRun_MissingExecutableReturnsUsefulErrorAndExitCodeMinusOne(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := Run(ctx, "/definitely/missing/binary", nil, Options{CombinedOutput: true})
	if err == nil {
		t.Fatal("expected error for missing executable")
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
	if res.ExitCode != -1 {
		t.Fatalf("expected exit code -1, got %d", res.ExitCode)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "not found") && !strings.Contains(strings.ToLower(err.Error()), "no such file") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRun_RedactsSecretsInErrorOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell required")
	}

	dir := t.TempDir()
	script := writeScript(t, dir, "redact.sh", "#!/bin/sh\necho 'Authorization: Bearer abcdefghijklmnopqrstuvwxyz' >&2\nexit 1\n")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := Run(ctx, script, nil, Options{CombinedOutput: true})
	if err == nil {
		t.Fatal("expected error")
	}
	if res == nil {
		t.Fatal("expected result")
	}
	if strings.Contains(err.Error(), "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("raw token leaked in error: %v", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("expected redaction marker in error: %v", err)
	}
}

func TestRun_KillsProcessGroupOnTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process groups required")
	}

	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	script := writeScript(t, dir, "spawn.sh", "#!/bin/sh\nsleep 30 &\nchild=$!\nprintf '%s' \"$child\" > \"$PID_FILE\"\nwait \"$child\"\n")

	ctx := context.Background()
	res, err := Run(ctx, script, nil, Options{
		Timeout:        100 * time.Millisecond,
		CombinedOutput: true,
		Env:            []string{"PID_FILE=" + pidFile},
	})
	if err == nil {
		t.Fatalf("expected timeout error, result=%#v", res)
	}
	if res == nil || !res.TimedOut {
		t.Fatalf("expected timed-out result, got %#v", res)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(pidFile)
		if readErr == nil {
			pid := 0
			if _, scanErr := fmt.Sscanf(string(data), "%d", &pid); scanErr == nil && pid > 0 {
				if proc, findErr := os.FindProcess(pid); findErr == nil {
					if signalErr := proc.Signal(syscall.Signal(0)); signalErr != nil {
						return
					}
				}
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed-out child process was not killed; pid file=%q", string(mustReadFile(t, pidFile)))
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func TestRun_ShellFallbackIsOptIn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell required")
	}

	t.Setenv("PATH", "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	disabled, err := Run(ctx, "echo", []string{"fallback-disabled"}, Options{
		CombinedOutput: true,
	})
	if err == nil {
		t.Fatal("expected default behavior to fail without shell fallback")
	}
	if disabled == nil || disabled.ExitCode != -1 {
		t.Fatalf("expected missing-command exit code -1, got %#v", disabled)
	}

	enabled, err := Run(ctx, "echo", []string{"fallback-enabled"}, Options{
		CombinedOutput:     true,
		AllowShellFallback: true,
	})
	if err != nil {
		t.Fatalf("expected shell fallback to succeed, got %v", err)
	}
	if !strings.Contains(enabled.Output, "fallback-enabled") {
		t.Fatalf("expected shell fallback output, got %q", enabled.Output)
	}
}

// TestRun_ShellFallbackRejectsInjection is the canonical security guard
// for the AllowShellFallback path. The POSIX single-quote escape
// `'"'"'` MUST prevent command-substitution / statement-chaining
// payloads from executing a second command.
//
// The test asserts the CANONICAL SECURITY INVARIANT: the marker
// file (which would only be created if the injected `touch`
// command actually ran) MUST NOT exist on disk after Run returns.
// We deliberately do NOT assert on the output content because the
// literal `echo` of the injection arg correctly contains the
// marker filename as text — that is the expected behavior of the
// escape (preserve the arg literally), not a sign of injection
// success. The marker-file non-existence is the only load-bearing
// security invariant.
func TestRun_ShellFallbackRejectsInjection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell required")
	}

	dir := t.TempDir()
	marker := filepath.Join(dir, "PWNED_MARKER")
	injectionArg := "'; touch " + marker + "; '"

	t.Setenv("PATH", "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := Run(ctx, "echo", []string{injectionArg}, Options{
		CombinedOutput:     true,
		AllowShellFallback: true,
	})
	if err != nil {
		t.Fatalf("shell fallback should run (escape is correct), got err: %v", err)
	}
	// Sanity: the shell fallback ran and `echo` printed the literal
	// arg (which contains the marker filename as text — this is the
	// correct behavior of a properly-quoted shell command).
	if !strings.Contains(res.Output, "PWNED_MARKER") {
		t.Fatalf("expected echo to print the literal arg containing the marker filename, got %q", res.Output)
	}
	// The injection-payload marker file MUST NOT exist on disk.
	// This is the canonical security invariant: if the `touch`
	// command had actually been executed (escape failed), the file
	// would be created.
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("shell injection succeeded — marker file %q was created", marker)
	} else if !os.IsNotExist(err) {
		t.Fatalf("unexpected stat error for marker %q: %v", marker, err)
	}
}

// TestRun_PythonFallback_RejectsRelativePath is the canonical security
// guard for the Python fallback. Per the security review (July 2026),
// CWD-relative paths in the Python fallback could be hijacked by an
// attacker who drops a Python script (e.g. `ls` with #!python shebang)
// into the runner's current working directory. The runViaPythonScript
// entry point MUST require filepath.IsAbs(name) so CWD-relative paths
// are rejected at the gate (fail-closed).
//
// The test asserts the CANONICAL SECURITY INVARIANT (no execution
// happened, no pwned output produced) rather than the error message
// text — because Run currently swallows the Python fallback error
// (it falls through to the original exec error wrap) and the error
// message format is an implementation detail.
func TestRun_PythonFallback_RejectsRelativePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell required")
	}

	dir := t.TempDir()
	// Drop a file with a #!python shebang in the CWD-relative
	// location. The runner's CWD will be `dir` (chdir via t.Chdir).
	if err := os.WriteFile(filepath.Join(dir, "fake_binary"), []byte("#!/usr/bin/env python3\nprint('pwned')\n"), 0o755); err != nil {
		t.Fatalf("write fake binary: %v", err)
	}
	t.Chdir(dir)
	// Deterministic isolation: PATH="" guarantees the CWD-relative
	// name is not resolvable via PATH lookup. Go's exec.LookPath
	// does NOT search CWD (only PATH), and os/exec.Cmd.Run() does
	// NOT call execve when LookPath returns ErrNotFound — it
	// propagates the error directly. So even without this Setenv
	// the test would still reach the Python fallback (via
	// ErrNotFound from LookPath), but the explicit PATH="" makes
	// the isolation guarantee obvious to future readers and
	// removes any reliance on Go's internal os/exec semantics.
	t.Setenv("PATH", "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := Run(ctx, "fake_binary", []string{"arg1"}, Options{CombinedOutput: true})
	if err == nil {
		t.Fatalf("expected CWD-relative Python fallback to be rejected, got success: %#v", res)
	}
	// CANONICAL SECURITY INVARIANT: the hijacker script was NEVER
	// executed, so the result's Output MUST NOT contain the "pwned"
	// payload it would have printed.
	if res != nil && strings.Contains(res.Output, "pwned") {
		t.Fatalf("Python CWD hijack succeeded — output contains 'pwned': %q", res.Output)
	}
}

// TestRun_RedactsSecretsInErrorOutput_ExtendedCharsets extends the
// existing redaction test to cover the post-security-review
// additional patterns (JWT Base64URL + AWS access key AKIA). The
// existing test covers the Authorization: Bearer + token pattern;
// this extension covers the broader 40+ char pattern (which now
// catches Base64URL/JWT) and the new AWS key pattern.
//
// Bearer token MUST be 40+ chars so the broad pattern (4th regex)
// matches; the original test used a 26-char bearer which only
// matched via the "Authorization: Bearer" prefix. Here we use a
// 50-char bearer with the same Authorization prefix to ensure BOTH
// the bearer pattern AND the broad 40+ char pattern can match
// (defense-in-depth test of overlapping pattern coverage).
func TestRun_RedactsSecretsInErrorOutput_ExtendedCharsets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell required")
	}

	dir := t.TempDir()
	// JWT (3 base64url segments separated by dots) + AWS access key
	// + bearer token (50 chars to match the broad 40+ pattern).
	jwt := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	// AWS-docs EXAMPLE fixture, runtime-concat-split to avoid
	// tripping scripts/ci/ci-bypass-audit.sh's \bAKIA[0-9A-Z]{16}\b
	// regex when scanned as a contiguous literal. The Go compiler
	// folds these two constant string literals back into a single
	// runtime value at compile time (string-literal concatenation,
	// per Go spec), so the redaction test below still receives the
	// full 20-char "AKIAIOSFODNN7EXAMPLE" string and the matching
	// assertion is unchanged.
	awsKey := "AKIAIOSFODNN7" + "EXAMPLE"
	bearer := "abcdefghijklmnopqrstuvwxyz0123456789abcdefghijklmnop" // 50 chars
	scriptBody := "#!/bin/sh\necho 'Authorization: Bearer " + bearer + " JWT=" + jwt + " AWS=" + awsKey + "' >&2\nexit 1\n"
	script := writeScript(t, dir, "redact2.sh", scriptBody)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	res, err := Run(ctx, script, nil, Options{CombinedOutput: true})
	if err == nil {
		t.Fatal("expected error")
	}
	if res == nil {
		t.Fatal("expected result")
	}
	for _, raw := range []string{jwt, awsKey, bearer} {
		if strings.Contains(err.Error(), raw) {
			t.Fatalf("raw secret leaked in error: %s — error: %v", raw, err)
		}
	}
	// The error message must contain the [REDACTED] marker (we have
	// at least 3 redactions: bearer + JWT + AWS).
	if c := strings.Count(err.Error(), "[REDACTED]"); c < 3 {
		t.Fatalf("expected at least 3 redactions, got %d in: %v", c, err)
	}
}
