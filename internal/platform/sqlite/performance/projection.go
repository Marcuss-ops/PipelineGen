package performance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
)

// Projection is the platform adapter that projects a completed job into the
// durable performance registry (performance_runs / performance_steps). It is
// the single production concrete for the capability ProjectionService port
// consumed by the job.completed outbox handler: it reads the job's
// correlation columns from the jobs table, then delegates to the canonical
// projector (which reads the finalized run report from the observability
// database and persists through the registry).
//
// Projecting is idempotent: run_id and step_id are deterministic, so a
// retry converges on the same rows instead of duplicating them.
type Projection struct {
	jobs      *sql.DB
	projector *capperformance.Projector
}

// Compile-time pin: the platform Projection implements the capability
// ProjectionService port consumed by the outbox job.completed handler.
var _ capperformance.ProjectionService = (*Projection)(nil)

// NewProjection builds a projection where the job table and performance
// registry share one database. It remains the compatibility constructor for
// deployments that have not enabled the execution-plane split.
func NewProjection(jobsDB, obsDB *sql.DB) (*Projection, error) {
	return NewSplitProjection(jobsDB, jobsDB, obsDB)
}

// NewSplitProjection builds a projection for the split topology. Job
// lifecycle rows and job_steps are read from jobsDB, while the derived
// performance registry is written to registryDB. Keeping these handles
// distinct is essential: the jobs plane must not grow media-plane tables,
// and the media plane no longer contains the canonical jobs table.
func NewSplitProjection(jobsDB, registryDB, obsDB *sql.DB) (*Projection, error) {
	src, err := NewSource(jobsDB, obsDB)
	if err != nil {
		return nil, err
	}
	reg, err := New(registryDB)
	if err != nil {
		return nil, err
	}
	return &Projection{jobs: jobsDB, projector: capperformance.NewProjector(src, reg)}, nil
}

// ProjectCompletedJob projects a completed job's finalized run report. It
// reads the job row first (single-row query, closed before projecting) so
// the single-connection writer pool is never held open across the report
// source's job_steps read — the same ordering the performance-backfill
// command relies on to avoid a self-deadlock.
func (p *Projection) ProjectCompletedJob(ctx context.Context, jobID string) error {
	if p == nil || p.projector == nil || p.jobs == nil {
		return errors.New("performance projection: not configured")
	}
	if jobID == "" {
		return errors.New("performance projection: job id is required")
	}

	var rootJobID, videoID, gitSHA, host string
	err := p.jobs.QueryRowContext(ctx,
		`SELECT COALESCE(root_job_id, ''), COALESCE(video_id, ''), COALESCE(git_sha, ''), COALESCE(host, '') FROM jobs WHERE id = ?`,
		jobID,
	).Scan(&rootJobID, &videoID, &gitSHA, &host)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("performance projection: job %s not found", jobID)
		}
		return fmt.Errorf("performance projection: read job meta %s: %w", jobID, err)
	}
	// root_job_id is canonical (populated at enqueue + backfilled by
	// migration 213); a defensive self-default guards only residual
	// pre-cutover rows so a projected run never loses correlation.
	if rootJobID == "" {
		rootJobID = jobID
	}

	_, _, err = p.projector.ProjectJob(ctx, jobID, capperformance.JobMeta{
		RootJobID: rootJobID,
		VideoID:   videoID,
		GitSHA:    gitSHA,
		HostID:    host,
	})
	return err
}
