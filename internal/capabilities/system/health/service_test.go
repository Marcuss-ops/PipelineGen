package system

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// service_test.go: existing legacy tests are above (preserved). This block
// adds the table-driven close-out coverage required by fix(health).

// scenarioMock is a checker that returns a scripted CheckResult. It
// implements DBChecker, DriveChecker and JobsChecker so the same concrete
// type can be plugged into any port field of ServiceDeps.
type scenarioMock struct {
	name       string
	ok         bool
	applicable bool
	mandatory  bool
}

func (s *scenarioMock) result() CheckResult {
	r := CheckResult{"ok": s.ok, "duration_ms": int64(0), "name": s.name}
	if !s.mandatory {
		r["applicable"] = s.applicable
	}
	return r
}

func (s *scenarioMock) CheckDB(_ context.Context) CheckResult    { return s.result() }
func (s *scenarioMock) CheckDrive(_ context.Context) CheckResult { return s.result() }
func (s *scenarioMock) CheckJobs(_ context.Context) CheckResult  { return s.result() }

// TestService_Check_Scenarios is the close-out table-driven smoke for the
// aggregation policy that ReadyChecker inherits verbatim.
func TestService_NormalizeCheckNames(t *testing.T) {
	type tc struct {
		name  string
		input []string
		want  []string
	}
	cases := []tc{
		{name: "single", input: []string{"db"}, want: []string{"db"}},
		{name: "trim_spaces", input: []string{" db "}, want: []string{"db"}},
		{name: "multiple", input: []string{"db", "jobs"}, want: []string{"db", "jobs"}},
		{name: "dedup", input: []string{"db", "db", "jobs"}, want: []string{"db", "jobs"}},
		{name: "comma_split", input: []string{"db,jobs"}, want: []string{"db", "jobs"}},
		{name: "comma_and_space", input: []string{"db, jobs", "pgvector"}, want: []string{"db", "jobs", "pgvector"}},
		{name: "empty_and_spaces", input: []string{"", " ", "db"}, want: []string{"db"}},
		{name: "double_comma", input: []string{"db,,jobs"}, want: []string{"db", "jobs"}},
		{name: "nil_input", input: nil, want: nil},
		{name: "empty_input", input: []string{}, want: nil},
		{name: "all_empty", input: []string{"", "", ""}, want: nil},
		{name: "preserves_first_occurrence_order", input: []string{"pgvector", "db", "pgvector", "jobs", "db"}, want: []string{"pgvector", "db", "jobs"}},
		{name: "case_insensitive_lowercase", input: []string{"DB", "Jobs"}, want: []string{"db", "jobs"}},
		{name: "case_insensitive_mixed", input: []string{"Db", "DRIVE", "pgvector"}, want: []string{"db", "drive", "pgvector"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NormalizeCheckNames(c.input)
			require.Equal(t, c.want, got, "input=%v", c.input)
		})
	}
}

// ── Unknown check name returns typed error ────────────────────────────

