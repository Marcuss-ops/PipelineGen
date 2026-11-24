package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// claimCandidate is one row selected by claimN before its per-job attempt
// bookkeeping runs. It is package-level because the read and mutation phases
// share the decoded values.
type claimCandidate struct {
	id                    string
	jobType               string
	publicationPolicy     string
	daemonAdmissionWaitMS float64
	schema                string
	version               sql.NullInt64
	plan                  []byte
	manifest              []byte
	attempts              int
	generation            int64
	queuedAt              time.Time
	artifactID            sql.NullString
	parentJobID           sql.NullString
	chunkIndex            int
	frameRange            []byte
	frameRangeVal         *model.FrameRange
	manifestVal           inputManifest
}

// claimStateFilter validates the requested state against the same vocabulary
// the SQL predicate is built from. An empty state means "every claimable
// state".
func claimStateFilter(state model.State) (string, error) {
	if state == "" {
		return "state IN " + stateIn(model.ClaimableStates()...), nil
	}
	if !model.IsClaimable(state) {
		return "", fmt.Errorf("unsupported claim state %q", state)
	}
	return "state = " + stateLiteral(state), nil
}

// loadClaimCandidates selects the whole claim window before any per-job
// statement runs: database/sql forbids another statement on the same
// connection while rows are still open. Corrupt JSONB poisons the row in this
// same transaction and is left out of the healthy window.
func loadClaimCandidates(ctx context.Context, tx *sql.Tx, stateFilter string, now time.Time, limit int) ([]claimCandidate, int, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, job_type, publication_policy, daemon_admission_wait_ms, job_schema, job_schema_version, render_plan, input_manifest, attempt_count, lease_generation, queued_at, artifact_id, parent_job_id, chunk_index, frame_range
		FROM render_jobs
		WHERE `+stateFilter+`
		  AND (not_before IS NULL OR not_before <= $1)
		  AND NOT EXISTS (
		      SELECT 1 FROM render_jobs AS child
		      WHERE child.parent_job_id = render_jobs.id)
		ORDER BY queued_at ASC
		FOR UPDATE SKIP LOCKED
		LIMIT $2`, now, limit)
	if err != nil {
		return nil, 0, err
	}

	candidates := make([]claimCandidate, 0, limit)
	poisoned := 0
	for rows.Next() {
		var c claimCandidate
		if err := rows.Scan(&c.id, &c.jobType, &c.publicationPolicy, &c.daemonAdmissionWaitMS, &c.schema, &c.version, &c.plan, &c.manifest, &c.attempts, &c.generation, &c.queuedAt, &c.artifactID, &c.parentJobID, &c.chunkIndex, &c.frameRange); err != nil {
			rows.Close()
			return nil, 0, err
		}
		// Fail-closed JSONB: corrupt frame_range or input_manifest must poison
		// the job (failed) rather than silently rendering full-plan with
		// dropped SHA256. In a batch one corrupt row must not cost the healthy
		// claims around it, so it is poisoned in this same transaction and
		// simply left out of the returned window.
		frameRangeVal, err := decodeFrameRange(c.frameRange)
		if err != nil {
			if err := poisonCorruptJob(ctx, tx, c.id, err.Error()); err != nil {
				rows.Close()
				return nil, 0, fmt.Errorf("postgres: corrupt job %s could not be poisoned: %w (decode: %v)", c.id, err, err)
			}
			poisoned++
			continue
		}
		manifestVal, err := decodeInputManifest(c.manifest)
		if err != nil {
			if err := poisonCorruptJob(ctx, tx, c.id, err.Error()); err != nil {
				rows.Close()
				return nil, 0, fmt.Errorf("postgres: corrupt job %s could not be poisoned: %w (decode: %v)", c.id, err, err)
			}
			poisoned++
			continue
		}
		c.frameRangeVal = frameRangeVal
		c.manifestVal = manifestVal
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, 0, err
	}
	rows.Close()
	return candidates, poisoned, nil
}

// claimCandidateJob performs the per-job bookkeeping inside the claim
// transaction: attempt number, running state, lease generation, attempt row,
// event and the in-memory job projection.
func claimCandidateJob(ctx context.Context, tx *sql.Tx, c claimCandidate, workerID string, now, leaseUntil time.Time) (*model.Job, error) {
	var maxRecordedAttempt int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(attempt_number), 0) FROM render_attempts WHERE job_id = $1`, c.id).Scan(&maxRecordedAttempt); err != nil {
		return nil, err
	}
	attemptNumber := c.attempts + 1
	if maxRecordedAttempt >= attemptNumber {
		attemptNumber = maxRecordedAttempt + 1
	}
	nextGeneration := c.generation + 1
	if err := createAttempt(ctx, tx, c.id, attemptNumber, workerID, now); err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE render_jobs
		SET state = `+stateLiteral(model.StateRunning)+`,
		    current_worker_id = $2,
		    lease_until = $3,
		    started_at = $4,
		    attempt_count = $5,
		    lease_generation = $6
		WHERE id = $1`, c.id, workerID, leaseUntil, now, attemptNumber, nextGeneration); err != nil {
		return nil, err
	}

	if err := recordEvent(ctx, tx, eventJobClaimed, c.id, attemptID(c.id, attemptNumber), workerID, nil); err != nil {
		return nil, err
	}

	return &model.Job{ID: c.id,
		ParentJobID:           c.parentJobID.String,
		ChunkIndex:            c.chunkIndex,
		FrameRange:            c.frameRangeVal,
		JobType:               c.jobType,
		PublicationPolicy:     c.publicationPolicy,
		DaemonAdmissionWaitMS: c.daemonAdmissionWaitMS,
		Schema:                c.schema,
		Version:               schemaVersion(c.version),
		RenderPlan:            json.RawMessage(c.plan),
		Assets:                c.manifestVal.Assets,
		PreparedRenderPackage: c.manifestVal.PreparedRenderPackage,
		State:                 model.StateRunning,
		Worker:                workerID,
		Attempts:              attemptNumber,
		QueuedAt:              c.queuedAt,
		StartedAt:             now,
		LeaseUntil:            leaseUntil,
		LeaseGeneration:       nextGeneration,
	}, nil
}

// attachClaimArtifacts rehydrates artifacts for jobs re-claimed from the
// rendered state, after the claim transaction has committed. A failure here is
// returned to the caller exactly as before.
func attachClaimArtifacts(ctx context.Context, db *sql.DB, claimed []*model.Job, candidates []claimCandidate) error {
	for _, job := range claimed {
		for _, c := range candidates {
			if c.id != job.ID || !c.artifactID.Valid {
				continue
			}
			artifact, err := getArtifact(ctx, db, c.artifactID.String)
			if err != nil {
				return fmt.Errorf("job %s artifact %s: %w", job.ID, c.artifactID.String, err)
			}
			job.Artifact = artifact
		}
	}
	return nil
}
