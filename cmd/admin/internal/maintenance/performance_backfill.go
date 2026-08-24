package maintenance

import (
	"github.com/Marcuss-ops/PipelineGen/cmd/admin/internal/cli"

	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	capperformance "github.com/Marcuss-ops/PipelineGen/internal/capabilities/performance"
	storage "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite"
	perfstore "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/performance"
	"go.uber.org/zap"
)

// runPerformanceBackfill projects completed jobs into the durable performance
// registry (performance_runs / performance_steps). It is the write-side
// companion to `performance-report`: both share the single canonical source
// (perfstore.Source) and resolver, so the projected rows always match what the
// read-only report would render for the same job.
//
// The projection is derived and idempotent: run_id and step_id are
// deterministic, so re-running converges instead of duplicating.
//
//	admin performance-backfill [--job-type script.generate,asset.text.materialize,...] [--limit N]
func RunPerformanceBackfill(args []string) error {
	fs := flag.NewFlagSet("performance-backfill", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	jobType := fs.String("job-type", "", "Comma-separated job types to project; empty means all terminal jobs")
	limit := fs.Int("limit", 0, "Maximum number of jobs to project; zero means all")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *limit < 0 {
		return fmt.Errorf("--limit must be non-negative")
	}
	types := splitBackfillCSV(*jobType)

	cfg, log, cleanup, err := cli.AppLogger()
	if err != nil {
		return err
	}
	defer cleanup()

	jobsDB, err := storage.OpenSQLiteDB(cfg.Storage.PrimaryDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open primary database: %w", err)
	}
	defer jobsDB.Close()
	obsDB, err := storage.OpenSQLiteDB(cfg.Storage.ObservabilityDBFullPath(), log)
	if err != nil {
		return fmt.Errorf("open observability database: %w", err)
	}
	defer obsDB.Close()

	src, err := perfstore.NewSource(jobsDB.DB, obsDB.DB)
	if err != nil {
		return err
	}
	reg, err := perfstore.New(jobsDB.DB)
	if err != nil {
		return err
	}
	projector := capperformance.NewProjector(src, reg)

	ctx := context.Background()

	query := `SELECT id, root_job_id, parent_job_id, video_id, git_sha, host FROM jobs WHERE status IN ('SUCCEEDED','FAILED') AND started_at IS NOT NULL AND completed_at IS NOT NULL`
	var queryArgs []any
	if len(types) > 0 {
		query += " AND type IN (" + strings.TrimSuffix(strings.Repeat("?,", len(types)), ",") + ")"
		for _, t := range types {
			queryArgs = append(queryArgs, t)
		}
	}
	query += " ORDER BY created_at ASC"
	if *limit > 0 {
		query += " LIMIT ?"
		queryArgs = append(queryArgs, *limit)
	}
	rows, err := jobsDB.DB.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return fmt.Errorf("select jobs for performance backfill: %w", err)
	}
	// Collect rows before projecting: the projector's report source also reads
	// job_steps from this same primary *sql.DB, which is limited to a single
	// connection (SetMaxOpenConns(1)). Holding the outer jobs cursor open while
	// projecting would deadlock the inner job_steps query on that connection.
	type backfillJob struct {
		id, rootJobID, parentJobID, videoID, gitSHA, host string
	}
	var candidates []backfillJob
	for rows.Next() {
		var j backfillJob
		if err := rows.Scan(&j.id, &j.rootJobID, &j.parentJobID, &j.videoID, &j.gitSHA, &j.host); err != nil {
			rows.Close()
			return fmt.Errorf("scan performance backfill job: %w", err)
		}
		// root_job_id is canonical now: it is populated at enqueue and
		// backfilled by migration 213, so the backfill no longer derives it
		// from parent_job_id. A defensive self-default guards only residual
		// pre-cutover rows so a projected run never loses correlation.
		if j.rootJobID == "" {
			j.rootJobID = j.id
		}
		candidates = append(candidates, j)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate performance backfill jobs: %w", err)
	}
	rows.Close()

	var projected, skipped int
	for _, j := range candidates {
		_, steps, projectErr := projector.ProjectJob(ctx, j.id, capperformance.JobMeta{
			RootJobID: j.rootJobID,
			VideoID:   j.videoID,
			GitSHA:    j.gitSHA,
			HostID:    j.host,
		})
		if projectErr != nil {
			skipped++
			log.Warn("performance backfill skipped job",
				zap.String("job_id", j.id),
				zap.Error(projectErr),
			)
			continue
		}
		projected++
		fmt.Printf("projected %s (%d steps)\n", j.id, len(steps))
	}

	fmt.Printf("performance backfill complete: projected=%d skipped=%d\n", projected, skipped)
	return nil
}
