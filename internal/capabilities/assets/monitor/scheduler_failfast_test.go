// Package monitor — scheduler_failfast_test.go locks in the Commit 1
// fail-fast posture introduced in scheduler.go::NewChannelMonitor.
//
// Contract under test:
//
//   - PRODUCTION composition (deps.Cfg != nil) AND deps.Discoveries
//     nil → PANIC. The verdict's P0 #1 directive is loud at boot
//     rather than silent at first scheduler tick.
//   - TEST composition (deps.Cfg == nil) — nil Discoveries is
//     TOLERATED. The pre-Step-9 test pattern `&ChannelMonitor{...}`
//     (and bare CompositionDeps{Log: ...}) keeps compiling.
//
// The proxy `deps.Cfg != nil` distinguishes the two paths because
// composition in internal/app/lifecycle.go::startBackgroundJobs
// always supplies a *config.Config to the production monitor; tests
// that construct the monitor directly via bare literal leave Cfg
// nil. The proxy is intentional because CompositionDeps is the
// canonical ctor payload (per AGENTS.md Pattern 0); a separate
// "production mode" bool would couple the package to a runtime
// concept it does not own.
package monitor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// TestNewChannelMonitor_PanicsWhenProductionMissingDiscoveries pins
// the fail-fast posture: with Cfg wired (production signal) and
// Discoveries nil, NewChannelMonitor MUST panic. The error message
// must mention the Discoveries port name so log-grep correlates.
func TestNewChannelMonitor_PanicsWhenProductionMissingDiscoveries(t *testing.T) {
	deps := CompositionDeps{
		Cfg: &config.Config{}, // production signal
		Log: zap.NewNop(),
		// Discoveries nil → triggers fail-fast panic.
		Ports: MonitorPorts{},
	}
	require.PanicsWithValue(t,
		"monitor.NewChannelMonitor: Discoveries port is required when Cfg is wired (production composition must wire *youtubediscoveries.YoutubeDiscoveriesRepository from internal/platform/sqlite/assets/youtube_discoveries_repository.go; the nil-port pre-Commit-1 path defeats per-video dedupe AND cycle-end MAX watermark)",
		func() { NewChannelMonitor(deps) },
		"Commit 1 fail-fast: production composition MUST panic on nil Discoveries (P0 #1 silent-already_scheduled regression) ")
}

// TestNewChannelMonitor_ToleratesNilDiscoveriesInTest pins the
// test-fixture path: with Cfg nil (test signal), nil Discoveries
// is tolerated. Existing tests (scheduler_test.go +
// youtube_discoveries_test.go + monitor_scheduler_test.go etc.) all
// construct the monitor with bare CompositionDeps leaving Cfg nil;
// this test pins that contract so a future refactor that flips
// "Cfg nil → tolerated" to "Cfg nil → panic" surfaces here.
func TestNewChannelMonitor_ToleratesNilDiscoveriesInTest(t *testing.T) {
	deps := CompositionDeps{
		// Cfg nil → test signal
		Log: zap.NewNop(),
		// Discoveries nil → tolerated in tests
		Ports: MonitorPorts{},
	}
	require.NotPanics(t, func() { NewChannelMonitor(deps) },
		"test-fixture path (Cfg=nil) MUST continue to permit nil Discoveries so the existing test suite keeps compiling")
}

// TestNewChannelMonitor_AcceptsWiredDiscoveries pins the happy
// path: production composition with a non-nil Discoveries port
// (the post-Commit 1 canonical wiring) does not panic. The stub
// port satisfies the YoutubeDiscoveriesPort interface so signature
// drift surfaces here as a compile failure before runtime.
func TestNewChannelMonitor_AcceptsWiredDiscoveries(t *testing.T) {
	deps := CompositionDeps{
		Cfg: &config.Config{},
		Log: zap.NewNop(),
		Ports: MonitorPorts{
			Discoveries: stubDiscoveries{}, // satisfies YoutubeDiscoveriesPort
		},
	}
	require.NotPanics(t, func() { _ = NewChannelMonitor(deps) },
		"production composition with a wired Discoveries port MUST NOT panic")
}

// stubDiscoveries is a minimal YoutubeDiscoveriesPort implementation
// used by the accept-wired test. Tests that drive specific behaviour
// (TryReserve dedupe contract, MaxDiscoveredAt watermark etc.) live
// in youtube_discoveries_test.go against the canonical SQLite repo;
// this stub is for compile-time conformance only.
type stubDiscoveries struct{}

func (stubDiscoveries) TryReserve(_ context.Context, _, _, _, _, _, _ string) (string, bool, int, error) {
	return "", false, 0, nil
}
func (stubDiscoveries) MarkEnqueued(_ context.Context, _, _ string) error           { return nil }
func (stubDiscoveries) MarkRejected(_ context.Context, _, _ string, _ bool) error   { return nil }
func (stubDiscoveries) MaxDiscoveredAt(_ context.Context, _ string) (string, error) { return "", nil }

// Blocco 3 (July 2026): outbox surface methods.
func (stubDiscoveries) CommitEnqueueOutbox(_ context.Context, _, _, _, _ string) error { return nil }
func (stubDiscoveries) DrainPendingOutbox(_ context.Context, _ int, _, _ string) ([]OutboxEntry, error) {
	return nil, nil
}
func (stubDiscoveries) DrainDispatched(_ context.Context, _ int, _, _ string) ([]OutboxEntry, error) {
	return nil, nil
}
func (stubDiscoveries) MarkOutboxDispatched(_ context.Context, _ int64, _ string) error { return nil }
func (stubDiscoveries) MarkOutboxFailed(_ context.Context, _ int64, _ string) error     { return nil }

// context import alias — required for method signature but a separate
// import block keeps the test surface small. (The stub's signature
// references context.Context even though the body is a no-op —
// compile-time conformance only.)
var _ = context.TODO
