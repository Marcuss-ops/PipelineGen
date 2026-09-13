package system

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func fixedNow(t time.Time) func() time.Time { return func() time.Time { return t } }

var testNow = time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)

func TestDefaultYTDLPHealthChecker_StaleVersionWarns(t *testing.T) {
	checker := NewYTDLPHealthChecker(
		func(context.Context) (string, error) { return "2026.02.04", nil },
		func(context.Context) ([]string, error) { return []string{"bgutil:http-1.3.1 (external)"}, nil },
		90,
		nil,
	)
	checker.now = fixedNow(testNow)

	report := checker.Refresh(context.Background())
	require.True(t, report.VersionKnown)
	require.True(t, report.Stale)
	require.Greater(t, report.AgeDays, 90)

	warnings := warningsFor(report)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "days old")
}

func TestDefaultYTDLPHealthChecker_MissingPOTWarns(t *testing.T) {
	checker := NewYTDLPHealthChecker(
		func(context.Context) (string, error) { return "2026.09.03", nil },
		func(context.Context) ([]string, error) { return nil, nil },
		90,
		nil,
	)
	checker.now = fixedNow(testNow)

	report := checker.Refresh(context.Background())
	require.True(t, report.POTChecked)
	require.True(t, report.POTMissing)

	warnings := warningsFor(report)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "PO Token provider")
}

func TestDefaultYTDLPHealthChecker_HealthyHasNoWarnings(t *testing.T) {
	checker := NewYTDLPHealthChecker(
		func(context.Context) (string, error) { return "2026.09.03", nil },
		func(context.Context) ([]string, error) { return []string{"bgutil:http-1.3.1 (external)"}, nil },
		90,
		nil,
	)
	checker.now = fixedNow(testNow)

	report := checker.Refresh(context.Background())
	require.False(t, report.Stale)
	require.False(t, report.POTMissing)
	require.Empty(t, report.ProbeError)
	require.Empty(t, warningsFor(report))
}

func TestDefaultYTDLPHealthChecker_ProbeErrorsAreNonFatal(t *testing.T) {
	checker := NewYTDLPHealthChecker(
		func(context.Context) (string, error) { return "", errors.New("boom") },
		func(context.Context) ([]string, error) { return nil, errors.New("network down") },
		90,
		nil,
	)
	checker.now = fixedNow(testNow)

	report := checker.Refresh(context.Background())
	require.Contains(t, report.ProbeError, "boom")
	require.Contains(t, report.ProbeError, "network down")
	require.True(t, report.POTMissing)
}

func TestDefaultYTDLPHealthChecker_CachedReadDoesNotReprobeWithinTTL(t *testing.T) {
	var versionCalls, potCalls int32
	checker := NewYTDLPHealthChecker(
		func(context.Context) (string, error) { atomic.AddInt32(&versionCalls, 1); return "2026.09.03", nil },
		func(context.Context) ([]string, error) { atomic.AddInt32(&potCalls, 1); return nil, nil },
		90,
		nil,
	)
	now := testNow
	checker.now = func() time.Time { return now }

	checker.Refresh(context.Background())
	require.EqualValues(t, 1, atomic.LoadInt32(&versionCalls))
	require.EqualValues(t, 1, atomic.LoadInt32(&potCalls))

	// Fresh cached reads must not spawn probes.
	first := checker.CheckYTDLPHealth(context.Background())
	second := checker.CheckYTDLPHealth(context.Background())
	require.True(t, first.POTChecked)
	require.Equal(t, first.Version, second.Version)
	time.Sleep(50 * time.Millisecond)
	require.EqualValues(t, 1, atomic.LoadInt32(&versionCalls))
	require.EqualValues(t, 1, atomic.LoadInt32(&potCalls))

	// Past the TTL a read still returns the cached snapshot immediately, but
	// schedules a background refresh.
	now = now.Add(DefaultYTDLPHealthTTL + time.Minute)
	require.True(t, checker.CheckYTDLPHealth(context.Background()).POTChecked)
	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&versionCalls) == 2 && atomic.LoadInt32(&potCalls) == 2
	}, 2*time.Second, 10*time.Millisecond, "TTL expiry must schedule a background refresh")
}

