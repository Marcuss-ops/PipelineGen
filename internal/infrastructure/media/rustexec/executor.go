package rustexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
)

// ProcessRunner owns the lifecycle of one Rust executor process. It is kept as
// a port so tests can exercise Executor without depending on a real binary.
type ProcessRunner interface {
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
	runner      ProcessRunner
	limiter     *ResourceLimiter
	outputLimit int64
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
		runner:      execProcessRunner{},
		limiter:     NewResourceLimiter(slots),
		outputLimit: defaultRustOutputLimit,
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

	stdout, stderr, runErr := e.runner.Run(ctx, e.binaryPath, input, e.outputLimit)
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

// execProcessRunner runs the Rust binary in its own process group. Both direct
// cancellation and timeout kill the group, preventing descendant FFmpeg
// processes from surviving the Rust adapter.
type execProcessRunner struct{}

func (execProcessRunner) Run(ctx context.Context, binary string, input []byte, outputLimit int64) ([]byte, []byte, error) {
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
	remaining := b.limit - int64(b.buf.Len())
	if remaining > 0 {
		if int64(len(p)) > remaining {
			_, _ = b.buf.Write(p[:remaining])
			b.truncated = true
		} else {
			_, _ = b.buf.Write(p)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

func (b *boundedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := append([]byte(nil), b.buf.Bytes()...)
	if b.truncated {
		result = append(result, []byte("\n[output truncated]")...)
	}
	return result
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

// Keep time imported in this file's API documentation and make cancellation
// behavior explicit in profiling without exposing process internals.
var _ = time.Second
