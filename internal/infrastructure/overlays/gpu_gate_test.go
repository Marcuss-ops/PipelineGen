package overlays

import (
	"context"
	"testing"
	"time"
)

func TestGPUGateSerializesAndHonorsCancellation(t *testing.T) {
	g, err := NewGPUGate(t.TempDir() + "/gpu.lock")
	if err != nil {
		t.Fatal(err)
	}
	release, err := g.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := g.Acquire(ctx); err == nil {
		t.Fatal("second acquire should be cancelled while first owns gate")
	}
	release()
	release2, err := g.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	release2()
}
