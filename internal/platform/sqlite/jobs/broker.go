package jobs

import (
	"context"
	"errors"
	"fmt"

	"github.com/mattn/go-sqlite3"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// Broker is the storage-boundary adapter exposed to the jobs capability. It
// embeds SQLiteStore for the canonical JobBroker surface and overrides writes
// that need driver error classification.
type Broker struct {
	*SQLiteStore
}

var _ job.JobBroker = (*Broker)(nil)

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
