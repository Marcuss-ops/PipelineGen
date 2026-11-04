// by_parent.go owns the run-scoped read: every job submitted under one
// parent_job_id, in submission order.
//
// It is deliberately NOT Children (children.go): that projection answers the
// assembly fan-in ("give me the child chunks of this anchor, in chunk order")
// and only its finalizer reads it. An operator asking "what did this run
// enqueue?" needs the sibling view — parent_job_id is set to the MASTER run id
// by every producer, so the same column carries both meanings and only the
// ordering differs. The projection is shared with Children for the same reason:
// render_plan / input_manifest are large JSONB blobs nobody reads on a listing
// call, and loading them per row would make a diagnostic read as expensive as
// the render it describes.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// ByParent returns every job whose parent_job_id is parentJobID, ordered by
// queued_at (then id, so equal timestamps stay deterministic). An unknown
// parent yields an empty slice and a nil error.
func (r *Repository) ByParent(ctx context.Context, parentJobID string) ([]*model.Job, error) {
	rows, release, err := r.parentJobRows(ctx, parentJobID, `
		SELECT j.id, j.state, j.chunk_index, j.frame_range, j.attempt_count,
		       j.queued_at, j.current_worker_id, j.error_message,`+artifactColumns+parentJobsFrom+`
		ORDER BY j.queued_at ASC, j.id ASC`)
	if err != nil {
		return nil, err
	}
	defer release()
	defer rows.Close()
	var jobs []*model.Job
	for rows.Next() {
		var (
			id, state  string
			chunkIndex int
			attempts   int
			frameRange []byte
			queuedAt   sql.NullTime
			nWorker    sql.NullString
			nError     sql.NullString
			artifact   artifactScan
		)
		dest := append([]any{
			&id, &state, &chunkIndex, &frameRange, &attempts,
			&queuedAt, &nWorker, &nError,
		}, artifact.destinations()...)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		fr, err := decodeFrameRange(frameRange)
		if err != nil {
			return nil, fmt.Errorf("job %s: %w", id, err)
		}
		job := &model.Job{
			ID:          id,
			State:       model.State(state),
			ParentJobID: parentJobID,
			ChunkIndex:  chunkIndex,
			FrameRange:  fr,
			Attempts:    attempts,
			Worker:      nWorker.String,
			QueuedAt:    queuedAt.Time,
		}
		if nError.Valid {
			job.FailReason = nError.String
		}
		job.Artifact = artifact.artifact()
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return jobs, nil
}
