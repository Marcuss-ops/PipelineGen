package render

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// countingProbe counts every underlying call and returns a fixed duration.
type countingProbe struct {
	mu       sync.Mutex
	calls    int
	duration float64
	err      error
}

func (c *countingProbe) ProbeDurationSec(_ context.Context, _ string) (float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	if c.err != nil {
		return 0, c.err
	}
	return c.duration, nil
}

func (c *countingProbe) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func writeStagedSource(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("staged-bytes"), 0o600); err != nil {
		t.Fatalf("write staged source: %v", err)
	}
	return path
}

func TestCachedSourceDurationProbeMeasuresOncePerIdentity(t *testing.T) {
	dir := t.TempDir()
	path := writeStagedSource(t, dir, "source.mp4")

	underlying := &countingProbe{duration: 36.5}
	probe := NewCachedSourceDurationProbe(underlying)

	for i := 0; i < 5; i++ {
		got, err := probe.ProbeDurationSec(context.Background(), path)
		if err != nil {
			t.Fatalf("probe %d: %v", i, err)
		}
		if got != 36.5 {
			t.Fatalf("probe %d duration = %v, want 36.5", i, got)
		}
	}
	if got := underlying.callCount(); got != 1 {
		t.Fatalf("underlying calls = %d, want 1 (identical file must be served from cache)", got)
	}
}

func TestCachedSourceDurationProbeRemasuresWhenFileChanges(t *testing.T) {
	dir := t.TempDir()
	path := writeStagedSource(t, dir, "source.mp4")

	underlying := &countingProbe{duration: 36.5}
	probe := NewCachedSourceDurationProbe(underlying)

	if _, err := probe.ProbeDurationSec(context.Background(), path); err != nil {
		t.Fatalf("first probe: %v", err)
	}

	// Same path, new bytes: size and mtime change → fresh measurement.
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if err := os.WriteFile(path, []byte("re-encoded-longer-bytes"), 0o600); err != nil {
		t.Fatalf("rewrite staged source: %v", err)
	}
	underlying.duration = 62.0

	got, err := probe.ProbeDurationSec(context.Background(), path)
	if err != nil {
		t.Fatalf("second probe: %v", err)
	}
	if got != 62.0 {
		t.Fatalf("duration after change = %v, want 62.0 (identity changed → remeasure)", got)
	}
	if got := underlying.callCount(); got != 2 {
		t.Fatalf("underlying calls = %d, want 2", got)
	}
}

func TestCachedSourceDurationProbeDoesNotCacheFailures(t *testing.T) {
	dir := t.TempDir()
	path := writeStagedSource(t, dir, "source.mp4")

	underlying := &countingProbe{err: errors.New("not a recognizable container")}
	probe := NewCachedSourceDurationProbe(underlying)

	if _, err := probe.ProbeDurationSec(context.Background(), path); err == nil {
		t.Fatal("first probe must surface the underlying error")
	}

	// The transient failure clears; the same identity must hit the probe
	// again instead of a cached error.
	underlying.err = nil
	underlying.duration = 10.0
	got, err := probe.ProbeDurationSec(context.Background(), path)
	if err != nil {
		t.Fatalf("retry probe: %v", err)
	}
	if got != 10.0 {
		t.Fatalf("retry duration = %v, want 10.0", got)
	}
	if got := underlying.callCount(); got != 2 {
		t.Fatalf("underlying calls = %d, want 2 (failures are never cached)", got)
	}
}

func TestCachedSourceDurationProbeBypassesCacheWhenStatFails(t *testing.T) {
	underlying := &countingProbe{duration: 5.0}
	probe := NewCachedSourceDurationProbe(underlying)

	// A stat failure (missing file) must not create a cache entry: every call
	// consults the underlying probe, and the result passes through unchanged.
	missing := filepath.Join(t.TempDir(), "does-not-exist.mp4")
	for i := 0; i < 3; i++ {
		got, err := probe.ProbeDurationSec(context.Background(), missing)
		if err != nil {
			t.Fatalf("probe %d: %v", i, err)
		}
		if got != 5.0 {
			t.Fatalf("probe %d duration = %v, want 5.0 passthrough", i, got)
		}
	}
	if got := underlying.callCount(); got != 3 {
		t.Fatalf("underlying calls = %d, want 3 (unknown identity always bypasses the cache)", got)
	}
}

func TestCachedSourceDurationProbeConcurrentAccessIsSafe(t *testing.T) {
	dir := t.TempDir()
	path := writeStagedSource(t, dir, "source.mp4")

	underlying := &countingProbe{duration: 42.0}
	probe := NewCachedSourceDurationProbe(underlying)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got, err := probe.ProbeDurationSec(context.Background(), path); err != nil || got != 42.0 {
				t.Errorf("concurrent probe: duration=%v err=%v", got, err)
			}
		}()
	}
	wg.Wait()
	if got := underlying.callCount(); got != 1 {
		t.Fatalf("underlying calls = %d, want exactly 1", got)
	}
}

type blockingDurationProbe struct {
	started chan string
	release chan struct{}
}