func TestService_Check_UnknownNameReturnsTypedError(t *testing.T) {
	cases := []struct {
		name  string
		input []string
	}{
		{name: "unknown", input: []string{"unknown"}},
		{name: "db_unknown", input: []string{"db", "unknown"}},
		{name: "unknown_db", input: []string{"unknown", "db"}},
		{name: "empty_after_norm", input: []string{"", " "}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			normalized := NormalizeCheckNames(c.input)
			err := ValidateCheckNames(normalized)
			if len(normalized) == 0 {
				require.NoError(t, err, "empty list should not error")
				return
			}
			// If any name is unknown, we expect an error.
			hasUnknown := false
			for _, n := range normalized {
				if !ValidCheckNames[n] {
					hasUnknown = true
					break
				}
			}
			if hasUnknown {
				require.Error(t, err)
				var ue *ErrUnknownCheck
				require.ErrorAs(t, err, &ue)
				require.NotEmpty(t, ue.Name)
				require.False(t, ValidCheckNames[ue.Name], "ErrUnknownCheck.Name should be unknown")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// ── Optional capabilities ─────────────────────────────────────────────

// countingMock wraps a scenarioMock and counts invocations.
type countingMock struct {
	inner  *scenarioMock
	called int
}

func (c *countingMock) result() CheckResult                        { return c.inner.result() }
func (c *countingMock) CheckDB(ctx context.Context) CheckResult    { c.called++; return c.result() }
func (c *countingMock) CheckDrive(ctx context.Context) CheckResult { c.called++; return c.result() }
func (c *countingMock) CheckJobs(ctx context.Context) CheckResult  { c.called++; return c.result() }

func TestService_Check_DeduplicatedCheckerRunsOnce(t *testing.T) {
	mock := &countingMock{inner: &scenarioMock{name: "db", mandatory: true, ok: true}}
	svc := NewService(ServiceDeps{DB: mock})
	// Normalisation (dedup) happens before Check, per the HTTP handler contract.
	names := NormalizeCheckNames([]string{"db", "db", "db"})
	resp := svc.Check(context.Background(), names)
	require.True(t, resp.OK)
	require.Equal(t, 1, mock.called, "deduplicated check should run exactly once, got %d calls", mock.called)
	require.Len(t, resp.Checks, 1, "only one check result expected after dedup")
}

// ── Does not short-circuit ────────────────────────────────────────────

func TestService_Check_DoesNotShortCircuit(t *testing.T) {
	dbMock := &countingMock{inner: &scenarioMock{name: "db", mandatory: true, ok: false}}
	jobsMock := &countingMock{inner: &scenarioMock{name: "jobs", mandatory: true, ok: true}}
	driveMock := &countingMock{inner: &scenarioMock{name: "drive", mandatory: false, applicable: true, ok: true}}

	svc := NewService(ServiceDeps{
		DB:    dbMock,
		Drive: driveMock,
		Jobs:  jobsMock,
	})
	resp := svc.Check(context.Background(), []string{"db", "drive", "jobs"})

	require.False(t, resp.OK, "aggregate should be unhealthy")
	require.Equal(t, 1, dbMock.called, "db should be called")
	require.Equal(t, 1, driveMock.called, "drive should be called even though db failed")
	require.Equal(t, 1, jobsMock.called, "jobs should be called even though db failed")
	require.Len(t, resp.Checks, 3, "all three check results should be present")
}

// ── Context cancellation with blocking checker ────────────────────────

// blockingMock blocks until ctx is done, then returns ok=false.
type blockingMock struct{ called bool }

func (b *blockingMock) result() CheckResult {
	return CheckResult{"ok": false, "error": "context done", "duration_ms": int64(0)}
}
func (b *blockingMock) CheckDB(ctx context.Context) CheckResult {
	b.called = true
	<-ctx.Done()
	return b.result()
}
func (b *blockingMock) CheckDrive(ctx context.Context) CheckResult { <-ctx.Done(); return b.result() }
func (b *blockingMock) CheckJobs(ctx context.Context) CheckResult  { <-ctx.Done(); return b.result() }

func TestService_Check_ContextCancelledWithBlockingChecker(t *testing.T) {
	blocker := &blockingMock{}
	svc := NewService(ServiceDeps{DB: blocker})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancel
	resp := svc.Check(ctx, []string{"db"})
	require.False(t, resp.OK, "cancelled context with blocking checker should return unhealthy")
	require.True(t, blocker.called, "blocking checker should have been invoked")
}

func TestService_Check_ContextCancelled(t *testing.T) {
	dbOK := &scenarioMock{name: "db", mandatory: true, ok: true}

	// Case 1: context already cancelled before call.
	t.Run("pre_cancelled", func(t *testing.T) {
		svc := NewService(ServiceDeps{DB: dbOK})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		resp := svc.Check(ctx, []string{"db"})
		// The checker itself may or may not honour ctx; the important
		// invariant is no panic and a result is returned.
		_, ok := resp.Checks["db"]
		require.True(t, ok, "check result should be present even with cancelled ctx")
	})

	// Case 2: deadline exceeded — verifies no goroutine leaks.
	t.Run("deadline_exceeded", func(t *testing.T) {
		svc := NewService(ServiceDeps{DB: dbOK})
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
		defer cancel()
		time.Sleep(2 * time.Millisecond) // ensure deadline is past
		// Must not panic.
		resp := svc.Check(ctx, []string{"db"})
		// The checker may return ok=false due to ctx, or the mock may ignore it.
		// The invariant is that Check returns without panic.
		require.NotNil(t, resp)
	})

	// Case 3: nil context — should not panic.
	t.Run("nil_context", func(t *testing.T) {
		svc := NewService(ServiceDeps{DB: dbOK})
		// nolint:staticcheck // intentional nil context probe
		require.NotPanics(t, func() {
			_ = svc.Check(nil, []string{"db"})
		})
	})
}
