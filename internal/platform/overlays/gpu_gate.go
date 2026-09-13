package overlays

import (
	"context"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
)

// GPUGate bounds concurrent GPU ownership on one host. The lock is
// synchronization only; it is not job state or SSOT.
//
// SLOTS. The historical gate was a single exclusive flock, so concurrency was
// pinned to 1 host-wide and no measured >1 experiment was expressible. The gate
// now owns N lock files (one per slot) and admits up to N holders:
//
//	slots == 1 → EXACTLY the historical behaviour: one exclusive flock on the
//	             configured path, host-wide. This is the default, so an
//	             existing single-lock deployment is byte-identical.
//	slots == N → N holders, each on its own flock file, so the mutual exclusion
//	             still holds ACROSS PROCESSES on the same lock path.
//
// Slot 0 is the configured path itself (not a renamed sibling), so a peer
// process that was told RENDERINGGEN_GPU_LOCK=/run/pipelinegen/gpu-0.lock keeps
// contending on the same file. Additional slots are siblings named
// <path>.slot<i>; a peer must be given the same slot count to use them.
type GPUGate struct {
	paths []string
	// slots is the in-process admission semaphore. Its capacity equals the
	// number of lock files, so at most N holders exist in this process and each
	// one holds a distinct file — the flock loop can never deadlock against it.
	slots chan struct{}
}

// NewGPUGate preserves the historical single-slot constructor.
func NewGPUGate(path string) (*GPUGate, error) {
	return NewGPUGateWithSlots(path, 1)
}

// NewGPUGateWithSlots constructs a gate admitting up to `slots` concurrent
// holders. A non-positive slot count falls back to the serialized default.
func NewGPUGateWithSlots(path string, slots int) (*GPUGate, error) {
	if path == "" {
		return nil, fmt.Errorf("gpu gate path is empty")
	}
	if slots < 1 {
		slots = 1
	}
	g := &GPUGate{
		paths: make([]string, slots),
		slots: make(chan struct{}, slots),
	}
	for i := range g.paths {
		p := path
		if i > 0 {
			p = fmt.Sprintf("%s.slot%d", path, i)
		}
		if err := os.MkdirAll(filepathDir(p), 0755); err != nil {
			return nil, err
		}
		g.paths[i] = p
	}
	return g, nil
}

func (g *GPUGate) Acquire(ctx context.Context) (func(), error) {
	if g == nil {
		return nil, fmt.Errorf("gpu gate is nil")
	}
	// In-process admission, then the cross-process flock. The semaphore is
	// acquired first so a waiting caller costs no file descriptor.
	select {
	case g.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	releaseSlot := func() { <-g.slots }

	f, err := g.acquireFile(ctx)
	if err != nil {
		releaseSlot()
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
			releaseSlot()
		})
	}, nil
}

// acquireFile takes an exclusive, non-blocking flock on the first free slot
// file, retrying with a short backoff until the context is done. Every slot is
// tried on each pass, so a holder released by ANOTHER PROCESS is picked up by
// this process without waiting for an in-process holder to finish.
func (g *GPUGate) acquireFile(ctx context.Context) (*os.File, error) {
	for {
		for _, path := range g.paths {
			f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
			if err != nil {
				return nil, err
			}
			err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
			if err == nil {
				return f, nil
			}
			f.Close()
			if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
				return nil, err
			}
		}
		t := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !t.Stop() {
				<-t.C
			}
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}

func filepathDir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			if i == 0 {
				return "/"
			}
			return path[:i]
		}
	}
	return "."
}
