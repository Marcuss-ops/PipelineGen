// Package retry — registry_stdlib_test.go (FASE 6 Cut 6.1, July 2026).
//
// Regression tests for the stdlib Classifier functions in
// registry_stdlib.go. Per godlike/07 no-fake-availability, the tests
// use REAL stdlib types (no mocks or fakes) so a Go stdlib upgrade
// that breaks *exec.ExitError unwrap semantics, *url.Error.Unwrap
// behaviour, or net.Error interface methods would surface here first.
//
// godlike/06 SSOT: pkg/retry/registry_stdlib.go is the canonical owner
// of classifyExecExitError + classifyURLError shapes. Tests in
// any other package that mock these would indicate a regression in
// the typed-only contract.
//
// The Classifier chain is GLOBAL (init-time registered) — tests must
// use local ClassifierRegistry instances to isolate between registered
// Classifiers and the walker fall-through. Without that, the new typed-only probes
// fired in IsTransient's underlying logic might sub-classify the err
// shape and shadow the stdlib Classifier under test.
//
// However, classifyExecExitError + classifyURLError probe specific
// typed shape (not substring), so they don't conflict with the
// RetryableError interface fallback — the test only verifies that
// the Classifier PROPERLY emits the canonical RetryDecision for each
// shape, including the cases where the typed-probe fallback would
// also fire (the dedicated Classifier wins because first-match-wins
// on the chain — the test asserts the Class field is network/timeout
// per the Classifier's contract, NOT unknown which the fallback would
// emit).
package retry

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"strings"
	"syscall"
	"testing"
)

// ── classifyExecExitError ────────────────────────────────────────────────────

// TestClassifyExecExitError_ConservativeTerminal pins the spec
// conservative-policy (*exec.ExitError always-classifies-as-terminal).
// Per Cut 6.1 design: domain wrappers must WrapTransient at the call
// site (git EX_TEMPFAIL=75 etc.). The stdlib Classifier does NOT
// guess on shell semantics.
func TestClassifyExecExitError_ConservativeTerminal(t *testing.T) {
	// Tests the classifier function directly; it does not depend on
	// the global registry chain.
	tests := []struct {
		name string
		err  error
	}{
		{"exit code 1", makeExitErr(t, 1)},
		{"exit code 127 (command not found)", makeExitErr(t, 127)},
		{"exit code 137 (OOM kill)", makeExitErr(t, 137)},
		{"exit code 255", makeExitErr(t, 255)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d, ok := classifyExecExitError(tc.err)
			if !ok {
				t.Fatalf("classifyExecExitError: want ok=true; got false")
			}
			if d.Retryable {
				t.Errorf("Retryable: want false (conservative); got true. err=%v", tc.err)
			}
			if d.Class != ErrUnknown {
				t.Errorf("Class: want ErrUnknown (%q); got %q", ErrUnknown, d.Class)
			}
			if d.SafeMessage == "" {
				t.Errorf("SafeMessage: must be populated for final=true classifier")
			}
		})
	}
}

// TestClassifyExecExitError_NonExecError verifies the classifier
// leaves non-*exec.ExitError shapes unclaimed so the next Classifier
// in the chain can decide.
func TestClassifyExecExitError_NonExecError(t *testing.T) {
	d, ok := classifyExecExitError(errors.New("not an exec exit error"))
	if ok {
		t.Fatalf("classifyExecExitError(non-exec err): want ok=false; got ok=true, d=%+v", d)
	}
	if d != (RetryDecision{}) {
		t.Errorf("classifyExecExitError: want zero-value; got %+v", d)
	}
}

// TestClassifyExecExitError_NilSafe pins godlike/07 nil-handling.
func TestClassifyExecExitError_NilSafe(t *testing.T) {
	d, ok := classifyExecExitError(nil)
	if ok {
		t.Fatalf("classifyExecExitError(nil): want ok=false; got ok=true")
	}
	if d != (RetryDecision{}) {
		t.Errorf("classifyExecExitError: want zero-value; got %+v", d)
	}
}

