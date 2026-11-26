// Package system — readyz_checkers_canary_test.go.
//
// LOCALJOBS-01 contract: GET /ready must never publish a file to Drive.
// The readiness canary is a read-only folder probe (ListFiles + trashed
// state); the only writer of the canary file is POST /canary-upload.
package system

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type recordingReadOnlyCanary struct {
	calls  int
	folder string
	err    error
}

func (c *recordingReadOnlyCanary) CheckCanaryReadOnly(_ context.Context, folderID string) error {
	c.calls++
	c.folder = folderID
	return c.err
}

func TestReadOnlyDriveCanaryListsFolderAndChecksTrashState(t *testing.T) {
	var listed, trashed bool
	probe := NewReadOnlyDriveCanary(
		func(_ context.Context, folderID string) error {
			listed = true
			require.Equal(t, "folder-1", folderID)
			return nil
		},
		func(_ context.Context, folderID string) (bool, error) {
			trashed = true
			require.Equal(t, "folder-1", folderID)
			return true, nil
		},
	)

	require.NoError(t, probe.CheckCanaryReadOnly(context.Background(), "folder-1"))
	require.True(t, listed, "the probe must list the folder")
	require.True(t, trashed, "the probe must check the trashed state")
}

func TestReadOnlyDriveCanaryFailsClosedOnTrashedFolder(t *testing.T) {
	probe := NewReadOnlyDriveCanary(
		func(context.Context, string) error {
			t.Fatal("reachability must not run for a trashed folder")
			return nil
		},
		func(context.Context, string) (bool, error) { return false, nil },
	)

	err := probe.CheckCanaryReadOnly(context.Background(), "folder-trashed")
	require.ErrorContains(t, err, "TRASHED")
}

func TestReadOnlyDriveCanaryFailsClosedWhenReaderIsUnwired(t *testing.T) {
	probe := NewReadOnlyDriveCanary(nil, nil)
	err := probe.CheckCanaryReadOnly(context.Background(), "folder-1")
	require.ErrorIs(t, err, errCanaryReaderNotWired)
}

func TestReadOnlyDriveFolderCheckerFailsClosedWhenReaderIsUnwired(t *testing.T) {
	checker := NewReadOnlyDriveFolderChecker(nil, nil)
	err := checker.CheckFolder(context.Background(), "folder-1")
	require.ErrorIs(t, err, errCanaryReaderNotWired)
}

// TestCheckReadyUsesTheReadOnlyCanary is the regression pin for the
// incident: CheckReady must call the read-only port and report
// read_only=true. There is no Publisher in the readiness path, so a
// Publish call is impossible by construction.
func TestCheckReadyUsesTheReadOnlyCanary(t *testing.T) {
	canary := &recordingReadOnlyCanary{}
	mock := &scenarioMock{name: "all", mandatory: true, ok: true}
	ready := NewReadyChecker(NewService(ServiceDeps{DB: mock, Jobs: mock})).
		WithDriveCanary(canary, "folder-1")

	resp := ready.CheckReady(context.Background())

	require.Equal(t, 1, canary.calls, "the read-only canary must run exactly once")
	require.Equal(t, "folder-1", canary.folder)
	require.True(t, resp.Checks["drive_canary"]["ok"].(bool))
	require.Equal(t, true, resp.Checks["drive_canary"]["read_only"].(bool),
		"the readiness result must advertise that the probe is read-only")
}
