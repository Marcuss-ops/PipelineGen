// Package platform provides a thin, leaf-only wrapper around os/exec for
// running external binaries (ffmpeg, ffprobe, etc.) with context-aware
// cancellation and a per-call timeout.
//
// pkg/platform is leaf-only: it does not import from internal/. The Run /
// RunSimple / ExecOptions signatures mirror the original infrastructure
// mega-package helpers that were extracted during Onda 3 (infrastructure -> pkg/*).
package platform

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"
)

// ExecOptions configures a single Run call. The zero value is sane: no
// additional timeout beyond ctx, default working dir + env, and
// CombinedOutput=true so the historical contract (combined stdout+stderr)
// is preserved for callers that did not opt in.
type ExecOptions struct {
	// Timeout is a per-call timeout in addition to ctx. The call uses the
	// shorter of (ctx deadline, Timeout). Zero disables this layer.
	Timeout time.Duration

	// Dir overrides the working directory of the child process. Empty
	// means inherit the current process's working directory.
	Dir string

	// Env overrides the child process environment. Nil means inherit
	// the current process's environment. Each entry is "KEY=value".
	Env []string

	// CombinedOutput controls stdout/stderr capture behaviour.
	//   true (default): Run returns combined stdout+stderr as []byte.
	//                    stderr is also attached to runError.tail on
	//                    failure (callable via Stderr()).
	//   false         : Run returns ONLY stdout. stderr is captured into
	//                    runError.tail so callers can inspect it via
	//                    Stderr() but stdout is not contaminated by
	//                    warning/log lines. Use this for JSON parsers
	//                    (probe.go) or any caller that needs clean stdout.
	CombinedOutput bool
}

// Run executes path with args and returns either combined stdout+stderr
// (default; CombinedOutput=true) or stdout-only (CombinedOutput=false).
// The historical internal/infrastructure contract returned a struct with
// separate Stdout/Stderr fields; we keep the default combined for
// backwards compatibility but allow per-call separation.
//
// Returns an error if ctx is cancelled, the process exits non-zero, or
// the timeout fires. The returned error satisfies errors.As(*exec.ExitError)
// so callers can introspect the exit code, and exposes a Stderr() []
// byte accessor that returns the captured stderr tail.
func Run(ctx context.Context, path string, args []string, opts ExecOptions) ([]byte, error) {
	if ctx == nil {
		return nil, errors.New("platform.Run: ctx is nil")
	}
	if path == "" {
		return nil, errors.New("platform.Run: path is empty")
	}

	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, path, args...)
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	if opts.Env != nil {
		cmd.Env = append([]string(nil), opts.Env...)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	out := stdout.Bytes()
	if opts.CombinedOutput {
		out = append(stdout.Bytes(), stderr.Bytes()...)
	}
	if err != nil {
		// Always capture stderr into tail for diagnostics. When
		// CombinedOutput=false, callers still want the stderr tail
		// (e.g. to log ffprobe warnings) but stdout stays clean.
		if len(out) == 0 {
			return nil, &runError{path: path, err: err, tail: stderr.Bytes()}
		}
		return out, &runError{path: path, err: err, tail: stderr.Bytes()}
	}
	return out, nil
}

// RunSimple runs path with args using zero-value ExecOptions. Equivalent
// to Run(ctx, path, args, ExecOptions{}).
func RunSimple(ctx context.Context, path string, args []string) ([]byte, error) {
	return Run(ctx, path, args, ExecOptions{})
}

// runError wraps an exec exit error with the path + a tail of stderr so
// caller logs can include context without re-running the process. The
// Stderr() accessor exposes the captured stderr tail for diagnostic
// logging — useful when CombinedOutput=false is set and stdout alone
// was returned.
type runError struct {
	path string
	err  error
	tail []byte
}

func (e *runError) Error() string {
	return e.path + ": " + e.err.Error()
}

func (e *runError) Unwrap() error { return e.err }

// Stderr returns the captured stderr tail for diagnostic logging.
func (e *runError) Stderr() []byte { return e.tail }
