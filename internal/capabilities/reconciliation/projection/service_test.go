package projection

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

// ─── CoverageRatio math ─────────────────────────────────────────────

func TestCoverageRatio(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		p    ProjectionParity
		want float64
	}{
		{
			name: "full coverage 527/527",
			p:    ProjectionParity{EligibleSQLite: 527, MissingCount: 0},
			want: 1.0,
		},
		{
			name: "partial coverage 500/527",
			p:    ProjectionParity{EligibleSQLite: 527, MissingCount: 27},
			want: 500.0 / 527.0,
		},
		{
			name: "zero eligible is vacuous full",
			p:    ProjectionParity{EligibleSQLite: 0, MissingCount: 0},
			want: 1.0,
		},
		{
			name: "missing clamped at zero",
			p:    ProjectionParity{EligibleSQLite: 10, MissingCount: 99},
			want: 0.0,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.p.CoverageRatio()
			if tc.want == 1.0 {
				assert.Equal(t, 1.0, got)
			} else {
				assert.InDelta(t, tc.want, got, 1e-9)
			}
		})
	}
}

// ─── Construction ───────────────────────────────────────────────────

func TestNewServiceFromDeps_PanicsOnNilChecker(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		NewServiceFromDeps(ServiceDeps{Checker: nil})
	})
}

// ─── ReconcileOnce ──────────────────────────────────────────────────

// fakeChecker returns a canned parity (or error).
type fakeChecker struct {
	parity ProjectionParity
	err    error
	calls  int64
}

func (f *fakeChecker) CheckProjectionParity(context.Context) (ProjectionParity, error) {
	atomic.AddInt64(&f.calls, 1)
	return f.parity, f.err
}

// fakeMetrics records what the service emitted.
type fakeMetrics struct {
	lastParity ProjectionParity
	paritySeen int64
	errorsSeen int64
}

func (f *fakeMetrics) ObserveParity(p ProjectionParity) {
	atomic.AddInt64(&f.paritySeen, 1)
	f.lastParity = p
}

func (f *fakeMetrics) ObserveError() {
	atomic.AddInt64(&f.errorsSeen, 1)
}

func TestReconcileOnce_SuccessEmitsParity(t *testing.T) {
	t.Parallel()
	parity := ProjectionParity{
		Collection:     "media_assets_v4_test",
		EligibleSQLite: 527,
		QdrantPoints:   527,
		CompleteScan:   true,
	}
	checker := &fakeChecker{parity: parity}
	metrics := &fakeMetrics{}
	svc := NewServiceFromDeps(ServiceDeps{
		Checker:  checker,
		Metrics:  metrics,
		Interval: time.Minute,
		Log:      zap.NewNop(),
	})

	err := svc.ReconcileOnce(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1), metrics.paritySeen)
	assert.Equal(t, int64(0), metrics.errorsSeen)
	assert.Equal(t, parity, metrics.lastParity)
	assert.Equal(t, int64(1), checker.calls)
}

func TestReconcileOnce_ErrorEmitsErrorMetric(t *testing.T) {
	t.Parallel()
	checker := &fakeChecker{err: errors.New("qdrant down")}
	metrics := &fakeMetrics{}
	svc := NewServiceFromDeps(ServiceDeps{
		Checker: checker,
		Metrics: metrics,
		Log:     zap.NewNop(),
	})

	err := svc.ReconcileOnce(context.Background())
	require.Error(t, err)
	assert.Equal(t, int64(0), metrics.paritySeen, "no parity observed on failure")
	assert.Equal(t, int64(1), metrics.errorsSeen)
}

// ─── Run ticker ─────────────────────────────────────────────────────

func TestRun_TicksImmediatelyThenEveryInterval(t *testing.T) {
	t.Parallel()
	checker := &fakeChecker{parity: ProjectionParity{EligibleSQLite: 5, QdrantPoints: 5, CompleteScan: true}}
	metrics := &fakeMetrics{}
	svc := NewServiceFromDeps(ServiceDeps{
		Checker:  checker,
		Metrics:  metrics,
		Interval: 20 * time.Millisecond,
		Log:      zap.NewNop(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.Run(ctx)
		close(done)
	}()

	// Immediate first tick + at least one interval tick.
	require.Eventually(t, func() bool {
		return atomic.LoadInt64(&metrics.paritySeen) >= 2
	}, 2*time.Second, 10*time.Millisecond, "run must tick immediately and on the interval")

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not exit after context cancellation")
	}
	assert.Equal(t, int64(0), metrics.errorsSeen)
}
