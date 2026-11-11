// Package postgres implements the job queue repository backed by PostgreSQL.
//
// Claim uses SELECT ... FOR UPDATE SKIP LOCKED so any number of workers can
// pull jobs concurrently without two of them ever receiving the same job.
// Every claim also creates a render_attempt and every state transition appends
// a render_event, so the full history of a job is preserved and never
// overwritten.
//
// File layout: postgres.go owns the repository core (submit/claim/get),
// finalize.go and lifecycle.go the terminal/transition writes, requeue.go the
// lease-expiry sweep, children.go the fan-in projection, stats.go the queue
// snapshot and decode.go the JSONB decode helpers.
package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository"
	"github.com/jackc/pgx/v5/pgconn"
)

// defaultJobType is the job type recorded for render jobs. A job is one
// render SEGMENT (a full chronon.render-plan), not a single overlay.
const defaultJobType = model.JobTypeRenderSegment

// emptyJobTypeTotal counts jobs whose empty job_type was normalized to
// render_segment. It is the metric behind the normalization warning: the
// default is observable instead of silent.
var emptyJobTypeTotal atomic.Int64

// EmptyJobTypeNormalizedTotal reports how many submits normalized an empty
// job_type to the default. It exists for tests and diagnostics.
func EmptyJobTypeNormalizedTotal() int64 { return emptyJobTypeTotal.Load() }

// normalizeJobType maps the producer-supplied job type to the stored value.
// An empty type is normalized to render_segment with a warning plus a metric
// increment (never silently again); a known type passes through; anything
// else is rejected instead of being silently coerced to the default.
func normalizeJobType(jobType string) (string, error) {
	if jobType == "" {
		log.Printf("queue: warning: empty job_type normalized to %q", defaultJobType)
		emptyJobTypeTotal.Add(1)
		return defaultJobType, nil
	}
	switch jobType {
	case model.JobTypeRenderSegment,
		model.JobTypeRenderPrepare,
		model.JobTypeOverlayPrepare,
		model.JobTypeOverlayRender,
		model.JobTypeOverlayImport:
		return jobType, nil
	default:
		return "", fmt.Errorf("unknown job_type %q", jobType)
	}
}

// defaultOpTimeout bounds EVERY statement/transaction the repository issues.
//
// The bound is load-bearing, not cosmetic: every operation now derives its
// deadline from the CALLER's context (repository v2), so a cancelled request —
// a disconnected client, a shutting-down worker — aborts its statement instead
// of running to completion. The timeout below is the implementation's own
// SECOND guarantee, kept because it must hold even for a caller that never
// cancels: a stalled connection, a lock wait or a mid-failover backend would
// otherwise pin the calling HTTP handler / worker goroutine for as long as the
// driver is willing to wait — which is forever. It is deliberately generous: it
// exists to fail a stuck operation, not to be a performance budget.
const defaultOpTimeout = 15 * time.Second

// defaultRequeueBatch bounds one lease-expiry recovery transaction. A sweep
// that selects every expired lease at once holds their row locks and performs
// several statements per job inside one 15-second operation; when many leases
// expire together (a worker fleet restart) that work does not fit, the whole
// batch rolls back, and the next sweep starts over. Serially draining small
// committed batches makes the recovery advance monotonically instead.
const defaultRequeueBatch = 256

// Repository is the PostgreSQL backend for the central job queue.
type Repository struct {
	db          *sql.DB
	lease       time.Duration
	maxAttempts int

	// opTimeout bounds one repository operation (defaultOpTimeout when the
	// repository was built by New).
	opTimeout time.Duration

	// requeueBatch is the maximum number of expired leases one transaction
	// recovers (defaultRequeueBatch when the repository was built by New).
	requeueBatch int
}

