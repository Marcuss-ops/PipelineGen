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
//     keeps compiling.
package monitor

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

func TestNewChannelMonitor_PanicsWhenProductionMissingDiscoveries(t *testing.T) {
	deps := CompositionDeps{
		MonitorRuntimeDeps: MonitorRuntimeDeps{
			Cfg: &config.Config{}, // production signal
			Log: zap.NewNop(),
		},
	}
	require.PanicsWithValue(t,
		"monitor.NewChannelMonitor: Discoveries port is required when Cfg is wired (production composition must wire *assets.YoutubeDiscoveriesRepository from internal/infrastructure/database/sqlite/assets/youtube_discoveries_repository.go; the nil-port pre-Commit-1 path defeats per-video dedupe AND cycle-end MAX watermark)",
		func() { NewChannelMonitor(deps) },
		"Commit 1 fail-fast: production composition MUST panic on nil Discoveries (P0 #1 silent-already_scheduled regression) ")
}

func TestNewChannelMonitor_ToleratesNilDiscoveriesInTest(t *testing.T) {
	deps := CompositionDeps{
		MonitorRuntimeDeps: MonitorRuntimeDeps{Log: zap.NewNop()},
	}
	require.NotPanics(t, func() { NewChannelMonitor(deps) },
		"test-fixture path (Cfg=nil) MUST continue to permit nil Discoveries so the existing test suite keeps compiling")
}

func TestNewChannelMonitor_AcceptsWiredDiscoveries(t *testing.T) {
	deps := CompositionDeps{
		MonitorRuntimeDeps: MonitorRuntimeDeps{
			Cfg: &config.Config{},
			Log: zap.NewNop(),
		},
		MonitorPersistenceDeps: MonitorPersistenceDeps{
			Discoveries: stubDiscoveries{},
		},
	}
	require.NotPanics(t, func() { _ = NewChannelMonitor(deps) },
		"production composition with a wired Discoveries port MUST NOT panic")
}

type stubDiscoveries struct{}

func (stubDiscoveries) TryReserve(_ context.Context, _, _, _, _, _, _ string) (string, bool, int, error) {
	return "", false, 0, nil
}
func (stubDiscoveries) MarkEnqueued(_ context.Context, _, _ string) error           { return nil }
func (stubDiscoveries) MarkRejected(_ context.Context, _, _ string, _ bool) error   { return nil }
func (stubDiscoveries) MaxDiscoveredAt(_ context.Context, _ string) (string, error) { return "", nil }
func (stubDiscoveries) CommitEnqueueOutbox(_ context.Context, _, _, _, _ string) error {
	return nil
}
func (stubDiscoveries) DrainPendingOutbox(_ context.Context, _ int, _, _ string) ([]OutboxEntry, error) {
	return nil, nil
}
func (stubDiscoveries) DrainDispatched(_ context.Context, _ int, _, _ string) ([]OutboxEntry, error) {
	return nil, nil
}
func (stubDiscoveries) MarkOutboxDispatched(_ context.Context, _ int64, _ string) error {
	return nil
}
func (stubDiscoveries) MarkOutboxFailed(_ context.Context, _ int64, _ string) error {
	return nil
}

var _ = context.TODO
