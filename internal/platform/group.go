package platform

import (
	"context"
	"fmt"
	"log"
	"sync"
)

// Group is a small errgroup-style helper for running a fixed set of
// goroutines with first-error-wins cancellation and panic recovery.
//
// Why not golang.org/x/sync/errgroup? Two reasons:
//
//  1. Keeps the module dependency surface minimal (no new transitive deps).
//  2. Lets us add panic recovery and structured logging under our own
//     control, which x/sync/errgroup intentionally does not provide.
//
// Behaviour matches errgroup.WithContext closely:
//
//   - Each Go() runs in a new goroutine.
//   - The FIRST non-nil error wins; subsequent errors are logged but
//     not returned.
//   - The context returned by WithContext is cancelled as soon as one
//     goroutine fails OR the parent context is cancelled.
//   - Wait() returns the first error (or nil if all succeeded).
//   - A panicking goroutine is recovered, the panic value is wrapped in
//     an error, and the context is cancelled so siblings unwind.
//
// Limitations vs. x/sync/errgroup:
//
//   - No TryGo (no non-blocking add). All goroutines are spawned up
//     front via Go().
//   - No SetLimit: callers needing bounded concurrency should wrap
//     each fn in their own semaphore (the existing pkg/concurrent.Pool
//     is the right tool for that case).
type Group struct {
	wg     sync.WaitGroup
	cancel context.CancelFunc
	err    error
	errMu  sync.Mutex
}

// WithContext returns a new Group and a child context derived from
// parent. The child context is cancelled as soon as any goroutine
// returns a non-nil error, or the parent context is cancelled.
//
// The caller is responsible for invoking Wait() exactly once and for
// calling the returned cancel func (typically via defer) to release
// the context resources.
func WithContext(parent context.Context) (*Group, context.Context) {
	ctx, cancel := context.WithCancel(parent)
	return &Group{cancel: cancel}, ctx
}

// Go runs fn in a new goroutine. Panics are recovered, logged with
// the supplied name, and converted to errors so they participate in
// the first-error-wins policy like any other error.
//
// Note: we intentionally do NOT call SafeGo here because SafeGo's own
// defer/recover would catch the panic before our error-recording defer
// gets a chance to run — the panic would be logged and silently
// swallowed, and Wait() would return nil. By recovering in our own
// goroutine we both log AND record the error, guaranteeing that a
// panicked worker is surfaced to the caller.
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

// recordError stores the first non-nil error and cancels the
// group context so siblings can stop early.
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

// Wait blocks until all goroutines spawned via Go have returned and
// then returns the first non-nil error, if any. It must be called
// exactly once.
func (g *Group) Wait() error {
	g.wg.Wait()
	g.errMu.Lock()
	defer g.errMu.Unlock()
	return g.err
}

// errPanic wraps a recovered panic so it satisfies the error
// interface and surfaces in Wait() output / logs.
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