// opContext derives one repository operation's context from the CALLER's
// context and bounds it by opTimeout, so no call can outlive either the caller
// or the deadline. Deriving from the caller is what makes cancellation and
// tracing actually reach PostgreSQL: the HTTP request context now flows
// handler → service → repository → driver.
func (r *Repository) opContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		// A nil caller context is a bug upstream, but it must not panic inside
		// the storage layer: fall back to a bounded background context so the
		// operation still cannot outlive opTimeout.
		ctx = context.Background()
	}
	timeout := r.opTimeout
	if timeout <= 0 {
		timeout = defaultOpTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

// SetOpTimeout overrides the per-operation deadline (values <= 0 restore
// defaultOpTimeout).
func (r *Repository) SetOpTimeout(d time.Duration) { r.opTimeout = d }

// SetRequeueBatch overrides the number of expired leases recovered per
// transaction (values <= 0 restore defaultRequeueBatch).
func (r *Repository) SetRequeueBatch(n int) { r.requeueBatch = n }

func (r *Repository) requeueBatchSize() int {
	if r.requeueBatch <= 0 {
		return defaultRequeueBatch
	}
	return r.requeueBatch
}

// rowsAffected reads an affected-row count, surfacing the driver error that
// would otherwise be indistinguishable from "zero rows". Every caller
// interprets zero as a semantic outcome (not owned by this worker, already
// claimed, duplicate id), so treating a failed count as zero silently mislabels
// a storage failure as a normal race — and for Submit it would report a
// dropped job as an idempotent duplicate.
func rowsAffected(res sql.Result) (int64, error) {
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("postgres: rows affected: %w", err)
	}
	return n, nil
}

// requireTransitionRows turns a zero-row conditional UPDATE into the ownership
// conflict every transition reports. Zero rows is a semantic outcome (wrong
// state or wrong worker), never a storage failure.
func requireTransitionRows(res sql.Result, id, workerID string) error {
	n, err := rowsAffected(res)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("job %s is not running or not owned by %s", id, workerID)
	}
	return nil
}

// withTx runs fn in one transaction: commit only when fn returns nil, otherwise
// roll back. Every transition shares this preamble instead of restating the
// BeginTx/Rollback/Commit triple, and fn must not commit the transaction itself.
func (r *Repository) withTx(ctx context.Context, fn func(context.Context, *sql.Tx) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Compile-time check that Repository satisfies the repository contract.
var _ repository.JobRepository = (*Repository)(nil)

// New creates a PostgreSQL-backed job repository.
func New(db *sql.DB, lease time.Duration, maxAttempts int) *Repository {
	return &Repository{
		db:           db,
		lease:        lease,
		maxAttempts:  maxAttempts,
		opTimeout:    defaultOpTimeout,
		requeueBatch: defaultRequeueBatch,
	}
}

// inputManifest is the JSONB shape stored in render_jobs.input_manifest.
type inputManifest struct {
	Assets                []model.AssetRef                `json:"assets"`
	PreparedRenderPackage *model.PreparedRenderPackageRef `json:"prepared_render_package,omitempty"`
}

// jobRow carries the wire defaults and encoded JSONB columns of one job row.
// Submit and SubmitBatch must produce byte-identical rows, so the encoding is
// computed in one place instead of once per insert path.
type jobRow struct {
	jobType  string
	schema   string
	version  int
	plan     []byte
	manifest []byte
}

func prepareJobRow(job model.Job) (jobRow, error) {
	row := jobRow{schema: job.Schema, version: job.Version}
	if row.schema == "" {
		row.schema = model.JobSchemaV1
	}
	if row.version == 0 {
		row.version = model.JobSchemaVersionV1
	}
	plan, err := normalizeJSON(job.RenderPlan)
	if err != nil {
		return jobRow{}, fmt.Errorf("render_plan: %w", err)
	}
	row.plan = plan
	manifest, err := json.Marshal(inputManifest{Assets: job.Assets, PreparedRenderPackage: job.PreparedRenderPackage})
	if err != nil {
		return jobRow{}, fmt.Errorf("input_manifest: %w", err)
	}
	row.manifest = manifest
	row.jobType, err = normalizeJobType(job.JobType)
	if err != nil {
		return jobRow{}, err
	}
	return row, nil
}

const insertJobSQL = `
		INSERT INTO render_jobs (id, job_type, job_schema, job_schema_version, render_plan, input_manifest, max_attempts, idempotency_key, parent_job_id, chunk_index, frame_range, not_before, publication_policy, daemon_admission_wait_ms)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''), NULLIF($9, ''), $10, $11, $12, $13, $14)
		ON CONFLICT (id) DO NOTHING`

// insertJobRowOnly inserts one job row without its creation event.
// ON CONFLICT DO NOTHING makes the duplicate id the idempotency signal.
//
// Splitting the row from the event lets SubmitBatch stage N JOB_CREATED rows
// and flush them with ONE multi-row INSERT (recordEvents) at the end of the
// transaction, instead of one statement per job. Submit and SubmitBatch keep
// producing byte-identical rows: the event VALUES are the same, only the
// statement shape differs.
func (r *Repository) insertJobRowOnly(ctx context.Context, tx *sql.Tx, job model.Job) error {
	row, err := prepareJobRow(job)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, insertJobSQL,
		job.ID, row.jobType, row.schema, row.version, row.plan, row.manifest, r.maxAttempts, job.IdempotencyKey, job.ParentJobID, job.ChunkIndex, job.FrameRange, job.NotBefore, job.PublicationPolicy, job.DaemonAdmissionWaitMS)
	if err != nil {
		return mapJobInsertError(err, job.ID)
	}
	n, err := rowsAffected(res)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: job %s", repository.ErrJobExists, job.ID)
	}
	return nil
}

