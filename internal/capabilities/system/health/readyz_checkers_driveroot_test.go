package system

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestDriveRootChecker_TrashedRootFailsClosed pins the fail-closed contract
// found in production: a trashed root folder still lists its children
// successfully, so the reachability probe alone reported /ready drive_root
// green while every artifact published under it was invisible in Drive.
func TestDriveRootChecker_TrashedRootFailsClosed(t *testing.T) {
	reachCalled := false
	checker := NewDriveRootCheckerWithTrashProbe(
		func(context.Context, string) error {
			reachCalled = true
			return nil // a trashed folder IS listable — that is the trap
		},
		func(context.Context, string) (bool, error) { return false, nil },
	)

	err := checker.CheckDriveRoot(context.Background(), "TRASHED-ROOT")
	require.Error(t, err)
	require.Contains(t, err.Error(), "TRASHED")
	require.Contains(t, err.Error(), "TRASHED-ROOT")
	require.False(t, reachCalled,
		"a trashed root must short-circuit before the reachability probe")
}

// TestDriveRootChecker_LiveRootPasses pins the healthy path.
func TestDriveRootChecker_LiveRootPasses(t *testing.T) {
	reachCalled := false
	checker := NewDriveRootCheckerWithTrashProbe(
		func(context.Context, string) error {
			reachCalled = true
			return nil
		},
		func(context.Context, string) (bool, error) { return true, nil },
	)

	require.NoError(t, checker.CheckDriveRoot(context.Background(), "LIVE-ROOT"))
	require.True(t, reachCalled, "a live root must still run the reachability probe")
}

// TestDriveRootChecker_TrashProbeErrorIsSurfaced keeps the probe fail-closed
// when the trashed-state lookup itself fails.
func TestDriveRootChecker_TrashProbeErrorIsSurfaced(t *testing.T) {
	checker := NewDriveRootCheckerWithTrashProbe(
		func(context.Context, string) error { return nil },
		func(context.Context, string) (bool, error) { return false, errors.New("boom") },
	)

	err := checker.CheckDriveRoot(context.Background(), "ROOT")
	require.Error(t, err)
	require.Contains(t, err.Error(), "trashed-state probe")
	require.True(t, strings.Contains(err.Error(), "boom"))
}

// TestDriveRootChecker_WithoutTrashProbeDegradesToReachability keeps the
// original behaviour when no trashed-state probe is wired.
func TestDriveRootChecker_WithoutTrashProbeDegradesToReachability(t *testing.T) {
	checker := NewDriveRootCheckerWithTrashProbe(
		func(context.Context, string) error { return nil },
		nil,
	)

	require.NoError(t, checker.CheckDriveRoot(context.Background(), "ROOT"))
}

// TestDriveRootChecker_NilReachFailsClosed pins the composition-time fail
// signal for an unwired Drive reader.
func TestDriveRootChecker_NilReachFailsClosed(t *testing.T) {
	checker := NewDriveRootCheckerWithTrashProbe(nil, func(context.Context, string) (bool, error) { return true, nil })

	err := checker.CheckDriveRoot(context.Background(), "ROOT")
	require.Error(t, err)
	require.Contains(t, err.Error(), "Drive reader not wired")
}

// TestReadyChecker_ReportsTrashedDriveRootAsUnhealthy pins the /ready
// aggregation: the trashed root must flip OK=false and status=unhealthy.
func TestReadyChecker_ReportsTrashedDriveRootAsUnhealthy(t *testing.T) {
	svc := NewService(ServiceDeps{
		DB:     &scenarioMock{name: "db", mandatory: true, ok: true},
		Drive:  &scenarioMock{name: "drive", mandatory: true, ok: true},
		Qdrant: &scenarioMock{name: "qdrant", mandatory: false, ok: true},
		Jobs:   &scenarioMock{name: "jobs", mandatory: true, ok: true},
	})
	rc := NewReadyChecker(svc).
		WithDriveRootFolder("17iOP").
		WithDriveRootChecker(NewDriveRootCheckerWithTrashProbe(
			func(context.Context, string) error { return nil },
			func(context.Context, string) (bool, error) { return false, nil },
		))

	resp := rc.CheckReady(context.Background())
	require.False(t, resp.OK, "a trashed Drive root must make /ready unhealthy")
	require.Equal(t, "unhealthy", resp.Status)

	check, ok := resp.Checks["drive_root"]
	require.True(t, ok, "drive_root check must be present")
	require.Equal(t, false, check["ok"])
	require.Contains(t, check["error"], "TRASHED")
}