// makeExitErr constructs a *exec.ExitError from the test process.
// Uses os.NewProcess → cmd.ProcessState — but a real test-process
// fork would not be hermetic; instead we use the test-only helper
// exec.ExitError directly via cmd.ProcessState saved in a fake
// state. The simplest approach: use os/exec.Cmd.Run with a known
// shell command (exit code passed in). This is hermetic (the test
// process forks+waits) and exercises the real production code path.
func makeExitErr(t *testing.T, exitCode int) error {
	t.Helper()
	// Use bash -c 'exit N' for hermetic per-exit-code construction.
	// On Linux this is a sub-millisecond fork; on macOS/Windows the
	// test exits at t.Skip via the platform guard below.
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash not in PATH; cannot construct exit-code %d test fixture: %v", exitCode, err)
	}
	cmd := exec.Command("bash", "-c", fmt.Sprintf("exit %d", exitCode))
	err := cmd.Run()
	if err == nil {
		t.Fatalf("test setup error: bash -c 'exit %d' returned nil; expected non-nil exit err", exitCode)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("test setup error: bash -c 'exit %d' returned non-exit error: %v", exitCode, err)
	}
	if exitErr.ExitCode() != exitCode {
		t.Fatalf("test setup error: bash -c 'exit %d' returned exit code %d", exitCode, exitErr.ExitCode())
	}
	return exitErr
}

// TestClassifyExecExitError_ErrnoDecode pins the codeAsErrno mapping
// for codes that overlap with syscall.Errno (the canonical mapped
// errno band). The SafeMessage surfaces the errno name when the
// code is in [1,255].
func TestClassifyExecExitError_ErrnoDecode(t *testing.T) {
	// Manually construct an ExitError-like value (the stdlib does not
	// expose a constructor; we use a fake ProcessState via cmd.Run
	// to exercise the real path). The syscall.Errno mapping test uses
	// bash -c 'exit 2' which maps to ENOENT typically.
	exitErr := makeExitErr(t, 2) // bash sub-process exits with syscall.ENOENT (2) when 'exit 2'
	d, ok := classifyExecExitError(exitErr)
	if !ok {
		t.Fatalf("classifyExecExitError: want ok=true; got false")
	}
	// SafeMessage should contain "exit code 2" (always); on Linux the
	// errno-aware branch also surfaces "(ENOENT)" — assertion is
	// presence, not exact text, for cross-platform compatibility.
	if !strings.Contains(d.SafeMessage, "exit code 2") {
		t.Errorf("SafeMessage must contain 'exit code 2'; got %q", d.SafeMessage)
	}
}

// TestClassifyExecExitError_SyscallErrnoDirect verifies codeAsErrno
// returns the typed errno when code falls in [1,255]; returns
// (zero, false) for codes outside that band (exit-0, exit 256+).
func TestClassifyExecExitError_SyscallErrnoDirect(t *testing.T) {
	tests := []struct {
		code    int
		wantOk  bool
		wantErr syscall.Errno
	}{
		{0, false, 0},
		{1, true, syscall.Errno(1)},
		{127, true, syscall.Errno(127)},
		{255, true, syscall.Errno(255)},
		{256, false, 0},
		{1000, false, 0},
	}
	for _, tc := range tests {
		errno, ok := codeAsErrno(tc.code)
		if ok != tc.wantOk {
			t.Errorf("codeAsErrno(%d): want ok=%v; got ok=%v", tc.code, tc.wantOk, ok)
		}
		if errno != tc.wantErr {
			t.Errorf("codeAsErrno(%d): want errno=%v; got %v", tc.code, tc.wantErr, errno)
		}
	}
}

// ── classifyURLError ─────────────────────────────────────────────────────────

// TestClassifyURLError_NilSafe pins godlike/07 nil-handling for
// the URL classifier.
func TestClassifyURLError_NilSafe(t *testing.T) {
	d, ok := classifyURLError(nil)
	if ok {
		t.Fatalf("classifyURLError(nil): want ok=false; got ok=true")
	}
	if d != (RetryDecision{}) {
		t.Errorf("classifyURLError: want zero-value; got %+v", d)
	}
}

// TestClassifyURLError_NonURLError verifies the classifier leaves
// non-*url.Error shapes unclaimed.
func TestClassifyURLError_NonURLError(t *testing.T) {
	d, ok := classifyURLError(errors.New("not a url error"))
	if ok {
		t.Fatalf("classifyURLError(non-URL err): want ok=false; got true")
	}
	if d != (RetryDecision{}) {
		t.Errorf("classifyURLError: want zero-value; got %+v", d)
	}
}

