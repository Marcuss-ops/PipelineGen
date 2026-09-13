package overlays

import (
	"context"
	"testing"
	"time"
)

// TestGPUGateAdmitsConfiguredConcurrency pins the new lever: with N slots, N
// holders run at once and the (N+1)-th waits; a released slot is reusable. This
// is what makes a measured >1 overlay-render canary expressible without
// deleting the cross-process lock.
func TestGPUGateAdmitsConfiguredConcurrency(t *testing.T) {
	g, err := NewGPUGateWithSlots(t.TempDir()+"/gpu.lock", 2)
	if err != nil {
		t.Fatal(err)
	}
	r1, err := g.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r2, err := g.Acquire(context.Background())
	if err != nil {
		t.Fatalf("second slot must be admitted: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := g.Acquire(ctx); err == nil {
		t.Fatal("a third acquire must wait while both slots are held")
	}
	r1()
	r3, err := g.Acquire(context.Background())
	if err != nil {
		t.Fatalf("a released slot must be reusable: %v", err)
	}
	r2()
	r3()
}

// TestGPUGateSlotsAreMutuallyExclusiveAcrossInstances pins the CROSS-PROCESS
// half: two independent gate instances (the shape of two processes sharing one
// host) contend on the SAME lock-file set, so the slot count is a real ceiling
// and not merely an in-process semaphore.
func TestGPUGateSlotsAreMutuallyExclusiveAcrossInstances(t *testing.T) {
	path := t.TempDir() + "/gpu-0.lock"
	a, err := NewGPUGateWithSlots(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewGPUGateWithSlots(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	ra1, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ra2, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if _, err := b.Acquire(ctx); err == nil {
		t.Fatal("a peer process must not exceed the slot ceiling")
	}
	ra1()
	rb, err := b.Acquire(context.Background())
	if err != nil {
		t.Fatalf("a released peer slot must be claimable: %v", err)
	}
	ra2()
	rb()
}

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