// idempotencyKeyIndexes names every index that enforces the idempotency-key
// uniqueness rule. Migration 007 created idx_render_jobs_idempotency and
// migration 011 added render_jobs_idempotency_key_unique; PostgreSQL reports
// whichever one it evaluated first, so both must be recognized.
var idempotencyKeyIndexes = map[string]struct{}{
	"idx_render_jobs_idempotency":        {},
	"render_jobs_idempotency_key_unique": {},
}

// mapJobInsertError maps the idempotency-key unique violation to the same
// ErrJobExists sentinel the in-memory backend returns, so a duplicate logical
// submit is a conflict on every backend instead of a driver-specific failure.
// ON CONFLICT (id) DO NOTHING already makes a duplicate id explicit; the key
// conflict is the only other uniqueness rule on this INSERT.
func mapJobInsertError(err error, jobID string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		if _, ok := idempotencyKeyIndexes[pgErr.ConstraintName]; ok {
			return fmt.Errorf("%w: job %s reuses an existing idempotency key", repository.ErrJobExists, jobID)
		}
	}
	return err
}

// insertJobRow inserts one job and records its creation event inside tx.
func (r *Repository) insertJobRow(ctx context.Context, tx *sql.Tx, job model.Job) error {
	if err := r.insertJobRowOnly(ctx, tx, job); err != nil {
		return err
	}
	return recordEvent(ctx, tx, eventJobCreated, job.ID, "", "", nil)
}

// Submit enqueues a job. The ID is required and must be unique.
func (r *Repository) Submit(ctx context.Context, job model.Job) error {
	if job.ID == "" {
		return fmt.Errorf("job id is required")
	}
	if err := model.ValidateChunk(job); err != nil {
		return err
	}

	ctx, cancel := r.opContext(ctx)
	defer cancel()
	return r.withTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return r.insertJobRow(ctx, tx, job)
	})
}

// SubmitBatch atomically inserts an anchor and its chunk children. The queue
// claim query intentionally skips parents that own children; atomic insertion
// closes the otherwise real window between creating the anchor and creating
// its first child.
func (r *Repository) SubmitBatch(ctx context.Context, jobs []model.Job) error {
	if err := model.ValidateJobBatch(jobs); err != nil {
		return err
	}
	for _, job := range jobs {
		if _, err := normalizeJobType(job.JobType); err != nil {
			return err
		}
	}
	ctx, cancel := r.opContext(ctx)
	defer cancel()
	return r.withTx(ctx, func(ctx context.Context, tx *sql.Tx) error {
		// One creation event per job, flushed as a single multi-row INSERT:
		// a batch submit pays one event statement instead of one per job. The
		// persisted rows are identical to the per-job path (same VALUES, same
		// BIGSERIAL order = jobs order).
		events := make([]eventRow, 0, len(jobs))
		for _, job := range jobs {
			if err := r.insertJobRowOnly(ctx, tx, job); err != nil {
				return err
			}
			events = append(events, eventRow{eventType: eventJobCreated, jobID: job.ID})
		}
		return recordEvents(ctx, tx, events)
	})
}

