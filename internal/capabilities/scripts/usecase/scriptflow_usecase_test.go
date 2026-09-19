package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── SemaphoreUseCase ────────────────────────────────────────────────────────

func TestSemaphoreUseCase_NewRejectsZeroCapacity(t *testing.T) {
	_, err := NewSemaphoreUseCase(0, zap.NewNop())
	require.ErrorIs(t, err, ErrSemaphoreMisconfigured)
	_, err = NewSemaphoreUseCase(-1, zap.NewNop())
	require.ErrorIs(t, err, ErrSemaphoreMisconfigured)
}

func TestSemaphoreUseCase_AcquireReturnRelease(t *testing.T) {
	t.Parallel()
	uc, err := NewSemaphoreUseCase(2, zap.NewNop())
	require.NoError(t, err)
	require.Equal(t, 2, uc.Capacity())

	r1, err := uc.Acquire(context.Background(), "jobA")
	require.NoError(t, err)
	r2, err := uc.Acquire(context.Background(), "jobB")
	require.NoError(t, err)
	require.Equal(t, int64(2), uc.AcquireCount())
	require.Equal(t, int64(0), uc.ReleaseCount())

	r1()
	r2()
	require.Equal(t, int64(2), uc.ReleaseCount())
}

func TestSemaphoreUseCase_DoubleReleaseIsNoop(t *testing.T) {
	t.Parallel()
	uc, _ := NewSemaphoreUseCase(1, zap.NewNop())
	r, err := uc.Acquire(context.Background(), "jobA")
	require.NoError(t, err)
	r()
	r() // second call must not panic or decrement counter
	require.Equal(t, int64(1), uc.ReleaseCount())
}

func TestSemaphoreUseCase_AcquireCanceledByCtx(t *testing.T) {
	t.Parallel()
	uc, _ := NewSemaphoreUseCase(1, zap.NewNop())
	_, err := uc.Acquire(context.Background(), "first") // occupies slot
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = uc.Acquire(ctx, "second")
	require.ErrorIs(t, err, ErrSemaphoreAcquisitionCanceled)
}

func TestSemaphoreUseCase_AcquireReleaseReuse(t *testing.T) {
	t.Parallel()
	uc, _ := NewSemaphoreUseCase(1, zap.NewNop())
	for i := 0; i < 5; i++ {
		r, err := uc.Acquire(context.Background(), "j")
		require.NoError(t, err)
		require.NotNil(t, r)
		r()
	}
	require.Equal(t, int64(5), uc.AcquireCount())
	require.Equal(t, int64(5), uc.ReleaseCount())
}

// TestSemaphoreUseCase_NilSafe is now in semaphore_usecase_test.go

// (Sprint 1.0: DocumentsUseCase was retired — document generation
// now lives at internal/application/document/usecase.go as the
// document.generate downstream job.)

// (PostGenUseCase was deleted in the post-cutover NLP cleanup: the
// deterministic VisualNER + phrases.Select chain replaced the LLM
// entity/metadata post-generation phase, leaving NewPostGenUseCase with
// zero production callers.)
