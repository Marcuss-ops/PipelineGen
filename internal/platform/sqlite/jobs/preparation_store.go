package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

var _ job.PreparationStore = (*SQLiteStore)(nil)

func (r *SQLiteStore) GetPreparationUnit(ctx context.Context, fingerprint string) (*job.PreparedUnit, error) {
	row := r.db.QueryRowContext(ctx, preparationUnitSelect+` WHERE fingerprint = ?`, fingerprint)
	unit, err := scanPreparedUnit(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get preparation unit: %w", err)
	}
	return unit, nil
}

// PlanPreparationUnit seeds a durable PLANNED row for a fingerprint. It is
// idempotent (INSERT OR IGNORE): an existing row in ANY state is left
// untouched, so a concurrently RUNNING or READY singleflight result wins
// over a new plan. This is the entry of the PLANNED → RUNNING → READY
// state machine — the coordinator plans units ahead of time and executors
// transition them via AcquirePreparationUnit.
//
// The plan also persists the v2 Control-Plane metadata (migration 242/244):
// resource_class, cost_class, speculation_level, expected_work_ms, the
// fingerprint/processor versions, and the canonical input_manifest_json. This
// makes the durable row self-describing at plan time instead of guessing later.
func (r *SQLiteStore) PlanPreparationUnit(ctx context.Context, input job.PreparationPlanInput) error {
	if input.Fingerprint == "" || input.UnitID == "" || input.UnitKind == "" || input.JobType == "" {
		return fmt.Errorf("preparation plan requires fingerprint, unit id, unit kind, and job type")
	}
	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)

	resourceClass := string(input.ResourceClass)
	if resourceClass == "" {
		resourceClass = string(job.ResourceCPULight)
	}
	costClass := string(input.CostClass)
	if costClass == "" {
		costClass = string(job.CostMedium)
	}
	manifest := "{}"
	if len(input.Inputs) > 0 {
		b, err := json.Marshal(input.Inputs)
		if err != nil {
			return fmt.Errorf("marshal preparation input manifest: %w", err)
		}
		manifest = string(b)
	}
	reusable, preemptible := 0, 0
	if input.Reusable {
		reusable = 1
	}
	if input.Preemptible {
		preemptible = 1
	}

	_, err := r.db.ExecContext(ctx, `INSERT OR IGNORE INTO preparation_units
		(fingerprint, unit_id, unit_kind, job_type, state, created_at, updated_at,
		 fingerprint_version, processor_version, input_manifest_json,
		 resource_class, cost_class, speculation_level, expected_work_ms,
		 reusable, preemptible)
		VALUES (?, ?, ?, ?, 'PLANNED', ?, ?,
		        ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.Fingerprint, input.UnitID, input.UnitKind, input.JobType, nowStr, nowStr,
		input.FingerprintVersion, input.ProcessorVersion, manifest,
		resourceClass, costClass, int(input.SpeculationLevel), input.ExpectedWorkMS,
		reusable, preemptible)
	if err != nil {
		return fmt.Errorf("plan preparation unit: %w", err)
	}
	return nil
}

// AcquirePreparationUnit atomically creates or claims a unit. ready=true means
// the caller may adopt the existing READY result; ready=false means it owns
// RUNNING work. An unexpired RUNNING lease is never stolen, preventing
// duplicate provider calls. Expired RUNNING/STALE/FAILED rows may be reclaimed.
func (r *SQLiteStore) AcquirePreparationUnit(ctx context.Context, claim job.PreparationUnitClaim) (*job.PreparedUnit, bool, error) {
	if claim.Fingerprint == "" || claim.UnitKind == "" || claim.JobType == "" || claim.LeaseOwner == "" {
		return nil, false, fmt.Errorf("preparation claim requires fingerprint, unit kind, job type, and lease owner")
	}
	if claim.LeaseDuration <= 0 {
		return nil, false, fmt.Errorf("preparation lease duration must be positive")
	}
	now := time.Now().UTC()
	expires := now.Add(claim.LeaseDuration)
	nowStr, expiryStr := timeutil.FormatRFC3339(now), timeutil.FormatRFC3339(expires)

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO preparation_units
		(fingerprint, unit_id, unit_kind, job_type, state, lease_owner,
		 lease_expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'RUNNING', ?, ?, ?, ?)
		ON CONFLICT(fingerprint) DO UPDATE SET
		 state = 'RUNNING', unit_id = excluded.unit_id, unit_kind = excluded.unit_kind,
		 job_type = excluded.job_type, lease_owner = excluded.lease_owner,
		 lease_expires_at = excluded.lease_expires_at, error = '', updated_at = excluded.updated_at
		WHERE preparation_units.state IN ('PLANNED','FAILED','STALE')
		   OR (preparation_units.state = 'RUNNING' AND
		       (preparation_units.lease_expires_at IS NULL OR preparation_units.lease_expires_at <= excluded.updated_at))`,
		claim.Fingerprint, claim.UnitID, claim.UnitKind, claim.JobType, claim.LeaseOwner, expiryStr, nowStr, nowStr)
	if err != nil {
		return nil, false, fmt.Errorf("acquire preparation unit: %w", err)
	}

	unit, err := r.GetPreparationUnit(ctx, claim.Fingerprint)
	if err != nil {
		return nil, false, err
	}
	if unit == nil {
		return nil, false, fmt.Errorf("acquire preparation unit: row disappeared")
	}
	if unit.State == job.PreparationReady {
		return unit, true, nil
	}
	return unit, unit.LeaseOwner == claim.LeaseOwner && unit.State == job.PreparationRunning, nil
}