// SubmitBatchIdempotent submits the complete family in one transaction. If
// that transaction loses a unique-key race, the persisted IDs and request
// envelopes are re-read and accepted only when every member matches exactly.
func (r *Repository) SubmitBatchIdempotent(ctx context.Context, jobs []model.Job) (bool, error) {
	if err := model.ValidateJobBatch(jobs); err != nil {
		return false, err
	}
	for _, job := range jobs {
		if job.IdempotencyKey == "" {
			return false, fmt.Errorf("job %s has no idempotency key", job.ID)
		}
	}
	if err := r.SubmitBatch(ctx, jobs); err == nil {
		return true, nil
	} else if !errors.Is(err, repository.ErrJobExists) && !errors.Is(err, repository.ErrIdempotencyConflict) {
		return false, err
	} else {
		submitErr := err
		if !errors.Is(submitErr, repository.ErrJobExists) && !errors.Is(submitErr, repository.ErrIdempotencyConflict) {
			return false, submitErr
		}
		matches, verifyErr := r.batchRequestMatches(ctx, jobs)
		if verifyErr != nil {
			return false, verifyErr
		}
		if !matches {
			return false, fmt.Errorf("%w: stored family differs or is incomplete", repository.ErrIdempotencyConflict)
		}
		return false, nil
	}
}

