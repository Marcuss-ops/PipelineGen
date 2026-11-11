package jobs

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// claimNextRenderPhase reads the indexed lane and the legacy empty-lane
// partition independently, then merges their already ordered pages. This
// avoids SQLite's temp B-tree for `execution_lane IN (phase, ”)` while keeping
// legacy payload-only jobs in the same global anti-starvation order.
func (r *SQLiteStore) claimNextRenderPhase(ctx context.Context, workerID string, leaseTTL time.Duration, types []string, phase string, now time.Time) (*job.Job, error) {
	cursors := map[string]*claimCandidateCursor{}
	exhausted := map[string]bool{}
	for {
		var candidates []rankedClaimCandidate
		for _, lane := range []string{phase, ""} {
			if exhausted[lane] {
				continue
			}
			page, cursor, err := r.claimLanePage(ctx, types, lane, cursors[lane], now)
			if err != nil {
				return nil, fmt.Errorf("ClaimNextMatching: select lane %q: %w", lane, err)
			}
			if len(page) == 0 {
				exhausted[lane] = true
				continue
			}
			cursors[lane] = cursor
			candidates = append(candidates, page...)
		}
		if len(candidates) == 0 {
			return nil, nil
		}
		sort.Slice(candidates, func(i, j int) bool {
			a, b := candidates[i], candidates[j]
			if a.effectivePriority != b.effectivePriority {
				return a.effectivePriority > b.effectivePriority
			}
			if a.createdAt != b.createdAt {
				return a.createdAt < b.createdAt
			}
			return a.job.ID < b.job.ID
		})
		for _, candidate := range candidates {
			if candidate.job.Scheduling.ExecutionLane == "" {
				if err := r.hydrateLatestPayload(ctx, candidate.job); err != nil {
					return nil, fmt.Errorf("ClaimNextMatching: hydrate legacy lane payload: %w", err)
				}
				if !job.MatchesPayload(candidate.job.Payload, job.PayloadMatch{"render_phase": phase}) {
					continue
				}
			}
			return r.claimCandidate(ctx, candidate.job, workerID, leaseTTL, now)
		}
	}
}

type rankedClaimCandidate struct {
	job               *job.Job
	effectivePriority int64
	createdAt         string
}

func (r *SQLiteStore) claimLanePage(ctx context.Context, types []string, lane string, cursor *claimCandidateCursor, now time.Time) ([]rankedClaimCandidate, *claimCandidateCursor, error) {
	query := `SELECT ` + jobColumns + `, claim_effective_priority, created_at FROM jobs
		WHERE status = 'QUEUED' AND execution_lane = ? AND (available_at IS NULL OR available_at <= ?)`
	args := []any{lane, timeutil.FormatRFC3339(now.UTC())}
	if len(types) > 0 {
		placeholders := make([]string, len(types))
		for i, t := range types {
			placeholders[i] = "?"
			args = append(args, t)
		}
		query += ` AND type IN (` + strings.Join(placeholders, ",") + `)`
	}
	if cursor != nil {
		query += ` AND (claim_effective_priority < ? OR
			(claim_effective_priority = ? AND (created_at > ? OR (created_at = ? AND id > ?))))`
		args = append(args, cursor.effectivePriority, cursor.effectivePriority, cursor.createdAt, cursor.createdAt, cursor.jobID)
	}
	query += ` ORDER BY claim_effective_priority DESC, created_at ASC, id ASC LIMIT ?`
	args = append(args, claimPayloadMatchScan)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	page := make([]rankedClaimCandidate, 0, claimPayloadMatchScan)
	var last *claimCandidateCursor
	for rows.Next() {
		candidate := &job.Job{}
		var effectivePriority int64
		var createdAt string
		if err := scanJobColumns(claimCandidateScanner{scanner: rows, effectivePriority: &effectivePriority, createdAt: &createdAt}, candidate); err != nil {
			return nil, nil, err
		}
		page = append(page, rankedClaimCandidate{job: candidate, effectivePriority: effectivePriority, createdAt: createdAt})
		last = &claimCandidateCursor{effectivePriority: effectivePriority, createdAt: createdAt, jobID: candidate.ID}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return page, last, nil
}

// schedulingProjectionMatches uses the persisted descriptor for explicit
// render phases. The enqueue boundary validates a declared render_phase
// against its payload; rows without a lane are legacy and still fall back to
// payload hydration. When a caller also matches unrelated payload keys, the
// payload remains necessary for those additional predicates.
func schedulingProjectionMatches(candidate *job.Job, match job.PayloadMatch, exclude job.PayloadNotMatch) (handled, matches bool) {
	if candidate == nil || candidate.Scheduling.ExecutionLane == "" {
		return false, false
	}
	phase, hasMatch := match["render_phase"]
	excludedPhase, hasExclude := exclude["render_phase"]
	if !hasMatch && !hasExclude {
		return false, false
	}
	if hasMatch && candidate.Scheduling.ExecutionLane != phase {
		return true, false
	}
	if hasExclude && candidate.Scheduling.ExecutionLane == excludedPhase {
		return true, false
	}
	matchCount, excludeCount := len(match), len(exclude)
	if hasMatch {
		matchCount--
	}
	if hasExclude {
		excludeCount--
	}
	if matchCount > 0 || excludeCount > 0 {
		return false, false
	}
	return true, true
}

type claimCandidateCursor struct {
	effectivePriority int64
	createdAt         string
	jobID             string
}

type claimCandidateScanner struct {
	scanner
	effectivePriority *int64
	createdAt         *string
}

// refreshClaimAging updates only queued jobs whose next hourly aging boundary
// has passed. The due index bounds the write set; each row is advanced directly
// to its current full-hour score, so a long pause does not require one update
// per missed hour. Invalid legacy timestamps retain the historical age-zero
// behavior and are excluded from future refreshes.
func (r *SQLiteStore) refreshClaimAging(ctx context.Context, now time.Time) error {
	now = time.UnixMilli(now.UTC().UnixMilli()).UTC()
	nowText := now.Format(time.RFC3339Nano)
	nowMillis := now.UnixMilli()
	_, err := r.db.ExecContext(ctx, `
		UPDATE jobs
		SET claim_effective_priority = priority + COALESCE(
		        CAST((julianday(?) - julianday(created_at)) * 24.0 AS INTEGER), 0),
		    claim_next_age_millis = CASE
		        WHEN julianday(created_at) IS NULL THEN 9223372036854775807
		        ELSE CAST((julianday(created_at) - 2440587.5) * 86400000 AS INTEGER) +
		            (COALESCE(CAST((julianday(?) - julianday(created_at)) * 24.0 AS INTEGER), 0) + 1) * 3600000
		    END
		WHERE status = 'QUEUED' AND claim_next_age_millis <= ?`, nowText, nowText, nowMillis)
	if err != nil {
		return fmt.Errorf("update queued aging scores: %w", err)
	}
	return nil
}
