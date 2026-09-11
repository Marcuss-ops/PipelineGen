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
	"sync/atomic"
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
	finished := atomic.Bool{}
	watcherDone := make(chan struct{})
	stdin, cmd := r.stdin, r.cmd
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			// A successful response may race with context cancellation at the
			// scene boundary. Once ReadBytes has completed, never tear down the
			// worker underneath the next request.
			if finished.Load() {
				return
			}
			_ = stdin.Close()
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		case <-stop:
		}
	}()
	line, err := r.stdout.ReadBytes('\n')
	if err == nil {
		finished.Store(true)
	}
	close(stop)
	<-watcherDone
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
	go func() {
		if _, copyErr := io.Copy(r.stderr, stderr); copyErr != nil {
			// The stderr pump must not be a silent black hole: a copy failure
			// means worker diagnostics are being lost exactly when the worker
			// is likely misbehaving. Surface it into the bounded stderr tail
			// itself so the next Run error report carries the fact.
			const marker = "[rustworker stderr copy failed]"
			_, _ = r.stderr.Write([]byte(marker))
		}
	}()
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
//
// Tail retention mirrors the Rust process.rs read_tail contract (amortized
// bulk trim): the buffer is compacted only when it exceeds Limit by more
// than one chunk (boundedBufferChunk), so a chatty stderr stream pays one
// O(Limit) in-place shift per chunk of overflow instead of a full copy +
// rewrite on every write. The final Bytes() pins the retained tail to
// exactly Limit bytes.
type BoundedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	Limit     int64
	truncated bool
}

// boundedBufferChunk is the compaction quantum: the buffer may grow up to
// Limit + boundedBufferChunk before the next amortized trim.
const boundedBufferChunk = 8 * 1024

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
	if int64(b.buf.Len()) > b.Limit+boundedBufferChunk {
		b.discardFront(int64(b.buf.Len()) - b.Limit)
		b.truncated = true
	}
	return len(p), nil
}

// discardFront drops the oldest n bytes in place. The buffer is only ever
// written (never read) on this path, so the read offset is always zero and
// the shift is a single overlapping memmove — no allocation, no rewrite.
func (b *BoundedBuffer) discardFront(n int64) {
	all := b.buf.Bytes()
	if int64(len(all)) <= n {
		b.buf.Reset()
		return
	}
	copy(all, all[n:])
	b.buf.Truncate(len(all) - int(n))
}

func (b *BoundedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Pin the retained tail to exactly Limit before reading (the write path
	// only compacts per chunk).
	if b.Limit > 0 && int64(b.buf.Len()) > b.Limit {
		b.discardFront(int64(b.buf.Len()) - b.Limit)
		b.truncated = true
	}
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
