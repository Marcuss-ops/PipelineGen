package chronon

import (
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultChrononGPUConcurrency = 4
	defaultChrononProbeTTL       = 10 * time.Minute
)

var (
	chrononRuntimeControlInit sync.Once
	chrononGPUAdmission       chan struct{}
	chrononGPUConcurrency     int
	chrononProbeMu            sync.Mutex
	chrononProbeCache         = map[chrononProbeKey]chrononProbeEntry{}
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
