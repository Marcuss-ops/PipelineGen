package scripts

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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

func TestSemaphoreUseCase_NilSafe(t *testing.T) {
	t.Parallel()
	var uc *SemaphoreUseCase
	assert.Nil(t, uc)
	// Acquire on nil use case must return a sentinel + a no-op release.
	rel, err := uc.Acquire(context.Background(), "j")
	require.Error(t, err)
	require.NotPanics(t, func() { rel() })
}

// ── PrewarmUseCase ──────────────────────────────────────────────────────────

type fakePrewarmSvc struct {
	calls atomic.Int32
	last  atomic.Pointer[struct {
		jobID string
		count int
	}]
}

func (f *fakePrewarmSvc) TriggerPrewarm(ctx context.Context, jobID string, count int) {
	f.calls.Add(1)
	f.last.Store(&struct {
		jobID string
		count int
	}{jobID: jobID, count: count})
}

func TestPrewarmUseCase_ShouldStart(t *testing.T) {
	t.Parallel()
	assert.True(t, ShouldStart(true, 0, 0))
	assert.True(t, ShouldStart(false, 3, 0))
	assert.True(t, ShouldStart(false, 0, 5))
	assert.False(t, ShouldStart(false, 0, 0))
}

func TestPrewarmUseCase_NilImgSvcNoop(t *testing.T) {
	t.Parallel()
	uc := NewPrewarmUseCase(nil, zap.NewNop())
	err := uc.Start(context.Background(), "jobA", true)
	require.NoError(t, err)
	uc.Wait()
}

func TestPrewarmUseCase_NotStartedWhenGuardFalse(t *testing.T) {
	t.Parallel()
	svc := &fakePrewarmSvc{}
	uc := NewPrewarmUseCase(svc, zap.NewNop())
	err := uc.Start(context.Background(), "jobA", false)
	require.NoError(t, err)
	uc.Wait()
	assert.Equal(t, int32(0), svc.calls.Load(), "TriggerPrewarm must not be called when shouldStart=false")
}

func TestPrewarmUseCase_FiresGoroutineWhenShouldStart(t *testing.T) {
	t.Parallel()
	svc := &fakePrewarmSvc{}
	uc := NewPrewarmUseCase(svc, zap.NewNop()).WithTimeout(2 * time.Second).WithCount(7)
	err := uc.Start(context.Background(), "jobA", true)
	require.NoError(t, err)
	uc.Wait()
	assert.Equal(t, int32(1), svc.calls.Load())
	last := svc.last.Load()
	require.NotNil(t, last)
	assert.Equal(t, "jobA", last.jobID)
	assert.Equal(t, 7, last.count)
}

func TestPrewarmUseCase_StartNilSafe(t *testing.T) {
	t.Parallel()
	var uc *PrewarmUseCase
	err := uc.Start(context.Background(), "jobA", true)
	require.ErrorIs(t, err, ErrPrewarmUnconfigured)
}

// ── SceneBuilderUseCase ─────────────────────────────────────────────────────

func TestSceneBuilderUseCase_RequiresImgAndVoSvc(t *testing.T) {
	t.Parallel()
	uc := NewSceneBuilderUseCase(nil, nil, zap.NewNop(), nil, nil, nil)
	_, err := uc.Build(context.Background())
	require.ErrorIs(t, err, ErrSceneBuilderUnconfigured)
}

func TestSceneBuilderUseCase_NilSafe(t *testing.T) {
	t.Parallel()
	var uc *SceneBuilderUseCase
	_, err := uc.Build(context.Background())
	require.ErrorIs(t, err, ErrSceneBuilderUnconfigured)
}

func TestSceneBuilderUseCase_BuildWhenDepsNil(t *testing.T) {
	t.Parallel()
	uc := NewSceneBuilderUseCase(nil, nil, zap.NewNop(), nil, nil, nil)
	svc, err := uc.Build(context.Background())
	require.ErrorIs(t, err, ErrSceneBuilderUnconfigured)
	require.Nil(t, svc)
}

// ── DocumentsUseCase ────────────────────────────────────────────────────────

func TestDocumentsUseCase_BuildAndCreateRejectsNilUseCase(t *testing.T) {
	t.Parallel()
	var uc *DocumentsUseCase
	ln, id, err := uc.BuildAndCreate(context.Background(), "T", "C", nil, "F")
	require.ErrorIs(t, err, ErrDocumentCreationFailed)
	assert.Empty(t, ln)
	assert.Empty(t, id)
}

func TestDocumentsUseCase_DocumentsServiceNilIfNoClient(t *testing.T) {
	t.Parallel()
	uc := NewDocumentsUseCase(nil, zap.NewNop(), "")
	assert.Nil(t, uc.DocumentsService())
	ln, id, err := uc.BuildAndCreate(context.Background(), "T", "C", nil, "F")
	require.ErrorIs(t, err, ErrDocumentCreationFailed)
	assert.Empty(t, ln)
	assert.Empty(t, id)
}

// ── PipelineUseCase (dispatch) ────────────────────────────────────────────
//
// PipelineUseCase.Run requires a *Pipeline to compile; we exercise
// only the public-typed-error behaviour (so tests can pin "given
// nil use case → returns ErrPipelineGenerationFailed") and the
// constructor validation.

func TestNewPipelineUseCase_RejectsNilEngine(t *testing.T) {
	t.Parallel()
	_, err := NewPipelineUseCase(zap.NewNop(), nil, 100, "", nil, nil, nil, nil, &Pipeline{})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrPipelineGenerationFailed)
}

func TestNewPipelineUseCase_RejectsNilPipeline(t *testing.T) {
	t.Parallel()
	_, err := NewPipelineUseCase(zap.NewNop(), &Engine{}, 100, "", nil, nil, nil, nil, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrPipelineGenerationFailed)
}

func TestPipelineUseCase_RunNilSafe(t *testing.T) {
	t.Parallel()
	var pu *PipelineUseCase
	_, err := pu.Run(context.Background(), nil, nil)
	require.ErrorIs(t, err, ErrPipelineGenerationFailed)
}

func TestPipelineUseCase_RegisterJobs_NilSvcNoOp(t *testing.T) {
	t.Parallel()
	pu := &PipelineUseCase{log: zap.NewNop()} // intentionally under-populated
	err := pu.RegisterJobs(nil)
	require.NoError(t, err)
}

func TestPipelineUseCase_HandleJob_NilUseCaseErrors(t *testing.T) {
	t.Parallel()
	var pu *PipelineUseCase
	_, err := pu.HandleJob(context.Background(), nil, nil)
	require.ErrorIs(t, err, ErrPipelineGenerationFailed)
}
