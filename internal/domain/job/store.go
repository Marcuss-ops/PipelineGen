package job

import (
	"context"
	"encoding/json"
)

// Store is the canonical persistence contract for jobs.
type Store interface {
	Create(ctx context.Context, job *Job) error
	Get(ctx context.Context, id string) (*Job, error)
	List(ctx context.Context, filter Filter) ([]*Job, int64, error)
	ClaimNext(ctx context.Context, workerID string, jobTypes []string) (*Job, error)
	Complete(ctx context.Context, id string, result json.RawMessage) error
	Fail(ctx context.Context, id string, errMsg string) error
	ScheduleRetry(ctx context.Context, id string) error
	DeadLetter(ctx context.Context, id string) error
	Cancel(ctx context.Context, id string) error
	Heartbeat(ctx context.Context, id, leaseID string) error
	UpdateProgress(ctx context.Context, id string, progress int) error
}