func (p *blockingDurationProbe) ProbeDurationSec(ctx context.Context, path string) (float64, error) {
	p.started <- path
	select {
	case <-p.release:
		return 42, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func waitForProbeWaiters(t *testing.T, probe *CachedSourceDurationProbe, path string, count int) {
	t.Helper()
	identity, _ := probeFileIdentity(path)
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		probe.mu.Lock()
		flight := probe.flights[identity]
		ready := flight != nil && flight.waiters == count
		probe.mu.Unlock()
		if ready {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("did not observe %d waiters", count)
		case <-ticker.C:
		}
	}
}

func TestCachedSourceDurationProbeCancellationIsPerCaller(t *testing.T) {
	path := writeStagedSource(t, t.TempDir(), "source.mp4")
	underlying := &blockingDurationProbe{started: make(chan string, 16), release: make(chan struct{})}
	probe := NewCachedSourceDurationProbe(underlying)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := probe.ProbeDurationSec(ctx, path); first <- err }()
	<-underlying.started
	second := make(chan error, 1)
	go func() {
		got, err := probe.ProbeDurationSec(context.Background(), path)
		if err == nil && got != 42 {
			err = errors.New("incorrect duration")
		}
		second <- err
	}()
	waitForProbeWaiters(t, probe, path, 2)
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("first caller: %v", err)
	}
	close(underlying.release)
	if err := <-second; err != nil {
		t.Fatalf("one caller cancelled the shared probe: %v", err)
	}
	if len(underlying.started) != 0 {
		t.Fatal("same identity launched more than one probe")
	}
}

func TestCachedSourceDurationProbeLastCancellationAllowsRetry(t *testing.T) {
	path := writeStagedSource(t, t.TempDir(), "source.mp4")
	underlying := &blockingDurationProbe{started: make(chan string, 16), release: make(chan struct{})}
	probe := NewCachedSourceDurationProbe(underlying)
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := probe.ProbeDurationSec(ctx, path); first <- err }()
	<-underlying.started
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled caller: %v", err)
	}
	second := make(chan error, 1)
	go func() { _, err := probe.ProbeDurationSec(context.Background(), path); second <- err }()
	select {
	case <-underlying.started:
	case <-time.After(2 * time.Second):
		t.Fatal("retry joined an orphaned flight")
	}
	close(underlying.release)
	if err := <-second; err != nil {
		t.Fatalf("fresh retry: %v", err)
	}
}

func TestCachedSourceDurationProbeDifferentFilesRunInParallel(t *testing.T) {
	dir := t.TempDir()
	paths := []string{writeStagedSource(t, dir, "a.mp4"), writeStagedSource(t, dir, "b.mp4")}
	underlying := &blockingDurationProbe{started: make(chan string, 16), release: make(chan struct{})}
	probe := NewCachedSourceDurationProbe(underlying)
	var wg sync.WaitGroup
	for _, path := range paths {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			if _, err := probe.ProbeDurationSec(context.Background(), path); err != nil {
				t.Errorf("probe: %v", err)
			}
		}(path)
	}
	for range paths {
		select {
		case <-underlying.started:
		case <-time.After(2 * time.Second):
			close(underlying.release)
			wg.Wait()
			t.Fatal("unrelated file probes were serialized")
		}
	}
	close(underlying.release)
	wg.Wait()
}

func TestCachedSourceDurationProbeDoesNotMemoizeChangedFile(t *testing.T) {
	path := writeStagedSource(t, t.TempDir(), "source.mp4")
	underlying := &blockingDurationProbe{started: make(chan string, 16), release: make(chan struct{})}
	probe := NewCachedSourceDurationProbe(underlying)
	done := make(chan error, 1)
	go func() { _, err := probe.ProbeDurationSec(context.Background(), path); done <- err }()
	<-underlying.started
	if err := os.WriteFile(path, []byte("different-sized-source-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	close(underlying.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	probe.mu.Lock()
	entries := len(probe.cache)
	probe.mu.Unlock()
	if entries != 0 {
		t.Fatalf("cached %d results measured across a file mutation", entries)
	}
}

func TestCachedSourceDurationProbeCancelledContextDoesNoWork(t *testing.T) {
	path := writeStagedSource(t, t.TempDir(), "source.mp4")
	underlying := &countingProbe{duration: 42}
	probe := NewCachedSourceDurationProbe(underlying)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := probe.ProbeDurationSec(ctx, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request: %v", err)
	}
	if underlying.callCount() != 0 {
		t.Fatal("cancelled request launched work")
	}
}

func BenchmarkDurationProbeFanout(b *testing.B) {
	for _, cached := range []bool{false, true} {
		name := "uncached"
		if cached {
			name = "cached"
		}
		b.Run(name, func(b *testing.B) {
			path := filepath.Join(b.TempDir(), "source.mp4")
			if err := os.WriteFile(path, []byte("staged-bytes"), 0o600); err != nil {
				b.Fatal(err)
			}
			underlying := &countingProbe{duration: 42}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var probe interface {
					ProbeDurationSec(context.Context, string) (float64, error)
				} = underlying
				if cached {
					probe = NewCachedSourceDurationProbe(underlying)
				}
				var wg sync.WaitGroup
				for j := 0; j < 16; j++ {
					wg.Add(1)
					go func() { defer wg.Done(); _, _ = probe.ProbeDurationSec(context.Background(), path) }()
				}
				wg.Wait()
			}
			b.ReportMetric(float64(underlying.callCount())/float64(b.N), "probes/op")
		})
	}
}
