package wiring

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	defaultChrononGPUConcurrency = 2
	defaultChrononProbeTTL        = 10 * time.Minute
	chrononProcessTailBytes       = 64 * 1024
)

var (
	chrononGPUAdmission   chan struct{}
	chrononGPUConcurrency int
	chrononProbeMu        sync.Mutex
	chrononProbeCache     = map[chrononProbeKey]chrononProbeEntry{}
)

type chrononProbeKey struct {
	Path    string
	Size    int64
	ModUnix int64
}

type chrononProbeEntry struct {
	DurationMS int64
	ExpiresAt  time.Time
}

type chrononProcessOutput struct {
	Tail       []byte
	TotalBytes int64
}

// initChrononRuntimeControl owns the process-wide GPU admission policy for the
// single canonical Chronon adapter. It replaces the old global mutex with a
// bounded semaphore: multiple renders can execute concurrently, while the
// device is still protected from unbounded CLI fan-out. The default of two is
// intentionally conservative and can be tuned without changing WORKER_SLOTS.
func initChrononRuntimeControl() {
	chrononRuntimeControlInit.Do(func() {
		chrononGPUConcurrency = envPositiveInt("CHRONON_GPU_CONCURRENCY", defaultChrononGPUConcurrency)
		chrononGPUAdmission = make(chan struct{}, chrononGPUConcurrency)
	})
}

func envPositiveInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func acquireChrononGPU(ctx context.Context) (time.Duration, func(), error) {
	initChrononRuntimeControl()
	started := time.Now()
	select {
	case chrononGPUAdmission <- struct{}{}:
		wait := time.Since(started)
		var once sync.Once
		return wait, func() {
			once.Do(func() { <-chrononGPUAdmission })
		}, nil
	case <-ctx.Done():
		return time.Since(started), func() {}, ctx.Err()
	}
}

func currentChrononGPUConcurrency() int {
	initChrononRuntimeControl()
	return chrononGPUConcurrency
}

// chrononProbeLookup/Store is a file-identity cache, not a path-only cache.
// Size + mtime are part of the key so a replaced source cannot inherit stale
// duration metadata. The cache is intentionally small and TTL bounded.
func chrononProbeLookup(path string) (int64, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return 0, false
	}
	key := chrononProbeKey{Path: path, Size: info.Size(), ModUnix: info.ModTime().UnixNano()}
	now := time.Now()
	chrononProbeMu.Lock()
	defer chrononProbeMu.Unlock()
	entry, ok := chrononProbeCache[key]
	if !ok || now.After(entry.ExpiresAt) {
		if ok {
			delete(chrononProbeCache, key)
		}
		return 0, false
	}
	return entry.DurationMS, true
}

func chrononProbeStore(path string, durationMS int64) {
	if durationMS <= 0 {
		return
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return
	}
	key := chrononProbeKey{Path: path, Size: info.Size(), ModUnix: info.ModTime().UnixNano()}
	ttlSeconds := envPositiveInt("CHRONON_PROBE_CACHE_TTL_SECONDS", int(defaultChrononProbeTTL/time.Second))
	chrononProbeMu.Lock()
	defer chrononProbeMu.Unlock()
	// This is a hot metadata cache, not a catalog. Bound growth aggressively;
	// stale entries are cheap to regenerate with ffprobe.
	if len(chrononProbeCache) >= 1024 {
		chrononProbeCache = map[chrononProbeKey]chrononProbeEntry{}
	}
	chrononProbeCache[key] = chrononProbeEntry{
		DurationMS: durationMS,
		ExpiresAt:  time.Now().Add(time.Duration(ttlSeconds) * time.Second),
	}
}

// runChrononCommandStreaming drains stdout and stderr concurrently while the
// process is running. Output is written directly to logPath; only a bounded
// tail is kept in memory for diagnostics. JSON event lines are surfaced as
// structured debug logs when Chronon emits them, without making event parsing
// part of the correctness path.
func runChrononCommandStreaming(cmd *exec.Cmd, logPath, runID string, log *zap.Logger) (chrononProcessOutput, error) {
	if cmd == nil {
		return chrononProcessOutput{}, fmt.Errorf("chronon: nil command")
	}
	if log == nil {
		log = zap.NewNop()
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return chrononProcessOutput{}, fmt.Errorf("chronon stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return chrononProcessOutput{}, fmt.Errorf("chronon stderr pipe: %w", err)
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return chrononProcessOutput{}, fmt.Errorf("chronon log open: %w", err)
	}
	defer file.Close()

	if err := cmd.Start(); err != nil {
		return chrononProcessOutput{}, err
	}

	var mu sync.Mutex
	var tail []byte
	var total int64
	consume := func(stream string, r io.Reader) {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			line = append(line, '\n')
			mu.Lock()
			_, _ = file.Write(line)
			total += int64(len(line))
			tail = append(tail, line...)
			if len(tail) > chrononProcessTailBytes {
				tail = append([]byte(nil), tail[len(tail)-chrononProcessTailBytes:]...)
			}
			mu.Unlock()

			var event map[string]any
			if json.Unmarshal(bytesTrimSpace(line), &event) == nil {
				log.Debug("clip.render.chronon.event",
					zap.String("run_id", runID),
					zap.String("stream", stream),
					zap.Any("event", event),
				)
			}
		}
	}

	var readers sync.WaitGroup
	readers.Add(2)
	go func() { defer readers.Done(); consume("stdout", stdout) }()
	go func() { defer readers.Done(); consume("stderr", stderr) }()
	// StdoutPipe/StderrPipe require the consumer to drain both streams before
	// Wait closes their descriptors. The child may exit while the goroutines
	// are still reading; EOF releases the readers, then Wait reaps the process.
	readers.Wait()
	waitErr := cmd.Wait()

	mu.Lock()
	result := chrononProcessOutput{Tail: append([]byte(nil), tail...), TotalBytes: total}
	mu.Unlock()
	return result, waitErr
}

func bytesTrimSpace(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && (b[start] == ' ' || b[start] == '\t' || b[start] == '\n' || b[start] == '\r') {
		start++
	}
	for end > start && (b[end-1] == ' ' || b[end-1] == '\t' || b[end-1] == '\n' || b[end-1] == '\r') {
		end--
	}
	return b[start:end]
}
