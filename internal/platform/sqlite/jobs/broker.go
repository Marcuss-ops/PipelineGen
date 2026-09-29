package jobs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/mattn/go-sqlite3"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// Broker is the storage-boundary adapter exposed to the jobs capability. It
// embeds SQLiteStore for the canonical JobBroker surface and overrides writes
// that need driver error classification.
type Broker struct {
	*SQLiteStore
}

var (
	_ job.JobBroker     = (*Broker)(nil)
	_ job.ScheduleStore = (*Broker)(nil)
)

func NewBroker(store *SQLiteStore) *Broker {
	if store == nil {
		return nil
	}
	return &Broker{SQLiteStore: store}
}

func (b *Broker) Create(ctx context.Context, j *job.Job) error {
	if b == nil || b.SQLiteStore == nil {
		return fmt.Errorf("sqlite jobs broker: store is nil")
	}
	return mapWriteError(b.SQLiteStore.Create(ctx, j))
}

// CreateScheduled is the deferred-enqueue write. It mirrors Create's error
// classification so a duplicate (active_key / client_idempotency) surfaces as
// the kernel sentinel and the queue idempotency rescue works unchanged.
func (b *Broker) CreateScheduled(ctx context.Context, j *job.Job, runAt time.Time) error {
	if b == nil || b.SQLiteStore == nil {
		return fmt.Errorf("sqlite jobs broker: store is nil")
	}
	return mapWriteError(b.SQLiteStore.CreateScheduled(ctx, j, runAt))
}

// mapWriteError is the single SQLite-driver classification boundary for job
// writes. Capability code must branch only on kernel sentinels.
func mapWriteError(err error) error {
	if err == nil {
		return nil
	}
	var sqliteErr sqlite3.Error
	if errors.As(err, &sqliteErr) && sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique {
		return fmt.Errorf("%w: %w", job.ErrDuplicate, err)
	}
	return err
}
