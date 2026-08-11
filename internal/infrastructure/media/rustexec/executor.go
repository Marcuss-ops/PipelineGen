package rustexec

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"go.uber.org/zap"
)

const (
	defaultRustExecutionSlots = 4
	defaultRustOutputLimit    = 64 * 1024
	defaultRustTimeout        = 10 * time.Minute
)

// RustProcessRunner owns the lifecycle of one Rust executor process. It is
// kept as a port so tests can exercise Executor without depending on a real
// binary.
type RustProcessRunner interface {
	Run(ctx context.Context, binary string, input []byte, outputLimit int64) ([]byte, []byte, error)
}

// ResourceLimiter bounds concurrent Rust/FFmpeg executions owned by one
// composition-root Executor. Waiting is cancellation-aware.
type ResourceLimiter struct {
	slots chan struct{}
}

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

// Executor is the shared owner of the Rust process runner, cancellation
// policy, output bounds, and execution slots. Adapters should share one
// instance when they belong to the same composition root.
type Executor struct {
	binaryPath  string
	ffmpegPath  string
	log         *zap.Logger
	runner      RustProcessRunner
	limiter     *ResourceLimiter
	outputLimit int64
	timeout     time.Duration
}

func NewExecutor(binaryPath, ffmpegPath string, log *zap.Logger) *Executor {
	return NewExecutorWithLimit(binaryPath, ffmpegPath, defaultRustExecutionSlots, log)
}

func NewExecutorWithLimit(binaryPath, ffmpegPath string, slots int, log *zap.Logger) *Executor {
	if log == nil {
		log = zap.NewNop()
	}
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	return &Executor{
		binaryPath:  binaryPath,
		ffmpegPath:  ffmpegPath,
		log:         log,
		runner:      newPersistentRustProcessRunner(),
		limiter:     NewResourceLimiter(slots),
		outputLimit: defaultRustOutputLimit,
		timeout:     defaultRustTimeout,
	}
}

// Run executes one line-delimited JSON request and removes deterministic .part
// outputs if the Rust process fails or is cancelled. The cleanup is performed
// by Go because SIGKILL cannot run Rust destructors.
func (e *Executor) Run(ctx context.Context, input []byte) ([]byte, []byte, error) {
	if e == nil {
		return nil, nil, fmt.Errorf("rust media executor is nil")
	}
	release, err := e.limiter.Acquire(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("acquire rust media execution slot: %w", err)
	}
	defer release()

	runCtx := ctx
	cancel := func() {}
	if e.timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, e.timeout)
	}
	defer cancel()
	stdout, stderr, runErr := e.runner.Run(runCtx, e.binaryPath, input, e.outputLimit)
	if runErr != nil {
		cleanupPartFiles(input)
		return stdout, stderr, fmt.Errorf("rust media executor: %w: %s", runErr, stderr)
	}
	return stdout, stderr, nil
}

// FFmpegPath is the resolved ffmpeg executable forwarded by Client.
func (e *Executor) FFmpegPath() string {
	if e == nil || e.ffmpegPath == "" {
		return "ffmpeg"
	}
	return e.ffmpegPath
}

// rustProcessRunner runs the Rust binary in its own process group. Both direct
// cancellation and timeout kill the group, preventing descendant FFmpeg
// processes from surviving the Rust adapter.
type rustProcessRunner struct{}

func (rustProcessRunner) Run(ctx context.Context, binary string, input []byte, outputLimit int64) ([]byte, []byte, error) {
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
	stdout := &boundedBuffer{limit: outputLimit}
	stderr := &boundedBuffer{limit: outputLimit}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if err != nil {
		return stdout.Bytes(), stderr.Bytes(), err
	}
	return stdout.Bytes(), stderr.Bytes(), nil
}

// persistentRustProcessRunner keeps the newline-delimited Rust dispatcher
// alive across requests. Requests are serialized because run_stdio currently
// has one request/response stream; the surrounding limiter still controls how
// many media jobs may execute concurrently in the composition root.
type persistentRustProcessRunner struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	stderr *boundedBuffer
}

func newPersistentRustProcessRunner() RustProcessRunner {
	return &persistentRustProcessRunner{}
}

func (r *persistentRustProcessRunner) Run(ctx context.Context, binary string, input []byte, outputLimit int64) ([]byte, []byte, error) {
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

	cancelled := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			if r.stdin != nil {
				_ = r.stdin.Close()
			}
			if r.cmd != nil && r.cmd.Process != nil {
				_ = syscall.Kill(-r.cmd.Process.Pid, syscall.SIGKILL)
			}
		case <-cancelled:
		}
	}()
	line, err := r.stdout.ReadBytes('\n')
	close(cancelled)
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

func (r *persistentRustProcessRunner) ensure(binary string, outputLimit int64) error {
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
	r.cmd = cmd
	r.stdin = stdin
	r.stdout = bufio.NewReader(stdout)
	r.stderr = &boundedBuffer{limit: outputLimit}
	go func() {
		_, _ = io.Copy(r.stderr, stderr)
	}()
	return nil
}

func (r *persistentRustProcessRunner) reset() {
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
	r.cmd = nil
	r.stdin = nil
	r.stdout = nil
	r.stderr = nil
}

type boundedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int64
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.limit <= 0 {
		return len(p), nil
	}
	if int64(len(p)) >= b.limit {
		b.buf.Reset()
		_, _ = b.buf.Write(p[len(p)-int(b.limit):])
		b.truncated = true
		return len(p), nil
	}
	_, _ = b.buf.Write(p)
	if int64(b.buf.Len()) > b.limit {
		all := b.buf.Bytes()
		start := len(all) - int(b.limit)
		tail := append([]byte(nil), all[start:]...)
		b.buf.Reset()
		_, _ = b.buf.Write(tail)
		b.truncated = true
	}
	return len(p), nil
}

func (b *boundedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := append([]byte(nil), b.buf.Bytes()...)
	if !b.truncated || b.limit <= 0 {
		return result
	}
	const marker = "[output truncated]"
	if int64(len(marker)) >= b.limit {
		return []byte(marker[len(marker)-int(b.limit):])
	}
	keep := int(b.limit) - len(marker)
	if len(result) > keep {
		result = result[len(result)-keep:]
	}
	result = append(result, marker...)
	return result
}

func cleanupPartFilesForRequest(req request) {
	paths := make([]string, 0, len(req.Jobs)+1)
	if req.OutputPath != "" {
		paths = append(paths, req.OutputPath)
	}
	for _, job := range req.Jobs {
		if job.OutputPath != "" {
			paths = append(paths, job.OutputPath)
		}
	}
	for _, path := range paths {
		_ = os.Remove(partPathForCleanup(path))
	}
}

func cleanupPartFiles(input []byte) {
	var req request
	if json.Unmarshal(bytes.TrimSpace(input), &req) != nil {
		return
	}
	paths := make([]string, 0, len(req.Jobs)+1)
	if req.OutputPath != "" {
		paths = append(paths, req.OutputPath)
	}
	for _, job := range req.Jobs {
		if job.OutputPath != "" {
			paths = append(paths, job.OutputPath)
		}
	}
	for _, path := range paths {
		_ = os.Remove(partPathForCleanup(path))
	}
}

func partPathForCleanup(finalPath string) string {
	path := filepath.Clean(finalPath)
	ext := filepath.Ext(path)
	if ext == "" {
		return finalPath + ".part"
	}
	stem := strings.TrimSuffix(filepath.Base(path), ext)
	return filepath.Join(filepath.Dir(path), stem+".part"+ext)
}
