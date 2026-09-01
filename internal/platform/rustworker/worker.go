// Package rustworker owns generic lifecycle concerns for long-lived Rust
// workers. It deliberately knows nothing about media, protocol operations,
// storage, or application policy.
package rustworker

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
)

// Runner executes one request against a Rust worker process.
type Runner interface {
	Run(ctx context.Context, binary string, input []byte, outputLimit int64) ([]byte, []byte, error)
}

// ResourceLimiter bounds concurrent worker requests and is cancellation-aware.
type ResourceLimiter struct{ slots chan struct{} }

func NewResourceLimiter(capacity int) *ResourceLimiter {
	if capacity < 1 {
		capacity = 1
	}
	return &ResourceLimiter{slots: make(chan struct{}, capacity)}
}

func (l *ResourceLimiter) Acquire(ctx context.Context) (func(), error) {
	if l == nil {
		return func() {}, nil
	}
	select {
	case l.slots <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-l.slots }) }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ProcessRunner runs a worker per request and kills its process group on
// cancellation. It is useful for one-shot workers and remains a public test
// seam for adapters.
type ProcessRunner struct{}

func (ProcessRunner) Run(ctx context.Context, binary string, input []byte, outputLimit int64) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, binary)
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
	cmd.Stdin = bytes.NewReader(input)
	stdout := &BoundedBuffer{Limit: outputLimit}
	stderr := &BoundedBuffer{Limit: outputLimit}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// PersistentRunner serializes requests on one newline-delimited worker.
type PersistentRunner struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *BoundedBuffer
}

func NewPersistentRunner() Runner { return &PersistentRunner{} }

func (r *PersistentRunner) Run(ctx context.Context, binary string, input []byte, outputLimit int64) ([]byte, []byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.ensure(binary, outputLimit); err != nil {
		return nil, nil, err
	}
	if len(input) == 0 || input[len(input)-1] != '\n' {
		input = append(input, '\n')
	}
	if _, err := r.stdin.Write(input); err != nil {
		r.reset()
		return nil, nil, fmt.Errorf("write persistent Rust request: %w", err)
	}
	stop := make(chan struct{})
	stdin, cmd := r.stdin, r.cmd
	go func() {
		select {
		case <-ctx.Done():
			_ = stdin.Close()
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		case <-stop:
		}
	}()
	line, err := r.stdout.ReadBytes('\n')
	close(stop)
	if err != nil {
		stderr := r.stderr.Bytes()
		r.reset()
		if ctx.Err() != nil {
			return nil, stderr, ctx.Err()
		}
		return line, stderr, fmt.Errorf("read persistent Rust response: %w", err)
	}
	if outputLimit > 0 && int64(len(line)) > outputLimit {
		stderr := r.stderr.Bytes()
		r.reset()
		return nil, stderr, fmt.Errorf("persistent Rust response exceeds output limit")
	}
	return line, r.stderr.Bytes(), nil
}

func (r *PersistentRunner) ensure(binary string, outputLimit int64) error {
	if r.cmd != nil {
		return nil
	}
	cmd := exec.Command(binary)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open persistent Rust stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("open persistent Rust stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("open persistent Rust stderr: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return fmt.Errorf("start persistent Rust: %w", err)
	}
	r.cmd, r.stdin, r.stdout, r.stderr = cmd, stdin, bufio.NewReader(stdout), &BoundedBuffer{Limit: outputLimit}
	go func() { _, _ = io.Copy(r.stderr, stderr) }()
	return nil
}

func (r *PersistentRunner) reset() {
	if r.cmd == nil {
		return
	}
	if r.stdin != nil {
		_ = r.stdin.Close()
	}
	if r.cmd.Process != nil {
		_ = syscall.Kill(-r.cmd.Process.Pid, syscall.SIGKILL)
	}
	_ = r.cmd.Wait()
	r.cmd, r.stdin, r.stdout, r.stderr = nil, nil, nil, nil
}

// Reset terminates the current worker, if any. It is intended for adapter
// shutdown and test cleanup; the next Run starts a fresh process.
func (r *PersistentRunner) Reset() { r.mu.Lock(); defer r.mu.Unlock(); r.reset() }

// BoundedBuffer retains only the tail of process output.
type BoundedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	Limit     int64
	truncated bool
}

func (b *BoundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.Limit <= 0 {
		return len(p), nil
	}
	if int64(len(p)) >= b.Limit {
		b.buf.Reset()
		_, _ = b.buf.Write(p[len(p)-int(b.Limit):])
		b.truncated = true
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	if int64(b.buf.Len()) > b.Limit {
		all := b.buf.Bytes()
		tail := append([]byte(nil), all[len(all)-int(b.Limit):]...)
		b.buf.Reset()
		_, _ = b.buf.Write(tail)
		b.truncated = true
	}
	return len(p), nil
}
func (b *BoundedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := append([]byte(nil), b.buf.Bytes()...)
	if !b.truncated || b.Limit <= 0 {
		return result
	}
	const marker = "[output truncated]"
	if int64(len(marker)) >= b.Limit {
		return []byte(marker[len(marker)-int(b.Limit):])
	}
	keep := int(b.Limit) - len(marker)
	if len(result) > keep {
		result = result[len(result)-keep:]
	}
	return append(result, marker...)
}
