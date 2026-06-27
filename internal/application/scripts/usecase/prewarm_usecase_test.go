package usecase

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── 5. PrewarmUseCase tests ─────────────────────────────────────────────────

// fakePrewarmImgSvc implements PrewarmImageService for tests.
type fakePrewarmImgSvc struct {
	calls   atomic.Int32
	lastJob atomic.Value // stores string jobID
	panics  bool
}

func (f *fakePrewarmImgSvc) TriggerPrewarm(ctx context.Context, jobID string, count int) {
	if f.panics {
		panic("forced panic in TriggerPrewarm")
	}
	f.calls.Add(1)
	f.lastJob.Store(jobID)
}

func (f *fakePrewarmImgSvc) callCount() int32 { return f.calls.Load() }
func (f *fakePrewarmImgSvc) lastJobID() string {
	if v, ok := f.lastJob.Load().(string); ok {
		return v
	}
	return ""
}

// ── shouldStart=false ───────────────────────────────────────────────────────

func TestPrewarmUseCase_ShouldStartFalse(t *testing.T) {
	t.Parallel()
	svc := &fakePrewarmImgSvc{}
	uc := NewPrewarmUseCase(svc, zap.NewNop())
	uc.WithTimeout(1 * time.Second)

	err := uc.Start(context.Background(), "jobA", false)
	require.NoError(t, err)
	uc.Wait()
	assert.Equal(t, int32(0), svc.callCount(), "TriggerPrewarm must not be called when shouldStart=false")
}

// ── image service nil ───────────────────────────────────────────────────────

func TestPrewarmUseCase_ImageServiceNil(t *testing.T) {
	t.Parallel()
	uc := NewPrewarmUseCase(nil, zap.NewNop())
	err := uc.Start(context.Background(), "jobA", true)
	require.NoError(t, err)
	uc.Wait()
}

// ── use case nil ────────────────────────────────────────────────────────────

func TestPrewarmUseCase_NilUseCase(t *testing.T) {
	t.Parallel()
	var uc *PrewarmUseCase
	err := uc.Start(context.Background(), "jobA", true)
	require.ErrorIs(t, err, ErrPrewarmUnconfigured)
}

// ── success ─────────────────────────────────────────────────────────────────

func TestPrewarmUseCase_Success(t *testing.T) {
	t.Parallel()
	svc := &fakePrewarmImgSvc{}
	uc := NewPrewarmUseCase(svc, zap.NewNop()).WithTimeout(2 * time.Second).WithCount(7)
	err := uc.Start(context.Background(), "jobA", true)
	require.NoError(t, err)
	uc.Wait()

	assert.Equal(t, int32(1), svc.callCount())
	assert.Equal(t, "jobA", svc.lastJobID())
}

// ── context cancelled ───────────────────────────────────────────────────────

func TestPrewarmUseCase_ContextCancelled(t *testing.T) {
	t.Parallel()
	svc := &fakePrewarmImgSvc{}
	uc := NewPrewarmUseCase(svc, zap.NewNop()).WithTimeout(10 * time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	err := uc.Start(ctx, "jobA", true)
	require.NoError(t, err)
	uc.Wait()

	// Prewarm goroutine starts but the inner context deadline fires quickly.
	// The goroutine completes (TriggerPrewarm may or may not be called depending on timing).
	// We just verify no panic and Wait() returns.
}

// ── timeout ─────────────────────────────────────────────────────────────────

func TestPrewarmUseCase_Timeout(t *testing.T) {
	t.Parallel()
	svc := &fakePrewarmImgSvc{}
	uc := NewPrewarmUseCase(svc, zap.NewNop()).WithTimeout(1 * time.Millisecond)
	err := uc.Start(context.Background(), "timeoutJob", true)
	require.NoError(t, err)
	uc.Wait()
	// Should not hang — the goroutine always calls inFlight.Done() regardless of timeout.
}

// ── panic del servizio ──────────────────────────────────────────────────────

func TestPrewarmUseCase_ServicePanic(t *testing.T) {
	t.Parallel()
	svc := &fakePrewarmImgSvc{panics: true}
	uc := NewPrewarmUseCase(svc, zap.NewNop()).WithTimeout(1 * time.Second)
	err := uc.Start(context.Background(), "panicJob", true)
	require.NoError(t, err)

	// SafeGo recovers the panic; Wait() must still return.
	done := make(chan struct{})
	go func() {
		uc.Wait()
		close(done)
	}()
	select {
	case <-done:
		// OK
	case <-time.After(3 * time.Second):
		t.Fatal("Wait() hung after service panic")
	}
}

// ── più prewarm concorrenti ─────────────────────────────────────────────────

func TestPrewarmUseCase_MultipleConcurrent(t *testing.T) {
	t.Parallel()
	svc := &fakePrewarmImgSvc{}
	uc := NewPrewarmUseCase(svc, zap.NewNop()).WithTimeout(2 * time.Second)

	for i := 0; i < 5; i++ {
		err := uc.Start(context.Background(), "job", true)
		require.NoError(t, err)
	}
	uc.Wait()

	assert.Equal(t, int32(5), svc.callCount())
}

// ── Wait blocca finché tutti terminano ──────────────────────────────────────

func TestPrewarmUseCase_WaitDrainsAll(t *testing.T) {
	t.Parallel()
	svc := &fakePrewarmImgSvc{}
	uc := NewPrewarmUseCase(svc, zap.NewNop()).WithTimeout(2 * time.Second)

	for i := 0; i < 3; i++ {
		require.NoError(t, uc.Start(context.Background(), "job", true))
	}

	done := make(chan struct{})
	go func() {
		uc.Wait()
		close(done)
	}()

	select {
	case <-done:
		assert.Equal(t, int32(3), svc.callCount())
	case <-time.After(5 * time.Second):
		t.Fatal("Wait() did not return within timeout")
	}
}

// ── Errori non ignorati: policy test ────────────────────────────────────────

func TestPrewarmUseCase_ErrorPolicy(t *testing.T) {
	t.Parallel()
	// ErrPrewarmUnconfigured → skip consentito per ShouldStart=true su nil use case.
	var ucNil *PrewarmUseCase
	err := ucNil.Start(context.Background(), "j", true)
	require.True(t, errors.Is(err, ErrPrewarmUnconfigured),
		"nil use case with shouldStart=true must return ErrPrewarmUnconfigured")

	// Success case: Start with valid deps must return nil.
	svc := &fakePrewarmImgSvc{}
	uc := NewPrewarmUseCase(svc, zap.NewNop())
	err = uc.Start(context.Background(), "j", true)
	require.NoError(t, err, "valid Start must not return error")
	uc.Wait()
}
