package system

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReadyChecker_RequiredSet verifies that CheckReady includes exactly
// the set [db, drive, qdrant, jobs].
func TestReadyChecker_RequiredSet(t *testing.T) {
	mock := &scenarioMock{name: "all", mandatory: true, ok: true}
	svc := NewService(ServiceDeps{
		DB:     mock,
		Drive:  mock,
		Qdrant: mock,
		Jobs:   mock,
	})
	ready := NewReadyChecker(svc)
	resp := ready.CheckReady(context.Background())
	require.True(t, resp.OK)
	require.Len(t, resp.Checks, 18, "ready should check 18: db, drive, qdrant, jobs + tools, clips_path, drive_canary, handlers + temp, tts, drive_root, ollama, outbox + drive_credentials, drive_folder, publisher, destination_clip + script_generate")
	for _, name := range []string{"db", "drive", "qdrant", "jobs", "tools", "clips_path", "drive_canary", "handlers", "temp", "tts", "drive_root", "ollama", "outbox", "drive_credentials", "drive_folder", "publisher", "destination_clip", "script_generate"} {
		require.Contains(t, resp.Checks, name, "ready missing %s", name)
	}
}

// TestReadyChecker_NilService does not panic.
func TestReadyChecker_NilService(t *testing.T) {
	require.NotPanics(t, func() {
		// NewReadyChecker(nil) creates a checker with nil svc;
		// calling CheckReady should be handled gracefully.
		r := &ReadyChecker{svc: nil}
		_ = r.CheckReady(context.Background())
	}, "ReadyChecker with nil service should not panic")
}

// TestReadyChecker_NilServiceReturnsUnhealthy verifies the explicit
// behaviour when svc is nil: ok=false, status=unhealthy.
func TestReadyChecker_NilServiceReturnsUnhealthy(t *testing.T) {
	r := &ReadyChecker{svc: nil}
	resp := r.CheckReady(context.Background())
	require.False(t, resp.OK, "nil service should return unhealthy")
	require.Equal(t, "unhealthy", resp.Status)
	require.NotEmpty(t, resp.Checks["db"]["error"])
}

// TestReadyChecker_OptionalCapabilitiesHealthy verifies that when Drive
// and Qdrant are nil (not wired), ReadyChecker still reports healthy
// because optional capabilities don't flip the aggregate.
func TestReadyChecker_OptionalCapabilitiesHealthy(t *testing.T) {
	core := &scenarioMock{name: "core", mandatory: true, ok: true}
	svc := NewService(ServiceDeps{
		DB:     core,
		Jobs:   core,
		Drive:  nil, // optional, not wired
		Qdrant: nil, // optional, not wired
	})
	ready := NewReadyChecker(svc)
	resp := ready.CheckReady(context.Background())
	require.True(t, resp.OK, "optional nil capabilities should not make ready unhealthy, got %v", resp)
	require.Len(t, resp.Checks, 18)
}

func TestReadyChecker_ReportsIndependentStoragePlanes(t *testing.T) {
	mock := &scenarioMock{name: "all", mandatory: true, ok: true}
	ready := NewReadyChecker(NewService(ServiceDeps{DB: mock, Jobs: mock})).WithStoragePlanes(
		func(context.Context) map[string]CheckResult {
			return map[string]CheckResult{
				"media":         {"ok": true},
				"jobs":          {"ok": false, "error": "jobs unavailable"},
				"cache":         {"ok": false, "applicable": true},
				"observability": {"ok": false, "applicable": true},
			}
		},
	)
	resp := ready.CheckReady(context.Background())
	require.False(t, resp.OK, "jobs outage must block readiness")
	require.False(t, resp.Checks["storage_jobs"]["ok"].(bool))
	require.False(t, resp.Checks["storage_cache"]["ok"].(bool))
	require.Contains(t, resp.Checks, "storage_observability")
}

// TestReadyChecker_Deterministic verifies that calling CheckReady
// multiple times with the same fakes returns identical results
// (excluding duration_ms which is time-dependent).
func TestReadyChecker_Deterministic(t *testing.T) {
	mock := &scenarioMock{name: "all", mandatory: true, ok: true}
	svc := NewService(ServiceDeps{
		DB:     mock,
		Drive:  mock,
		Qdrant: mock,
		Jobs:   mock,
	})
	ready := NewReadyChecker(svc)
	ctx := context.Background()

	resp1 := ready.CheckReady(ctx)
	resp2 := ready.CheckReady(ctx)

	require.Equal(t, resp1.OK, resp2.OK)
	require.Equal(t, resp1.Status, resp2.Status)
	require.Len(t, resp1.Checks, len(resp2.Checks))
	for name, c1 := range resp1.Checks {
		c2, ok := resp2.Checks[name]
		require.True(t, ok, "check %q missing in second call", name)
		require.Equal(t, c1["ok"], c2["ok"], "check %q non-deterministic ok", name)
		require.Equal(t, c1["name"], c2["name"], "check %q non-deterministic name", name)
	}
}