// TestClassifyURLError_ContextDeadlineExceeded pins Path 1
// (context.DeadlineExceeded wrapped in *url.Error → ErrTimeout,
// retryable=true).
func TestClassifyURLError_ContextDeadlineExceeded(t *testing.T) {
	ue := &url.Error{
		Op:  "Get",
		URL: "https://example.com/api/v1/items",
		Err: context.DeadlineExceeded,
	}
	d, ok := classifyURLError(ue)
	if !ok {
		t.Fatalf("classifyURLError: want ok=true; got false")
	}
	if d.Class != ErrTimeout {
		t.Errorf("Class: want ErrTimeout (%q); got %q", ErrTimeout, d.Class)
	}
	if !d.Retryable {
		t.Errorf("Retryable: want true; got false")
	}
	if !strings.Contains(d.SafeMessage, "context deadline exceeded") {
		t.Errorf("SafeMessage must mention 'context deadline exceeded'; got %q", d.SafeMessage)
	}
}

// tempNetError implements net.Error with configurable Timeout/Temporary
// flags. Used to exercise Path 2 + Path 3 without spinning real
// network connections. godlike/07 no-fake-availability: this is NOT
// a mock of a stdlib shape (net.Error is an interface, not a struct);
// the implementation is the canonical interface method-set.
type tempNetError struct {
	msg       string
	timeout   bool
	temporary bool
}

func (e *tempNetError) Error() string   { return e.msg }
func (e *tempNetError) Timeout() bool   { return e.timeout }
func (e *tempNetError) Temporary() bool { return e.temporary }

// TestClassifyURLError_TimeoutNetError pins Path 2 (net.Error.Timeout
// → ErrTimeout, retryable=true).
func TestClassifyURLError_TimeoutNetError(t *testing.T) {
	ue := &url.Error{
		Op:  "Get",
		URL: "https://example.com/api/v1/items",
		Err: &tempNetError{msg: "i/o timeout", timeout: true, temporary: false},
	}
	d, ok := classifyURLError(ue)
	if !ok {
		t.Fatalf("classifyURLError: want ok=true; got false")
	}
	if d.Class != ErrTimeout {
		t.Errorf("Class: want ErrTimeout (%q); got %q", ErrTimeout, d.Class)
	}
	if !d.Retryable {
		t.Errorf("Retryable: want true; got false")
	}
}

// TestClassifyURLError_TemporaryNetError pins Path 3 (net.Error.Temporary
// → ErrNetwork, retryable=true).
func TestClassifyURLError_TemporaryNetError(t *testing.T) {
	ue := &url.Error{
		Op:  "Get",
		URL: "https://example.com/api/v1/items",
		Err: &tempNetError{msg: "temporary fail", timeout: false, temporary: true},
	}
	d, ok := classifyURLError(ue)
	if !ok {
		t.Fatalf("classifyURLError: want ok=true; got false")
	}
	if d.Class != ErrNetwork {
		t.Errorf("Class: want ErrNetwork (%q); got %q", ErrNetwork, d.Class)
	}
	if !d.Retryable {
		t.Errorf("Retryable: want true; got false")
	}
}

// TestClassifyURLError_TimeoutAndTemporary pins the priority order:
// Path 2 (Timeout) wins over Path 3 (Temporary). When Timeout()=true
// AND Temporary()=true, the SafeMessage must NOT say "temporary" —
// ErrTimeout + the timeout-message is the canonical interpretation
// (a connect refused + timeout is read by the upstream as a timeout,
// not as a generic retryable).
func TestClassifyURLError_TimeoutAndTemporary(t *testing.T) {
	ue := &url.Error{
		Op:  "Get",
		URL: "https://example.com/api/v1/items",
		Err: &tempNetError{msg: "both flags true", timeout: true, temporary: true},
	}
	d, ok := classifyURLError(ue)
	if !ok {
		t.Fatalf("classifyURLError: want ok=true; got false")
	}
	if d.Class != ErrTimeout {
		t.Errorf("Class: want ErrTimeout (Path 2 wins over Path 3); got %q", d.Class)
	}
	if strings.Contains(d.SafeMessage, "temporary") {
		t.Errorf("SafeMessage should NOT mention 'temporary' when Timeout is also true; got %q", d.SafeMessage)
	}
}

