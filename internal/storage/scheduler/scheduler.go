package scheduler

import (
	"context"

	"go.uber.org/zap"
)

// DriveSyncScheduler is a minimal compatibility shim that preserves the
// existing bootstrap shape while the real scheduler logic migrates.
type DriveSyncScheduler struct {
	log *zap.Logger
}

func NewDriveSyncScheduler(_ any, _ any, _ any, log *zap.Logger, _ any) *DriveSyncScheduler {
	return &DriveSyncScheduler{log: log}
}

func (s *DriveSyncScheduler) Start(ctx context.Context) {
	<-ctx.Done()
}
