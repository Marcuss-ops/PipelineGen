// Package concurrent provides goroutine-safe concurrency primitives:
// errgroup-style parallel execution with cancellation, bounded map/reduce,
// and panic-safe fire-and-forget goroutines.
package concurrent

import (
	"context"
	"fmt"
	"log"
	"sync"
)

// ── SafeGo — fire-and-forget goroutines with panic recovery ─────────────

// SafeGo runs fn in a new goroutine with panic recovery.
// If fn panics, the panic is recovered and logged with the goroutine name.
func SafeGo(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[panic recovery] goroutine %q panicked: %v", name, r)
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
				log.Printf("[panic recovery] goroutine %q panicked: %v", name, r)
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
				log.Printf("[panic recovery] goroutine %q panicked: %v", name, r)
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

// ── ParallelMap — bounded parallel map/reduce ──────────────────────────

// Map executes fn for each item in parallel with at most workers concurrent
// goroutines. Returns results in the same order as items. On first error,
// remaining items are cancelled via context.
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

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make(chan result, len(items))
	sem := make(chan struct{}, workers)

	var wg sync.WaitGroup
	for i, item := range items {
		wg.Add(1)
		go func(idx int, it T) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			select {
			case <-ctx.Done():
				return
			default:
			}

			r, err := fn(ctx, idx, it)
			select {
			case results <- result{idx: idx, val: r, err: err}:
			case <-ctx.Done():
			}
		}(i, item)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	out := make([]R, len(items))
	var firstErr error
	for res := range results {
		if res.err != nil && firstErr == nil {
			firstErr = res.err
			cancel() // cancel remaining goroutines
		}
		if res.err == nil {
			out[res.idx] = res.val
		}
	}
	return out, firstErr
}