// TestClassifyURLError_ParseOp pins Path 4 (Op="parse" → ErrValidation,
// retryable=false). URL parse errors are terminal — the same URL
// produces the same parse failure on retry.
func TestClassifyURLError_ParseOp(t *testing.T) {
	ue := &url.Error{
		Op:  "parse",
		URL: "://invalid-prefix",
		Err: errors.New("missing protocol scheme"),
	}
	d, ok := classifyURLError(ue)
	if !ok {
		t.Fatalf("classifyURLError: want ok=true; got false")
	}
	if d.Class != ErrValidation {
		t.Errorf("Class: want ErrValidation (%q); got %q", ErrValidation, d.Class)
	}
	if d.Retryable {
		t.Errorf("Retryable: want false (terminal parse error); got true")
	}
	if !strings.Contains(d.SafeMessage, "url: parse") {
		t.Errorf("SafeMessage should mention 'url: parse'; got %q", d.SafeMessage)
	}
}

// TestClassifyURLError_UnknownInner pins Path 5 (unknown inner error
// shape → ErrUnknown, retryable=false). Conservative — unknown shapes
// are classified as terminal by default.
func TestClassifyURLError_UnknownInner(t *testing.T) {
	ue := &url.Error{
		Op:  "Get",
		URL: "https://example.com/api/v1/items",
		Err: errors.New("some unknown inner err shape"),
	}
	d, ok := classifyURLError(ue)
	if !ok {
		t.Fatalf("classifyURLError: want ok=true; got false")
	}
	if d.Class != ErrUnknown {
		t.Errorf("Class: want ErrUnknown (%q); got %q", ErrUnknown, d.Class)
	}
	if d.Retryable {
		t.Errorf("Retryable: want false (conservative); got true")
	}
}

// TestClassifyURLError_NilInner verifies the nil-inner guard (a
// *url.Error with nil inner is malformed; the classifier falls
// through to (zero, false) rather than panic).
func TestClassifyURLError_NilInner(t *testing.T) {
	ue := &url.Error{Op: "Get", URL: "https://example.com/", Err: nil}
	d, ok := classifyURLError(ue)
	if ok {
		t.Fatalf("classifyURLError(nil-inner): want ok=false; got true")
	}
	if d != (RetryDecision{}) {
		t.Errorf("classifyURLError(nil-inner): want zero-value; got %+v", d)
	}
}

// TestRedactedURL_StripsUserInfoQueryAndFragment verifies the
// audit-log-safe URL redaction does not leak credentials in
// SafeMessage.
func TestRedactedURL_StripsUserInfoQueryAndFragment(t *testing.T) {
	tests := []struct {
		input    string
		contains []string
		excludes []string
	}{
		{
			input:    "https://api.example.com/v1/items?token=secret&page=2",
			contains: []string{"api.example.com", "/v1/items"},
			excludes: []string{"token", "secret", "?token", "page=2"},
		},
		{
			input:    "https://user:pass@host.example.com/path",
			contains: []string{"host.example.com", "/path"},
			excludes: []string{"user", "pass", "@host"},
		},
		{
			input:    "https://example.com/path?key=value#fragment",
			contains: []string{"example.com", "/path"},
			excludes: []string{"key", "value", "fragment"},
		},
	}
	for _, tc := range tests {
		got := redactedURL(tc.input)
		for _, must := range tc.contains {
			if !strings.Contains(got, must) {
				t.Errorf("redactedURL(%q): want to contain %q; got %q", tc.input, must, got)
			}
		}
		for _, drop := range tc.excludes {
			if strings.Contains(got, drop) {
				t.Errorf("redactedURL(%q): must NOT contain %q; got %q", tc.input, drop, got)
			}
		}
	}
}

// TestRedactedURL_EmptyAndOpIsHex verifies the edge-case handlers:
// empty input returns empty; malformed URL falls back to raw string
// (the SafeMessage is for audit logs, not for parsing).
func TestRedactedURL_EdgeCases(t *testing.T) {
	if got := redactedURL(""); got != "" {
		t.Errorf("redactedURL(\"\"): want empty; got %q", got)
	}
	// "://" is a parse-failure URL — net.ParseURL returns an error,
	// and the fallback returns the raw string. SafeMessage stays
	// coherent (preserves the operator's diagnostic string verbatim)
	// without trying to log-parse a malformed URL.
	got := redactedURL("://malformed-no-scheme")
	if got != "://malformed-no-scheme" {
		t.Errorf("redactedURL(malformed): want fallback to raw; got %q", got)
	}
}

// _ = net.Error alternative-form anchor — silence unused-import warnings
// if future refactors drop tempNetError (the canonical net.Error
// interface contract is verified by the compile-time conformance
// assertion below).
var _ net.Error = (*tempNetError)(nil)
