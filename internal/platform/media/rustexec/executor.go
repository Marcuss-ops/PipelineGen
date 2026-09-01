package rustexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/rustworker"
	"go.uber.org/zap"
)

const (
	defaultRustExecutionSlots = 2
	maxRustExecutionSlots     = 4
	mediaExecutionSlotsEnv    = "VELOX_MEDIA_EXECUTION_SLOTS"
	defaultRustOutputLimit    = 64 * 1024
	defaultRustTimeout        = 10 * time.Minute
)

// RustProcessRunner owns the lifecycle of one Rust executor process. It is
// kept as a port so tests can exercise Executor without depending on a real
// binary.
type RustProcessRunner = rustworker.Runner
type ResourceLimiter = rustworker.ResourceLimiter

func NewResourceLimiter(capacity int) *ResourceLimiter {
	return rustworker.NewResourceLimiter(capacity)
}

// Executor is the shared owner of the Rust process runner, cancellation
// policy, output bounds, and execution slots. Adapters should share one
// instance when they belong to the same composition root.
type Executor struct {
	binaryPath  string
	ffmpegPath  string
	log         *zap.Logger
	runner      RustProcessRunner
	runnerPool  chan RustProcessRunner
	limiter     *ResourceLimiter
	outputLimit int64
	timeout     time.Duration
}

func NewExecutor(binaryPath, ffmpegPath string, log *zap.Logger) *Executor {
	return NewExecutorWithLimit(binaryPath, ffmpegPath, configuredExecutionSlots(), log)
}

// configuredExecutionSlots controls bounded media concurrency without making
// the composition root depend on a GPU-specific config type. The ceiling is
// deliberate: more than four simultaneous NVENC graphs generally increases
// contention instead of throughput on the supported single-GPU workers.
func configuredExecutionSlots() int {
	slots := defaultRustExecutionSlots
	if raw := strings.TrimSpace(os.Getenv(mediaExecutionSlotsEnv)); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil {
			slots = parsed
		}
	}
	if slots < 1 {
		return 1
	}
	if slots > maxRustExecutionSlots {
		return maxRustExecutionSlots
	}
	return slots
}

func NewExecutorWithLimit(binaryPath, ffmpegPath string, slots int, log *zap.Logger) *Executor {
	if log == nil {
		log = zap.NewNop()
	}
	if ffmpegPath == "" {
		ffmpegPath = "ffmpeg"
	}
	primary := newPersistentRustProcessRunner()
	executor := &Executor{
		binaryPath:  binaryPath,
		ffmpegPath:  ffmpegPath,
		log:         log,
		runner:      primary,
		limiter:     NewResourceLimiter(slots),
		outputLimit: defaultRustOutputLimit,
		timeout:     defaultRustTimeout,
	}
	if slots > 1 {
		executor.runnerPool = make(chan RustProcessRunner, slots)
		executor.runnerPool <- primary
		for i := 1; i < slots; i++ {
			executor.runnerPool <- newPersistentRustProcessRunner()
		}
	}
	return executor
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
	runner := e.runner
	if e.runnerPool != nil {
		select {
		case runner = <-e.runnerPool:
			defer func() { e.runnerPool <- runner }()
		case <-runCtx.Done():
			return nil, nil, fmt.Errorf("acquire Rust process runner: %w", runCtx.Err())
		}
	}
	stdout, stderr, runErr := runner.Run(runCtx, e.binaryPath, input, e.outputLimit)
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

type rustProcessRunner = rustworker.ProcessRunner

// persistentRustProcessRunner keeps the newline-delimited Rust dispatcher
// alive across requests. Requests are serialized per runner because run_stdio
// has one request/response stream; Executor's bounded runner pool provides
// cross-request concurrency without spawning a Rust process for every job.
//
// FFmpeg itself remains one process per render. Keeping it alive through a raw
// frame pipe would force host-memory frames and defeat NVDEC/CUDA zero-copy;
// the safe optimization here is persistent Rust dispatch plus bounded CUDA
// concurrency, while a future native libavcodec path can reuse AVCodecContext
// directly without changing this contract.
type persistentRustProcessRunner struct{ inner RustProcessRunner }

func (r *persistentRustProcessRunner) Run(ctx context.Context, binary string, input []byte, outputLimit int64) ([]byte, []byte, error) {
	if r.inner == nil {
		r.inner = rustworker.NewPersistentRunner()
	}
	return r.inner.Run(ctx, binary, input, outputLimit)
}

func (r *persistentRustProcessRunner) reset() {
	if runner, ok := r.inner.(*rustworker.PersistentRunner); ok {
		runner.Reset()
	}
}

func newPersistentRustProcessRunner() RustProcessRunner {
	return &persistentRustProcessRunner{}
}

// boundedBuffer remains a small compatibility seam for package-local tests;
// production process lifecycle and output handling live in rustworker.
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
		tail := append([]byte(nil), all[len(all)-int(b.limit):]...)
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
	return append(result, marker...)
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
