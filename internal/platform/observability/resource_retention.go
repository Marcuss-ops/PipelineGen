package observability

import (
	"context"
	"errors"
	"time"
)

// ResourceRetentionPolicy separates high-volume raw sample retention from the
// compact aggregate retention. Zero/negative durations are rejected so a
// caller cannot accidentally delete all telemetry.
type ResourceRetentionPolicy struct {
	RawSampleAge time.Duration
	AggregateAge time.Duration
}

func (p ResourceRetentionPolicy) validate() error {
	if p.RawSampleAge <= 0 || p.AggregateAge <= 0 {
		return errors.New("resource retention: ages must be positive")
	}
	if p.RawSampleAge > p.AggregateAge {
		return errors.New("resource retention: raw age must not exceed aggregate age")
	}
	return nil
}

// ApplyResourceRetention deletes only raw samples older than RawSampleAge;
// aggregates are retained until AggregateAge. It returns both deletion
// counts. The cutoff is supplied by the caller to keep maintenance scheduling
// clocks outside the Job–Attempt–Run measurement source.
func (r *SQLiteRecorder) ApplyResourceRetention(ctx context.Context, now time.Time, policy ResourceRetentionPolicy) (rawDeleted, aggregateDeleted int64, err error) {
	if err := policy.validate(); err != nil {
		return 0, 0, err
	}
	if r == nil || r.db == nil {
		return 0, 0, errors.New("resource retention: nil database")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	rawCutoff := now.Add(-policy.RawSampleAge).UTC().Format(time.RFC3339Nano)
	aggCutoff := now.Add(-policy.AggregateAge).UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx, `DELETE FROM run_resource_samples WHERE observed_at < ?`, rawCutoff)
	if err != nil {
		return 0, 0, err
	}
	rawDeleted, err = res.RowsAffected()
	if err != nil {
		return 0, 0, err
	}
	res, err = tx.ExecContext(ctx, `DELETE FROM run_resource_aggregates WHERE updated_at < ?`, aggCutoff)
	if err != nil {
		return 0, 0, err
	}
	aggregateDeleted, err = res.RowsAffected()
	if err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return rawDeleted, aggregateDeleted, nil
}

var _ interface {
	ApplyResourceRetention(context.Context, time.Time, ResourceRetentionPolicy) (int64, int64, error)
} = (*SQLiteRecorder)(nil)