// batchRequestMatches reads every immutable field in one SQL statement. A
// replay therefore gets one PostgreSQL snapshot for the full family instead
// of a producer-side series of GETs that could observe a mixed state.
func (r *Repository) batchRequestMatches(ctx context.Context, jobs []model.Job) (bool, error) {
	ctx, cancel := r.opContext(ctx)
	defer cancel()
	args := make([]any, len(jobs))
	placeholders := make([]string, len(jobs))
	expected := make(map[string]model.Job, len(jobs))
	for i, job := range jobs {
		args[i] = job.ID
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		expected[job.ID] = job
	}
	query := `SELECT id, job_type, publication_policy, daemon_admission_wait_ms, job_schema,
		job_schema_version, render_plan, input_manifest, idempotency_key, parent_job_id,
		chunk_index, frame_range, not_before FROM render_jobs WHERE id IN (` + strings.Join(placeholders, ",") + `)`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	found := make(map[string]model.Job, len(jobs))
	for rows.Next() {
		var actual model.Job
		var schema string
		var version sql.NullInt64
		var plan, manifest, frameRange []byte
		var idemKey, parentID sql.NullString
		var notBefore sql.NullTime
		if err := rows.Scan(&actual.ID, &actual.JobType, &actual.PublicationPolicy,
			&actual.DaemonAdmissionWaitMS, &schema, &version, &plan, &manifest,
			&idemKey, &parentID, &actual.ChunkIndex, &frameRange, &notBefore); err != nil {
			return false, err
		}
		actual.Schema = schema
		actual.Version = schemaVersion(version)
		actual.RenderPlan = json.RawMessage(plan)
		actual.IdempotencyKey = idemKey.String
		actual.ParentJobID = parentID.String
		actual.FrameRange, err = decodeFrameRange(frameRange)
		if err != nil {
			return false, fmt.Errorf("batch replay frame range %s: %w", actual.ID, err)
		}
		manifestValue, err := decodeInputManifest(manifest)
		if err != nil {
			return false, fmt.Errorf("batch replay manifest %s: %w", actual.ID, err)
		}
		actual.Assets = manifestValue.Assets
		actual.PreparedRenderPackage = manifestValue.PreparedRenderPackage
		if notBefore.Valid {
			t := notBefore.Time
			actual.NotBefore = &t
		}
		found[actual.ID] = actual
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(found) != len(expected) {
		return false, nil
	}
	for id, requested := range expected {
		actual, ok := found[id]
		if !ok || !model.SameSubmitRequest(actual, requested) {
			return false, nil
		}
	}
	return true, nil
}

// SubmitIdempotent uses the database uniqueness constraint as the race-safe
// winner selection for concurrent retries of the same logical request.
func (r *Repository) SubmitIdempotent(ctx context.Context, job model.Job) (*model.Job, bool, error) {
	if job.IdempotencyKey == "" {
		if err := r.Submit(ctx, job); err != nil {
			return nil, false, err
		}
		canonical, err := r.Get(ctx, job.ID)
		return canonical, true, err
	}
	ctx, cancel := r.opContext(ctx)
	defer cancel()
	var existingID string
	err := r.db.QueryRowContext(ctx, `SELECT id FROM render_jobs WHERE idempotency_key = $1`, job.IdempotencyKey).Scan(&existingID)
	if err == nil {
		canonical, getErr := r.Get(ctx, existingID)
		if getErr != nil {
			return nil, false, getErr
		}
		if !model.SameSubmitRequest(*canonical, job) {
			return nil, false, fmt.Errorf("%w: key %q belongs to job %s", repository.ErrIdempotencyConflict, job.IdempotencyKey, existingID)
		}
		return canonical, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	submitErr := r.Submit(ctx, job)
	if submitErr == nil {
		canonical, getErr := r.Get(ctx, job.ID)
		return canonical, true, getErr
	}
	// Only a uniqueness conflict may be reinterpreted as "another submitter
	// won". Re-running the lookup after ANY insert failure could replace the
	// caller's real error (an unknown job_type, a malformed plan) with a row
	// that happened to appear under the same key — masking a validation bug as
	// an idempotent duplicate. Everything else is returned as itself.
	if !isSubmitConflict(submitErr) {
		return nil, false, submitErr
	}
	// Another submitter may have won the unique-key race. If NO row owns the
	// key, the failure was a duplicate id under a different key: propagate
	// ErrJobExists exactly like the in-memory backend, instead of returning a
	// raw sql.ErrNoRows that would hide the conflict behind a 500.
	if err := r.db.QueryRowContext(ctx, `SELECT id FROM render_jobs WHERE idempotency_key = $1`, job.IdempotencyKey).Scan(&existingID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, false, submitErr
		}
		return nil, false, err
	}
	canonical, getErr := r.Get(ctx, existingID)
	if getErr != nil {
		return nil, false, getErr
	}
	if !model.SameSubmitRequest(*canonical, job) {
		return nil, false, fmt.Errorf("%w: key %q belongs to job %s", repository.ErrIdempotencyConflict, job.IdempotencyKey, existingID)
	}
	return canonical, false, nil
}

// isSubmitConflict reports whether err is the uniqueness collision that means
// another submitter may have created the canonical row: either the job-id
// conflict (ON CONFLICT (id) DO NOTHING) or a PostgreSQL unique violation on an
// idempotency-key index. Any other error is the caller's answer, not a race.
func isSubmitConflict(err error) bool {
	if errors.Is(err, repository.ErrJobExists) {
		return true
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	return strings.Contains(pgErr.ConstraintName, "idempotency")
}

// Claim atomically claims the longest-waiting pending job for a worker,
// holding it under a lease. It returns nil when no job is pending.
func (r *Repository) RecoverableParents(ctx context.Context) ([]string, error) {
	ctx, cancel := r.opContext(ctx)
	defer cancel()
	rows, err := r.db.QueryContext(ctx, `SELECT parent.id FROM render_jobs AS parent
		WHERE parent.state IN `+stateIn(model.StatePending, model.StateFinalizing)+`
		AND EXISTS (SELECT 1 FROM render_jobs child WHERE child.parent_job_id = parent.id)
		ORDER BY parent.queued_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *Repository) Claim(ctx context.Context, workerID string) (*model.Job, time.Duration, error) {
	return r.ClaimState(ctx, workerID, "")
}

func (r *Repository) ClaimState(ctx context.Context, workerID string, state model.State) (*model.Job, time.Duration, error) {
	ctx, cancel := r.opContext(ctx)
	defer cancel()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()

	now := time.Now()
	leaseUntil := now.Add(r.lease)

	var (
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
	)
	// The claimable vocabulary is owned by model.ClaimableStates(), so adding
	// a claimable state updates this query instead of leaving a hand-typed
	// literal behind. A requested state is validated against the same
	// vocabulary before it reaches the query text.
	stateFilter := "state IN " + stateIn(model.ClaimableStates()...)
	if state != "" {
		if !model.IsClaimable(state) {
			return nil, 0, fmt.Errorf("unsupported claim state %q", state)
		}
		stateFilter = "state = " + stateLiteral(state)
	}
	// Assembly anchors (model/lifecycle.go) are never claimable for rendering:
	// a job that owns chunk children has its output assembled from them, so a
	// claim here would re-render the whole plan on the GPU. The EXISTS form
	// keeps the rule structural — no producer has to remember to flag a
	// parent, and a parent submitted without children stays ordinary work.
	err = tx.QueryRowContext(ctx, `
		SELECT id, job_type, publication_policy, daemon_admission_wait_ms, job_schema, job_schema_version, render_plan, input_manifest, attempt_count, lease_generation, queued_at, artifact_id, parent_job_id, chunk_index, frame_range
		FROM render_jobs
		WHERE `+stateFilter+`
		  AND (not_before IS NULL OR not_before <= $1)
		  AND NOT EXISTS (
		      SELECT 1 FROM render_jobs AS child
		      WHERE child.parent_job_id = render_jobs.id)
		ORDER BY queued_at ASC
		FOR UPDATE SKIP LOCKED
		LIMIT 1`, now).Scan(&id, &jobType, &publicationPolicy, &daemonAdmissionWaitMS, &schema, &version, &plan, &manifest, &attempts, &generation, &queuedAt, &artifactID, &parentJobID, &chunkIndex, &frameRange)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}

	// Fail-closed JSONB: corrupt frame_range or input_manifest must poison the
	// job (failed) rather than silently rendering full-plan with dropped SHA256.
	// Decode BEFORE creating an attempt so a corrupt row never gets a running
	// attempt or a lease — it is directly failed.
	frameRangeVal, err := decodeFrameRange(frameRange)
	if err != nil {
		return nil, 0, poisonCorruptClaim(ctx, tx, id, err)
	}
	manifestVal, err := decodeInputManifest(manifest)
	if err != nil {
		return nil, 0, poisonCorruptClaim(ctx, tx, id, err)
	}

	var maxRecordedAttempt int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(attempt_number), 0) FROM render_attempts WHERE job_id = $1`, id).Scan(&maxRecordedAttempt); err != nil {
		return nil, 0, err
	}
	attemptNumber := attempts + 1
	if maxRecordedAttempt >= attemptNumber {
		attemptNumber = maxRecordedAttempt + 1
	}
	nextGeneration := generation + 1
	if err := createAttempt(ctx, tx, id, attemptNumber, workerID, now); err != nil {
		return nil, 0, err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE render_jobs
		SET state = `+stateLiteral(model.StateRunning)+`,
		    current_worker_id = $2,
		    lease_until = $3,
		    started_at = $4,
		    attempt_count = $5,
		    lease_generation = $6
		WHERE id = $1`, id, workerID, leaseUntil, now, attemptNumber, nextGeneration); err != nil {
		return nil, 0, err
	}

	if err := recordEvent(ctx, tx, eventJobClaimed, id, attemptID(id, attemptNumber), workerID, nil); err != nil {
		return nil, 0, err
	}

	if err := tx.Commit(); err != nil {
		return nil, 0, err
	}

	job := &model.Job{ID: id,
		ParentJobID:           parentJobID.String,
		ChunkIndex:            chunkIndex,
		FrameRange:            frameRangeVal,
		JobType:               jobType,
		PublicationPolicy:     publicationPolicy,
		DaemonAdmissionWaitMS: daemonAdmissionWaitMS,
		Schema:                schema,
		Version:               schemaVersion(version),
		RenderPlan:            json.RawMessage(plan),
		Assets:                manifestVal.Assets,
		PreparedRenderPackage: manifestVal.PreparedRenderPackage,
		State:                 model.StateRunning,
		Worker:                workerID,
		Attempts:              attemptNumber,
		QueuedAt:              queuedAt,
		StartedAt:             now,
		LeaseUntil:            leaseUntil,
		LeaseGeneration:       nextGeneration,
	}
	// A job re-claimed from the rendered state carries its already-stored
	// artifact so the worker can skip rendering and only retry publication.
	if artifactID.Valid {
		artifact, err := getArtifact(ctx, r.db, artifactID.String)
		if err != nil {
			return nil, 0, fmt.Errorf("job %s artifact %s: %w", id, artifactID.String, err)
		}
		job.Artifact = artifact
	}
	return job, r.lease, nil
}

// Get returns the current state of a job, including its artifact when done.
func (r *Repository) Get(ctx context.Context, id string) (*model.Job, error) {
	ctx, cancel := r.opContext(ctx)
	defer cancel()

	var (
		job            model.Job
		state          string
		schema         string
		version        sql.NullInt64
		plan           []byte
		manifest       []byte
		worker         sql.NullString
		queuedAt       sql.NullTime
		startedAt      sql.NullTime
		completedAt    sql.NullTime
		leaseUntil     sql.NullTime
		errorMsg       sql.NullString
		artifactID     sql.NullString
		idempotencyKey sql.NullString
		parentJobID    sql.NullString
		chunkIndex     int
		frameRange     []byte
		notBefore      sql.NullTime
		pFramesDone    sql.NullInt64
		pTotalFrames   sql.NullInt64
		pLastFrameAt   sql.NullTime
		pWorker        sql.NullString
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT id, state, job_type, publication_policy, daemon_admission_wait_ms, job_schema, job_schema_version, render_plan,
		       input_manifest, attempt_count, lease_generation,
		       current_worker_id, queued_at, started_at, completed_at,
		       lease_until, error_message, artifact_id, idempotency_key,
		       parent_job_id, chunk_index, frame_range, not_before,
		       progress_frames_done, progress_total_frames, progress_last_frame_at, progress_worker
		FROM render_jobs
		WHERE id = $1`, id).Scan(
		&job.ID, &state, &job.JobType, &job.PublicationPolicy, &job.DaemonAdmissionWaitMS, &schema, &version, &plan,
		&manifest, &job.Attempts, &job.LeaseGeneration,
		&worker, &queuedAt, &startedAt, &completedAt,
		&leaseUntil, &errorMsg, &artifactID, &idempotencyKey,
		&parentJobID, &chunkIndex, &frameRange, &notBefore,
		&pFramesDone, &pTotalFrames, &pLastFrameAt, &pWorker)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("job %s: %w", id, repository.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}

	job.State = model.State(state)
	job.Schema = schema
	job.Version = schemaVersion(version)
	job.RenderPlan = json.RawMessage(plan)
	job.IdempotencyKey = idempotencyKey.String
	job.ParentJobID = parentJobID.String
	job.ChunkIndex = chunkIndex
	var decErr error
	if job.FrameRange, decErr = decodeFrameRange(frameRange); decErr != nil {
		return nil, r.poisonCorruptRead(ctx, id, decErr)
	}
	var manifestVal inputManifest
	if manifestVal, decErr = decodeInputManifest(manifest); decErr != nil {
		return nil, r.poisonCorruptRead(ctx, id, decErr)
	}
	job.Assets = manifestVal.Assets
	job.PreparedRenderPackage = manifestVal.PreparedRenderPackage
	if notBefore.Valid {
		nb := notBefore.Time
		job.NotBefore = &nb
	}
	if worker.Valid {
		job.Worker = worker.String
	}
	if queuedAt.Valid {
		job.QueuedAt = queuedAt.Time
	}
	if startedAt.Valid {
		job.StartedAt = startedAt.Time
	}
	if completedAt.Valid {
		job.CompletedAt = completedAt.Time
	}
	if leaseUntil.Valid {
		job.LeaseUntil = leaseUntil.Time
	}
	if errorMsg.Valid {
		job.FailReason = errorMsg.String
	}

	if pLastFrameAt.Valid {
		job.Progress = &model.Progress{
			FramesDone:  int(pFramesDone.Int64),
			TotalFrames: int(pTotalFrames.Int64),
			LastFrameAt: pLastFrameAt.Time,
			Worker:      pWorker.String,
		}
	}

	if artifactID.Valid {
		artifact, err := getArtifact(ctx, r.db, artifactID.String)
		if err != nil {
			return nil, err
		}
		job.Artifact = artifact
	}
	return &job, nil
}