func (r *SQLiteStore) RenewPreparationUnitLease(ctx context.Context, fingerprint, leaseOwner string, duration time.Duration) error {
	if duration <= 0 {
		return fmt.Errorf("preparation lease duration must be positive")
	}
	now := time.Now().UTC()
	res, err := r.db.ExecContext(ctx, `UPDATE preparation_units
		SET lease_expires_at = ?, updated_at = ?
		WHERE fingerprint = ? AND state = 'RUNNING' AND lease_owner = ?
		 AND lease_expires_at > ?`,
		timeutil.FormatRFC3339(now.Add(duration)), timeutil.FormatRFC3339(now), fingerprint, leaseOwner, timeutil.FormatRFC3339(now))
	if err != nil {
		return fmt.Errorf("renew preparation lease: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("renew preparation lease: ownership lost")
	}
	return nil
}

// MarkPreparationReady marks a RUNNING unit READY and persists the v2 result
// metadata: result_kind/result_ref (where the produced bytes live), the
// actual_work_ms (measured execution time, consumed by the work estimator and
// claim-time saved-work accounting), and the ready_at timestamp.
func (r *SQLiteStore) MarkPreparationReady(ctx context.Context, update job.PreparationReadyUpdate) error {
	result := update.Result
	if len(result) == 0 {
		result = json.RawMessage(`{}`)
	}
	now := time.Now().UTC()
	nowStr := timeutil.FormatRFC3339(now)
	resultKind := string(update.ResultKind)
	if resultKind == "" {
		resultKind = string(job.ResultNone)
	}
	res, err := r.db.ExecContext(ctx, `UPDATE preparation_units
		SET state = 'READY', lease_owner = '', lease_expires_at = NULL,
		 artifact_id = ?, cache_key = ?, result_json = ?, error = '',
		 result_kind = ?, result_ref = ?, actual_work_ms = ?, ready_at = ?,
		 expires_at = ?, updated_at = ?
		WHERE fingerprint = ? AND state = 'RUNNING' AND lease_owner = ?
		 AND lease_expires_at > ?`,
		update.ArtifactID, update.CacheKey, string(result), resultKind, update.ResultRef, update.ActualWorkMS,
		nowStr, timeutil.FormatPtrRFC3339(update.ExpiresAt),
		timeutil.FormatRFC3339(now), update.Fingerprint, update.LeaseOwner, timeutil.FormatRFC3339(now))
	if err != nil {
		return fmt.Errorf("mark preparation ready: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("mark preparation ready: ownership lost")
	}
	return nil
}

func (r *SQLiteStore) MarkPreparationFailed(ctx context.Context, fingerprint, leaseOwner, message string) error {
	now := time.Now().UTC()
	res, err := r.db.ExecContext(ctx, `UPDATE preparation_units
		SET state = 'FAILED', lease_owner = '', lease_expires_at = NULL,
		 error = ?, updated_at = ?
		WHERE fingerprint = ? AND state = 'RUNNING' AND lease_owner = ?
		 AND lease_expires_at > ?`,
		message, timeutil.FormatRFC3339(now), fingerprint, leaseOwner, timeutil.FormatRFC3339(now))
	if err != nil {
		return fmt.Errorf("mark preparation failed: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("mark preparation failed: ownership lost")
	}
	return nil
}

// ExpirePreparationUnits flips READY units whose expires_at is at or before
// now to STALE (lease fields cleared; result metadata retained for
// diagnostics/metrics). STALE rows are reclaimable by the next
// AcquirePreparationUnit, which restarts them as RUNNING.
func (r *SQLiteStore) ExpirePreparationUnits(ctx context.Context, now time.Time) (int, error) {
	nowStr := timeutil.FormatRFC3339(now)
	res, err := r.db.ExecContext(ctx, `UPDATE preparation_units
		SET state = 'STALE', lease_owner = '', lease_expires_at = NULL, updated_at = ?
		WHERE state = 'READY' AND expires_at IS NOT NULL AND expires_at <= ?`,
		nowStr, nowStr)
	if err != nil {
		return 0, fmt.Errorf("expire preparation units: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

const preparationUnitSelect = `SELECT fingerprint, unit_id, unit_kind, job_type,
 state, lease_owner, lease_expires_at, artifact_id, cache_key, result_json, error,
 created_at, updated_at, expires_at, fingerprint_version, processor_version,
 resource_class, cost_class, speculation_level, expected_work_ms, result_kind, result_ref
 FROM preparation_units`

type preparationUnitScanner interface{ Scan(...any) error }

func scanPreparedUnit(s preparationUnitScanner) (*job.PreparedUnit, error) {
	var unit job.PreparedUnit
	var leaseExpiry, createdAt, updatedAt, expiresAt *string
	var (
		result                   string
		fingerprintVersion       string
		processorVersion         string
		resourceClass, costClass string
		kind                     string
		specLevel                int
	)
	if err := s.Scan(&unit.Fingerprint, &unit.UnitID, &unit.UnitKind, &unit.JobType, &unit.State,
		&unit.LeaseOwner, &leaseExpiry, &unit.ArtifactID, &unit.CacheKey, &result, &unit.Error,
		&createdAt, &updatedAt, &expiresAt, &fingerprintVersion, &processorVersion,
		&resourceClass, &costClass, &specLevel, &unit.ExpectedWorkMS, &kind, &unit.ResultRef); err != nil {
		return nil, err
	}
	unit.Result = json.RawMessage(result)
	unit.LeaseExpires = parsePreparationTime(leaseExpiry)
	unit.FingerprintVersion = fingerprintVersion
	unit.ProcessorVersion = processorVersion
	unit.ResourceClass = job.ResourceClass(resourceClass)
	unit.CostClass = job.CostClass(costClass)
	unit.SpeculationLevel = job.SpeculationLevel(specLevel)
	unit.ResultKind = job.ResultKind(kind)
	if parsed := parsePreparationTime(createdAt); parsed != nil {
		unit.CreatedAt = *parsed
	}
	if parsed := parsePreparationTime(updatedAt); parsed != nil {
		unit.UpdatedAt = *parsed
	}
	unit.ExpiresAt = parsePreparationTime(expiresAt)
	return &unit, nil
}

func parsePreparationTime(value *string) *time.Time {
	if value == nil || *value == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, *value)
	if err != nil {
		return nil
	}
	return &t
}
