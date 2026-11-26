package assetop

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// The real Drive adapter must keep satisfying the capability-owned port
// without a wrapper: the composition root passes drive.Reader unchanged.
var _ DriveFileState = (drive.Reader)(nil)

type fakeDriveState struct {
	trashed map[string]bool
	err     error
	asked   []string
}

func (f *fakeDriveState) FileIsNotTrashed(_ context.Context, fileID string) (bool, error) {
	f.asked = append(f.asked, fileID)
	if f.err != nil {
		return false, f.err
	}
	return !f.trashed[fileID], nil
}

func TestCheckDriveFileExistsUsesTheFakePort(t *testing.T) {
	fake := &fakeDriveState{trashed: map[string]bool{"gone": true}}
	svc := NewReconcileService(&dedupeStoreStub{}, fake, ReconcilePolicy{Enabled: true}, zap.NewNop())

	ok, err := svc.checkDriveFileExists(context.Background(), "alive")
	if err != nil {
		t.Fatalf("checkDriveFileExists(alive): %v", err)
	}
	if !ok {
		t.Fatal("checkDriveFileExists(alive) = false, want true")
	}
	ok, err = svc.checkDriveFileExists(context.Background(), "gone")
	if err != nil {
		t.Fatalf("checkDriveFileExists(gone): %v", err)
	}
	if ok {
		t.Fatal("checkDriveFileExists(gone) = true, want false (trashed)")
	}
}

func TestCheckDriveFileExistsPropagatesPortErrors(t *testing.T) {
	boom := errors.New("drive unavailable")
	svc := NewReconcileService(&dedupeStoreStub{}, &fakeDriveState{err: boom}, ReconcilePolicy{Enabled: true}, zap.NewNop())

	if _, err := svc.checkDriveFileExists(context.Background(), "x"); !errors.Is(err, boom) {
		t.Fatalf("checkDriveFileExists error = %v, want %v", err, boom)
	}
}

func TestCheckDriveFileExistsWithoutReaderIsNotAnError(t *testing.T) {
	svc := NewReconcileService(&dedupeStoreStub{}, nil, ReconcilePolicy{Enabled: true}, zap.NewNop())

	ok, err := svc.checkDriveFileExists(context.Background(), "x")
	if err != nil || ok {
		t.Fatalf("checkDriveFileExists(nil reader) = (%v, %v), want (false, nil)", ok, err)
	}
}