func TestDefaultYTDLPHealthChecker_ColdReadIsNonBlockingAndPending(t *testing.T) {
	release := make(chan struct{})
	checker := NewYTDLPHealthChecker(
		func(context.Context) (string, error) {
			<-release
			return "2026.09.03", nil
		},
		func(context.Context) ([]string, error) { return []string{"bgutil:http-1.3.1 (external)"}, nil },
		90,
		nil,
	)
	checker.now = fixedNow(testNow)

	start := time.Now()
	report := checker.CheckYTDLPHealth(context.Background())
	elapsed := time.Since(start)
	close(release)

	require.Less(t, elapsed, 100*time.Millisecond, "cold read must not block on the probe")
	require.False(t, report.POTChecked, "cold report is pending")
	require.Empty(t, report.Version)
}

func TestDefaultYTDLPHealthChecker_StartupWarningsOfflineOnly(t *testing.T) {
	var potCalls int32
	checker := NewYTDLPHealthChecker(
		func(context.Context) (string, error) { return "2026.02.04", nil },
		func(context.Context) ([]string, error) { atomic.AddInt32(&potCalls, 1); return nil, nil },
		90,
		nil,
	)
	checker.now = fixedNow(testNow)

	warnings := checker.StartupWarnings(context.Background())
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "2026.02.04")
	require.EqualValues(t, 0, atomic.LoadInt32(&potCalls), "StartupWarnings must not run the network POT probe")

	fresh := NewYTDLPHealthChecker(
		func(context.Context) (string, error) { return "2026.09.03", nil }, nil, 90, nil)
	fresh.now = fixedNow(testNow)
	require.Empty(t, fresh.StartupWarnings(context.Background()))

	// A missing tool must not warn here — the `tools` check owns that.
	broken := NewYTDLPHealthChecker(
		func(context.Context) (string, error) { return "", errors.New("not found") }, nil, 90, nil)
	require.Empty(t, broken.StartupWarnings(context.Background()))
}

// fakeYTDLPHealth is a stand-in for the concrete checker in ReadyChecker tests.
type fakeYTDLPHealth struct{ report YTDLPHealthReport }

func (f fakeYTDLPHealth) CheckYTDLPHealth(context.Context) YTDLPHealthReport { return f.report }

func TestReadyChecker_RunYTDLPHealthCheck_WarnOnly(t *testing.T) {
	resp := HealthResponse{OK: true, Status: "healthy", Checks: map[string]CheckResult{}}
	rc := &ReadyChecker{ytdlpHealth: fakeYTDLPHealth{report: YTDLPHealthReport{
		Version: "2026.02.04", VersionKnown: true, AgeDays: 221, MaxAgeDays: 90, Stale: true,
		POTChecked: true, POTMissing: true,
	}}}

	rc.runYTDLPHealthCheck(context.Background(), &resp)

	check, ok := resp.Checks["ytdlp_health"]
	require.True(t, ok, "ytdlp_health check missing")
	require.Equal(t, true, check["ok"], "ytdlp_health must stay ok=true (warn-only)")
	require.True(t, resp.OK, "warn-only check must not flip readiness")
	require.Equal(t, "healthy", resp.Status)

	note, _ := check["note"].(string)
	require.Contains(t, note, "days old")
	require.Contains(t, note, "PO Token provider")
}

func TestReadyChecker_RunYTDLPHealthCheck_PendingColdStart(t *testing.T) {
	resp := HealthResponse{OK: true, Status: "healthy", Checks: map[string]CheckResult{}}
	rc := &ReadyChecker{ytdlpHealth: fakeYTDLPHealth{report: YTDLPHealthReport{MaxAgeDays: 90, CheckedAt: testNow}}}

	rc.runYTDLPHealthCheck(context.Background(), &resp)

	check := resp.Checks["ytdlp_health"]
	require.Equal(t, true, check["pending"])
	note, _ := check["note"].(string)
	require.True(t, strings.Contains(note, "pending"))
	require.True(t, resp.OK)
}

func TestReadyChecker_RunYTDLPHealthCheck_OptedOut(t *testing.T) {
	resp := HealthResponse{OK: true, Status: "healthy", Checks: map[string]CheckResult{}}
	(&ReadyChecker{}).runYTDLPHealthCheck(context.Background(), &resp)

	check := resp.Checks["ytdlp_health"]
	require.Equal(t, false, check["applicable"])
}
