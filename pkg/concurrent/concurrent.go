// Package concurrent provides goroutine-safe concurrency primitives:
// errgroup-style parallel execution with cancellation, bounded map/reduce,
// and panic-safe fire-and-forget goroutines.
package concurrent

import (
	"context"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
)

// ── Recovered-panic reporting ──────────────────────────────────────────
//
// A recovered goroutine panic is the most severe anomaly a fire-and-forget
// path can hide: the caller never sees it, no error propagates, and the
// work is simply lost. The sink below defaults to the stdlib logger so a
// bare pkg/ consumer still keeps SOME signal, but the count is always
// maintained and composition roots that own a structured logger MUST
// install theirs via SetPanicReporter so the anomaly reaches zap/metrics
// instead of degrading to an unstructured stderr line.

// PanicReporter receives every recovered goroutine panic. goroutine is the
// caller-supplied name (or a synthesized worker name); recovered is the
// panic value.
//
// Implementations MUST be safe for concurrent use.
type PanicReporter func(goroutine string, recovered any)

var (
	panicReporter   atomic.Pointer[PanicReporter]
	panicsRecovered atomic.Int64
)

// SetPanicReporter installs the canonical structured sink for recovered
// goroutine panics. Passing nil restores the default stdlib logger. The
// recovered-panic counter is maintained independently and is unaffected.
func SetPanicReporter(reporter PanicReporter) {
	if reporter == nil {
		panicReporter.Store(nil)
		return
	}
	panicReporter.Store(&reporter)
}

// PanicsRecovered returns the process-wide count of recovered goroutine
// panics since start. It is exported so a metrics exporter can surface the
// anomaly even when no reporter is installed.
func PanicsRecovered() int64 { return panicsRecovered.Load() }

// reportPanic is the SINGLE sink for every recovered panic in this package.
func reportPanic(goroutine string, recovered any) {
	panicsRecovered.Add(1)
	if reporter := panicReporter.Load(); reporter != nil {
		(*reporter)(goroutine, recovered)
		return
	}
	log.Printf("[panic recovery] goroutine %q panicked: %v", goroutine, recovered)
}

// ── SafeGo — fire-and-forget goroutines with panic recovery ─────────────

// SafeGo runs fn in a new goroutine with panic recovery.
// If fn panics, the panic is recovered and reported with the goroutine name.
func SafeGo(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				reportPanic(name, r)
			}
		}()
		fn()
	}()
}

// SafeGoFunc runs fn(arg) in a new goroutine with panic recovery.
func SafeGoFunc[T any](name string, arg T, fn func(T)) {
	go func(a T) {
		defer func() {
			if r := recover(); r != nil {
				reportPanic(name, r)
			}
		}()
		fn(a)
	}(arg)
}

// ── Group — errgroup-style parallel execution ──────────────────────────

// Group is an errgroup-style helper for running a fixed set of goroutines
// with first-error-wins cancellation and panic recovery.
//
// Behaviour:
//   - Each Go() runs in a new goroutine.
//   - The FIRST non-nil error wins; subsequent errors are logged but
//     not returned.
//   - The context returned by WithContext is cancelled as soon as one
//     goroutine fails OR the parent context is cancelled.
//   - Wait() returns the first error (or nil if all succeeded).
//   - A panicking goroutine is recovered and surfaced as an error.
type Group struct {
	wg     sync.WaitGroup
	cancel context.CancelFunc
	err    error
	errMu  sync.Mutex
}

// WithContext returns a new Group and a child context derived from parent.
// The child context is cancelled as soon as any goroutine returns a non-nil
// error, or the parent context is cancelled.
func WithContext(parent context.Context) (*Group, context.Context) {
	ctx, cancel := context.WithCancel(parent)
	return &Group{cancel: cancel}, ctx
}

// Go runs fn in a new goroutine. Panics are recovered and converted to
// errors so they participate in the first-error-wins policy.
func (g *Group) Go(name string, fn func() error) {
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				reportPanic(name, r)
				g.recordError(&errPanic{name: name, val: r})
			}
		}()
		if err := fn(); err != nil {
			g.recordError(err)
		}
	}()
}

func (g *Group) recordError(err error) {
	g.errMu.Lock()
	first := g.err == nil
	if first {
		g.err = err
	}
	g.errMu.Unlock()
	if first && g.cancel != nil {
		g.cancel()
	}
}

