package overlays

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"
)

// GPUGate serializes GPU ownership between Creator and RenderingGen on one
// host. The lock is synchronization only; it is not job state or SSOT.
type GPUGate struct{ path string }

func NewGPUGate(path string) (*GPUGate, error) {
	if path == "" {
		return nil, fmt.Errorf("gpu gate path is empty")
	}
	if err := os.MkdirAll(filepathDir(path), 0755); err != nil {
		return nil, err
	}
	return &GPUGate{path: path}, nil
}

func (g *GPUGate) Acquire(ctx context.Context) (func(), error) {
	if g == nil {
		return nil, fmt.Errorf("gpu gate is nil")
	}
	f, err := os.OpenFile(g.path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			f.Close()
			return nil, err
		}
		t := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !t.Stop() {
				<-t.C
			}
			f.Close()
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
