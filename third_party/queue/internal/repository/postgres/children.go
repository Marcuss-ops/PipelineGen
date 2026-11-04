// children.go owns the finalizer fan-in projection: child chunks in
// deterministic chunk order with their artifacts, without the heavy plan
// JSONB blobs the fan-in never reads.
package postgres

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// Children returns child chunks in deterministic chunk order.
//
// The projection deliberately excludes render_plan and input_manifest: the
// finalizer's fan-in calls this after every child completion, and loading the
// full plan JSONB per child would turn the fan-in into an O(children²)
// transfer of large blobs nobody reads. A child's frame range, state and
// artifact are all the validation and assembly need.
func (r *Repository) Children(ctx context.Context, parentJobID string) ([]*model.Job, error) {
	rows, release, err := r.parentJobRows(ctx, parentJobID, `
		SELECT j.id, j.state, j.chunk_index, j.frame_range,`+artifactColumns+parentJobsFrom+`
		ORDER BY j.chunk_index ASC`)
	if err != nil {
		return nil, err
	}
	defer release()
	defer rows.Close()
	var children []*model.Job
	for rows.Next() {
		var (
			id, state  string
			chunkIndex int
			frameRange []byte
			// Nullable mirrors so a NULL join row (child without an artifact
			// yet) stays a nil job.Artifact.
			artifact artifactScan
		)
		dest := append([]any{&id, &state, &chunkIndex, &frameRange}, artifact.destinations()...)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		fr, err := decodeFrameRange(frameRange)
		if err != nil {
			return nil, fmt.Errorf("child %s: %w", id, err)
		}
		job := &model.Job{
			ID:          id,
			State:       model.State(state),
			ParentJobID: parentJobID,
			ChunkIndex:  chunkIndex,
			FrameRange:  fr,
		}
		job.Artifact = artifact.artifact()
		children = append(children, job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return children, nil
}