// Wait blocks until all goroutines have returned and then returns the first
// non-nil error, if any.
func (g *Group) Wait() error {
	g.wg.Wait()
	g.errMu.Lock()
	defer g.errMu.Unlock()
	return g.err
}

type errPanic struct {
	name string
	val  any
}

func (e *errPanic) Error() string {
	return "goroutine " + e.name + " panicked: " + asString(e.val)
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case error:
		return x.Error()
	default:
		return fmt.Sprintf("%v", x)
	}
}

// ── Map — bounded parallel map/reduce with worker pool ────────────────

// Map executes fn for each item using a bounded worker pool of at most
// workers goroutines. Results are returned in item order. On first error,
// the context is cancelled and remaining work items are skipped.
// The fn callback runs inside a panic-recovery wrapper.
func Map[T, R any](
	ctx context.Context,
	items []T,
	workers int,
	fn func(context.Context, int, T) (R, error),
) ([]R, error) {
	if len(items) == 0 {
		return []R{}, nil
	}
	if workers <= 0 {
		workers = 1
	}
	if workers > len(items) {
		workers = len(items)
	}

	type result struct {
		idx int
		val R
		err error
	}
	type job struct {
		idx  int
		item T
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobsCh := make(chan job, len(items))
	results := make([]result, len(items))

	var (
		wg       sync.WaitGroup
		firstErr error
		errMu    sync.Mutex
	)

	// Bounded worker pool: exactly `workers` goroutines consume from the
	// jobs channel. No goroutine-per-item explosion.
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobsCh {
				select {
				case <-ctx.Done():
					return
				default:
				}

				// Panic recovery per work item.
				var (
					res R
					err error
				)
				func() {
					workerName := fmt.Sprintf("map-worker-%d", j.idx)
					defer func() {
						if r := recover(); r != nil {
							reportPanic(workerName, r)
							err = &errPanic{
								name: workerName,
								val:  r,
							}
						}
					}()
					res, err = fn(ctx, j.idx, j.item)
				}()

				results[j.idx] = result{idx: j.idx, val: res, err: err}

				if err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
						cancel()
					}
					errMu.Unlock()
				}
			}
		}()
	}

	// Enqueue all jobs.
loop:
	for i, item := range items {
		select {
		case jobsCh <- job{idx: i, item: item}:
		case <-ctx.Done():
			break loop
		}
	}
	close(jobsCh)
	wg.Wait()

	if firstErr != nil {
		return nil, firstErr
	}

	out := make([]R, len(items))
	for _, r := range results {
		out[r.idx] = r.val
	}
	return out, nil
}

// ParallelMap runs fn for each item with bounded concurrency. Results keep
// item order (results[idx] belongs to items[idx]). Prefer Map for new code
// when context propagation and error returns are needed.
//
// Concurrency is enforced by a fixed worker pool, not by a semaphore in front
// of a goroutine-per-item spawn: the previous form created one goroutine (and
// stack) per item regardless of the `concurrency` argument, so a large scene
// or query slice paid for N schedulable goroutines while only `concurrency`
// units of work ran. `concurrency <= 0` is clamped to 1 instead of producing a
// zero-capacity semaphore that suspends every worker forever.
//
// Each item is isolated so a panicking fn cannot take down its worker (and
// with it the process): the panic is reported through the package panic sink
// and the item keeps its zero value.
func ParallelMap[T, R any](
	items []T,
	concurrency int,
	fn func(int, T) R,
) []R {
	if len(items) == 0 {
		return []R{}
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > len(items) {
		concurrency = len(items)
	}

	results := make([]R, len(items))
	indexes := make(chan int, len(items))
	for i := range items {
		indexes <- i
	}
	close(indexes)

	var wg sync.WaitGroup
	wg.Add(concurrency)
	for range concurrency {
		go func() {
			defer wg.Done()
			for idx := range indexes {
				runParallelMapItem(results, idx, items[idx], fn)
			}
		}()
	}
	wg.Wait()
	return results
}

// runParallelMapItem executes one ParallelMap item with panic isolation. Each
// index is written by exactly one worker, so distinct slice elements are not
// concurrently mutated.
func runParallelMapItem[T, R any](results []R, idx int, item T, fn func(int, T) R) {
	defer func() {
		if r := recover(); r != nil {
			reportPanic(fmt.Sprintf("parallel-map-item-%d", idx), r)
		}
	}()
	results[idx] = fn(idx, item)
}
